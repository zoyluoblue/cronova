package scheduler

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/model"
)

func registerSimpleDAG(t *testing.T, s *Scheduler, id, command string) {
	t.Helper()
	d := &model.DAG{
		DagID: id, MaxActiveRuns: 5, StartDate: time.Now().UTC().Add(-time.Hour),
		Tasks: []model.Task{{ID: "work", Command: command, Pool: model.DefaultPoolName}},
	}
	if err := s.registerDAG(context.Background(), d); err != nil {
		t.Fatal(err)
	}
}

func registerParent(t *testing.T, s *Scheduler, id, target string, retries int) {
	t.Helper()
	d := &model.DAG{
		DagID: id, MaxActiveRuns: 5, StartDate: time.Now().UTC().Add(-time.Hour),
		Tasks: []model.Task{{ID: "child", Type: "subdag", Subdag: target, Pool: model.DefaultPoolName, Retries: retries}},
	}
	if err := s.registerDAG(context.Background(), d); err != nil {
		t.Fatal(err)
	}
}

// childOf returns the child run the parent's subdag task points at.
func childOf(t *testing.T, s *Scheduler, parentRunID string) *model.DagRun {
	t.Helper()
	ctx := context.Background()
	tis, err := s.store.ListTaskInstances(ctx, parentRunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, ti := range tis {
		if child, ok := cutSubdagRef(ti.ExecutorRef); ok {
			run, err := s.store.GetDagRun(ctx, child)
			if err != nil {
				t.Fatalf("child run %s: %v", child, err)
			}
			return run
		}
	}
	return nil
}

func cutSubdagRef(ref string) (string, bool) {
	if len(ref) > len(subdagRefPrefix) && ref[:len(subdagRefPrefix)] == subdagRefPrefix {
		return ref[len(subdagRefPrefix):], true
	}
	return "", false
}

// TestSubdagRunsChild: the parent's subdag task creates a linked child run,
// mirrors its success, and the child records its parentage.
func TestSubdagRunsChild(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	registerSimpleDAG(t, s, "sub_target", "echo child-ran")
	registerParent(t, s, "sub_parent", "sub_target", 0)
	runID, err := s.TriggerManual(ctx, "sub_parent", nil)
	if err != nil {
		t.Fatal(err)
	}
	run := s.driveToTerminal(t, ctx, runID, 60)
	if run.State != model.RunSuccess {
		t.Fatalf("parent run = %s, want success", run.State)
	}
	child := childOf(t, s, runID)
	if child == nil {
		t.Fatal("no child run linked")
	}
	if child.State != model.RunSuccess {
		t.Errorf("child run = %s, want success", child.State)
	}
	if child.ParentRunID != runID {
		t.Errorf("child parent_run_id = %q, want %q", child.ParentRunID, runID)
	}
	if child.TriggerType != model.TriggerSubdag {
		t.Errorf("child trigger = %s, want subdag", child.TriggerType)
	}
}

// TestSubdagChildFailurePropagates: a failing child fails the parent task and run.
func TestSubdagChildFailurePropagates(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	registerSimpleDAG(t, s, "sub_boom", "exit 1")
	registerParent(t, s, "sub_parent2", "sub_boom", 0)
	runID, _ := s.TriggerManual(ctx, "sub_parent2", nil)
	run := s.driveToTerminal(t, ctx, runID, 60)
	if run.State != model.RunFailed {
		t.Fatalf("parent run = %s, want failed", run.State)
	}
	if child := childOf(t, s, runID); child == nil || child.State != model.RunFailed {
		t.Fatalf("child = %+v, want failed", child)
	}
}

// TestSubdagCancelCascades: cancelling the parent cancels the in-flight child.
func TestSubdagCancelCascades(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	registerSimpleDAG(t, s, "sub_slow", "sleep 5")
	registerParent(t, s, "sub_parent3", "sub_slow", 0)
	runID, _ := s.TriggerManual(ctx, "sub_parent3", nil)
	// Tick until the child exists and is underway.
	var child *model.DagRun
	for i := 0; i < 40 && child == nil; i++ {
		s.tickOnce(ctx)
		child = childOf(t, s, runID)
		time.Sleep(20 * time.Millisecond)
	}
	if child == nil {
		t.Fatal("child never created")
	}
	if err := s.CancelRun(ctx, runID); err != nil {
		t.Fatal(err)
	}
	s.WaitInflight()
	got, _ := s.store.GetDagRun(ctx, child.RunID)
	if got.State != model.RunCancelled {
		t.Fatalf("child = %s after parent cancel, want cancelled", got.State)
	}
	parent, _ := s.store.GetDagRun(ctx, runID)
	if parent.State != model.RunCancelled {
		t.Fatalf("parent = %s, want cancelled", parent.State)
	}
}

// TestSubdagAutoRetryNewChild: the parent task's retry launches a NEW child
// run — the failed one stays behind as history — and the parent ends success.
func TestSubdagAutoRetryNewChild(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	marker := filepath.Join(t.TempDir(), "second-try")
	// Fails on the first pass, succeeds once the marker exists.
	registerSimpleDAG(t, s, "sub_flaky", "test -f "+marker+" || { touch "+marker+"; exit 1; }")
	registerParent(t, s, "sub_parent4", "sub_flaky", 1)
	runID, _ := s.TriggerManual(ctx, "sub_parent4", nil)
	run := s.driveToTerminal(t, ctx, runID, 120)
	if run.State != model.RunSuccess {
		t.Fatalf("parent run = %s, want success after retry", run.State)
	}
	children, err := s.store.ListDagRuns(ctx, "sub_flaky", 10)
	if err != nil {
		t.Fatal(err)
	}
	var failed, succeeded int
	for _, c := range children {
		if c.ParentRunID != runID {
			t.Errorf("child %s parent = %q, want %q", c.RunID, c.ParentRunID, runID)
		}
		switch c.State {
		case model.RunFailed:
			failed++
		case model.RunSuccess:
			succeeded++
		}
	}
	if failed != 1 || succeeded != 1 {
		t.Fatalf("children = %d failed / %d success, want 1/1 (old run kept as history)", failed, succeeded)
	}
}

// TestSubdagDepthGuard: mutually recursive subdags stop at the depth limit
// instead of spawning children forever.
func TestSubdagDepthGuard(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	// a -> b -> a -> ... registered afterward so save-time knowledge can't help.
	da := &model.DAG{DagID: "cyc_a", MaxActiveRuns: 10, StartDate: time.Now().UTC().Add(-time.Hour),
		Tasks: []model.Task{{ID: "t", Type: "subdag", Subdag: "cyc_b", Pool: model.DefaultPoolName}}}
	db := &model.DAG{DagID: "cyc_b", MaxActiveRuns: 10, StartDate: time.Now().UTC().Add(-time.Hour),
		Tasks: []model.Task{{ID: "t", Type: "subdag", Subdag: "cyc_a", Pool: model.DefaultPoolName}}}
	if err := s.registerDAG(ctx, da); err != nil {
		t.Fatal(err)
	}
	if err := s.registerDAG(ctx, db); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "cyc_a", nil)
	run := s.driveToTerminal(t, ctx, runID, 200)
	if run.State != model.RunFailed {
		t.Fatalf("cyclic parent = %s, want failed via depth guard", run.State)
	}
}
