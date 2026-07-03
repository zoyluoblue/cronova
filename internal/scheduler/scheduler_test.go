package scheduler

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/executor"
	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/store/sqlite"
)

func newTestScheduler(t *testing.T) *Scheduler {
	t.Helper()
	st, err := sqlite.New(filepath.Join(t.TempDir(), "sched.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return New(st, executor.NewLocal(), Options{
		LogDir:       filepath.Join(t.TempDir(), "logs"),
		Tick:         10 * time.Millisecond,
		PollInterval: 5 * time.Millisecond,
	})
}

// driveToTerminal repeatedly ticks (and waits for dispatched tasks) until the
// run reaches a terminal state, making the async loop deterministic.
func (s *Scheduler) driveToTerminal(t *testing.T, ctx context.Context, runID string, maxTicks int) *model.DagRun {
	t.Helper()
	for i := 0; i < maxTicks; i++ {
		s.tickOnce(ctx)
		s.WaitInflight()
		run, err := s.store.GetDagRun(ctx, runID)
		if err != nil {
			t.Fatalf("GetDagRun: %v", err)
		}
		if run.State == model.RunSuccess || run.State == model.RunFailed {
			return run
		}
	}
	t.Fatalf("run %s did not terminate within %d ticks", runID, maxTicks)
	return nil
}

func (s *Scheduler) tiStates(t *testing.T, ctx context.Context, runID string) map[string]model.TaskState {
	t.Helper()
	tis, err := s.store.ListTaskInstances(ctx, runID)
	if err != nil {
		t.Fatalf("ListTaskInstances: %v", err)
	}
	out := map[string]model.TaskState{}
	for _, ti := range tis {
		out[ti.TaskID] = ti.State
	}
	return out
}

func TestLinearDAGRunsToSuccess(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID:         "linear",
		MaxActiveRuns: 1,
		StartDate:     time.Now().UTC(),
		Tasks: []model.Task{
			{ID: "extract", Type: "shell", Command: "echo extracting", Pool: model.DefaultPoolName},
			{ID: "transform", Type: "shell", Command: "echo transforming", Deps: []string{"extract"}, Pool: model.DefaultPoolName},
			{ID: "load", Type: "shell", Command: "echo loading", Deps: []string{"transform"}, Pool: model.DefaultPoolName},
		},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatalf("registerDAG: %v", err)
	}
	runID, err := s.TriggerManual(ctx, "linear", nil)
	if err != nil {
		t.Fatalf("TriggerManual: %v", err)
	}

	run := s.driveToTerminal(t, ctx, runID, 20)
	if run.State != model.RunSuccess {
		t.Fatalf("run state = %s, want success", run.State)
	}
	states := s.tiStates(t, ctx, runID)
	for _, id := range []string{"extract", "transform", "load"} {
		if states[id] != model.TaskSuccess {
			t.Errorf("task %s = %s, want success", id, states[id])
		}
	}
}

func TestFailurePropagationBlocksDownstream(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	// Diamond: a -> b(fail), a -> c, b -> d, c -> d.
	// b fails; d must become upstream_failed; c (parallel branch) must succeed;
	// run must be failed.
	dag := &model.DAG{
		DagID:         "diamond",
		MaxActiveRuns: 1,
		StartDate:     time.Now().UTC(),
		Tasks: []model.Task{
			{ID: "a", Command: "echo a", Pool: model.DefaultPoolName},
			{ID: "b", Command: "exit 1", Deps: []string{"a"}, Pool: model.DefaultPoolName},
			{ID: "c", Command: "echo c", Deps: []string{"a"}, Pool: model.DefaultPoolName},
			{ID: "d", Command: "echo d", Deps: []string{"b", "c"}, Pool: model.DefaultPoolName},
		},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatalf("registerDAG: %v", err)
	}
	runID, err := s.TriggerManual(ctx, "diamond", nil)
	if err != nil {
		t.Fatal(err)
	}

	run := s.driveToTerminal(t, ctx, runID, 20)
	if run.State != model.RunFailed {
		t.Fatalf("run state = %s, want failed", run.State)
	}
	states := s.tiStates(t, ctx, runID)
	want := map[string]model.TaskState{
		"a": model.TaskSuccess,
		"b": model.TaskFailed,
		"c": model.TaskSuccess,        // unrelated branch keeps running
		"d": model.TaskUpstreamFailed, // blocked by b
	}
	for id, w := range want {
		if states[id] != w {
			t.Errorf("task %s = %s, want %s", id, states[id], w)
		}
	}
}

func TestTriggerUnknownDAG(t *testing.T) {
	s := newTestScheduler(t)
	if _, err := s.TriggerManual(context.Background(), "ghost", nil); err == nil {
		t.Fatal("expected error triggering unknown dag")
	}
}
