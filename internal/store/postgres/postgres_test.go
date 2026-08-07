package postgres

// These tests need a live PostgreSQL server and are skipped unless
// CRONOVA_TEST_PG_DSN is set, e.g.:
//
//	CRONOVA_TEST_PG_DSN='postgres://cronova:cronova@localhost:5432/cronova_test?sslmode=disable' go test ./internal/store/postgres/
//
// The tests share one database across runs, so every row they create carries a
// unique (nanosecond-stamped) identifier instead of relying on a clean slate.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/store"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("CRONOVA_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set CRONOVA_TEST_PG_DSN to run")
	}
	s, err := New(dsn)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

func uniqueID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}

func mustUpsertDAG(t *testing.T, s *Store, dagID string) *model.DAG {
	t.Helper()
	d := &model.DAG{
		DagID:          dagID,
		Schedule:       "0 3 * * *",
		Timezone:       "Asia/Shanghai",
		StartDate:      time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Catchup:        true,
		MaxActiveRuns:  3,
		DefinitionYAML: "dag_id: " + dagID,
		Owner:          "tester",
		Project:        "pg-tests",
	}
	if err := s.UpsertDAG(context.Background(), d); err != nil {
		t.Fatalf("UpsertDAG: %v", err)
	}
	return d
}

func mustCreateRun(t *testing.T, s *Store, dagID string, logical time.Time) *model.DagRun {
	t.Helper()
	r := &model.DagRun{
		RunID:       uniqueID("run"),
		DagID:       dagID,
		LogicalDate: logical,
		State:       model.RunQueued,
		TriggerType: model.TriggerManual,
		Params:      map[string]string{"env": "test"},
	}
	if err := s.CreateDagRun(context.Background(), r); err != nil {
		t.Fatalf("CreateDagRun: %v", err)
	}
	return r
}

func TestDAGRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	dagID := uniqueID("dag_rt")
	want := mustUpsertDAG(t, s, dagID)

	got, err := s.GetDAG(ctx, dagID)
	if err != nil {
		t.Fatalf("GetDAG: %v", err)
	}
	if got.Timezone != "Asia/Shanghai" {
		t.Errorf("Timezone = %q, want %q", got.Timezone, "Asia/Shanghai")
	}
	if got.Schedule != want.Schedule || !got.Catchup || got.MaxActiveRuns != 3 {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if !got.StartDate.Equal(want.StartDate) {
		t.Errorf("StartDate = %v, want %v", got.StartDate, want.StartDate)
	}
	if got.Paused {
		t.Errorf("new DAG should not be paused")
	}

	if err := s.SetDAGPaused(ctx, dagID, true); err != nil {
		t.Fatalf("SetDAGPaused: %v", err)
	}
	got, err = s.GetDAG(ctx, dagID)
	if err != nil {
		t.Fatalf("GetDAG after pause: %v", err)
	}
	if !got.Paused {
		t.Errorf("Paused not persisted")
	}
	// Re-registering must preserve the paused flag (operational state).
	if err := s.UpsertDAG(ctx, want); err != nil {
		t.Fatalf("re-UpsertDAG: %v", err)
	}
	got, _ = s.GetDAG(ctx, dagID)
	if !got.Paused {
		t.Errorf("re-register un-paused the DAG")
	}

	if err := s.SoftDeleteDAG(ctx, dagID); err != nil {
		t.Fatalf("SoftDeleteDAG: %v", err)
	}
	got, err = s.GetDAG(ctx, dagID)
	if err != nil {
		t.Fatalf("GetDAG after delete: %v", err)
	}
	if got.DeletedAt == nil {
		t.Errorf("DeletedAt not set")
	}
	if err := s.SoftDeleteDAG(ctx, dagID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("double delete = %v, want ErrNotFound", err)
	}
}

func TestCreateDagRunDuplicate(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	dagID := uniqueID("dag_dup")
	mustUpsertDAG(t, s, dagID)

	logical := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	r := mustCreateRun(t, s, dagID, logical)

	got, err := s.GetDagRun(ctx, r.RunID)
	if err != nil {
		t.Fatalf("GetDagRun: %v", err)
	}
	if !got.LogicalDate.Equal(logical) || got.State != model.RunQueued || got.Params["env"] != "test" {
		t.Errorf("run round-trip mismatch: %+v", got)
	}

	// Same (dag_id, logical_date), different run_id -> unique violation.
	dup := &model.DagRun{
		RunID:       uniqueID("run_dup"),
		DagID:       dagID,
		LogicalDate: logical,
		State:       model.RunQueued,
		TriggerType: model.TriggerManual,
	}
	if err := s.CreateDagRun(ctx, dup); !errors.Is(err, store.ErrAlreadyExists) {
		t.Errorf("duplicate CreateDagRun = %v, want ErrAlreadyExists", err)
	}

	// Runs for a missing DAG are refused with ErrNotFound (delete-race guard).
	orphan := &model.DagRun{
		RunID:       uniqueID("run_orphan"),
		DagID:       uniqueID("dag_missing"),
		LogicalDate: logical,
		State:       model.RunQueued,
		TriggerType: model.TriggerManual,
	}
	if err := s.CreateDagRun(ctx, orphan); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("orphan CreateDagRun = %v, want ErrNotFound", err)
	}
}

func TestTaskInstanceLifecycle(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	dagID := uniqueID("dag_ti")
	mustUpsertDAG(t, s, dagID)
	r := mustCreateRun(t, s, dagID, time.Date(2024, 6, 2, 0, 0, 0, 0, time.UTC))

	ti := &model.TaskInstance{
		RunID:      r.RunID,
		TaskID:     "extract",
		State:      model.TaskQueued,
		MaxRetries: 2,
		Pool:       model.DefaultPoolName,
		Priority:   5,
	}
	if err := s.CreateTaskInstance(ctx, ti); err != nil {
		t.Fatalf("CreateTaskInstance: %v", err)
	}
	if ti.ID == 0 {
		t.Fatalf("CreateTaskInstance did not populate ID (RETURNING broken)")
	}
	if err := s.CreateTaskInstance(ctx, &model.TaskInstance{
		RunID: r.RunID, TaskID: "extract", State: model.TaskQueued, Pool: model.DefaultPoolName,
	}); !errors.Is(err, store.ErrAlreadyExists) {
		t.Errorf("duplicate CreateTaskInstance = %v, want ErrAlreadyExists", err)
	}

	now := time.Now().UTC()
	ti.State = model.TaskRunning
	ti.TryNumber = 1
	ti.ExecutorRef = "pid:1234"
	ti.StartedAt = &now
	if err := s.UpdateTaskInstance(ctx, ti); err != nil {
		t.Fatalf("UpdateTaskInstance: %v", err)
	}
	got, err := s.GetTaskInstance(ctx, ti.ID)
	if err != nil {
		t.Fatalf("GetTaskInstance: %v", err)
	}
	if got.State != model.TaskRunning || got.TryNumber != 1 || got.ExecutorRef != "pid:1234" || got.StartedAt == nil {
		t.Errorf("update round-trip mismatch: %+v", got)
	}

	// Guarded CAS: wrong ref does not apply; right ref does; terminal blocks.
	ti.State = model.TaskSuccess
	ti.FinishedAt = &now
	ok, err := s.UpdateTaskInstanceGuarded(ctx, ti, "pid:wrong")
	if err != nil || ok {
		t.Errorf("guarded with wrong ref: ok=%v err=%v, want no-op", ok, err)
	}
	ok, err = s.UpdateTaskInstanceGuarded(ctx, ti, "pid:1234")
	if err != nil || !ok {
		t.Fatalf("guarded with right ref: ok=%v err=%v, want applied", ok, err)
	}
	ok, err = s.UpdateTaskInstanceGuarded(ctx, ti, "pid:1234")
	if err != nil || ok {
		t.Errorf("guarded on terminal row: ok=%v err=%v, want no-op", ok, err)
	}
}

func TestLease(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := uniqueID("holder_a")
	b := uniqueID("holder_b")
	const ttl = 400 * time.Millisecond

	// A crashed previous run may have left a live lease; short TTLs mean it
	// expires quickly, so poll-acquire instead of assuming a clean table.
	deadline := time.Now().Add(15 * time.Second)
	for {
		err := s.AcquireLease(ctx, a, ttl)
		if err == nil {
			break
		}
		if !errors.Is(err, store.ErrLeaseHeld) {
			t.Fatalf("AcquireLease: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("lease never became free: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Cleanup(func() {
		_ = s.ReleaseLease(ctx, a)
		_ = s.ReleaseLease(ctx, b)
	})

	// Held by A: B is refused, A may re-acquire and renew.
	if err := s.AcquireLease(ctx, b, ttl); !errors.Is(err, store.ErrLeaseHeld) {
		t.Errorf("AcquireLease(b) while held = %v, want ErrLeaseHeld", err)
	}
	if err := s.AcquireLease(ctx, a, ttl); err != nil {
		t.Errorf("re-AcquireLease(a): %v", err)
	}
	if err := s.RenewLease(ctx, a, ttl); err != nil {
		t.Errorf("RenewLease(a): %v", err)
	}
	if err := s.RenewLease(ctx, b, ttl); !errors.Is(err, store.ErrLeaseLost) {
		t.Errorf("RenewLease(b) = %v, want ErrLeaseLost", err)
	}

	// Expiry: after ttl passes without renewal, B steals the stale lease and
	// A's next renew reports the loss.
	time.Sleep(ttl + 100*time.Millisecond)
	if err := s.AcquireLease(ctx, b, ttl); err != nil {
		t.Fatalf("steal expired lease: %v", err)
	}
	if err := s.RenewLease(ctx, a, ttl); !errors.Is(err, store.ErrLeaseLost) {
		t.Errorf("RenewLease(a) after steal = %v, want ErrLeaseLost", err)
	}
	if err := s.ReleaseLease(ctx, b); err != nil {
		t.Errorf("ReleaseLease(b): %v", err)
	}
}

func TestPublishEventIdempotent(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	source := uniqueID("src")
	key := uniqueID("key")

	created, err := s.PublishEvent(ctx, source, key, `{"n":1}`)
	if err != nil || !created {
		t.Fatalf("PublishEvent first = (%v, %v), want (true, nil)", created, err)
	}
	created, err = s.PublishEvent(ctx, source, key, `{"n":2}`)
	if err != nil || created {
		t.Fatalf("PublishEvent repeat = (%v, %v), want (false, nil)", created, err)
	}

	evs, err := s.ListPendingEvents(ctx, source, 10)
	if err != nil {
		t.Fatalf("ListPendingEvents: %v", err)
	}
	if len(evs) != 1 || evs[0].EventKey != key || evs[0].Payload != `{"n":1}` {
		t.Fatalf("pending events = %+v, want the single original payload", evs)
	}

	if err := s.ConsumeEvent(ctx, evs[0].ID); err != nil {
		t.Fatalf("ConsumeEvent: %v", err)
	}
	evs, err = s.ListPendingEvents(ctx, source, 10)
	if err != nil {
		t.Fatalf("ListPendingEvents after consume: %v", err)
	}
	if len(evs) != 0 {
		t.Errorf("consumed event still pending: %+v", evs)
	}
	// Re-publishing a consumed key stays a no-op (idempotency survives consumption).
	created, err = s.PublishEvent(ctx, source, key, `{"n":3}`)
	if err != nil || created {
		t.Errorf("PublishEvent after consume = (%v, %v), want (false, nil)", created, err)
	}
}

func TestTaskOutput(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	runID := uniqueID("outrun")

	if _, err := s.GetTaskOutput(ctx, runID, "t1"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetTaskOutput missing = %v, want ErrNotFound", err)
	}
	if err := s.SetTaskOutput(ctx, runID, "t1", `{"rows":"42"}`); err != nil {
		t.Fatalf("SetTaskOutput: %v", err)
	}
	out, err := s.GetTaskOutput(ctx, runID, "t1")
	if err != nil || out != `{"rows":"42"}` {
		t.Fatalf("GetTaskOutput = (%q, %v)", out, err)
	}
	// Setting again replaces (upsert).
	if err := s.SetTaskOutput(ctx, runID, "t1", `{"rows":"43"}`); err != nil {
		t.Fatalf("SetTaskOutput replace: %v", err)
	}
	out, err = s.GetTaskOutput(ctx, runID, "t1")
	if err != nil || out != `{"rows":"43"}` {
		t.Fatalf("GetTaskOutput after replace = (%q, %v)", out, err)
	}
}
