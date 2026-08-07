package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/model"
)

// A task writes a JSON map to $CRONOVA_OUTPUT; the downstream command sees the
// value via {{ ti.<task>.<key> }}; when: gates evaluate against params.
func TestTaskOutputFlowsDownstreamAndWhenGates(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	now := time.Now().UTC()

	dag := &model.DAG{
		DagID: "xcom", MaxActiveRuns: 1, StartDate: now,
		Tasks: []model.Task{
			{ID: "produce", Command: `printf '{"answer":"42"}' > "$CRONOVA_OUTPUT"`, Pool: model.DefaultPoolName},
			{ID: "consume", Command: `test "{{ ti.produce.answer }}" = "42"`, Deps: []string{"produce"}, Pool: model.DefaultPoolName},
			{ID: "gated_off", Command: "echo never", When: "{{ params.go }}", Deps: []string{"produce"}, Pool: model.DefaultPoolName, TriggerRule: model.RuleAllSuccess},
		},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, err := s.TriggerManual(ctx, "xcom", nil) // params.go unset → gated_off skips
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		s.tickOnce(ctx)
		r, err := s.store.GetDagRun(ctx, runID)
		if err != nil {
			t.Fatal(err)
		}
		if r.State.IsTerminal() {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}

	r, _ := s.store.GetDagRun(ctx, runID)
	if r.State != model.RunSuccess {
		tis, _ := s.store.ListTaskInstances(ctx, runID)
		for _, ti := range tis {
			t.Logf("ti %s state=%s try=%d", ti.TaskID, ti.State, ti.TryNumber)
		}
		t.Fatalf("run state = %s, want success (consume must see the produced output)", r.State)
	}
	tis, _ := s.store.ListTaskInstances(ctx, runID)
	states := map[string]model.TaskState{}
	for _, ti := range tis {
		states[ti.TaskID] = ti.State
	}
	if states["produce"] != model.TaskSuccess || states["consume"] != model.TaskSuccess {
		t.Fatalf("produce/consume = %s/%s, want success/success", states["produce"], states["consume"])
	}
	if states["gated_off"] != model.TaskSkipped {
		t.Fatalf("gated_off = %s, want skipped (falsy when)", states["gated_off"])
	}
	out, err := s.store.GetTaskOutput(ctx, runID, "produce")
	if err != nil || out != `{"answer":"42"}` {
		t.Fatalf("stored output = %q err=%v", out, err)
	}
}

// Exit code 99 marks the attempt skipped (self-short-circuit), not failed.
func TestSkipExitCode(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if err := s.registerDAG(ctx, &model.DAG{
		DagID: "shortcircuit", MaxActiveRuns: 1, StartDate: now,
		Tasks: []model.Task{
			{ID: "decide", Command: "exit 99", Pool: model.DefaultPoolName},
			{ID: "after", Command: "echo ran", Deps: []string{"decide"}, Pool: model.DefaultPoolName, TriggerRule: model.RuleNoneFailed},
		},
	}); err != nil {
		t.Fatal(err)
	}
	runID, err := s.TriggerManual(ctx, "shortcircuit", nil)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		s.tickOnce(ctx)
		r, _ := s.store.GetDagRun(ctx, runID)
		if r != nil && r.State.IsTerminal() {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	tis, _ := s.store.ListTaskInstances(ctx, runID)
	states := map[string]model.TaskState{}
	for _, ti := range tis {
		states[ti.TaskID] = ti.State
	}
	if states["decide"] != model.TaskSkipped {
		t.Fatalf("decide = %s, want skipped (exit 99)", states["decide"])
	}
	if states["after"] != model.TaskSuccess {
		t.Fatalf("after = %s, want success (none_failed passes a skip)", states["after"])
	}
}
