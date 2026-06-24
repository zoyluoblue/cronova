package scheduler

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/executor"
	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/store/sqlite"
)

// mockExecutor is a controllable Executor for deterministic concurrency tests.
// Tasks stay running until the test explicitly finishes them.
type mockExecutor struct {
	mu      sync.Mutex
	running map[string]bool
	exit    map[string]int
	cur     int
	max     int
}

func newMockExecutor() *mockExecutor {
	return &mockExecutor{running: map[string]bool{}, exit: map[string]int{}}
}

func (m *mockExecutor) Launch(_ context.Context, spec executor.Spec) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ref := spec.TaskRunID
	if m.running[ref] {
		return ref, nil
	}
	if _, done := m.exit[ref]; done {
		return ref, nil
	}
	m.running[ref] = true
	m.cur++
	if m.cur > m.max {
		m.max = m.cur
	}
	return ref, nil
}

func (m *mockExecutor) Probe(_ context.Context, ref string) (executor.Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if code, ok := m.exit[ref]; ok {
		return executor.Status{Phase: executor.PhaseExited, ExitCode: code}, nil
	}
	if m.running[ref] {
		return executor.Status{Phase: executor.PhaseRunning}, nil
	}
	return executor.Status{Phase: executor.PhaseUnknown}, nil
}

func (m *mockExecutor) Cancel(_ context.Context, _ string) error { return nil }

func (m *mockExecutor) finishAll(code int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for ref, on := range m.running {
		if on {
			m.running[ref] = false
			m.exit[ref] = code
			m.cur--
		}
	}
}

func (m *mockExecutor) snapshot() (cur, max int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cur, m.max
}

func waitMockCur(t *testing.T, m *mockExecutor, want int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cur, _ := m.snapshot(); cur == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	cur, _ := m.snapshot()
	t.Fatalf("mock running = %d, want %d within %s", cur, want, within)
}

func newStore(t *testing.T) *sqlite.Store {
	t.Helper()
	st, err := sqlite.New(filepath.Join(t.TempDir(), "m3.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestPoolLimitsConcurrency(t *testing.T) {
	st := newStore(t)
	mock := newMockExecutor()
	s := New(st, mock, Options{LogDir: filepath.Join(t.TempDir(), "logs"), Tick: 5 * time.Millisecond, PollInterval: 5 * time.Millisecond})
	ctx := context.Background()

	if err := st.UpsertPool(ctx, &model.Pool{Name: "p", Slots: 2}); err != nil {
		t.Fatal(err)
	}
	dag := &model.DAG{
		DagID: "fan", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{
			{ID: "a", Command: "x", Pool: "p"},
			{ID: "b", Command: "x", Pool: "p"},
			{ID: "c", Command: "x", Pool: "p"},
			{ID: "d", Command: "x", Pool: "p"},
		},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, err := s.TriggerManual(ctx, "fan")
	if err != nil {
		t.Fatal(err)
	}

	remaining := 4
	for remaining > 0 {
		s.tickOnce(ctx)
		want := 2
		if remaining < 2 {
			want = remaining
		}
		waitMockCur(t, mock, want, 2*time.Second)
		// Extra ticks must NOT push past the pool limit.
		s.tickOnce(ctx)
		s.tickOnce(ctx)
		if cur, _ := mock.snapshot(); cur != want {
			t.Fatalf("pool overflow: %d running, want %d", cur, want)
		}
		mock.finishAll(0)
		s.WaitInflight()
		remaining -= want
	}
	s.tickOnce(ctx)

	if _, max := mock.snapshot(); max != 2 {
		t.Errorf("max concurrency = %d, want 2 (pool slots)", max)
	}
	run, _ := st.GetDagRun(ctx, runID)
	if run.State != model.RunSuccess {
		t.Errorf("run state = %s, want success", run.State)
	}
}

func TestRetryExhausted(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "retry", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{{ID: "t", Command: "exit 1", Pool: model.DefaultPoolName, Retries: 2, RetryDelay: 0}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "retry")
	run := s.driveToTerminal(t, ctx, runID, 40)
	if run.State != model.RunFailed {
		t.Fatalf("run = %s, want failed", run.State)
	}
	tis, _ := s.store.ListTaskInstances(ctx, runID)
	if tis[0].State != model.TaskFailed {
		t.Errorf("task = %s, want failed", tis[0].State)
	}
	if tis[0].TryNumber != 3 { // 1 initial + 2 retries
		t.Errorf("try_number = %d, want 3", tis[0].TryNumber)
	}
}

func TestRetryThenSuccess(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	marker := filepath.Join(t.TempDir(), "m")
	// First attempt: no marker -> create it and fail. Second: marker exists -> ok.
	cmd := "if [ -f " + marker + " ]; then echo ok; else touch " + marker + "; exit 1; fi"
	dag := &model.DAG{
		DagID: "flaky", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{{ID: "t", Command: cmd, Pool: model.DefaultPoolName, Retries: 1, RetryDelay: 0}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "flaky")
	run := s.driveToTerminal(t, ctx, runID, 40)
	if run.State != model.RunSuccess {
		t.Fatalf("run = %s, want success", run.State)
	}
	tis, _ := s.store.ListTaskInstances(ctx, runID)
	if tis[0].State != model.TaskSuccess || tis[0].TryNumber != 2 {
		t.Errorf("task = %s try=%d, want success try=2", tis[0].State, tis[0].TryNumber)
	}
}

func TestTimeoutFailsTask(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "to", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{{ID: "t", Command: "sleep 30", Pool: model.DefaultPoolName, Timeout: 1, Retries: 0}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "to")
	run := s.driveToTerminal(t, ctx, runID, 80)
	if run.State != model.RunFailed {
		t.Fatalf("run = %s, want failed (timeout)", run.State)
	}
}

func TestDependencyTrigger(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	up := &model.DAG{DagID: "up", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{{ID: "u", Command: "echo up", Pool: model.DefaultPoolName}}}
	down := &model.DAG{DagID: "down", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		TriggerAfter: []string{"up"},
		Tasks:        []model.Task{{ID: "d", Command: "echo down", Pool: model.DefaultPoolName}}}
	if err := s.registerDAG(ctx, up); err != nil {
		t.Fatal(err)
	}
	if err := s.registerDAG(ctx, down); err != nil {
		t.Fatal(err)
	}

	runUp, _ := s.TriggerManual(ctx, "up")
	s.driveToTerminal(t, ctx, runUp, 20) // up succeeds and triggers down

	// Drive the scheduler until the dependency-triggered down run finishes.
	var downRun *model.DagRun
	for i := 0; i < 60; i++ {
		s.tickOnce(ctx)
		s.WaitInflight()
		runs, _ := s.store.ListDagRuns(ctx, "down", 10)
		if len(runs) > 0 && (runs[0].State == model.RunSuccess || runs[0].State == model.RunFailed) {
			downRun = runs[0]
			break
		}
	}
	if downRun == nil {
		t.Fatal("downstream run was never created/finished")
	}
	if downRun.State != model.RunSuccess {
		t.Errorf("down run = %s, want success", downRun.State)
	}
	if downRun.TriggerType != model.TriggerDependency {
		t.Errorf("down run trigger = %s, want dependency", downRun.TriggerType)
	}
}
