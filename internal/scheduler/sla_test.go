package scheduler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/model"
)

// tickUntilTerminal drives the loop with real time passing (so elapsed-based
// deadlines fire) until the run is terminal or the budget runs out.
func tickUntilTerminal(t *testing.T, s *Scheduler, ctx context.Context, runID string, budget time.Duration) *model.DagRun {
	t.Helper()
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		s.tickOnce(ctx)
		run, err := s.store.GetDagRun(ctx, runID)
		if err != nil {
			t.Fatalf("GetDagRun: %v", err)
		}
		if run.State.IsTerminal() {
			return run
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("run %s did not terminate within %s", runID, budget)
	return nil
}

func countState(t *testing.T, bodies [][]byte, state string) int {
	t.Helper()
	n := 0
	for _, b := range bodies {
		var p map[string]any
		if err := json.Unmarshal(b, &p); err != nil {
			t.Fatalf("payload: %v", err)
		}
		if p["state"] == state {
			n++
		}
	}
	return n
}

// TestDagrunTimeout: a run exceeding dagrun_timeout is force-failed to timed_out,
// its running task killed → timed_out, and a failure webhook fires (state=timed_out).
func TestDagrunTimeout(t *testing.T) {
	url, bodies := captureHook(t)
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "to", MaxActiveRuns: 1, StartDate: time.Now().UTC(), DagrunTimeout: 1,
		NotifyURL: url, NotifyOn: []string{"failure"},
		Tasks: []model.Task{{ID: "t", Command: "sleep 10", Pool: model.DefaultPoolName}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "to", nil)
	run := tickUntilTerminal(t, s, ctx, runID, 5*time.Second)
	if run.State != model.RunTimedOut {
		t.Fatalf("run = %s, want timed_out", run.State)
	}
	s.WaitInflight()
	if st := s.tiStates(t, ctx, runID)["t"]; st != model.TaskTimedOut {
		t.Fatalf("task = %s, want timed_out", st)
	}
	if n := countState(t, bodies(), "timed_out"); n != 1 {
		t.Fatalf("timed_out webhooks = %d, want 1", n)
	}
}

// TestRetryRunTimedOut: a run killed by dagrun_timeout can be retried (RetryRun
// must treat timed_out tasks as retryable, matching what the UI offers).
func TestRetryRunTimedOut(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "rto", MaxActiveRuns: 1, StartDate: time.Now().UTC(), DagrunTimeout: 1,
		Tasks: []model.Task{{ID: "t", Command: "sleep 10", Pool: model.DefaultPoolName}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "rto", nil)
	if run := tickUntilTerminal(t, s, ctx, runID, 5*time.Second); run.State != model.RunTimedOut {
		t.Fatalf("run = %s, want timed_out", run.State)
	}
	s.WaitInflight()
	// retry must succeed (not ErrNothingToRetry) and reactivate the run
	if err := s.RetryRun(ctx, runID); err != nil {
		t.Fatalf("RetryRun on timed_out run: %v", err)
	}
	if st := s.tiStates(t, ctx, runID)["t"]; st != model.TaskScheduled {
		t.Fatalf("task = %s, want scheduled after retry", st)
	}
	if run, _ := s.store.GetDagRun(ctx, runID); run.State != model.RunRunning {
		t.Fatalf("run = %s, want running after retry", run.State)
	}
}

// TestRetryTimedOutRunGetsFreshWindow: retrying a timed-out run must restart the
// deadline clock — otherwise elapsed-from-original-start already exceeds
// dagrun_timeout and the run re-times-out on the very first tick, never progressing.
func TestRetryTimedOutRunGetsFreshWindow(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "rfw", MaxActiveRuns: 1, StartDate: time.Now().UTC(), DagrunTimeout: 1,
		Tasks: []model.Task{{ID: "t", Command: "sleep 10", Pool: model.DefaultPoolName}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "rfw", nil)
	if run := tickUntilTerminal(t, s, ctx, runID, 5*time.Second); run.State != model.RunTimedOut {
		t.Fatalf("run = %s, want timed_out", run.State)
	}
	s.WaitInflight()
	if err := s.RetryRun(ctx, runID); err != nil {
		t.Fatalf("RetryRun: %v", err)
	}
	s.tickOnce(ctx) // one fast tick after retry — a fresh window means NO immediate re-timeout
	run, _ := s.store.GetDagRun(ctx, runID)
	if run.State == model.RunTimedOut {
		t.Fatal("run re-timed-out on the first tick after retry — deadline clock was not reset")
	}
	if run.State != model.RunRunning {
		t.Fatalf("run = %s, want running", run.State)
	}
	_ = s.CancelRun(ctx, runID) // clean up the re-dispatched sleep
	s.WaitInflight()
}

// TestDagrunTimeoutAlertsWithoutNotifyOn: a hard timeout alerts whenever a webhook
// is configured, even with notify_on empty (the threshold is the opt-in).
func TestDagrunTimeoutAlertsWithoutNotifyOn(t *testing.T) {
	url, bodies := captureHook(t)
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "toa", MaxActiveRuns: 1, StartDate: time.Now().UTC(), DagrunTimeout: 1,
		NotifyURL: url, // NotifyOn intentionally empty
		Tasks:     []model.Task{{ID: "t", Command: "sleep 10", Pool: model.DefaultPoolName}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "toa", nil)
	if run := tickUntilTerminal(t, s, ctx, runID, 5*time.Second); run.State != model.RunTimedOut {
		t.Fatalf("run = %s, want timed_out", run.State)
	}
	s.WaitInflight()
	if n := countState(t, bodies(), "timed_out"); n != 1 {
		t.Fatalf("timed_out webhooks = %d, want 1 (must fire even with empty notify_on)", n)
	}
}

// TestRunSLAAlert: a run past its SLA fires exactly one sla_miss alert and keeps
// running to a normal finish.
func TestRunSLAAlert(t *testing.T) {
	url, bodies := captureHook(t)
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "sla", MaxActiveRuns: 1, StartDate: time.Now().UTC(), SLA: 1,
		NotifyURL: url, // SLA fires whenever a webhook is set (no notify_on needed)
		Tasks:     []model.Task{{ID: "t", Command: "sleep 3", Pool: model.DefaultPoolName}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "sla", nil)
	run := tickUntilTerminal(t, s, ctx, runID, 6*time.Second)
	if run.State != model.RunSuccess {
		t.Fatalf("run = %s, want success (SLA is soft)", run.State)
	}
	s.WaitInflight()
	if n := countState(t, bodies(), "sla_miss"); n != 1 {
		t.Fatalf("sla_miss alerts = %d, want exactly 1 (deduped)", n)
	}
}

// TestTaskSLAAlert: a task still pending past its task-level SLA fires one
// task_sla_miss naming the task.
func TestTaskSLAAlert(t *testing.T) {
	url, bodies := captureHook(t)
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "tsla", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		NotifyURL: url,
		Tasks:     []model.Task{{ID: "slow", Command: "sleep 3", Pool: model.DefaultPoolName, SLA: 1}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "tsla", nil)
	if run := tickUntilTerminal(t, s, ctx, runID, 6*time.Second); run.State != model.RunSuccess {
		t.Fatalf("run = %s, want success", run.State)
	}
	s.WaitInflight()
	got := bodies()
	if n := countState(t, got, "task_sla_miss"); n != 1 {
		t.Fatalf("task_sla_miss alerts = %d, want 1", n)
	}
	var p map[string]any
	for _, b := range got {
		_ = json.Unmarshal(b, &p)
		if p["state"] == "task_sla_miss" {
			if p["task_id"] != "slow" {
				t.Fatalf("task_sla_miss task_id = %v, want slow", p["task_id"])
			}
		}
	}
}
