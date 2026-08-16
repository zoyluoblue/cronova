package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/model"
)

// TestHoldBlocksNewDispatch: holding a mid-flight run lets the running task
// finish but keeps the downstream task un-dispatched until release; after
// release the run completes normally (plan2 R5B: hold != suspend).
func TestHoldBlocksNewDispatch(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	d := &model.DAG{
		DagID: "holdme", MaxActiveRuns: 1, StartDate: time.Now().UTC().Add(-time.Hour),
		Tasks: []model.Task{
			{ID: "first", Command: "sleep 0.2", Pool: model.DefaultPoolName},
			{ID: "second", Command: "echo done", Pool: model.DefaultPoolName, Deps: []string{"first"}},
		},
	}
	if err := s.registerDAG(ctx, d); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "holdme", nil)
	s.tickOnce(ctx) // first starts
	if err := s.store.SetDagRunHeld(ctx, runID, true); err != nil {
		t.Fatal(err)
	}
	// Let first finish, then tick repeatedly: second must stay scheduled.
	time.Sleep(400 * time.Millisecond)
	for i := 0; i < 5; i++ {
		s.tickOnce(ctx)
		s.WaitInflight()
	}
	if st := taskStateOf(t, s, runID, "second"); st != model.TaskScheduled {
		t.Fatalf("second = %s while held, want scheduled", st)
	}
	run, _ := s.store.GetDagRun(ctx, runID)
	if run.State.IsTerminal() {
		t.Fatalf("held run finalized to %s, want frozen active", run.State)
	}
	// Release: the run resumes and completes.
	if err := s.store.SetDagRunHeld(ctx, runID, false); err != nil {
		t.Fatal(err)
	}
	if run := s.driveToTerminal(t, ctx, runID, 60); run.State != model.RunSuccess {
		t.Fatalf("run = %s after release, want success", run.State)
	}
}

// TestHeldQueuedNotPromoted: a held queued run is skipped by promotion until
// released.
func TestHeldQueuedNotPromoted(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	d := &model.DAG{
		DagID: "holdq", MaxActiveRuns: 1, StartDate: time.Now().UTC().Add(-time.Hour),
		Tasks: []model.Task{{ID: "t", Command: "echo hi", Pool: model.DefaultPoolName}},
	}
	if err := s.registerDAG(ctx, d); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "holdq", nil)
	if err := s.store.SetDagRunHeld(ctx, runID, true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		s.tickOnce(ctx)
	}
	run, _ := s.store.GetDagRun(ctx, runID)
	if run.State != model.RunQueued {
		t.Fatalf("held queued run = %s, want still queued", run.State)
	}
	_ = s.store.SetDagRunHeld(ctx, runID, false)
	if run := s.driveToTerminal(t, ctx, runID, 60); run.State != model.RunSuccess {
		t.Fatalf("run = %s after release, want success", run.State)
	}
}
