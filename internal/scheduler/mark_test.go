package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/model"
)

// TestMarkTaskSuccessReleasesDownstream: marking a failed task success clears the
// downstream upstream_failed task and the run re-drives to success.
func TestMarkTaskSuccessReleasesDownstream(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "mk", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{
			{ID: "a", Command: "exit 1", Pool: model.DefaultPoolName},
			{ID: "b", Command: "echo b", Deps: []string{"a"}, Pool: model.DefaultPoolName},
		},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "mk", nil)
	if run := s.driveToTerminal(t, ctx, runID, 40); run.State != model.RunFailed {
		t.Fatalf("pre-mark run = %s, want failed", run.State)
	}
	if st := s.tiStates(t, ctx, runID); st["a"] != model.TaskFailed || st["b"] != model.TaskUpstreamFailed {
		t.Fatalf("pre-mark states = %v", st)
	}

	if err := s.MarkTask(ctx, runID, "a", model.TaskSuccess); err != nil {
		t.Fatalf("MarkTask: %v", err)
	}
	if st := s.tiStates(t, ctx, runID); st["a"] != model.TaskSuccess || st["b"] != model.TaskScheduled {
		t.Fatalf("post-mark states = %v, want a=success b=scheduled", st)
	}
	run, _ := s.store.GetDagRun(ctx, runID)
	if run.State != model.RunRunning {
		t.Fatalf("run = %s, want running after mark", run.State)
	}
	if run := s.driveToTerminal(t, ctx, runID, 40); run.State != model.RunSuccess {
		t.Fatalf("post-drive run = %s, want success", run.State)
	}
	if st := s.tiStates(t, ctx, runID)["b"]; st != model.TaskSuccess {
		t.Fatalf("b = %s, want success (released and ran)", st)
	}
}

// TestMarkRunningTaskSuccess: marking a running task kills its process; the poll
// goroutine must not clobber the override.
func TestMarkRunningTaskSuccess(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "mkr", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{{ID: "t", Command: "sleep 5", Pool: model.DefaultPoolName}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "mkr", nil)
	s.tickOnce(ctx)
	waitTI(t, s, ctx, runID, "t", model.TaskRunning, 2*time.Second)

	if err := s.MarkTask(ctx, runID, "t", model.TaskSuccess); err != nil {
		t.Fatalf("MarkTask: %v", err)
	}
	s.WaitInflight() // the poll goroutine must not overwrite the marked state
	if st := s.tiStates(t, ctx, runID)["t"]; st != model.TaskSuccess {
		t.Fatalf("task = %s, want success (poll goroutine clobbered it)", st)
	}
	if run := s.driveToTerminal(t, ctx, runID, 40); run.State != model.RunSuccess {
		t.Fatalf("run = %s, want success", run.State)
	}
}

// TestMarkTaskSkipped: a skipped task is terminal and non-failing, so the run
// finalizes success.
func TestMarkTaskSkipped(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "sk", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{{ID: "a", Command: "exit 1", Pool: model.DefaultPoolName}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "sk", nil)
	if run := s.driveToTerminal(t, ctx, runID, 40); run.State != model.RunFailed {
		t.Fatalf("run = %s, want failed", run.State)
	}
	if err := s.MarkTask(ctx, runID, "a", model.TaskSkipped); err != nil {
		t.Fatalf("MarkTask: %v", err)
	}
	if run := s.driveToTerminal(t, ctx, runID, 40); run.State != model.RunSuccess {
		t.Fatalf("run = %s, want success after skip", run.State)
	}
	if st := s.tiStates(t, ctx, runID)["a"]; st != model.TaskSkipped {
		t.Fatalf("a = %s, want skipped", st)
	}
}

// TestMarkTaskFailedRefinalizesRun: marking a succeeded task failed re-drives the
// terminal run to failed.
func TestMarkTaskFailedRefinalizesRun(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "mf", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{{ID: "a", Command: "echo ok", Pool: model.DefaultPoolName}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "mf", nil)
	if run := s.driveToTerminal(t, ctx, runID, 40); run.State != model.RunSuccess {
		t.Fatalf("run = %s, want success", run.State)
	}
	if err := s.MarkTask(ctx, runID, "a", model.TaskFailed); err != nil {
		t.Fatalf("MarkTask: %v", err)
	}
	if run := s.driveToTerminal(t, ctx, runID, 40); run.State != model.RunFailed {
		t.Fatalf("run = %s, want failed after mark", run.State)
	}
}

// TestMarkRunFlipsTerminalState: a finished run's outcome can be overridden, and
// the tick must not revert it.
func TestMarkRunFlipsTerminalState(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "mr", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{{ID: "a", Command: "exit 1", Pool: model.DefaultPoolName}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "mr", nil)
	if run := s.driveToTerminal(t, ctx, runID, 40); run.State != model.RunFailed {
		t.Fatalf("run = %s, want failed", run.State)
	}
	if err := s.MarkRun(ctx, runID, model.RunSuccess); err != nil {
		t.Fatalf("MarkRun: %v", err)
	}
	if run, _ := s.store.GetDagRun(ctx, runID); run.State != model.RunSuccess {
		t.Fatalf("run = %s, want success", run.State)
	}
	// idempotent, and a tick must not reprocess a terminal run back to failed
	if err := s.MarkRun(ctx, runID, model.RunSuccess); err != nil {
		t.Fatalf("idempotent MarkRun: %v", err)
	}
	s.tickOnce(ctx)
	if run, _ := s.store.GetDagRun(ctx, runID); run.State != model.RunSuccess {
		t.Fatalf("tick reverted the mark: %s", run.State)
	}
}

// TestMarkRunRefusesActive: an active run's state is tick-derived, so overriding
// it is refused (cancel or mark tasks instead).
func TestMarkRunRefusesActive(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "ma", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{{ID: "t", Command: "sleep 5", Pool: model.DefaultPoolName}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "ma", nil)
	s.tickOnce(ctx)
	waitTI(t, s, ctx, runID, "t", model.TaskRunning, 2*time.Second)
	if err := s.MarkRun(ctx, runID, model.RunSuccess); !errors.Is(err, model.ErrRunStillActive) {
		t.Fatalf("MarkRun on active run = %v, want ErrRunStillActive", err)
	}
	_ = s.CancelRun(ctx, runID)
	s.WaitInflight()
}

// TestMarkTaskSuppressesReNotify: reactivating a finished run to apply a mark must
// NOT re-fire the notify webhook — the operator already saw it finish.
func TestMarkTaskSuppressesReNotify(t *testing.T) {
	url, bodies := captureHook(t)
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "mn", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		NotifyURL: url, NotifyOn: []string{"failure"},
		Tasks: []model.Task{{ID: "a", Command: "exit 1", Pool: model.DefaultPoolName}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "mn", nil)
	if run := s.driveToTerminal(t, ctx, runID, 40); run.State != model.RunFailed {
		t.Fatalf("run = %s, want failed", run.State)
	}
	s.WaitInflight()
	if n := len(bodies()); n != 1 {
		t.Fatalf("initial webhook count = %d, want 1", n)
	}
	// re-mark the failed task failed → run re-finalizes failed, but no 2nd webhook
	if err := s.MarkTask(ctx, runID, "a", model.TaskFailed); err != nil {
		t.Fatalf("MarkTask: %v", err)
	}
	if run := s.driveToTerminal(t, ctx, runID, 40); run.State != model.RunFailed {
		t.Fatalf("re-finalized run = %s, want failed", run.State)
	}
	s.WaitInflight()
	if n := len(bodies()); n != 1 {
		t.Fatalf("webhook count after mark = %d, want 1 (re-finalize must not re-notify)", n)
	}
}

// TestMarkRejectsBadState: only success/failed/skipped (task) and success/failed
// (run) are legal targets.
func TestMarkRejectsBadState(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "bad", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{{ID: "a", Command: "echo ok", Pool: model.DefaultPoolName}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "bad", nil)
	s.driveToTerminal(t, ctx, runID, 40)
	if err := s.MarkTask(ctx, runID, "a", model.TaskRunning); !errors.Is(err, model.ErrBadMarkState) {
		t.Fatalf("marking task running = %v, want ErrBadMarkState", err)
	}
	if err := s.MarkRun(ctx, runID, model.RunCancelled); !errors.Is(err, model.ErrBadMarkState) {
		t.Fatalf("marking run cancelled = %v, want ErrBadMarkState", err)
	}
	if err := s.MarkTask(ctx, runID, "ghost", model.TaskSuccess); err == nil {
		t.Fatal("marking a missing task should error")
	}
}
