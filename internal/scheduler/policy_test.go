package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/model"
)

// waitRunState ticks until the run reaches want (or fails the test).
func (s *Scheduler) waitRunState(t *testing.T, ctx context.Context, runID string, want model.RunState, maxTicks int) {
	t.Helper()
	for i := 0; i < maxTicks; i++ {
		s.tickOnce(ctx)
		run, err := s.store.GetDagRun(ctx, runID)
		if err != nil {
			t.Fatalf("GetDagRun: %v", err)
		}
		if run.State == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	run, _ := s.store.GetDagRun(ctx, runID)
	t.Fatalf("run %s state = %s after %d ticks, want %s", runID, run.State, maxTicks, want)
}

// TestSerialWaitAdmitsOneRun: serial_wait admits one active run at a time even
// when max_active_runs allows more; the second run waits, then completes.
func TestSerialWaitAdmitsOneRun(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	d := &model.DAG{
		DagID: "p_wait", MaxActiveRuns: 5, ExecutionPolicy: model.PolicySerialWait,
		StartDate: time.Now().UTC(),
		Tasks:     []model.Task{{ID: "work", Command: "sleep 0.3", Pool: model.DefaultPoolName}},
	}
	if err := s.registerDAG(ctx, d); err != nil {
		t.Fatal(err)
	}
	r1, _ := s.TriggerManual(ctx, "p_wait", nil)
	r2, _ := s.TriggerManual(ctx, "p_wait", nil)
	s.tickOnce(ctx)
	run1, _ := s.store.GetDagRun(ctx, r1)
	run2, _ := s.store.GetDagRun(ctx, r2)
	if run1.State != model.RunRunning {
		t.Fatalf("run1 = %s, want running", run1.State)
	}
	if run2.State != model.RunQueued {
		t.Fatalf("run2 = %s, want queued (serial_wait holds it)", run2.State)
	}
	// Ticking again while run1 is still going must not admit run2.
	s.tickOnce(ctx)
	if run2, _ = s.store.GetDagRun(ctx, r2); run2.State != model.RunQueued {
		t.Fatalf("run2 = %s while run1 active, want queued", run2.State)
	}
	// Once run1 finishes, run2 gets its turn and completes.
	s.waitRunState(t, ctx, r1, model.RunSuccess, 60)
	s.waitRunState(t, ctx, r2, model.RunSuccess, 60)
}

// TestSerialDiscardCancelsWhileBusy: a run arriving while another is active is
// finalized cancelled, visibly, and the active run is unaffected.
func TestSerialDiscardCancelsWhileBusy(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	d := &model.DAG{
		DagID: "p_disc", MaxActiveRuns: 1, ExecutionPolicy: model.PolicySerialDiscard,
		StartDate: time.Now().UTC(),
		Tasks:     []model.Task{{ID: "work", Command: "sleep 0.4", Pool: model.DefaultPoolName}},
	}
	if err := s.registerDAG(ctx, d); err != nil {
		t.Fatal(err)
	}
	r1, _ := s.TriggerManual(ctx, "p_disc", nil)
	s.tickOnce(ctx)
	if run1, _ := s.store.GetDagRun(ctx, r1); run1.State != model.RunRunning {
		t.Fatalf("run1 = %s, want running", run1.State)
	}
	r2, _ := s.TriggerManual(ctx, "p_disc", nil)
	s.tickOnce(ctx)
	run2, _ := s.store.GetDagRun(ctx, r2)
	if run2.State != model.RunCancelled {
		t.Fatalf("run2 = %s, want cancelled (discarded while busy)", run2.State)
	}
	s.waitRunState(t, ctx, r1, model.RunSuccess, 60)
}

// TestSerialPriorityDrainsHighestFirst: with one run active and two queued,
// the higher-priority queued run starts first once the active one finishes.
func TestSerialPriorityDrainsHighestFirst(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	d := &model.DAG{
		DagID: "p_pri", MaxActiveRuns: 1, ExecutionPolicy: model.PolicySerialPriority,
		StartDate: time.Now().UTC(),
		Tasks:     []model.Task{{ID: "work", Command: "sleep 0.2", Pool: model.DefaultPoolName}},
	}
	if err := s.registerDAG(ctx, d); err != nil {
		t.Fatal(err)
	}
	r1, _ := s.TriggerManualPriority(ctx, "p_pri", nil, 0)
	s.tickOnce(ctx)
	rLow, _ := s.TriggerManualPriority(ctx, "p_pri", nil, 1)
	rHigh, _ := s.TriggerManualPriority(ctx, "p_pri", nil, 10)
	s.waitRunState(t, ctx, r1, model.RunSuccess, 60)
	// Next admission must pick the priority-10 run over the earlier priority-1 one.
	s.tickOnce(ctx)
	high, _ := s.store.GetDagRun(ctx, rHigh)
	low, _ := s.store.GetDagRun(ctx, rLow)
	if high.State != model.RunRunning && high.State != model.RunSuccess {
		t.Fatalf("high-priority run = %s, want running/success", high.State)
	}
	if low.State != model.RunQueued {
		t.Fatalf("low-priority run = %s, want still queued", low.State)
	}
	s.waitRunState(t, ctx, rHigh, model.RunSuccess, 60)
	s.waitRunState(t, ctx, rLow, model.RunSuccess, 60)
}

// TestGlobalTaskPriorityAcrossRuns: two DAGs compete for a 1-slot pool in the
// same tick; the higher task priority wins the slot regardless of which DAG
// was processed first.
func TestGlobalTaskPriorityAcrossRuns(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	if err := s.store.UpsertPool(ctx, &model.Pool{Name: "tight", Slots: 1}); err != nil {
		t.Fatal(err)
	}
	lo := &model.DAG{
		DagID: "aa_lo", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{{ID: "t", Command: "sleep 0.3", Pool: "tight", Priority: 0}},
	}
	hi := &model.DAG{
		DagID: "zz_hi", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{{ID: "t", Command: "sleep 0.3", Pool: "tight", Priority: 5}},
	}
	// Register/trigger the low-priority DAG FIRST: under the old per-run
	// dispatch it would grab the slot by arrival order.
	if err := s.registerDAG(ctx, lo); err != nil {
		t.Fatal(err)
	}
	if err := s.registerDAG(ctx, hi); err != nil {
		t.Fatal(err)
	}
	rLo, _ := s.TriggerManual(ctx, "aa_lo", nil)
	rHi, _ := s.TriggerManual(ctx, "zz_hi", nil)
	s.tickOnce(ctx)

	tiLo := taskState(t, s, ctx, rLo, "t")
	tiHi := taskState(t, s, ctx, rHi, "t")
	if tiHi != model.TaskQueued && tiHi != model.TaskRunning && tiHi != model.TaskSuccess {
		t.Fatalf("high-priority task = %s, want dispatched", tiHi)
	}
	if tiLo != model.TaskScheduled {
		t.Fatalf("low-priority task = %s, want scheduled (waiting for the pool)", tiLo)
	}
	s.waitRunState(t, ctx, rHi, model.RunSuccess, 60)
	s.waitRunState(t, ctx, rLo, model.RunSuccess, 60)
}

// TestRunPriorityBeatsTaskPriority: an urgent run (run priority) wins the pool
// slot even against a task with higher task-level priority.
func TestRunPriorityBeatsTaskPriority(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	if err := s.store.UpsertPool(ctx, &model.Pool{Name: "tight2", Slots: 1}); err != nil {
		t.Fatal(err)
	}
	a := &model.DAG{
		DagID: "dag_a", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{{ID: "t", Command: "sleep 0.3", Pool: "tight2", Priority: 50}},
	}
	b := &model.DAG{
		DagID: "dag_b", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{{ID: "t", Command: "sleep 0.3", Pool: "tight2", Priority: 0}},
	}
	if err := s.registerDAG(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := s.registerDAG(ctx, b); err != nil {
		t.Fatal(err)
	}
	rA, _ := s.TriggerManual(ctx, "dag_a", nil) // high task priority, default run priority
	rB, _ := s.TriggerManualPriority(ctx, "dag_b", nil, 10)
	s.tickOnce(ctx)
	if st := taskState(t, s, ctx, rA, "t"); st != model.TaskScheduled {
		t.Fatalf("dag_a task = %s, want scheduled (outranked by run priority)", st)
	}
	if st := taskState(t, s, ctx, rB, "t"); st == model.TaskScheduled {
		t.Fatalf("dag_b task = %s, want dispatched", st)
	}
	s.waitRunState(t, ctx, rB, model.RunSuccess, 60)
	s.waitRunState(t, ctx, rA, model.RunSuccess, 60)
}

func taskState(t *testing.T, s *Scheduler, ctx context.Context, runID, taskID string) model.TaskState {
	t.Helper()
	tis, err := s.store.ListTaskInstances(ctx, runID)
	if err != nil {
		t.Fatalf("ListTaskInstances: %v", err)
	}
	for _, ti := range tis {
		if ti.TaskID == taskID {
			return ti.State
		}
	}
	t.Fatalf("task %s not found in run %s", taskID, runID)
	return ""
}
