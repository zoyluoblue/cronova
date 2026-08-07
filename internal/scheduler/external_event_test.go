package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/model"
)

// An external event triggers every subscriber exactly once (idempotent under
// redelivery), passes its payload as run params, and unsubscribed events drop.
func TestExternalEventTriggersSubscribers(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mk := func(id string, keys []string) *model.DAG {
		return &model.DAG{
			DagID: id, MaxActiveRuns: 1, StartDate: now, TriggerOnEvent: keys,
			Tasks: []model.Task{{ID: "task", Command: "echo ok", Pool: model.DefaultPoolName}},
		}
	}
	for _, d := range []*model.DAG{
		mk("sub_a", []string{"orders_ready"}),
		mk("sub_b", []string{"orders_ready", "other"}),
		mk("bystander", nil),
	} {
		if err := s.registerDAG(ctx, d); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := s.store.PublishEvent(ctx, model.EventSourceExternal, "orders_ready", `{"date":"2026-08-07"}`); err != nil {
		t.Fatal(err)
	}
	// idempotent re-publish is a no-op
	if created, err := s.store.PublishEvent(ctx, model.EventSourceExternal, "orders_ready", ""); err != nil || created {
		t.Fatalf("re-publish created=%v err=%v, want false,nil", created, err)
	}
	// an event nobody subscribes to must be consumed, not loop forever
	if _, err := s.store.PublishEvent(ctx, model.EventSourceExternal, "nobody_cares", ""); err != nil {
		t.Fatal(err)
	}

	s.processPendingExternalEvents(ctx)

	for _, dagID := range []string{"sub_a", "sub_b"} {
		runs, err := s.store.ListDagRuns(ctx, dagID, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(runs) != 1 {
			t.Fatalf("%s runs = %d, want 1", dagID, len(runs))
		}
		r := runs[0]
		if r.TriggerType != model.TriggerEvent {
			t.Errorf("%s trigger_type = %s, want event", dagID, r.TriggerType)
		}
		if r.Params["date"] != "2026-08-07" || r.Params["event_key"] != "orders_ready" {
			t.Errorf("%s params = %v, want payload + event_key", dagID, r.Params)
		}
	}
	if runs, _ := s.store.ListDagRuns(ctx, "bystander", 10); len(runs) != 0 {
		t.Fatalf("bystander got %d runs, want 0", len(runs))
	}

	// second pass: everything consumed, nothing double-triggers
	s.processPendingExternalEvents(ctx)
	if runs, _ := s.store.ListDagRuns(ctx, "sub_a", 10); len(runs) != 1 {
		t.Fatalf("sub_a after redelivery = %d runs, want 1", len(runs))
	}
	if pending, _ := s.store.ListPendingEvents(ctx, model.EventSourceExternal, 10); len(pending) != 0 {
		t.Fatalf("pending events = %d, want 0", len(pending))
	}
}

// A full global queue defers the event (not consumed, no partial loss); it
// delivers once capacity returns.
func TestExternalEventDefersWhenQueueFull(t *testing.T) {
	s := newTestScheduler(t)
	s.opts.MaxQueuedRunsGlobal = 1
	ctx := context.Background()
	now := time.Now().UTC()

	if err := s.registerDAG(ctx, &model.DAG{
		DagID: "sub", MaxActiveRuns: 1, StartDate: now, TriggerOnEvent: []string{"go"},
		Tasks: []model.Task{{ID: "task", Command: "echo ok", Pool: model.DefaultPoolName}},
	}); err != nil {
		t.Fatal(err)
	}
	// occupy the single global queued slot
	if err := s.store.CreateDagRun(ctx, &model.DagRun{
		RunID: "blocker__q", DagID: "sub", LogicalDate: now.Add(-time.Hour),
		State: model.RunQueued, TriggerType: model.TriggerManual,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.PublishEvent(ctx, model.EventSourceExternal, "go", ""); err != nil {
		t.Fatal(err)
	}

	s.processPendingExternalEvents(ctx)
	if pending, _ := s.store.ListPendingEvents(ctx, model.EventSourceExternal, 10); len(pending) != 1 {
		t.Fatalf("queue-full event consumed early (pending=%d, want 1)", len(pending))
	}

	// capacity returns → the deferred event delivers
	fin := now
	if err := s.store.UpdateDagRunState(ctx, "blocker__q", model.RunCancelled, nil, &fin); err != nil {
		t.Fatal(err)
	}
	s.processPendingExternalEvents(ctx)
	runs, _ := s.store.ListDagRuns(ctx, "sub", 10)
	var eventRuns int
	for _, r := range runs {
		if r.TriggerType == model.TriggerEvent {
			eventRuns++
		}
	}
	if eventRuns != 1 {
		t.Fatalf("event runs after capacity returned = %d, want 1", eventRuns)
	}
	if pending, _ := s.store.ListPendingEvents(ctx, model.EventSourceExternal, 10); len(pending) != 0 {
		t.Fatalf("event still pending after delivery")
	}
}
