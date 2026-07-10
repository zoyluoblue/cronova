package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/model"
)

func TestDependencyEventRetriesAfterGlobalQueueCapacityReturns(t *testing.T) {
	s := newTestScheduler(t)
	s.opts.MaxQueuedRunsGlobal = 1
	ctx := context.Background()
	logical := time.Now().UTC().Truncate(time.Second)

	mkDAG := func(id string, after []string) *model.DAG {
		return &model.DAG{
			DagID: id, MaxActiveRuns: 1, StartDate: logical, TriggerAfter: after,
			Tasks: []model.Task{{ID: "task", Command: "echo ok", Pool: model.DefaultPoolName}},
		}
	}
	for _, dag := range []*model.DAG{
		mkDAG("event_up", nil),
		mkDAG("event_down", []string{"event_up"}),
		mkDAG("queue_blocker", nil),
	} {
		if err := s.registerDAG(ctx, dag); err != nil {
			t.Fatal(err)
		}
	}

	// An existing running downstream must not suppress a dependency run. The
	// queued->running gate, not event delivery, owns max_active_runs enforcement.
	existingLogical := logical.Add(-time.Hour)
	if err := s.store.CreateDagRun(ctx, &model.DagRun{
		RunID: "event_down__active", DagID: "event_down", LogicalDate: existingLogical,
		State: model.RunRunning, TriggerType: model.TriggerManual,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.CreateDagRun(ctx, &model.DagRun{
		RunID: "queue_blocker__queued", DagID: "queue_blocker", LogicalDate: logical,
		State: model.RunQueued, TriggerType: model.TriggerManual,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.CreateDagRun(ctx, &model.DagRun{
		RunID: "event_up__success", DagID: "event_up", LogicalDate: logical,
		State: model.RunRunning, TriggerType: model.TriggerManual,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.UpdateDagRunSuccess(ctx, "event_up__success", &logical, &logical); err != nil {
		t.Fatal(err)
	}

	s.processPendingDependencyEvents(ctx)
	if runs, _ := s.store.ListDagRuns(ctx, "event_down", 10); len(runs) != 1 {
		t.Fatalf("downstream runs while queue full = %d, want only existing active run", len(runs))
	}
	if events, _ := s.store.ListPendingEvents(ctx, model.EventSourceDependency, 10); len(events) != 1 {
		t.Fatalf("pending events while queue full = %d, want 1", len(events))
	}

	// Moving the blocker out of queued state releases admission capacity. The
	// next delivery creates a queued downstream even though another run of that
	// DAG is still active, and then consumes the event.
	if err := s.store.UpdateDagRunState(ctx, "queue_blocker__queued", model.RunRunning, &logical, nil); err != nil {
		t.Fatal(err)
	}
	s.processPendingDependencyEvents(ctx)
	runs, err := s.store.ListDagRuns(ctx, "event_down", 10)
	if err != nil || len(runs) != 2 {
		t.Fatalf("downstream runs after capacity returns = %+v, err=%v", runs, err)
	}
	var dependency *model.DagRun
	for _, run := range runs {
		if run.LogicalDate.Equal(logical) {
			dependency = run
		}
	}
	if dependency == nil || dependency.State != model.RunQueued || dependency.TriggerType != model.TriggerDependency {
		t.Fatalf("dependency run = %+v, want queued dependency run", dependency)
	}
	if events, _ := s.store.ListPendingEvents(ctx, model.EventSourceDependency, 10); len(events) != 0 {
		t.Fatalf("pending events after delivery = %d, want 0", len(events))
	}
}
