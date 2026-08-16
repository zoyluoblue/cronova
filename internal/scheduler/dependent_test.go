package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/model"
)

// depFixture registers a target DAG "upstream_dep" and a waiting DAG whose
// single task depends on it, then creates a queued run of the waiter at a
// fixed logical date.
func depFixture(t *testing.T, s *Scheduler, dep *model.DependsOnDag, logical time.Time) (runID string) {
	t.Helper()
	ctx := context.Background()
	up := &model.DAG{
		DagID: "upstream_dep", MaxActiveRuns: 1, StartDate: time.Now().UTC().Add(-24 * time.Hour),
		Tasks: []model.Task{{ID: "t", Command: "true", Pool: model.DefaultPoolName}},
	}
	if err := s.registerDAG(ctx, up); err != nil {
		t.Fatal(err)
	}
	waiter := &model.DAG{
		DagID: "waiter", MaxActiveRuns: 1, StartDate: time.Now().UTC().Add(-24 * time.Hour),
		Tasks: []model.Task{{ID: "go", Command: "echo done", Pool: model.DefaultPoolName, DependsOnDag: dep}},
	}
	if err := s.registerDAG(ctx, waiter); err != nil {
		t.Fatal(err)
	}
	run := &model.DagRun{
		RunID: "waiter__test", DagID: "waiter", LogicalDate: logical,
		State: model.RunQueued, TriggerType: model.TriggerManual,
	}
	if err := s.store.CreateDagRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	return run.RunID
}

// upstreamRun inserts a target-DAG run at the given logical date and state.
func upstreamRun(t *testing.T, s *Scheduler, logical time.Time, state model.RunState) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	run := &model.DagRun{
		RunID: "upstream_dep__" + logical.Format("20060102T150405"), DagID: "upstream_dep",
		LogicalDate: logical, State: model.RunQueued, TriggerType: model.TriggerManual,
	}
	if err := s.store.CreateDagRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if state != model.RunQueued {
		if err := s.store.UpdateDagRunState(ctx, run.RunID, state, &now, &now); err != nil {
			t.Fatal(err)
		}
	}
}

func taskStateOf(t *testing.T, s *Scheduler, runID, taskID string) model.TaskState {
	t.Helper()
	tis, err := s.store.ListTaskInstances(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, ti := range tis {
		if ti.TaskID == taskID {
			return ti.State
		}
	}
	return ""
}

// TestDependentWaitsThenRuns: the waiter's task holds while the target period
// run is missing, then proceeds as soon as it succeeds.
func TestDependentWaitsThenRuns(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	logical := time.Date(2026, 8, 8, 6, 0, 0, 0, time.UTC)
	runID := depFixture(t, s, &model.DependsOnDag{Dag: "upstream_dep", OnTimeout: "fail"}, logical)

	for i := 0; i < 3; i++ {
		s.tickOnce(ctx)
	}
	if st := taskStateOf(t, s, runID, "go"); st != model.TaskScheduled {
		t.Fatalf("task = %s while dependency missing, want scheduled", st)
	}
	upstreamRun(t, s, logical, model.RunSuccess)
	if run := s.driveToTerminal(t, ctx, runID, 40); run.State != model.RunSuccess {
		t.Fatalf("run = %s, want success after dependency satisfied", run.State)
	}
}

// TestDependentIgnoresFailedTarget: a failed target run does NOT satisfy the
// wait (it may be retried) — the task keeps holding.
func TestDependentIgnoresFailedTarget(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	logical := time.Date(2026, 8, 8, 7, 0, 0, 0, time.UTC)
	runID := depFixture(t, s, &model.DependsOnDag{Dag: "upstream_dep", OnTimeout: "fail"}, logical)
	upstreamRun(t, s, logical, model.RunFailed)
	for i := 0; i < 3; i++ {
		s.tickOnce(ctx)
	}
	if st := taskStateOf(t, s, runID, "go"); st != model.TaskScheduled {
		t.Fatalf("task = %s with failed target, want still scheduled", st)
	}
}

// TestDependentOffset: offset "- 1d" awaits the previous day's period.
func TestDependentOffset(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	logical := time.Date(2026, 8, 8, 6, 0, 0, 0, time.UTC)
	runID := depFixture(t, s, &model.DependsOnDag{Dag: "upstream_dep", Offset: "- 1d", OnTimeout: "fail"}, logical)
	// A run at the SAME date must not satisfy an offset of -1d.
	upstreamRun(t, s, logical, model.RunSuccess)
	for i := 0; i < 3; i++ {
		s.tickOnce(ctx)
	}
	if st := taskStateOf(t, s, runID, "go"); st != model.TaskScheduled {
		t.Fatalf("task = %s, want scheduled (same-day run must not satisfy -1d)", st)
	}
	upstreamRun(t, s, logical.AddDate(0, 0, -1), model.RunSuccess)
	if run := s.driveToTerminal(t, ctx, runID, 40); run.State != model.RunSuccess {
		t.Fatalf("run = %s, want success via -1d target", run.State)
	}
}

// TestDependentTimeoutSkip: on_timeout: skip resolves the wait as a skipped
// task and the run completes.
func TestDependentTimeoutSkip(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	logical := time.Date(2026, 8, 8, 8, 0, 0, 0, time.UTC)
	runID := depFixture(t, s, &model.DependsOnDag{Dag: "upstream_dep", Timeout: 1, OnTimeout: "skip"}, logical)
	s.tickOnce(ctx) // run starts; timeout clock anchors at StartedAt
	time.Sleep(1200 * time.Millisecond)
	run := s.driveToTerminal(t, ctx, runID, 40)
	if st := taskStateOf(t, s, runID, "go"); st != model.TaskSkipped {
		t.Fatalf("task = %s, want skipped after timeout", st)
	}
	if run.State != model.RunSuccess {
		t.Fatalf("run = %s, want success (all-skipped run finalizes success)", run.State)
	}
}

// TestDependentTimeoutFail: default on_timeout fails the task and the run.
func TestDependentTimeoutFail(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	logical := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)
	runID := depFixture(t, s, &model.DependsOnDag{Dag: "upstream_dep", Timeout: 1, OnTimeout: "fail"}, logical)
	s.tickOnce(ctx)
	time.Sleep(1200 * time.Millisecond)
	run := s.driveToTerminal(t, ctx, runID, 40)
	if st := taskStateOf(t, s, runID, "go"); st != model.TaskFailed {
		t.Fatalf("task = %s, want failed after timeout", st)
	}
	if run.State != model.RunFailed {
		t.Fatalf("run = %s, want failed", run.State)
	}
}
