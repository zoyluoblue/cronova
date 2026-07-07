// Package api exposes cronova over HTTP: a JSON REST API for DAGs, runs, task
// instances, logs, and pools, plus Server-Sent-Events live log tailing, and it
// serves the embedded web UI. It runs in-process with the scheduler, so manual
// triggers go straight through (no cross-process DB hop). See ARCHITECTURE §16.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/scheduler/parser"
	"github.com/zoyluo/cronova/internal/store"
	"gopkg.in/yaml.v3"
)

// Engine is the slice of the scheduler the API needs.
type Engine interface {
	TriggerManual(ctx context.Context, dagID string, params map[string]string) (string, error)
	CreateDAG(ctx context.Context, yamlText string) (string, error)
	DeleteDAG(ctx context.Context, dagID string) error
	NextSchedule(ctx context.Context, d *model.DAG) (time.Time, bool)
	CancelRun(ctx context.Context, runID string) error
	RetryRun(ctx context.Context, runID string) error
	RetryTask(ctx context.Context, runID, taskID string) error
	MarkTask(ctx context.Context, runID, taskID string, target model.TaskState) error
	MarkRun(ctx context.Context, runID string, target model.RunState) error
}

// Info is static runtime metadata shown in the console's status panel.
type Info struct {
	Executor    string `json:"executor"`
	Tick        string `json:"tick"`
	TZ          string `json:"tz"` // server timezone label, e.g. "CST (UTC+08:00)"
	AuthEnabled bool   `json:"auth_enabled"`
}

// Server holds the API dependencies.
type Server struct {
	store       store.Store
	eng         Engine
	logDir      string
	projectsDir string // uploaded project files ("" = uploads disabled)
	web         fs.FS
	info        Info
	auth        AuthConfig
	started     time.Time // for /metrics uptime
}

func New(st store.Store, eng Engine, logDir string, web fs.FS, info Info) *Server {
	if info.TZ == "" {
		// The engine evaluates schedules against UTC anchors (all persisted
		// timestamps are UTC), so UTC — not the server's wall clock — is the
		// honest label for "what timezone do cron fields mean".
		info.TZ = "UTC"
	}
	return &Server{store: st, eng: eng, logDir: logDir, web: web, info: info, started: time.Now()}
}

// SetAuth enables/configures authentication. Must be called before Handler().
func (s *Server) SetAuth(cfg AuthConfig) {
	s.auth = cfg
	s.info.AuthEnabled = cfg.Enabled
}

// SetProjectsDir points project uploads at dir. "" disables the endpoints. Must
// be called before Handler(). It is the same dir the scheduler stages from.
func (s *Server) SetProjectsDir(dir string) { s.projectsDir = dir }

// Handler builds the HTTP routes (Go 1.22+ method+pattern mux).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/info", s.getInfo)
	mux.HandleFunc("GET /api/overview", s.overview)
	mux.HandleFunc("GET /api/dags", s.listDAGs)
	mux.HandleFunc("GET /api/dag-graph", s.dagGraph)
	mux.HandleFunc("GET /api/schedule/preview", s.schedulePreview)
	mux.HandleFunc("POST /api/dags", s.createDAG)
	mux.HandleFunc("POST /api/dags/build", s.buildDAG)
	mux.HandleFunc("POST /api/dags/validate", s.validateDAG)
	mux.HandleFunc("GET /api/dags/{id}", s.getDAG)
	mux.HandleFunc("DELETE /api/dags/{id}", s.deleteDAG)
	mux.HandleFunc("POST /api/dags/{id}/trigger", s.triggerDAG)
	mux.HandleFunc("POST /api/dags/{id}/pause", s.pauseDAG)
	mux.HandleFunc("GET /api/dags/{id}/runs", s.listRuns)
	mux.HandleFunc("GET /api/runs/{runID}", s.getRun)
	mux.HandleFunc("POST /api/runs/{runID}/cancel", s.cancelRun)
	mux.HandleFunc("POST /api/runs/{runID}/retry", s.retryRun)
	mux.HandleFunc("POST /api/runs/{runID}/tasks/{taskID}/retry", s.retryTask)
	mux.HandleFunc("POST /api/runs/{runID}/tasks/{taskID}/mark", s.markTask)
	mux.HandleFunc("POST /api/runs/{runID}/mark", s.markRun)
	mux.HandleFunc("GET /api/tasks/{tiID}/log", s.getLog)
	mux.HandleFunc("GET /api/tasks/{tiID}/log/stream", s.streamLog)
	mux.HandleFunc("GET /api/pools", s.listPools)
	mux.HandleFunc("POST /api/pools/{name}", s.setPool)
	// UI-managed config: variables + connections
	mux.HandleFunc("GET /api/variables", s.listVariables)
	mux.HandleFunc("POST /api/variables/{key}", s.setVariable)
	mux.HandleFunc("DELETE /api/variables/{key}", s.deleteVariable)
	mux.HandleFunc("GET /api/connections", s.listConnections)
	mux.HandleFunc("POST /api/connections/{id}", s.setConnection)
	mux.HandleFunc("DELETE /api/connections/{id}", s.deleteConnection)
	// uploaded projects (attach to shell tasks): writes are admin-gated by withAuth
	mux.HandleFunc("GET /api/projects", s.listProjects)
	mux.HandleFunc("GET /api/projects/{name}", s.getProject)
	mux.HandleFunc("POST /api/projects/{name}", s.uploadProject)
	mux.HandleFunc("DELETE /api/projects/{name}", s.deleteProject)
	// auth + ops endpoints
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.HandleFunc("GET /api/me", s.me)
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.HandleFunc("GET /metrics", s.metrics) // Prometheus; unauthenticated (non-/api/ path)
	mux.HandleFunc("GET /api/audit", s.listAudit)
	mux.HandleFunc("GET /api/tokens", s.listTokens)
	mux.HandleFunc("POST /api/tokens", s.createToken)
	mux.HandleFunc("DELETE /api/tokens/{id}", s.deleteToken)
	mux.HandleFunc("GET /openapi.json", s.openAPISpec) // unauthenticated (non-/api/ path)
	mux.HandleFunc("GET /docs", s.docsPage)            // unauthenticated (non-/api/ path)
	if s.web != nil {
		// no-cache: embedded assets share a fixed modtime, so without this a
		// browser can serve a stale console after the binary is upgraded.
		fileServer := http.FileServerFS(s.web)
		mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-cache")
			fileServer.ServeHTTP(w, r)
		}))
	}
	return s.withAuth(mux)
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// decodeJSON reads a JSON request body (capped at 1 MiB) into v.
func decodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
}

func mapErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		httpErr(w, http.StatusNotFound, "not found")
	case errors.Is(err, model.ErrNoTasks), errors.Is(err, model.ErrBadMarkState):
		httpErr(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, model.ErrActiveRuns), errors.Is(err, model.ErrRunNotActive), errors.Is(err, model.ErrNothingToRetry), errors.Is(err, model.ErrRunStillActive):
		httpErr(w, http.StatusConflict, err.Error())
	default:
		httpErr(w, http.StatusInternalServerError, err.Error())
	}
}

// --- DAG handlers ---

func (s *Server) getInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.info)
}

// schedulePreview returns the next n fire times for a schedule expression,
// computed by the SAME parser the engine uses — the console never does
// client-side cron math (it would drift and lie). Stateless editing preview:
// anchored at now (or start_date, if later), NOT at the engine's run anchor.
func (s *Server) schedulePreview(w http.ResponseWriter, r *http.Request) {
	spec := r.URL.Query().Get("schedule")
	if spec == "" {
		httpErr(w, http.StatusBadRequest, "schedule is required")
		return
	}
	sched, err := parser.ParseSchedule(spec)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "invalid schedule: "+err.Error())
		return
	}
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	if n <= 0 {
		n = 3
	}
	if n > 10 {
		n = 10
	}
	anchor := time.Now().UTC()
	if sd := r.URL.Query().Get("start_date"); sd != "" {
		if ts, terr := time.Parse("2006-01-02", sd); terr == nil && ts.After(anchor) {
			anchor = ts.UTC()
		}
	}
	fires := make([]string, 0, n)
	cur := anchor
	for i := 0; i < n; i++ {
		cur = sched.Next(cur)
		if cur.IsZero() {
			break
		}
		fires = append(fires, cur.Format(time.RFC3339))
	}
	writeJSON(w, http.StatusOK, map[string]any{"fires": fires})
}

func (s *Server) listDAGs(w http.ResponseWriter, r *http.Request) {
	dags, err := s.store.ListDAGs(r.Context())
	if err != nil {
		mapErr(w, err)
		return
	}
	for _, d := range dags {
		d.DefinitionYAML = "" // keep the list light; full YAML is on the detail endpoint
	}
	if dags == nil {
		dags = []*model.DAG{} // [] not null for a fresh/empty instance
	}
	writeJSON(w, http.StatusOK, dags)
}

// dagGraph returns the cross-DAG dependency graph: one node per DAG plus an edge
// from each upstream DAG to every DAG that declares trigger_after on it. Edges to
// unknown DAGs are surfaced as nodes flagged `missing` so dangling refs are visible.
func (s *Server) dagGraph(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dags, err := s.store.ListDAGs(ctx)
	if err != nil {
		mapErr(w, err)
		return
	}
	type node struct {
		DagID       string `json:"dag_id"`
		Type        string `json:"type"`
		Paused      bool   `json:"paused"`
		LatestState string `json:"latest_state"`
		Missing     bool   `json:"missing"`
	}
	type edge struct {
		From string `json:"from"` // upstream DAG
		To   string `json:"to"`   // DAG that triggers after `from`
	}
	known := make(map[string]bool, len(dags))
	for _, d := range dags {
		known[d.DagID] = true
	}
	nodes := make([]node, 0, len(dags))
	edges := make([]edge, 0)
	seenMissing := map[string]bool{}
	for _, d := range dags {
		typ := "manual"
		var upstreams []string
		if pd, perr := parser.Parse([]byte(d.DefinitionYAML)); perr == nil {
			upstreams = pd.TriggerAfter
			if len(pd.TriggerAfter) > 0 {
				typ = "dependency"
			}
			if d.Schedule != "" {
				typ = "schedule"
			}
		} else if d.Schedule != "" {
			typ = "schedule"
		}
		latest := ""
		if runs, _ := s.store.ListDagRuns(ctx, d.DagID, 1); len(runs) > 0 {
			latest = string(runs[0].State)
		}
		nodes = append(nodes, node{DagID: d.DagID, Type: typ, Paused: d.Paused, LatestState: latest})
		for _, up := range upstreams {
			edges = append(edges, edge{From: up, To: d.DagID})
			if !known[up] && !seenMissing[up] {
				seenMissing[up] = true
				nodes = append(nodes, node{DagID: up, Type: "manual", Missing: true})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes, "edges": edges})
}

// editTask is a DAG task shaped for the editor: per-task retries/retry_delay are
// pointers so an UNSET value (which inherits default_retries) round-trips as null
// rather than being collapsed into — and then baked back as — the effective int.
type editTask struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Command     string          `json:"command"`
	Deps        []string        `json:"deps,omitempty"`
	Pool        string          `json:"pool"`
	Priority    int             `json:"priority"`
	Retries     *int            `json:"retries"`     // null => inherit default_retries
	RetryDelay  *int            `json:"retry_delay"` // null => inherit default
	Timeout     int             `json:"timeout"`
	SLA         int             `json:"sla"`
	TriggerRule string          `json:"trigger_rule"`
	HTTP        *model.HTTPSpec `json:"http,omitempty"`
	Conn        string          `json:"conn,omitempty"`
	Project     string          `json:"project,omitempty"`
}

// dagDetail is the editor-facing DAG. The outer Tasks shadows model.DAG.Tasks in
// JSON (shallower field wins), so the editor gets pointer-typed retry fields.
type dagDetail struct {
	*model.DAG
	Tasks []editTask `json:"tasks"`
}

func (s *Server) getDAG(w http.ResponseWriter, r *http.Request) {
	d, err := s.store.GetDAG(r.Context(), r.PathValue("id"))
	if err != nil {
		mapErr(w, err)
		return
	}
	// Parse the stored YAML (source of truth) for tasks, dependencies, and the
	// DAG-level default_retries (not a stored column).
	tasks := []editTask{}
	if parsed, perr := parser.Parse([]byte(d.DefinitionYAML)); perr == nil {
		d.TriggerAfter = parsed.TriggerAfter
		d.DefaultRetries = parsed.DefaultRetries
		d.NotifyURL = parsed.NotifyURL
		d.NotifyOn = parsed.NotifyOn
		d.SLA = parsed.SLA
		d.DagrunTimeout = parsed.DagrunTimeout
		// Pull the RAW per-task retry pointers so "unset" stays null for the editor.
		var raw struct {
			Tasks []struct {
				ID         string `yaml:"id"`
				Retries    *int   `yaml:"retries"`
				RetryDelay *int   `yaml:"retry_delay"`
			} `yaml:"tasks"`
		}
		_ = yaml.Unmarshal([]byte(d.DefinitionYAML), &raw)
		type rp struct{ retries, retryDelay *int }
		rawByID := make(map[string]rp, len(raw.Tasks))
		for _, rt := range raw.Tasks {
			rawByID[rt.ID] = rp{rt.Retries, rt.RetryDelay}
		}
		for _, tk := range parsed.Tasks {
			et := editTask{ID: tk.ID, Type: tk.Type, Command: tk.Command, Deps: tk.Deps, Pool: tk.Pool, Priority: tk.Priority, Timeout: tk.Timeout, SLA: tk.SLA, TriggerRule: tk.TriggerRule, HTTP: tk.HTTP, Conn: tk.Conn, Project: tk.Project}
			if p, ok := rawByID[tk.ID]; ok {
				et.Retries, et.RetryDelay = p.retries, p.retryDelay
			}
			tasks = append(tasks, et)
		}
	}
	d.Tasks = nil // emit via dagDetail.Tasks instead
	writeJSON(w, http.StatusOK, dagDetail{DAG: d, Tasks: tasks})
}

func (s *Server) deleteDAG(w http.ResponseWriter, r *http.Request) {
	if err := s.eng.DeleteDAG(r.Context(), r.PathValue("id")); err != nil {
		mapErr(w, err) // ErrNotFound -> 404, ErrActiveRuns -> 409
		return
	}
	s.audit(r, "delete_dag", r.PathValue("id"), "")
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (s *Server) triggerDAG(w http.ResponseWriter, r *http.Request) {
	// optional trigger-time params (JSON {"params":{...}}); empty body = no params
	var req struct {
		Params map[string]string `json:"params"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		_ = decodeJSON(r, &req) // tolerate an absent/blank body — params are optional
	}
	runID, err := s.eng.TriggerManual(r.Context(), r.PathValue("id"), req.Params)
	if err != nil {
		mapErr(w, err)
		return
	}
	s.audit(r, "trigger", r.PathValue("id"), runID)
	writeJSON(w, http.StatusOK, map[string]string{"run_id": runID})
}

func (s *Server) cancelRun(w http.ResponseWriter, r *http.Request) {
	if err := s.eng.CancelRun(r.Context(), r.PathValue("runID")); err != nil {
		mapErr(w, err)
		return
	}
	s.audit(r, "cancel", r.PathValue("runID"), "")
	writeJSON(w, http.StatusOK, map[string]bool{"cancelled": true})
}

func (s *Server) retryRun(w http.ResponseWriter, r *http.Request) {
	if err := s.eng.RetryRun(r.Context(), r.PathValue("runID")); err != nil {
		mapErr(w, err)
		return
	}
	s.audit(r, "retry_run", r.PathValue("runID"), "")
	writeJSON(w, http.StatusOK, map[string]bool{"retried": true})
}

func (s *Server) retryTask(w http.ResponseWriter, r *http.Request) {
	if err := s.eng.RetryTask(r.Context(), r.PathValue("runID"), r.PathValue("taskID")); err != nil {
		mapErr(w, err)
		return
	}
	s.audit(r, "retry_task", r.PathValue("runID"), r.PathValue("taskID"))
	writeJSON(w, http.StatusOK, map[string]bool{"retried": true})
}

// markState is the request body for the two mark endpoints: {"state": "..."}.
type markState struct {
	State string `json:"state"`
}

func decodeMarkState(r *http.Request) (string, error) {
	var m markState
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&m); err != nil {
		return "", err
	}
	return m.State, nil
}

func (s *Server) markTask(w http.ResponseWriter, r *http.Request) {
	state, err := decodeMarkState(r)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if err := s.eng.MarkTask(r.Context(), r.PathValue("runID"), r.PathValue("taskID"), model.TaskState(state)); err != nil {
		mapErr(w, err)
		return
	}
	s.audit(r, "mark_task", r.PathValue("runID"), r.PathValue("taskID")+"="+state)
	writeJSON(w, http.StatusOK, map[string]bool{"marked": true})
}

func (s *Server) markRun(w http.ResponseWriter, r *http.Request) {
	state, err := decodeMarkState(r)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if err := s.eng.MarkRun(r.Context(), r.PathValue("runID"), model.RunState(state)); err != nil {
		mapErr(w, err)
		return
	}
	s.audit(r, "mark_run", r.PathValue("runID"), state)
	writeJSON(w, http.StatusOK, map[string]bool{"marked": true})
}

func (s *Server) createDAG(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		httpErr(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	dagID, err := s.eng.CreateDAG(r.Context(), string(body))
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error()) // YAML/validation errors are client errors
		return
	}
	s.audit(r, "create_dag", dagID, "")
	writeJSON(w, http.StatusOK, map[string]string{"dag_id": dagID})
}

// --- structured DAG builder (UI form -> YAML -> create/update) ---

type taskSpec struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Command     string          `json:"command"`
	Deps        []string        `json:"deps"`
	Pool        string          `json:"pool"`
	Priority    int             `json:"priority"`
	Retries     *int            `json:"retries"`
	RetryDelay  *int            `json:"retry_delay"`
	Timeout     int             `json:"timeout"`
	SLA         int             `json:"sla"`
	TriggerRule string          `json:"trigger_rule"`
	HTTP        *model.HTTPSpec `json:"http,omitempty"`
	Conn        string          `json:"conn,omitempty"`
	Project     string          `json:"project,omitempty"`
}

type dagSpec struct {
	DagID          string     `json:"dag_id"`
	Schedule       string     `json:"schedule"`
	StartDate      string     `json:"start_date"`
	Catchup        bool       `json:"catchup"`
	MaxActiveRuns  int        `json:"max_active_runs"`
	DefaultRetries int        `json:"default_retries"`
	Tasks          []taskSpec `json:"tasks"`
	TriggerAfter   []string   `json:"trigger_after"`
	NotifyURL      string     `json:"notify_url"`
	NotifyOn       []string   `json:"notify_on"` // "failure", "success"
	SLA            int        `json:"sla"`
	DagrunTimeout  int        `json:"dagrun_timeout"`
}

// buildDAG accepts a structured DAG spec from the UI form, renders it to the
// canonical YAML, and routes it through CreateDAG — so the same parser does all
// validation (cycles, deps, cron, id charset) and the YAML file stays the
// source of truth. Keyed by dag_id, so editing an existing DAG is an upsert.
func (s *Server) buildDAG(w http.ResponseWriter, r *http.Request) {
	var spec dagSpec
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&spec); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid spec: "+err.Error())
		return
	}
	yml, err := specToYAML(spec)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// Audit a genuinely NEW dag as create_dag; an edit (the console re-POSTs this on
	// every debounced save) is not audited, so the trail stays meaningful, not spammed.
	// A soft-deleted id counts as new — CreateDAG revives it (deleted_at cleared).
	existing, existsErr := s.store.GetDAG(r.Context(), spec.DagID)
	isNew := errors.Is(existsErr, store.ErrNotFound) || (existing != nil && existing.DeletedAt != nil)
	dagID, err := s.eng.CreateDAG(r.Context(), string(yml))
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if isNew {
		s.audit(r, "create_dag", dagID, "")
	}
	writeJSON(w, http.StatusOK, map[string]string{"dag_id": dagID})
}

// validateDAG is a dry run of buildDAG: it renders + validates a spec (same
// checks as create — cycle/dep/cron/id) but persists NOTHING. It always returns
// 200 with {valid, ...} so an AI author gets structured feedback to iterate on
// rather than an HTTP error to interpret.
func (s *Server) validateDAG(w http.ResponseWriter, r *http.Request) {
	var spec dagSpec
	if err := decodeJSON(r, &spec); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": "invalid spec: " + err.Error()})
		return
	}
	yml, err := specToYAML(spec)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": err.Error()})
		return
	}
	d, err := parser.Parse(yml)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": err.Error(), "canonical_yaml": string(yml)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": true, "dag_id": d.DagID, "tasks": len(d.Tasks), "canonical_yaml": string(yml)})
}

func specToYAML(spec dagSpec) ([]byte, error) {
	type httpOut struct {
		Method         string            `yaml:"method,omitempty"`
		URL            string            `yaml:"url"`
		Headers        map[string]string `yaml:"headers,omitempty"`
		Body           string            `yaml:"body,omitempty"`
		ExpectedStatus []int             `yaml:"expected_status,omitempty"`
	}
	type taskOut struct {
		ID          string   `yaml:"id"`
		Type        string   `yaml:"type,omitempty"`
		Command     string   `yaml:"command,omitempty"`
		Deps        []string `yaml:"deps,omitempty"`
		Pool        string   `yaml:"pool,omitempty"`
		Priority    int      `yaml:"priority,omitempty"`
		Retries     *int     `yaml:"retries,omitempty"`
		RetryDelay  *int     `yaml:"retry_delay,omitempty"`
		Timeout     int      `yaml:"timeout,omitempty"`
		SLA         int      `yaml:"sla,omitempty"`
		TriggerRule string   `yaml:"trigger_rule,omitempty"`
		Conn        string   `yaml:"conn,omitempty"`
		Project     string   `yaml:"project,omitempty"`
		HTTP        *httpOut `yaml:"http,omitempty"`
	}
	type triggerOut struct {
		DagID string `yaml:"dag_id"`
	}
	type notifyOut struct {
		URL string   `yaml:"url"`
		On  []string `yaml:"on"`
	}
	type dagOut struct {
		DagID          string       `yaml:"dag_id"`
		Schedule       string       `yaml:"schedule,omitempty"`
		StartDate      string       `yaml:"start_date,omitempty"`
		Catchup        bool         `yaml:"catchup"`
		MaxActiveRuns  int          `yaml:"max_active_runs,omitempty"`
		DefaultRetries int          `yaml:"default_retries,omitempty"`
		SLA            int          `yaml:"sla,omitempty"`
		DagrunTimeout  int          `yaml:"dagrun_timeout,omitempty"`
		Tasks          []taskOut    `yaml:"tasks"`
		TriggerAfter   []triggerOut `yaml:"trigger_after,omitempty"`
		Notify         *notifyOut   `yaml:"notify,omitempty"`
	}
	out := dagOut{
		DagID: spec.DagID, Schedule: spec.Schedule, StartDate: spec.StartDate,
		Catchup: spec.Catchup, MaxActiveRuns: spec.MaxActiveRuns, DefaultRetries: spec.DefaultRetries,
		SLA: spec.SLA, DagrunTimeout: spec.DagrunTimeout,
	}
	if url := strings.TrimSpace(spec.NotifyURL); url != "" {
		on := []string{}
		for _, ev := range spec.NotifyOn {
			if ev == "failure" || ev == "success" {
				on = append(on, ev)
			}
		}
		// Persist the URL even with no events yet (a "configured but idle" state
		// that round-trips), rather than silently dropping what the user typed.
		out.Notify = &notifyOut{URL: url, On: on}
	}
	for _, t := range spec.Tasks {
		to := taskOut{
			ID: t.ID, Type: t.Type, Command: t.Command, Deps: t.Deps, Pool: t.Pool,
			Priority: t.Priority, Retries: t.Retries, RetryDelay: t.RetryDelay, Timeout: t.Timeout,
			SLA: t.SLA, TriggerRule: t.TriggerRule, Conn: t.Conn, Project: t.Project,
		}
		if t.Type == "http" && t.HTTP != nil {
			to.HTTP = &httpOut{
				Method: t.HTTP.Method, URL: t.HTTP.URL, Headers: t.HTTP.Headers,
				Body: t.HTTP.Body, ExpectedStatus: t.HTTP.ExpectedStatus,
			}
		}
		out.Tasks = append(out.Tasks, to)
	}
	for _, ta := range spec.TriggerAfter {
		if ta != "" {
			out.TriggerAfter = append(out.TriggerAfter, triggerOut{DagID: ta})
		}
	}
	return yaml.Marshal(out)
}

// overview powers the DAGs dashboard in a single call: aggregate stats plus a
// per-DAG summary (type, pool, latest state, recent-run sparkline, next fire).
func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dags, err := s.store.ListDAGs(ctx)
	if err != nil {
		mapErr(w, err)
		return
	}
	running, _ := s.store.ListDagRunsByState(ctx, model.RunRunning)

	// sparkPoint: one recent run in the per-DAG sparkline; ms is real run
	// duration (0 when not finished), so the bar can honestly encode height.
	type sparkPoint struct {
		State string `json:"state"`
		MS    int64  `json:"ms"`
	}
	type dagRow struct {
		DagID        string       `json:"dag_id"`
		Type         string       `json:"type"`
		Description  string       `json:"description"`
		Paused       bool         `json:"paused"`
		Pool         string       `json:"pool"`
		LatestState  string       `json:"latest_state"`
		Sparkline    []sparkPoint `json:"sparkline"`
		NextSchedule string       `json:"next_schedule"`
	}
	rows := make([]dagRow, 0, len(dags))
	active, failedDags, success, terminal := 0, 0, 0, 0

	for _, d := range dags {
		if !d.Paused {
			active++
		}
		runs, _ := s.store.ListDagRuns(ctx, d.DagID, 14) // newest first
		spark := make([]sparkPoint, 0, len(runs))
		for i := len(runs) - 1; i >= 0; i-- { // oldest -> newest for the bar chart
			rr := runs[i]
			var ms int64
			if rr.StartedAt != nil && rr.FinishedAt != nil {
				if d := rr.FinishedAt.Sub(*rr.StartedAt).Milliseconds(); d > 0 {
					ms = d
				}
			}
			spark = append(spark, sparkPoint{State: string(rr.State), MS: ms})
			switch rr.State {
			case model.RunSuccess:
				success++
				terminal++
			case model.RunFailed, model.RunTimedOut:
				terminal++
			}
		}
		latest := ""
		if len(runs) > 0 {
			latest = string(runs[0].State)
			if runs[0].State == model.RunFailed || runs[0].State == model.RunTimedOut {
				failedDags++
			}
		}

		typ, pool := "manual", "default"
		if pd, perr := parser.Parse([]byte(d.DefinitionYAML)); perr == nil {
			if len(pd.TriggerAfter) > 0 {
				typ = "dependency"
			}
			if d.Schedule != "" {
				typ = "schedule"
			}
			if len(pd.Tasks) > 0 {
				seen := map[string]bool{}
				pools := []string{}
				for _, t := range pd.Tasks {
					if !seen[t.Pool] {
						seen[t.Pool] = true
						pools = append(pools, t.Pool)
					}
				}
				pool = strings.Join(pools, ", ")
			}
		}

		rows = append(rows, dagRow{
			DagID:        d.DagID,
			Type:         typ,
			Description:  dagDescription(d),
			Paused:       d.Paused,
			Pool:         pool,
			LatestState:  latest,
			Sparkline:    spark,
			NextSchedule: s.nextScheduleLabel(ctx, d),
		})
	}

	rate := 100.0
	if terminal > 0 {
		rate = float64(success) / float64(terminal) * 100
	}

	// global recent-run timeline (across all live DAGs), newest first
	type activityItem struct {
		DagID    string `json:"dag_id"`
		RunID    string `json:"run_id"`
		State    string `json:"state"`
		Started  string `json:"started,omitempty"`
		Finished string `json:"finished,omitempty"`
		MS       int64  `json:"ms"`
	}
	recent, _ := s.store.RecentRuns(ctx, 24)
	activity := make([]activityItem, 0, len(recent))
	for _, rr := range recent {
		it := activityItem{DagID: rr.DagID, RunID: rr.RunID, State: string(rr.State)}
		if rr.StartedAt != nil {
			it.Started = rr.StartedAt.UTC().Format(time.RFC3339)
		}
		if rr.FinishedAt != nil {
			it.Finished = rr.FinishedAt.UTC().Format(time.RFC3339)
		}
		if rr.StartedAt != nil && rr.FinishedAt != nil {
			if d := rr.FinishedAt.Sub(*rr.StartedAt).Milliseconds(); d > 0 {
				it.MS = d
			}
		}
		activity = append(activity, it)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"stats": map[string]any{
			"active_dags":  active,
			"total_dags":   len(dags),
			"running_runs": len(running),
			"success_rate": rate,
			"failed":       failedDags,
		},
		"dags":     rows,
		"activity": activity,
	})
}

func dagDescription(d *model.DAG) string {
	if d.Schedule != "" {
		return d.Schedule
	}
	if d.Owner != "" {
		return d.Owner
	}
	return "manual trigger"
}

func (s *Server) nextScheduleLabel(ctx context.Context, d *model.DAG) string {
	if d.Paused {
		return "paused"
	}
	next, ok := s.eng.NextSchedule(ctx, d)
	if !ok {
		return "—"
	}
	now := time.Now()
	delta := next.Sub(now)
	switch {
	case delta < time.Minute:
		return "due"
	case delta < time.Hour:
		return fmt.Sprintf("in %dm", int(delta.Minutes()))
	default:
		return next.Local().Format("01-02 15:04")
	}
}

func (s *Server) pauseDAG(w http.ResponseWriter, r *http.Request) {
	paused := r.URL.Query().Get("paused") != "false" // default true
	if err := s.store.SetDAGPaused(r.Context(), r.PathValue("id"), paused); err != nil {
		mapErr(w, err)
		return
	}
	action := "unpause"
	if paused {
		action = "pause"
	}
	s.audit(r, action, r.PathValue("id"), "")
	writeJSON(w, http.StatusOK, map[string]bool{"paused": paused})
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	runs, err := s.store.ListDagRuns(r.Context(), r.PathValue("id"), limit)
	if err != nil {
		mapErr(w, err)
		return
	}
	if runs == nil {
		runs = []*model.DagRun{} // return [] not null for an empty history
	}
	writeJSON(w, http.StatusOK, runs)
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.store.GetDagRun(r.Context(), r.PathValue("runID"))
	if err != nil {
		mapErr(w, err)
		return
	}
	tis, err := s.store.ListTaskInstances(r.Context(), run.RunID)
	if err != nil {
		mapErr(w, err)
		return
	}
	if tis == nil {
		tis = []*model.TaskInstance{} // [] not null: a queued run has no task instances yet
	}
	writeJSON(w, http.StatusOK, struct {
		Run   *model.DagRun         `json:"run"`
		Tasks []*model.TaskInstance `json:"tasks"`
	}{run, tis})
}

func (s *Server) listPools(w http.ResponseWriter, r *http.Request) {
	pools, err := s.store.ListPools(r.Context())
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pools)
}

func (s *Server) setPool(w http.ResponseWriter, r *http.Request) {
	slots, err := strconv.Atoi(r.URL.Query().Get("slots"))
	if err != nil || slots <= 0 {
		httpErr(w, http.StatusBadRequest, "slots must be a positive integer")
		return
	}
	if err := s.store.UpsertPool(r.Context(), &model.Pool{Name: r.PathValue("name"), Slots: slots}); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": r.PathValue("name"), "slots": slots})
}

// --- logs ---

func (s *Server) taskInstance(w http.ResponseWriter, r *http.Request) (*model.TaskInstance, bool) {
	id, err := strconv.ParseInt(r.PathValue("tiID"), 10, 64)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "invalid task instance id")
		return nil, false
	}
	ti, err := s.store.GetTaskInstance(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return nil, false
	}
	return ti, true
}

func (s *Server) getLog(w http.ResponseWriter, r *http.Request) {
	ti, ok := s.taskInstance(w, r)
	if !ok {
		return
	}
	data, err := os.ReadFile(ti.LogPath)
	if err != nil {
		if os.IsNotExist(err) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("(no log yet)\n"))
			return
		}
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(data)
}

// streamLog tails the task's log over Server-Sent Events until the task reaches
// a terminal state and no new bytes remain, or the client disconnects.
func (s *Server) streamLog(w http.ResponseWriter, r *http.Request) {
	ti, ok := s.taskInstance(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ctx := r.Context()
	var offset int64
	var lineBuf []byte // holds an incomplete trailing line across ticks
	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()

	emit := func(line []byte) {
		_, _ = w.Write([]byte("data: "))
		_, _ = w.Write(line)
		_, _ = w.Write([]byte("\n\n"))
	}
	// drain reads newly-appended bytes and emits only complete lines, buffering
	// any partial trailing line until its newline arrives (or final flush).
	drain := func(final bool) {
		f, err := os.Open(ti.LogPath)
		if err != nil {
			return
		}
		defer f.Close()
		if _, err := f.Seek(offset, 0); err != nil {
			return
		}
		buf := make([]byte, 32*1024)
		for {
			n, err := f.Read(buf)
			if n > 0 {
				offset += int64(n)
				lineBuf = append(lineBuf, buf[:n]...)
			}
			if err != nil {
				break
			}
		}
		for {
			i := bytes.IndexByte(lineBuf, '\n')
			if i < 0 {
				break
			}
			emit(lineBuf[:i])
			lineBuf = lineBuf[i+1:]
		}
		if final && len(lineBuf) > 0 {
			emit(lineBuf)
			lineBuf = nil
		}
		flusher.Flush()
	}

	for {
		drain(false)
		if cur, err := s.store.GetTaskInstance(ctx, ti.ID); err == nil && cur.State.IsTerminal() {
			drain(true)
			_, _ = w.Write([]byte("event: done\ndata: end\n\n"))
			flusher.Flush()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
