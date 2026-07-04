package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/store"
	"github.com/zoyluo/cronova/internal/store/sqlite"
)

type stubTrigger struct {
	got         string
	gotParams   map[string]string
	createdYML  string
	triggerErr  error  // if set, TriggerManual returns it (e.g. model.ErrNoTasks)
	deleted     string // last dagID passed to DeleteDAG
	deleteErr   error  // if set, DeleteDAG returns it (e.g. model.ErrActiveRuns)
	cancelled   string // last runID passed to CancelRun
	retriedRun  string // last runID passed to RetryRun
	retriedTask string // last "runID/taskID" passed to RetryTask
	markedTask  string // last "runID/taskID=state" passed to MarkTask
	markedRun   string // last "runID=state" passed to MarkRun
	opErr       error  // if set, cancel/retry/mark return it
}

func (s *stubTrigger) TriggerManual(_ context.Context, dagID string, params map[string]string) (string, error) {
	s.got = dagID
	s.gotParams = params
	if s.triggerErr != nil {
		return "", s.triggerErr
	}
	return dagID + "__run1", nil
}

func (s *stubTrigger) CreateDAG(_ context.Context, yamlText string) (string, error) {
	s.createdYML = yamlText
	return "created_dag", nil
}

func (s *stubTrigger) DeleteDAG(_ context.Context, dagID string) error {
	s.deleted = dagID
	return s.deleteErr
}

func (s *stubTrigger) NextSchedule(_ context.Context, _ *model.DAG) (time.Time, bool) {
	return time.Time{}, false
}

func (s *stubTrigger) CancelRun(_ context.Context, runID string) error {
	s.cancelled = runID
	return s.opErr
}
func (s *stubTrigger) RetryRun(_ context.Context, runID string) error {
	s.retriedRun = runID
	return s.opErr
}
func (s *stubTrigger) RetryTask(_ context.Context, runID, taskID string) error {
	s.retriedTask = runID + "/" + taskID
	return s.opErr
}
func (s *stubTrigger) MarkTask(_ context.Context, runID, taskID string, target model.TaskState) error {
	s.markedTask = runID + "/" + taskID + "=" + string(target)
	return s.opErr
}
func (s *stubTrigger) MarkRun(_ context.Context, runID string, target model.RunState) error {
	s.markedRun = runID + "=" + string(target)
	return s.opErr
}

func setup(t *testing.T) (http.Handler, *sqlite.Store, *stubTrigger, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := sqlite.New(filepath.Join(dir, "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	yaml := "dag_id: etl\ntasks:\n  - id: extract\n    command: \"echo hi\"\n"
	if err := st.UpsertDAG(ctx, &model.DAG{DagID: "etl", DefinitionYAML: yaml, MaxActiveRuns: 1, StartDate: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDagRun(ctx, &model.DagRun{RunID: "etl__r1", DagID: "etl", LogicalDate: time.Now().UTC(), State: model.RunSuccess, TriggerType: model.TriggerManual}); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "extract.log")
	_ = os.WriteFile(logPath, []byte("line one\nline two\n"), 0o644)
	ti := &model.TaskInstance{RunID: "etl__r1", TaskID: "extract", State: model.TaskSuccess, Pool: "default", LogPath: logPath}
	if err := st.CreateTaskInstance(ctx, ti); err != nil {
		t.Fatal(err)
	}
	trig := &stubTrigger{}
	return New(st, trig, dir, nil, Info{Executor: "in-process", Tick: "2s"}).Handler(), st, trig, logPath
}

func get(t *testing.T, h http.Handler, method, path string) (*httptest.ResponseRecorder, any) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var body any
	if strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
	} else {
		body = rec.Body.String()
	}
	return rec, body
}

func TestListAndGetDAG(t *testing.T) {
	h, _, _, _ := setup(t)
	rec, body := get(t, h, "GET", "/api/dags")
	if rec.Code != 200 {
		t.Fatalf("list dags: %d", rec.Code)
	}
	arr := body.([]any)
	if len(arr) != 1 {
		t.Fatalf("got %d dags", len(arr))
	}

	rec, body = get(t, h, "GET", "/api/dags/etl")
	if rec.Code != 200 {
		t.Fatalf("get dag: %d", rec.Code)
	}
	d := body.(map[string]any)
	tasks, _ := d["tasks"].([]any)
	if len(tasks) != 1 { // parsed from DefinitionYAML
		t.Errorf("expected 1 parsed task, got %d", len(tasks))
	}

	rec, _ = get(t, h, "GET", "/api/dags/ghost")
	if rec.Code != 404 {
		t.Errorf("missing dag should 404, got %d", rec.Code)
	}
}

func TestDagGraph(t *testing.T) {
	h, st, _, _ := setup(t)
	ctx := context.Background()
	// `report` triggers after `etl` (exists) and `ghost` (missing).
	yaml := "dag_id: report\ntasks:\n  - id: build\n    command: \"echo x\"\ntrigger_after:\n  - dag_id: etl\n  - dag_id: ghost\n"
	if err := st.UpsertDAG(ctx, &model.DAG{DagID: "report", DefinitionYAML: yaml, MaxActiveRuns: 1, StartDate: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	rec, body := get(t, h, "GET", "/api/dag-graph")
	if rec.Code != 200 {
		t.Fatalf("dag-graph: %d", rec.Code)
	}
	m := body.(map[string]any)
	nodes := m["nodes"].([]any)
	edges := m["edges"].([]any)
	// nodes: etl, report, plus a synthetic `ghost` flagged missing.
	byID := map[string]map[string]any{}
	for _, n := range nodes {
		nm := n.(map[string]any)
		byID[nm["dag_id"].(string)] = nm
	}
	if _, ok := byID["etl"]; !ok {
		t.Error("missing etl node")
	}
	if byID["report"] == nil || byID["report"]["type"] != "dependency" {
		t.Errorf("report node = %v, want type=dependency", byID["report"])
	}
	if byID["ghost"] == nil || byID["ghost"]["missing"] != true {
		t.Errorf("ghost node = %v, want missing=true", byID["ghost"])
	}
	// edges: etl->report and ghost->report.
	want := map[string]bool{"etl->report": false, "ghost->report": false}
	for _, e := range edges {
		em := e.(map[string]any)
		want[em["from"].(string)+"->"+em["to"].(string)] = true
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("missing edge %s", k)
		}
	}
}

func TestTriggerAndPause(t *testing.T) {
	h, st, trig, _ := setup(t)
	rec, body := get(t, h, "POST", "/api/dags/etl/trigger")
	if rec.Code != 200 || trig.got != "etl" {
		t.Fatalf("trigger: code=%d got=%q", rec.Code, trig.got)
	}
	if body.(map[string]any)["run_id"] != "etl__run1" {
		t.Errorf("unexpected run_id: %v", body)
	}

	rec, _ = get(t, h, "POST", "/api/dags/etl/pause?paused=true")
	if rec.Code != 200 {
		t.Fatalf("pause: %d", rec.Code)
	}
	d, _ := st.GetDAG(context.Background(), "etl")
	if !d.Paused {
		t.Error("dag not paused")
	}
}

func TestRunDetailAndLog(t *testing.T) {
	h, st, _, _ := setup(t)
	rec, body := get(t, h, "GET", "/api/runs/etl__r1")
	if rec.Code != 200 {
		t.Fatalf("get run: %d", rec.Code)
	}
	m := body.(map[string]any)
	if m["run"].(map[string]any)["state"] != "success" {
		t.Errorf("run state wrong: %v", m["run"])
	}
	if len(m["tasks"].([]any)) != 1 {
		t.Errorf("expected 1 task instance")
	}

	// Log endpoint.
	tis, _ := st.ListTaskInstances(context.Background(), "etl__r1")
	rec, body = get(t, h, "GET", "/api/tasks/"+strconv.FormatInt(tis[0].ID, 10)+"/log")
	if rec.Code != 200 {
		t.Fatalf("get log: %d", rec.Code)
	}
	if !strings.Contains(body.(string), "line one") {
		t.Errorf("log content missing: %q", body)
	}
}

func TestBuildDAG(t *testing.T) {
	h, _, trig, _ := setup(t)
	body := `{"dag_id":"build_test","schedule":"@every 1m","catchup":true,"max_active_runs":2,
	  "tasks":[{"id":"a","type":"shell","command":"echo a"},
	           {"id":"b","command":"echo b","deps":["a"],"pool":"heavy"}],
	  "trigger_after":["up"]}`
	req := httptest.NewRequest("POST", "/api/dags/build", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("build: %d %s", rec.Code, rec.Body.String())
	}
	// The structured spec was rendered to canonical YAML and passed to CreateDAG.
	y := trig.createdYML
	for _, want := range []string{"dag_id: build_test", "@every 1m", "id: a", "deps:", "- a", "pool: heavy", "trigger_after:", "dag_id: up", "catchup: true"} {
		if !strings.Contains(y, want) {
			t.Errorf("generated YAML missing %q:\n%s", want, y)
		}
	}

	// Malformed JSON -> 400.
	bad := httptest.NewRequest("POST", "/api/dags/build", strings.NewReader("{not json"))
	brec := httptest.NewRecorder()
	h.ServeHTTP(brec, bad)
	if brec.Code != 400 {
		t.Errorf("malformed spec should 400, got %d", brec.Code)
	}
}

// A 0-task "shell" DAG: build accepts empty tasks, the detail endpoint round-trips
// (tasks empty, default_retries present), and triggering it is a 400 client error.
func TestShellDAG(t *testing.T) {
	h, st, trig, _ := setup(t)
	ctx := context.Background()

	// build with no tasks -> 200, YAML carries an (empty) tasks key.
	body := `{"dag_id":"shell","default_retries":2,"tasks":[]}`
	req := httptest.NewRequest("POST", "/api/dags/build", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("build shell: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(trig.createdYML, "dag_id: shell") || !strings.Contains(trig.createdYML, "tasks:") {
		t.Errorf("shell YAML unexpected:\n%s", trig.createdYML)
	}

	// Persist a shell directly (stub CreateDAG doesn't touch the store) to test GET round-trip.
	yaml := "dag_id: shell\ndefault_retries: 2\n"
	if err := st.UpsertDAG(ctx, &model.DAG{DagID: "shell", DefinitionYAML: yaml, MaxActiveRuns: 1, DefaultRetries: 2, StartDate: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	rec, bdy := get(t, h, "GET", "/api/dags/shell")
	if rec.Code != 200 {
		t.Fatalf("get shell: %d", rec.Code)
	}
	d := bdy.(map[string]any)
	if tasks, ok := d["tasks"].([]any); ok && len(tasks) != 0 {
		t.Errorf("shell should have no tasks, got %v", d["tasks"])
	}
	if dr, _ := d["default_retries"].(float64); dr != 2 {
		t.Errorf("default_retries = %v, want 2", d["default_retries"])
	}

	// Triggering a shell is a client error (scheduler returns model.ErrNoTasks -> 400).
	trig.triggerErr = model.ErrNoTasks
	rec, _ = get(t, h, "POST", "/api/dags/shell/trigger")
	if rec.Code != 400 {
		t.Errorf("triggering a shell DAG should 400, got %d", rec.Code)
	}
}

// getDAG must distinguish a per-task UNSET retries (inherits default_retries ->
// null) from an explicit value, so the editor can round-trip without baking the
// effective default into the task.
func TestGetDAGRetriesInheritRoundTrip(t *testing.T) {
	h, st, _, _ := setup(t)
	ctx := context.Background()
	yaml := "dag_id: rt\ndefault_retries: 3\ntasks:\n" +
		"  - id: a\n    command: \"echo a\"\n" + // no retries -> inherits 3 -> null in API
		"  - id: b\n    command: \"echo b\"\n    retries: 5\n" // explicit -> 5
	if err := st.UpsertDAG(ctx, &model.DAG{DagID: "rt", DefinitionYAML: yaml, MaxActiveRuns: 1, StartDate: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	_, body := get(t, h, "GET", "/api/dags/rt")
	d := body.(map[string]any)
	if dr, _ := d["default_retries"].(float64); dr != 3 {
		t.Errorf("default_retries = %v, want 3", d["default_retries"])
	}
	tasks := d["tasks"].([]any)
	byID := map[string]map[string]any{}
	for _, tk := range tasks {
		m := tk.(map[string]any)
		byID[m["id"].(string)] = m
	}
	if byID["a"]["retries"] != nil {
		t.Errorf("task a retries = %v, want null (inherits default)", byID["a"]["retries"])
	}
	if r, _ := byID["b"]["retries"].(float64); r != 5 {
		t.Errorf("task b retries = %v, want 5", byID["b"]["retries"])
	}
}

func TestDeleteDAG(t *testing.T) {
	h, _, trig, _ := setup(t)
	// success
	rec, body := get(t, h, "DELETE", "/api/dags/etl")
	if rec.Code != 200 || trig.deleted != "etl" {
		t.Fatalf("delete: code=%d deleted=%q", rec.Code, trig.deleted)
	}
	if body.(map[string]any)["deleted"] != true {
		t.Errorf("unexpected body: %v", body)
	}
	// active runs -> 409
	trig.deleteErr = model.ErrActiveRuns
	rec, _ = get(t, h, "DELETE", "/api/dags/etl")
	if rec.Code != http.StatusConflict {
		t.Errorf("delete with active runs should 409, got %d", rec.Code)
	}
	// not found -> 404
	trig.deleteErr = store.ErrNotFound
	rec, _ = get(t, h, "DELETE", "/api/dags/ghost")
	if rec.Code != 404 {
		t.Errorf("delete missing should 404, got %d", rec.Code)
	}
}

// The stateless schedule preview must reuse the engine's parser and return
// ascending future fire times; invalid expressions are client errors.
func TestSchedulePreview(t *testing.T) {
	h, _, _, _ := setup(t)
	rec, body := get(t, h, "GET", "/api/schedule/preview?schedule="+strings.ReplaceAll("@every 1m", " ", "%20")+"&n=3")
	if rec.Code != 200 {
		t.Fatalf("preview: %d %v", rec.Code, body)
	}
	fires := body.(map[string]any)["fires"].([]any)
	if len(fires) != 3 {
		t.Fatalf("want 3 fires, got %d", len(fires))
	}
	var prev time.Time
	for i, f := range fires {
		ts, err := time.Parse(time.RFC3339, f.(string))
		if err != nil {
			t.Fatalf("fire %d not RFC3339: %v", i, err)
		}
		if !ts.After(prev) {
			t.Errorf("fires not ascending at %d", i)
		}
		if ts.Before(time.Now().Add(-time.Minute)) {
			t.Errorf("fire %d in the past: %v", i, ts)
		}
		prev = ts
	}

	// future start_date anchors the first fire after it
	rec, body = get(t, h, "GET", "/api/schedule/preview?schedule=%40every%201h&start_date=2030-01-01&n=1")
	if rec.Code != 200 {
		t.Fatalf("anchored preview: %d", rec.Code)
	}
	first, _ := time.Parse(time.RFC3339, body.(map[string]any)["fires"].([]any)[0].(string))
	if first.Year() != 2030 {
		t.Errorf("start_date anchor ignored: first fire %v", first)
	}

	// invalid cron -> 400; missing schedule -> 400
	rec, _ = get(t, h, "GET", "/api/schedule/preview?schedule=not%20a%20cron")
	if rec.Code != 400 {
		t.Errorf("invalid schedule should 400, got %d", rec.Code)
	}
	rec, _ = get(t, h, "GET", "/api/schedule/preview")
	if rec.Code != 400 {
		t.Errorf("missing schedule should 400, got %d", rec.Code)
	}
}

func TestPools(t *testing.T) {
	h, st, _, _ := setup(t)
	rec, _ := get(t, h, "POST", "/api/pools/heavy?slots=3")
	if rec.Code != 200 {
		t.Fatalf("set pool: %d", rec.Code)
	}
	p, err := st.GetPool(context.Background(), "heavy")
	if err != nil || p.Slots != 3 {
		t.Fatalf("pool not set: %v %v", p, err)
	}
	rec, body := get(t, h, "GET", "/api/pools")
	if rec.Code != 200 || len(body.([]any)) < 2 {
		t.Errorf("list pools: code=%d body=%v", rec.Code, body)
	}
}

func TestMetrics(t *testing.T) {
	h, _, _, _ := setup(t)
	rec, body := get(t, h, "GET", "/metrics")
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	text, _ := body.(string)
	for _, want := range []string{"cronova_up 1", "cronova_dags_total 1", `cronova_runs_total{state="success"} 1`, "# TYPE cronova_runs_active gauge"} {
		if !strings.Contains(text, want) {
			t.Errorf("/metrics missing %q:\n%s", want, text)
		}
	}
}

func TestAuditTrail(t *testing.T) {
	h, _, _, _ := setup(t)
	// a trigger is recorded (the stub engine succeeds, so the handler audits it)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/dags/etl/trigger", nil))
	if rec.Code != 200 {
		t.Fatalf("trigger status = %d", rec.Code)
	}
	rec2, body := get(t, h, "GET", "/api/audit")
	if rec2.Code != 200 {
		t.Fatalf("audit status = %d", rec2.Code)
	}
	entries, _ := body.([]any)
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1: %v", len(entries), body)
	}
	e := entries[0].(map[string]any)
	if e["action"] != "trigger" || e["target"] != "etl" || e["actor"] != "anonymous" {
		t.Fatalf("audit entry = %v", e)
	}
}
