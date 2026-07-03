package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/model"
)

func TestDownstreamClosure(t *testing.T) {
	// diamond: a -> b -> d ; a -> c -> d
	tasks := []model.Task{
		{ID: "a"}, {ID: "b", Deps: []string{"a"}}, {ID: "c", Deps: []string{"a"}}, {ID: "d", Deps: []string{"b", "c"}},
	}
	all := downstreamClosure(tasks, "a")
	for _, id := range []string{"a", "b", "c", "d"} {
		if !all[id] {
			t.Errorf("closure(a) missing %s", id)
		}
	}
	if leaf := downstreamClosure(tasks, "d"); len(leaf) != 1 || !leaf["d"] {
		t.Errorf("closure(d) = %v, want {d}", leaf)
	}
	if mid := downstreamClosure(tasks, "b"); !mid["b"] || !mid["d"] || mid["a"] || mid["c"] {
		t.Errorf("closure(b) = %v, want {b,d}", mid)
	}
}

// waitTI polls until a task reaches want, or fails.
func waitTI(t *testing.T, s *Scheduler, ctx context.Context, runID, taskID string, want model.TaskState, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s.tiStates(t, ctx, runID)[taskID] == want {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach %s within %s (got %s)", taskID, want, timeout, s.tiStates(t, ctx, runID)[taskID])
}

func TestCancelRun(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "cancel", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{{ID: "t", Command: "sleep 5", Pool: model.DefaultPoolName}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "cancel", nil)
	s.tickOnce(ctx) // dispatch the task (goroutine launches sleep 5)
	waitTI(t, s, ctx, runID, "t", model.TaskRunning, 2*time.Second)

	if err := s.CancelRun(ctx, runID); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	run, _ := s.store.GetDagRun(ctx, runID)
	if run.State != model.RunCancelled {
		t.Fatalf("run = %s, want cancelled", run.State)
	}
	// the poll goroutine must NOT overwrite the cancelled task with success/failure
	s.WaitInflight()
	if st := s.tiStates(t, ctx, runID)["t"]; st != model.TaskCancelled {
		t.Fatalf("task = %s, want cancelled (goroutine overwrote it)", st)
	}
	// cancelling an already-terminal run is refused
	if err := s.CancelRun(ctx, runID); !errors.Is(err, model.ErrRunNotActive) {
		t.Fatalf("re-cancel err = %v, want ErrRunNotActive", err)
	}
}

func TestRetryTask(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	// a fails → b (downstream) becomes upstream_failed
	dag := &model.DAG{
		DagID: "rt", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{
			{ID: "a", Command: "exit 1", Pool: model.DefaultPoolName},
			{ID: "b", Command: "echo b", Deps: []string{"a"}, Pool: model.DefaultPoolName},
		},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "rt", nil)
	if run := s.driveToTerminal(t, ctx, runID, 40); run.State != model.RunFailed {
		t.Fatalf("run = %s, want failed", run.State)
	}
	if st := s.tiStates(t, ctx, runID); st["a"] != model.TaskFailed || st["b"] != model.TaskUpstreamFailed {
		t.Fatalf("pre-retry states = %v", st)
	}
	// retry a → a and downstream b reset to scheduled, run reactivated to running
	if err := s.RetryTask(ctx, runID, "a"); err != nil {
		t.Fatalf("RetryTask: %v", err)
	}
	if st := s.tiStates(t, ctx, runID); st["a"] != model.TaskScheduled || st["b"] != model.TaskScheduled {
		t.Fatalf("post-retry states = %v, want both scheduled", st)
	}
	run, _ := s.store.GetDagRun(ctx, runID)
	if run.State != model.RunRunning {
		t.Fatalf("run = %s, want running after retry", run.State)
	}
	// drive again: a fails again → run failed again (proves the subtree re-ran)
	if run := s.driveToTerminal(t, ctx, runID, 40); run.State != model.RunFailed {
		t.Fatalf("re-run = %s, want failed", run.State)
	}
	// RetryRun on a run with no failed tasks... this one HAS failed tasks, so it works;
	// retrying a missing task is not found.
	if err := s.RetryTask(ctx, runID, "ghost"); err == nil {
		t.Fatal("retry of a missing task should error")
	}
}

// TestRetryRefusesActiveRun: retry is only allowed on a finished run.
func TestRetryRefusesActiveRun(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "act", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{{ID: "t", Command: "sleep 5", Pool: model.DefaultPoolName}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "act", nil)
	s.tickOnce(ctx)
	waitTI(t, s, ctx, runID, "t", model.TaskRunning, 2*time.Second)
	if err := s.RetryTask(ctx, runID, "t"); !errors.Is(err, model.ErrRunStillActive) {
		t.Fatalf("RetryTask on active run = %v, want ErrRunStillActive", err)
	}
	if err := s.RetryRun(ctx, runID); !errors.Is(err, model.ErrRunStillActive) {
		t.Fatalf("RetryRun on active run = %v, want ErrRunStillActive", err)
	}
	_ = s.CancelRun(ctx, runID) // clean up the sleep
	s.WaitInflight()
}

// TestPartialRetryLeftoverCancelled: cancel a 2-independent-task run, retry only
// one; the run must finalize as cancelled (not a clean success) and not trigger.
func TestPartialRetryLeftoverCancelled(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "part", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{
			{ID: "x", Command: "sleep 3", Pool: model.DefaultPoolName},
			{ID: "y", Command: "sleep 3", Pool: model.DefaultPoolName},
		},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "part", nil)
	s.tickOnce(ctx)
	waitTI(t, s, ctx, runID, "x", model.TaskRunning, 2*time.Second)
	if err := s.CancelRun(ctx, runID); err != nil {
		t.Fatal(err)
	}
	s.WaitInflight()
	if st := s.tiStates(t, ctx, runID); st["x"] != model.TaskCancelled || st["y"] != model.TaskCancelled {
		t.Fatalf("after cancel: %v, want both cancelled", s.tiStates(t, ctx, runID))
	}
	// retry only x
	if err := s.RetryTask(ctx, runID, "x"); err != nil {
		t.Fatalf("RetryTask(x): %v", err)
	}
	// drive: x succeeds; y stays cancelled → run must finalize cancelled, not success
	var run *model.DagRun
	for i := 0; i < 40; i++ {
		s.tickOnce(ctx)
		s.WaitInflight()
		run, _ = s.store.GetDagRun(ctx, runID)
		if run.State != model.RunRunning {
			break
		}
	}
	if run.State != model.RunCancelled {
		t.Fatalf("run finalized as %s, want cancelled (leftover cancelled task must not read as success)", run.State)
	}
	if st := s.tiStates(t, ctx, runID); st["x"] != model.TaskSuccess || st["y"] != model.TaskCancelled {
		t.Fatalf("final task states = %v, want x=success y=cancelled", s.tiStates(t, ctx, runID))
	}
}

// TestRetryRemovedTaskNoWedge: retrying/finalizing after a task is removed from
// the DAG must not wedge the run in `running` forever.
func TestRetryRemovedTaskNoWedge(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "rm", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{
			{ID: "a", Command: "exit 1", Pool: model.DefaultPoolName}, // retryable
			{ID: "b", Command: "exit 1", Pool: model.DefaultPoolName}, // will be removed (orphan)
		},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "rm", nil)
	if run := s.driveToTerminal(t, ctx, runID, 40); run.State != model.RunFailed {
		t.Fatalf("run = %s, want failed", run.State)
	}
	// remove task b from the DAG (its instance becomes an orphan)
	dag.Tasks = dag.Tasks[:1] // only "a"
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	// RetryRun retries a (valid); b (orphan) is NOT reactivated — so the run must
	// still finalize rather than wedge in `running` on a task with no dispatch path.
	if err := s.RetryRun(ctx, runID); err != nil {
		t.Fatalf("RetryRun: %v", err)
	}
	var run *model.DagRun
	for i := 0; i < 40; i++ {
		s.tickOnce(ctx)
		s.WaitInflight()
		run, _ = s.store.GetDagRun(ctx, runID)
		if run.State != model.RunRunning {
			break
		}
	}
	if run.State == model.RunRunning {
		t.Fatal("run wedged in running after retrying with a removed task")
	}
	// b's orphan instance stays in its terminal state; the run finalizes (failed).
	if run.State != model.RunFailed {
		t.Fatalf("run = %s, want failed", run.State)
	}
}
