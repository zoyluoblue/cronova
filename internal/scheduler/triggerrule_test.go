package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/model"
)

// all_done: a cleanup task should run even though an upstream failed.
func TestTriggerRuleAllDone(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "rule_alldone", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{
			{ID: "a", Command: "echo a", Pool: model.DefaultPoolName, TriggerRule: model.RuleAllSuccess},
			{ID: "b", Command: "exit 1", Pool: model.DefaultPoolName, TriggerRule: model.RuleAllSuccess},
			{ID: "cleanup", Command: "echo cleanup", Deps: []string{"a", "b"}, Pool: model.DefaultPoolName, TriggerRule: model.RuleAllDone},
		},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "rule_alldone", nil)
	run := s.driveToTerminal(t, ctx, runID, 40)
	states := s.tiStates(t, ctx, runID)
	// b failed, but cleanup (all_done) still ran to success; run overall failed.
	if states["b"] != model.TaskFailed {
		t.Errorf("b = %s, want failed", states["b"])
	}
	if states["cleanup"] != model.TaskSuccess {
		t.Errorf("cleanup = %s, want success (all_done runs despite b's failure)", states["cleanup"])
	}
	if run.State != model.RunFailed {
		t.Errorf("run = %s, want failed", run.State)
	}
}

// one_failed: an alert task should run precisely because an upstream failed.
func TestTriggerRuleOneFailed(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "rule_onefailed", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{
			{ID: "a", Command: "echo a", Pool: model.DefaultPoolName},
			{ID: "b", Command: "exit 1", Pool: model.DefaultPoolName},
			{ID: "alert", Command: "echo ALERT", Deps: []string{"a", "b"}, Pool: model.DefaultPoolName, TriggerRule: model.RuleOneFailed},
		},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "rule_onefailed", nil)
	s.driveToTerminal(t, ctx, runID, 40)
	states := s.tiStates(t, ctx, runID)
	if states["alert"] != model.TaskSuccess {
		t.Errorf("alert = %s, want success (one_failed fires because b failed)", states["alert"])
	}
}

// one_failed with no failures must block (never fires).
func TestTriggerRuleOneFailedBlocked(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "rule_block", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{
			{ID: "a", Command: "echo a", Pool: model.DefaultPoolName},
			{ID: "alert", Command: "echo never", Deps: []string{"a"}, Pool: model.DefaultPoolName, TriggerRule: model.RuleOneFailed},
		},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "rule_block", nil)
	run := s.driveToTerminal(t, ctx, runID, 40)
	states := s.tiStates(t, ctx, runID)
	if states["alert"] != model.TaskUpstreamFailed {
		t.Errorf("alert = %s, want upstream_failed (one_failed blocked: a succeeded)", states["alert"])
	}
	if run.State != model.RunFailed { // a succeeded but alert is upstream_failed -> run failed
		t.Errorf("run = %s, want failed", run.State)
	}
}
