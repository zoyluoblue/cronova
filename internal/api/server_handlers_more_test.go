package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/store"
	"github.com/zoyluo/cronova/internal/store/sqlite"
)

// newEngineServer builds a handler around a fresh (empty) store with a custom
// engine, for tests that need engine behavior the shared stubTrigger can't fake.
func newEngineServer(t *testing.T, eng Engine) (http.Handler, *sqlite.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := sqlite.New(filepath.Join(dir, "api-extra.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return New(st, eng, dir, nil, Info{}).Handler(), st, dir
}

// schedStubEngine fixes NextSchedule so nextScheduleLabel's branches are testable.
type schedStubEngine struct {
	stubTrigger
	next time.Time
	ok   bool
}

func (e *schedStubEngine) NextSchedule(context.Context, *model.DAG) (time.Time, bool) {
	return e.next, e.ok
}

// failCreateEngine rejects every CreateDAG like a YAML/validation error would.
type failCreateEngine struct{ stubTrigger }

func (e *failCreateEngine) CreateDAG(context.Context, string) (string, error) {
	return "", errors.New("boom: dag_id is required")
}

func TestGetInfoReportsRuntime(t *testing.T) {
	h, _, _, _ := setup(t)
	rec, body := get(t, h, "GET", "/api/info")
	if rec.Code != 200 {
		t.Fatalf("info = %d", rec.Code)
	}
	m := body.(map[string]any)
	if m["executor"] != "in-process" || m["tick"] != "2s" {
		t.Errorf("info executor/tick = %v/%v", m["executor"], m["tick"])
	}
	if m["tz"] != "UTC" { // New() defaults an empty TZ label to UTC
		t.Errorf("tz = %v, want UTC", m["tz"])
	}
	if m["auth_enabled"] != false {
		t.Errorf("auth_enabled = %v, want false", m["auth_enabled"])
	}
}

func TestRunLifecycleOps(t *testing.T) {
	h, _, trig, _ := setup(t)
	cases := []struct {
		name     string
		path     string
		body     string
		opErr    error
		wantCode int
		wantKey  string                    // response key that must be true on success
		recorded func(*stubTrigger) string // what the engine saw
		wantRec  string
	}{
		{name: "cancel ok", path: "/api/runs/etl__r1/cancel", wantCode: 200, wantKey: "cancelled",
			recorded: func(s *stubTrigger) string { return s.cancelled }, wantRec: "etl__r1"},
		{name: "cancel terminal run conflicts", path: "/api/runs/etl__r1/cancel",
			opErr: model.ErrRunNotActive, wantCode: 409},
		{name: "cancel unknown run", path: "/api/runs/ghost/cancel",
			opErr: store.ErrNotFound, wantCode: 404},
		{name: "retry run ok", path: "/api/runs/etl__r1/retry", wantCode: 200, wantKey: "retried",
			recorded: func(s *stubTrigger) string { return s.retriedRun }, wantRec: "etl__r1"},
		{name: "retry run nothing to retry", path: "/api/runs/etl__r1/retry",
			opErr: model.ErrNothingToRetry, wantCode: 409},
		{name: "retry run still active", path: "/api/runs/etl__r1/retry",
			opErr: model.ErrRunStillActive, wantCode: 409},
		{name: "retry task ok", path: "/api/runs/etl__r1/tasks/extract/retry", wantCode: 200, wantKey: "retried",
			recorded: func(s *stubTrigger) string { return s.retriedTask }, wantRec: "etl__r1/extract"},
		{name: "mark task success", path: "/api/runs/etl__r1/tasks/extract/mark",
			body: `{"state":"success"}`, wantCode: 200, wantKey: "marked",
			recorded: func(s *stubTrigger) string { return s.markedTask }, wantRec: "etl__r1/extract=success"},
		{name: "mark task invalid state", path: "/api/runs/etl__r1/tasks/extract/mark",
			body: `{"state":"sparkly"}`, opErr: model.ErrBadMarkState, wantCode: 400},
		{name: "mark task malformed body", path: "/api/runs/etl__r1/tasks/extract/mark",
			body: `{not json`, wantCode: 400},
		{name: "mark run failed", path: "/api/runs/etl__r1/mark",
			body: `{"state":"failed"}`, wantCode: 200, wantKey: "marked",
			recorded: func(s *stubTrigger) string { return s.markedRun }, wantRec: "etl__r1=failed"},
		{name: "mark run malformed body", path: "/api/runs/etl__r1/mark",
			body: `{oops`, wantCode: 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trig.opErr = tc.opErr
			rec := do(h, "POST", tc.path, tc.body, nil)
			if rec.Code != tc.wantCode {
				t.Fatalf("code = %d, want %d (body %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantCode == 200 {
				var m map[string]bool
				if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil || !m[tc.wantKey] {
					t.Errorf("body = %s, want {%q:true}", rec.Body.String(), tc.wantKey)
				}
				if got := tc.recorded(trig); got != tc.wantRec {
					t.Errorf("engine saw %q, want %q", got, tc.wantRec)
				}
			} else {
				var m map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil || m["error"] == "" {
					t.Errorf("error body = %s, want {\"error\":...}", rec.Body.String())
				}
			}
		})
	}

	// Every successful mutation above was audited; errors were not.
	trig.opErr = nil
	rec, body := get(t, h, "GET", "/api/audit")
	if rec.Code != 200 {
		t.Fatalf("audit = %d", rec.Code)
	}
	gotActions := map[string]bool{}
	for _, e := range body.([]any) {
		gotActions[e.(map[string]any)["action"].(string)] = true
	}
	for _, want := range []string{"cancel", "retry_run", "retry_task", "mark_task", "mark_run"} {
		if !gotActions[want] {
			t.Errorf("audit missing action %q (got %v)", want, gotActions)
		}
	}
	// limit caps the page
	rec, body = get(t, h, "GET", "/api/audit?limit=2")
	if rec.Code != 200 || len(body.([]any)) != 2 {
		t.Errorf("audit limit=2 -> code=%d len=%d", rec.Code, len(body.([]any)))
	}
}

func TestBackfillDAG(t *testing.T) {
	h, _, trig, _ := setup(t)
	cases := []struct {
		name       string
		body       string
		triggerErr error
		wantCode   int
	}{
		{"date range", `{"from":"2026-07-01","to":"2026-07-03"}`, nil, 200},
		{"rfc3339 range", `{"from":"2026-07-01T00:00:00Z","to":"2026-07-02T12:30:00Z"}`, nil, 200},
		{"invalid from", `{"from":"01/07/2026","to":"2026-07-03"}`, nil, 400},
		{"invalid to", `{"from":"2026-07-01","to":"soon"}`, nil, 400},
		{"malformed body", `{`, nil, 400},
		{"engine client error", `{"from":"2026-07-01","to":"2026-07-02"}`, model.ErrNoTasks, 400},
		{"unknown dag", `{"from":"2026-07-01","to":"2026-07-02"}`, store.ErrNotFound, 404},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trig.triggerErr = tc.triggerErr
			rec := do(h, "POST", "/api/dags/etl/backfill", tc.body, nil)
			if rec.Code != tc.wantCode {
				t.Fatalf("code = %d, want %d (body %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantCode == 200 {
				var m map[string]int
				if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil || m["created"] != 2 || m["skipped"] != 1 {
					t.Errorf("body = %s, want {created:2,skipped:1}", rec.Body.String())
				}
				if trig.got != "etl" {
					t.Errorf("engine saw dag %q, want etl", trig.got)
				}
			}
		})
	}
}

func TestCreateDAGFromYAML(t *testing.T) {
	h, _, trig, _ := setup(t)
	yml := "dag_id: fresh\ntasks:\n  - id: a\n    command: \"echo hi\"\n"
	rec := do(h, "POST", "/api/dags", yml, nil)
	if rec.Code != 200 {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}
	var m map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil || m["dag_id"] != "created_dag" {
		t.Errorf("body = %s, want dag_id=created_dag", rec.Body.String())
	}
	if trig.createdYML != yml {
		t.Errorf("engine got YAML %q, want the raw body", trig.createdYML)
	}
	// the create is audited, addressable by target
	rec, body := get(t, h, "GET", "/api/audit?target=created_dag")
	if rec.Code != 200 {
		t.Fatalf("audit = %d", rec.Code)
	}
	entries := body.([]any)
	if len(entries) != 1 || entries[0].(map[string]any)["action"] != "create_dag" {
		t.Errorf("audit for created_dag = %v, want one create_dag entry", body)
	}

	// engine rejection (bad YAML / validation) is a client error
	h2, _, _ := newEngineServer(t, &failCreateEngine{})
	rec2 := do(h2, "POST", "/api/dags", "not: [valid", nil)
	if rec2.Code != 400 {
		t.Fatalf("rejected create = %d, want 400", rec2.Code)
	}
	if !strings.Contains(rec2.Body.String(), "boom") {
		t.Errorf("error body should carry the engine message: %s", rec2.Body.String())
	}
}

func TestListRunsFilterAndPaging(t *testing.T) {
	h, st, _, _ := setup(t)
	ctx := context.Background()
	// a second, newer, failed run
	if err := st.CreateDagRun(ctx, &model.DagRun{
		RunID: "etl__r2", DagID: "etl", LogicalDate: time.Now().UTC().Add(time.Hour),
		State: model.RunFailed, TriggerType: model.TriggerSchedule,
	}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		qs        string
		wantCode  int
		wantLen   int
		wantFirst string
	}{
		{"all newest first", "", 200, 2, "etl__r2"},
		{"failed only", "?state=failed", 200, 1, "etl__r2"},
		{"multi state", "?state=failed,success", 200, 2, "etl__r2"},
		{"limit", "?limit=1", 200, 1, "etl__r2"},
		{"offset pages past the newest", "?limit=1&offset=1", 200, 1, "etl__r1"},
		{"unknown state rejected", "?state=meh", 400, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, body := get(t, h, "GET", "/api/dags/etl/runs"+tc.qs)
			if rec.Code != tc.wantCode {
				t.Fatalf("code = %d, want %d (body %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantCode != 200 {
				return
			}
			runs := body.([]any)
			if len(runs) != tc.wantLen {
				t.Fatalf("len = %d, want %d", len(runs), tc.wantLen)
			}
			if first := runs[0].(map[string]any)["run_id"]; first != tc.wantFirst {
				t.Errorf("first run = %v, want %s", first, tc.wantFirst)
			}
		})
	}
	// a DAG with no history returns [] (not null)
	rec, body := get(t, h, "GET", "/api/dags/ghost/runs")
	if rec.Code != 200 {
		t.Fatalf("empty history = %d", rec.Code)
	}
	if runs, ok := body.([]any); !ok || len(runs) != 0 {
		t.Errorf("empty history body = %v, want []", body)
	}
}

func TestListRunsCapsPageSize(t *testing.T) {
	h, st, _, _ := setup(t)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < maxRunPageSize+5; i++ {
		if err := st.CreateDagRun(ctx, &model.DagRun{
			RunID: fmt.Sprintf("etl__page_%03d", i), DagID: "etl", LogicalDate: base.Add(time.Duration(i) * time.Second),
			State: model.RunSuccess, TriggerType: model.TriggerManual,
		}); err != nil {
			t.Fatal(err)
		}
	}
	rec, body := get(t, h, "GET", "/api/dags/etl/runs?limit=999999")
	if rec.Code != http.StatusOK || len(body.([]any)) != maxRunPageSize {
		t.Fatalf("capped page: code=%d len=%d, want %d", rec.Code, len(body.([]any)), maxRunPageSize)
	}
	rec, _ = get(t, h, "GET", "/api/dags/etl/runs?offset=-1")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("negative offset code = %d, want 400", rec.Code)
	}
}

func TestGetRunNotFound(t *testing.T) {
	h, _, _, _ := setup(t)
	rec, body := get(t, h, "GET", "/api/runs/no_such_run")
	if rec.Code != 404 {
		t.Fatalf("code = %d, want 404", rec.Code)
	}
	if body.(map[string]any)["error"] != "not found" {
		t.Errorf("body = %v", body)
	}
}

func TestTaskLogEdgeCases(t *testing.T) {
	h, st, _, _ := setup(t)
	dir := t.TempDir()
	cases := []struct {
		name     string
		path     string
		wantCode int
	}{
		{"non-numeric id", "/api/tasks/notanumber/log", 400},
		{"unknown instance", "/api/tasks/424242/log", 404},
		{"non-numeric id stream", "/api/tasks/notanumber/log/stream", 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, _ := get(t, h, "GET", tc.path)
			if rec.Code != tc.wantCode {
				t.Errorf("code = %d, want %d", rec.Code, tc.wantCode)
			}
		})
	}

	// a task whose log file was never written yields a placeholder, not an error
	ctx := context.Background()
	ti := &model.TaskInstance{RunID: "etl__r1", TaskID: "load", State: model.TaskSuccess,
		Pool: "default", LogPath: filepath.Join(dir, "never-written.log")}
	if err := st.CreateTaskInstance(ctx, ti); err != nil {
		t.Fatal(err)
	}
	id := findTaskInstanceID(t, st, "etl__r1", "load")
	rec, body := get(t, h, "GET", "/api/tasks/"+strconv.FormatInt(id, 10)+"/log")
	if rec.Code != 200 {
		t.Fatalf("missing log = %d, want 200", rec.Code)
	}
	if body.(string) != "(no log yet)\n" {
		t.Errorf("missing log body = %q", body)
	}
}

func TestTaskLogTailAndStreamingDownload(t *testing.T) {
	h, st, _, _ := setup(t)
	path := filepath.Join(t.TempDir(), "large.log")
	payload := bytes.Repeat([]byte("x"), int(defaultLogTailBytes)+257)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTaskInstance(context.Background(), &model.TaskInstance{
		RunID: "etl__r1", TaskID: "large", State: model.TaskSuccess, Pool: "default", LogPath: path,
	}); err != nil {
		t.Fatal(err)
	}
	id := findTaskInstanceID(t, st, "etl__r1", "large")
	url := "/api/tasks/" + strconv.FormatInt(id, 10) + "/log"
	rec, body := get(t, h, "GET", url)
	if rec.Code != http.StatusOK || len(body.(string)) != int(defaultLogTailBytes) {
		t.Fatalf("tail response: code=%d bytes=%d", rec.Code, len(body.(string)))
	}
	if rec.Header().Get("X-Cronova-Log-Truncated") != "true" {
		t.Fatal("tail response did not report truncation")
	}
	rec, body = get(t, h, "GET", url+"?download=1")
	if rec.Code != http.StatusOK || len(body.(string)) != len(payload) {
		t.Fatalf("download response: code=%d bytes=%d want=%d", rec.Code, len(body.(string)), len(payload))
	}
}

func findTaskInstanceID(t *testing.T, st *sqlite.Store, runID, taskID string) int64 {
	t.Helper()
	tis, err := st.ListTaskInstances(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, ti := range tis {
		if ti.TaskID == taskID {
			return ti.ID
		}
	}
	t.Fatalf("task instance %s/%s not found", runID, taskID)
	return 0
}

// A terminal task's SSE stream replays the whole log as data: lines, flushes a
// partial trailing line on the final drain, and closes with an explicit done event.
func TestStreamLogReplaysTerminalTask(t *testing.T) {
	h, st, _, _ := setup(t)
	ctx := context.Background()
	partial := filepath.Join(t.TempDir(), "partial.log")
	if err := os.WriteFile(partial, []byte("alpha\nbeta"), 0o644); err != nil { // no trailing \n
		t.Fatal(err)
	}
	if err := st.CreateTaskInstance(ctx, &model.TaskInstance{
		RunID: "etl__r1", TaskID: "tail", State: model.TaskSuccess, Pool: "default", LogPath: partial,
	}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		taskID    string
		wantLines []string
	}{
		{"complete lines", "extract", []string{"data: line one\n\n", "data: line two\n\n"}},
		{"partial trailing line flushed", "tail", []string{"data: alpha\n\n", "data: beta\n\n"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := findTaskInstanceID(t, st, "etl__r1", tc.taskID)
			rec, _ := get(t, h, "GET", "/api/tasks/"+strconv.FormatInt(id, 10)+"/log/stream")
			if rec.Code != 200 {
				t.Fatalf("stream = %d", rec.Code)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
				t.Errorf("Content-Type = %q", ct)
			}
			out := rec.Body.String()
			for _, want := range tc.wantLines {
				if !strings.Contains(out, want) {
					t.Errorf("stream missing %q:\n%s", want, out)
				}
			}
			if !strings.HasSuffix(out, "event: done\ndata: end\n\n") {
				t.Errorf("stream did not end with a done event:\n%s", out)
			}
		})
	}
}

func TestStreamLogBoundsInitialReplayAndLongLines(t *testing.T) {
	h, st, _, _ := setup(t)
	path := filepath.Join(t.TempDir(), "no-newline.log")
	payload := append(bytes.Repeat([]byte("A"), 256), bytes.Repeat([]byte("B"), int(sseInitialTailBytes))...)
	payload = append(payload, '\n')
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTaskInstance(context.Background(), &model.TaskInstance{
		RunID: "etl__r1", TaskID: "long-line", State: model.TaskSuccess, Pool: "default", LogPath: path,
	}); err != nil {
		t.Fatal(err)
	}
	id := findTaskInstanceID(t, st, "etl__r1", "long-line")
	rec, _ := get(t, h, "GET", "/api/tasks/"+strconv.FormatInt(id, 10)+"/log/stream")
	out := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(out, "event: truncated") {
		t.Fatalf("bounded SSE response: code=%d prefix=%q", rec.Code, out[:min(len(out), 120)])
	}
	if strings.Contains(out, strings.Repeat("A", 64)) {
		t.Fatal("SSE replay included bytes older than its initial tail window")
	}
	for _, event := range strings.Split(out, "\n\n") {
		if strings.HasPrefix(event, "data: ") && len(event) > sseLineChunkBytes+len("data: ") {
			t.Fatalf("SSE data event has %d bytes, exceeds chunk bound", len(event))
		}
	}
}

func TestStreamLogRejectsWhenConnectionLimitReached(t *testing.T) {
	_, st, trig, _ := setup(t)
	srv := New(st, trig, t.TempDir(), nil, Info{})
	srv.sseSlots = make(chan struct{}, 1)
	srv.sseSlots <- struct{}{}
	h := srv.Handler()
	id := findTaskInstanceID(t, st, "etl__r1", "extract")
	rec, _ := get(t, h, "GET", "/api/tasks/"+strconv.FormatInt(id, 10)+"/log/stream")
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("full SSE pool: code=%d retry-after=%q", rec.Code, rec.Header().Get("Retry-After"))
	}
}

func TestOverviewDashboard(t *testing.T) {
	h, st, _ := newEngineServer(t, &stubTrigger{})
	ctx := context.Background()
	now := time.Now().UTC()
	tp := func(s string) *time.Time {
		ts, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return &ts
	}

	// etl: manual DAG with an older success (5m) and a newer failure (2s)
	if err := st.UpsertDAG(ctx, &model.DAG{DagID: "etl",
		DefinitionYAML: "dag_id: etl\ntasks:\n  - id: extract\n    command: \"echo hi\"\n",
		MaxActiveRuns:  1, StartDate: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDagRun(ctx, &model.DagRun{RunID: "etl__ok", DagID: "etl",
		LogicalDate: *tp("2026-07-01T10:00:00Z"), State: model.RunSuccess, TriggerType: model.TriggerSchedule,
		StartedAt: tp("2026-07-01T10:00:00Z"), FinishedAt: tp("2026-07-01T10:05:00Z")}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDagRun(ctx, &model.DagRun{RunID: "etl__bad", DagID: "etl",
		LogicalDate: *tp("2026-07-01T11:00:00Z"), State: model.RunFailed, TriggerType: model.TriggerSchedule,
		StartedAt: tp("2026-07-01T11:00:00Z"), FinishedAt: tp("2026-07-01T11:00:02Z")}); err != nil {
		t.Fatal(err)
	}
	// sched: paused scheduled DAG
	if err := st.UpsertDAG(ctx, &model.DAG{DagID: "sched", Schedule: "0 6 * * *", Paused: true,
		DefinitionYAML: "dag_id: sched\nschedule: \"0 6 * * *\"\ntasks:\n  - id: t\n    command: \"echo x\"\n",
		MaxActiveRuns:  1, StartDate: now}); err != nil {
		t.Fatal(err)
	}
	// owned: no schedule, description falls back to the owner
	if err := st.UpsertDAG(ctx, &model.DAG{DagID: "owned", Owner: "alice",
		DefinitionYAML: "dag_id: owned\ntasks:\n  - id: t\n    command: \"echo x\"\n",
		MaxActiveRuns:  1, StartDate: now}); err != nil {
		t.Fatal(err)
	}

	rec, body := get(t, h, "GET", "/api/overview")
	if rec.Code != 200 {
		t.Fatalf("overview = %d", rec.Code)
	}
	m := body.(map[string]any)

	stats := m["stats"].(map[string]any)
	for k, want := range map[string]float64{
		"total_dags": 3, "active_dags": 2, "failed": 1, "running_runs": 0, "success_rate": 50,
	} {
		if got, _ := stats[k].(float64); got != want {
			t.Errorf("stats.%s = %v, want %v", k, stats[k], want)
		}
	}

	rows := map[string]map[string]any{}
	for _, r := range m["dags"].([]any) {
		rm := r.(map[string]any)
		rows[rm["dag_id"].(string)] = rm
	}
	etl := rows["etl"]
	if etl["type"] != "manual" || etl["description"] != "manual trigger" || etl["pool"] != "default" {
		t.Errorf("etl row = %v", etl)
	}
	if etl["latest_state"] != "failed" {
		t.Errorf("etl latest_state = %v, want failed", etl["latest_state"])
	}
	if etl["next_schedule"] != "—" { // stub engine reports no next schedule
		t.Errorf("etl next_schedule = %v, want —", etl["next_schedule"])
	}
	spark := etl["sparkline"].([]any)
	if len(spark) != 2 {
		t.Fatalf("sparkline len = %d, want 2", len(spark))
	}
	first := spark[0].(map[string]any) // oldest first
	if first["state"] != "success" || first["ms"] != float64(300000) {
		t.Errorf("sparkline[0] = %v, want success/300000ms", first)
	}
	if sched := rows["sched"]; sched["type"] != "schedule" || sched["description"] != "0 6 * * *" ||
		sched["paused"] != true || sched["next_schedule"] != "paused" {
		t.Errorf("sched row = %v", rows["sched"])
	}
	if owned := rows["owned"]; owned["description"] != "alice" {
		t.Errorf("owned description = %v, want alice", owned["description"])
	}

	activity := m["activity"].([]any)
	if len(activity) != 2 {
		t.Fatalf("activity len = %d, want 2", len(activity))
	}
	newest := activity[0].(map[string]any)
	if newest["run_id"] != "etl__bad" || newest["ms"] != float64(2000) ||
		newest["started"] == "" || newest["finished"] == "" {
		t.Errorf("activity[0] = %v, want etl__bad with 2000ms and timestamps", newest)
	}
}

func TestDagDescription(t *testing.T) {
	cases := []struct {
		name string
		dag  model.DAG
		want string
	}{
		{"schedule wins", model.DAG{Schedule: "0 6 * * *", Owner: "alice"}, "0 6 * * *"},
		{"owner fallback", model.DAG{Owner: "alice"}, "alice"},
		{"manual default", model.DAG{}, "manual trigger"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dagDescription(&tc.dag); got != tc.want {
				t.Errorf("dagDescription = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNextScheduleLabel(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name   string
		paused bool
		next   time.Time
		ok     bool
		want   string // exact match, unless wantRe is set
		wantRe string
	}{
		{name: "paused dag", paused: true, want: "paused"},
		{name: "no upcoming fire", ok: false, want: "—"},
		{name: "due within a minute", next: now.Add(20 * time.Second), ok: true, want: "due"},
		{name: "minutes away", next: now.Add(30*time.Minute + 30*time.Second), ok: true, wantRe: `^in \d+m$`},
		{name: "hours away shows clock time", next: now.Add(3 * time.Hour), ok: true, wantRe: `^\d{2}-\d{2} \d{2}:\d{2}$`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New(nil, &schedStubEngine{next: tc.next, ok: tc.ok}, "", nil, Info{})
			got := s.nextScheduleLabel(context.Background(), &model.DAG{DagID: "d", Paused: tc.paused})
			if tc.wantRe != "" {
				if !regexp.MustCompile(tc.wantRe).MatchString(got) {
					t.Errorf("label = %q, want match %q", got, tc.wantRe)
				}
				return
			}
			if got != tc.want {
				t.Errorf("label = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidateDAGSpec(t *testing.T) {
	h, _, _, _ := setup(t) // no projects dir configured
	cases := []struct {
		name      string
		body      string
		wantValid bool
		wantErrIn string // substring of the error (invalid specs)
		wantWarn  string // substring of a warning (valid specs)
	}{
		{name: "valid spec",
			body:      `{"dag_id":"vd1","start_date":"2026-01-01","tasks":[{"id":"a","type":"shell","command":"echo hi"}]}`,
			wantValid: true},
		{name: "malformed json",
			body: `{"dag_id":`, wantValid: false, wantErrIn: "invalid spec"},
		{name: "unknown dep fails parse",
			body:      `{"dag_id":"vd2","start_date":"2026-01-01","tasks":[{"id":"a","command":"x","deps":["ghost"]}]}`,
			wantValid: false, wantErrIn: "unknown task"},
		{name: "project without projects dir warns",
			body:      `{"dag_id":"vd3","start_date":"2026-01-01","tasks":[{"id":"a","command":"x","project":"p1"}]}`,
			wantValid: true, wantWarn: "no projects dir configured"},
		{name: "invalid project name warns",
			body:      `{"dag_id":"vd4","start_date":"2026-01-01","tasks":[{"id":"a","command":"x","project":"bad name"}]}`,
			wantValid: true, wantWarn: "invalid project name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(h, "POST", "/api/dags/validate", tc.body, nil)
			if rec.Code != 200 { // validate ALWAYS answers 200 with structured feedback
				t.Fatalf("code = %d, want 200 (body %s)", rec.Code, rec.Body.String())
			}
			var m map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
				t.Fatal(err)
			}
			if m["valid"] != tc.wantValid {
				t.Fatalf("valid = %v, want %v: %s", m["valid"], tc.wantValid, rec.Body.String())
			}
			if !tc.wantValid {
				errMsg, _ := m["error"].(string)
				if errMsg == "" || !strings.Contains(errMsg, tc.wantErrIn) {
					t.Errorf("error = %q, want it to contain %q", errMsg, tc.wantErrIn)
				}
				return
			}
			if tc.wantWarn == "" {
				if _, has := m["warnings"]; has {
					t.Errorf("unexpected warnings: %v", m["warnings"])
				}
				if m["tasks"] != float64(1) || m["dag_id"] != "vd1" {
					t.Errorf("valid response = %v", m)
				}
				if cy, _ := m["canonical_yaml"].(string); !strings.Contains(cy, "dag_id: vd1") {
					t.Errorf("canonical_yaml = %q", cy)
				}
				return
			}
			warns, _ := m["warnings"].([]any)
			joined := ""
			for _, w := range warns {
				joined += w.(string) + "\n"
			}
			if !strings.Contains(joined, tc.wantWarn) {
				t.Errorf("warnings = %v, want one containing %q", warns, tc.wantWarn)
			}
		})
	}
}

func TestListDAGsEmptyAndLight(t *testing.T) {
	// a fresh instance answers [] (not null)
	h, _, _ := newEngineServer(t, &stubTrigger{})
	rec, body := get(t, h, "GET", "/api/dags")
	if rec.Code != 200 {
		t.Fatalf("empty list = %d", rec.Code)
	}
	if dags, ok := body.([]any); !ok || len(dags) != 0 {
		t.Errorf("empty list body = %v, want []", body)
	}
	// the list omits the full YAML (detail endpoint carries it)
	h2, _, _, _ := setup(t)
	rec, _ = get(t, h2, "GET", "/api/dags")
	if strings.Contains(rec.Body.String(), "definition_yaml") {
		t.Errorf("list should strip definition_yaml: %s", rec.Body.String())
	}
}

func TestSetPoolValidation(t *testing.T) {
	h, _, _, _ := setup(t)
	for _, qs := range []string{"?slots=0", "?slots=-2", "?slots=abc", ""} {
		rec, _ := get(t, h, "POST", "/api/pools/heavy"+qs)
		if rec.Code != 400 {
			t.Errorf("set pool %q = %d, want 400", qs, rec.Code)
		}
	}
}
