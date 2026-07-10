package api

import (
	"encoding/json"
	"strings"
	"sync"
)

// This file exposes the entire cronova HTTP API as a self-describing OpenAPI 3
// document served at /openapi.json, plus a Redoc reference UI at /docs. The spec
// is generated from a single endpoint catalog (apiCatalog) so the docs can never
// drift from the routes, and per-language code samples (curl/Go/Python/Java) are
// generated from that same catalog into x-codeSamples — giving the in-page
// language switcher without hand-writing four snippets per endpoint.

// docsBaseURL is the placeholder origin shown in every code sample. Integrators
// replace it with their own deployment; kept static so the spec is cacheable.
const docsBaseURL = "http://localhost:8090"

// apiParam is a path or query parameter for one endpoint.
type apiParam struct {
	Name     string
	In       string // "path" or "query"
	Required bool
	Desc     string
	Example  string
}

// apiEndpoint is one route in the catalog. Both the OpenAPI path item and the
// four code samples are derived from these fields.
// Endpoint is a public projection of one catalog entry, for external tooling
// (the MCP server) that derives from the SAME single source of truth as the
// OpenAPI spec and code samples — so an AI's tools can never drift from the API.
type Endpoint struct {
	Method       string
	Path         string
	Tag          string
	Summary      string
	Desc         string
	Params       []Param
	HasBody      bool
	BodyExample  any    // example request body, nil if none
	BodyType     string // "json" (default) or "yaml"
	OptionalBody bool
	NoAuth       bool
}

// Param is a public projection of apiParam.
type Param struct {
	Name     string
	In       string // "path" or "query"
	Required bool
	Desc     string
	Example  string
}

// Catalog returns the public API surface (the same catalog that drives OpenAPI).
func Catalog() []Endpoint {
	src := apiCatalog()
	out := make([]Endpoint, 0, len(src))
	for _, e := range src {
		ep := Endpoint{
			Method: e.Method, Path: e.Path, Tag: e.Tag,
			Summary: e.Summary, Desc: e.Desc,
			HasBody: e.Request != nil, BodyExample: e.Request,
			BodyType: e.RequestType, OptionalBody: e.OptionalBody, NoAuth: e.NoAuth,
		}
		if ep.HasBody && ep.BodyType == "" {
			ep.BodyType = "json"
		}
		for _, p := range e.Params {
			ep.Params = append(ep.Params, Param{Name: p.Name, In: p.In, Required: p.Required, Desc: p.Desc, Example: p.Example})
		}
		out = append(out, ep)
	}
	return out
}

type apiEndpoint struct {
	Method       string
	Path         string
	Tag          string
	Summary      string
	Desc         string
	Params       []apiParam
	Request      any    // example request body (marshaled to JSON); nil = no body
	RequestType  string // "json" (default) or "yaml" for the raw-YAML create endpoint
	RequestDesc  string
	OptionalBody bool // body may be omitted (e.g. trigger params)
	Response     any  // example response body
	ResponseDesc string
	SuccessCode  string // "200" (default), "201", ...
	NoAuth       bool   // true for endpoints that never require a bearer token
}

// apiCatalog is the single source of truth: every public endpoint, in the order
// tags are declared. Code samples and OpenAPI paths both derive from this.
func apiCatalog() []apiEndpoint {
	dagSpecExample := map[string]any{
		"dag_id":          "etl_daily",
		"schedule":        "0 6 * * *",
		"start_date":      "2026-01-01",
		"catchup":         false,
		"max_active_runs": 1,
		"default_retries": 2,
		"tasks": []any{
			map[string]any{"id": "extract", "type": "shell", "command": "python extract.py"},
			map[string]any{"id": "load", "type": "shell", "command": "python load.py", "deps": []string{"extract"}},
		},
		"notify_url": "https://hooks.example.com/cronova",
		"notify_on":  []string{"failure"},
		"sla":        3600,
	}

	return []apiEndpoint{
		// ---- DAGs ----
		{Method: "GET", Path: "/api/dags", Tag: "DAGs",
			Summary: "List DAGs", Desc: "Return all active (non-deleted) DAGs with their schedule, pause state, and parsed tasks.",
			Response: []any{map[string]any{"dag_id": "etl_daily", "schedule": "0 6 * * *", "paused": false, "catchup": false, "max_active_runs": 1}}},
		{Method: "POST", Path: "/api/dags/build", Tag: "DAGs",
			Summary: "Create or update a DAG", Desc: "Upsert a DAG from a structured spec. The server renders it to canonical YAML and runs the same validation as file-loaded DAGs (cycle/dep/cron/id checks). Keyed by dag_id.",
			Request: dagSpecExample, Response: map[string]any{"dag_id": "etl_daily"}},
		{Method: "POST", Path: "/api/dags/validate", Tag: "DAGs",
			Summary: "Validate a DAG (dry run)", Desc: "Render + validate a DAG spec WITHOUT persisting — same cycle/dep/cron/id checks as create. Returns {valid, error?, canonical_yaml}. Use it to check a generated DAG before creating it.",
			Request: dagSpecExample, Response: map[string]any{"valid": true, "dag_id": "etl_daily", "tasks": 2}},
		{Method: "POST", Path: "/api/dags", Tag: "DAGs",
			Summary: "Create a DAG from YAML", Desc: "Create/update a DAG by POSTing its raw YAML definition (same format as an on-disk DAG file).",
			RequestType: "yaml", RequestDesc: "Raw DAG YAML.",
			Request:  "dag_id: etl_daily\nschedule: \"0 6 * * *\"\nstart_date: 2026-01-01\ntasks:\n  - id: extract\n    type: shell\n    command: python extract.py\n",
			Response: map[string]any{"dag_id": "etl_daily"}},
		{Method: "GET", Path: "/api/dags/{id}", Tag: "DAGs",
			Summary: "Get a DAG", Desc: "Fetch a single DAG definition by id, including its parsed tasks.",
			Params:   []apiParam{{Name: "id", In: "path", Required: true, Desc: "DAG id.", Example: "etl_daily"}},
			Response: map[string]any{"dag_id": "etl_daily", "schedule": "0 6 * * *", "paused": false}},
		{Method: "DELETE", Path: "/api/dags/{id}", Tag: "DAGs",
			Summary: "Delete a DAG", Desc: "Soft-delete (archive) a DAG. Refused with 409 while the DAG has active runs. History is preserved.",
			Params:   []apiParam{{Name: "id", In: "path", Required: true, Desc: "DAG id.", Example: "etl_daily"}},
			Response: map[string]any{"deleted": true}},
		{Method: "POST", Path: "/api/dags/{id}/pause", Tag: "DAGs",
			Summary: "Pause or resume a DAG", Desc: "Toggle scheduling for a DAG. Paused DAGs are not scheduled but can still be triggered manually.",
			Params: []apiParam{
				{Name: "id", In: "path", Required: true, Desc: "DAG id.", Example: "etl_daily"},
				{Name: "paused", In: "query", Desc: "true to pause (default), false to resume.", Example: "true"}},
			Response: map[string]any{"paused": true}},
		{Method: "GET", Path: "/api/dags/{id}/runs", Tag: "DAGs",
			Summary: "List a DAG's runs", Desc: "Return recent runs for a DAG, newest first.",
			Params: []apiParam{
				{Name: "id", In: "path", Required: true, Desc: "DAG id.", Example: "etl_daily"},
				{Name: "limit", In: "query", Desc: "Max runs to return (default 100, capped at 200).", Example: "20"},
				{Name: "offset", In: "query", Desc: "Non-negative paging offset.", Example: "0"},
				{Name: "state", In: "query", Desc: "Optional comma-separated run states.", Example: "failed,cancelled"}},
			Response: []any{map[string]any{"run_id": "etl_daily__2026-07-05T06:00:00Z", "dag_id": "etl_daily", "state": "success", "trigger_type": "schedule"}}},
		{Method: "GET", Path: "/api/dag-graph", Tag: "DAGs",
			Summary: "Cross-DAG dependency graph", Desc: "Return the global DAG dependency graph (trigger_after edges) for visualization.",
			Response: map[string]any{"nodes": []any{map[string]any{"id": "etl_daily"}}, "edges": []any{}}},
		{Method: "GET", Path: "/api/schedule/preview", Tag: "DAGs",
			Summary: "Preview a schedule", Desc: "Compute the next fire times for a cron expression using the engine's own parser (no client-side cron math).",
			Params: []apiParam{
				{Name: "schedule", In: "query", Required: true, Desc: "Cron expression.", Example: "0 6 * * *"},
				{Name: "n", In: "query", Desc: "How many upcoming times (1-10, default 3).", Example: "3"}},
			Response: map[string]any{"next": []string{"2026-07-06T06:00:00Z", "2026-07-07T06:00:00Z"}}},

		// ---- Runs ----
		{Method: "POST", Path: "/api/dags/{id}/trigger", Tag: "Runs",
			Summary: "Trigger a run", Desc: "Start a manual run of a DAG. Optional params are injected as CRONOVA_PARAM_* env vars and {{ params.KEY }} template values.",
			Params:       []apiParam{{Name: "id", In: "path", Required: true, Desc: "DAG id.", Example: "etl_daily"}},
			Request:      map[string]any{"params": map[string]string{"date": "2026-07-05", "region": "us"}},
			RequestDesc:  "Optional trigger-time params. Send an empty body for none.",
			OptionalBody: true,
			Response:     map[string]any{"run_id": "etl_daily__manual__2026-07-05T12:00:00Z"}},
		{Method: "POST", Path: "/api/dags/{id}/backfill", Tag: "Runs",
			Summary: "Backfill a date range", Desc: "Enqueue one run per schedule period in [from, to] (inclusive; dates or RFC3339). Periods that already have a run are skipped, `to` is clamped to now, and execution is throttled by the DAG's max_active_runs.",
			Params:      []apiParam{{Name: "id", In: "path", Required: true, Desc: "DAG id.", Example: "etl_daily"}},
			Request:     map[string]any{"from": "2026-07-01", "to": "2026-07-05"},
			RequestDesc: "The window to backfill. Requires the DAG to have a schedule.",
			Response:    map[string]any{"created": 4, "skipped": 1}},
		{Method: "GET", Path: "/api/runs/{runID}", Tag: "Runs",
			Summary: "Get a run", Desc: "Fetch a run with its task instances and current states.",
			Params:   []apiParam{{Name: "runID", In: "path", Required: true, Desc: "Run id.", Example: "etl_daily__2026-07-05T06:00:00Z"}},
			Response: map[string]any{"run_id": "etl_daily__2026-07-05T06:00:00Z", "state": "running", "tasks": []any{}}},
		{Method: "POST", Path: "/api/runs/{runID}/cancel", Tag: "Runs",
			Summary: "Cancel a run", Desc: "Stop an active run and its running tasks. 409 if the run is already terminal.",
			Params:   []apiParam{{Name: "runID", In: "path", Required: true, Desc: "Run id.", Example: "etl_daily__2026-07-05T06:00:00Z"}},
			Response: map[string]any{"cancelled": true}},
		{Method: "POST", Path: "/api/runs/{runID}/retry", Tag: "Runs",
			Summary: "Retry failed tasks", Desc: "Re-queue the failed/upstream_failed tasks of a finished run and reactivate it. 409 if nothing to retry or the run is still active.",
			Params:   []apiParam{{Name: "runID", In: "path", Required: true, Desc: "Run id.", Example: "etl_daily__2026-07-05T06:00:00Z"}},
			Response: map[string]any{"retried": true}},
		{Method: "POST", Path: "/api/runs/{runID}/mark", Tag: "Runs",
			Summary: "Mark run state", Desc: "Manually set a run's state (success or failed) — an operator override. Serialized against the scheduler's own finalization.",
			Params:  []apiParam{{Name: "runID", In: "path", Required: true, Desc: "Run id.", Example: "etl_daily__2026-07-05T06:00:00Z"}},
			Request: map[string]any{"state": "success"}, Response: map[string]any{"marked": true}},

		// ---- Tasks ----
		{Method: "POST", Path: "/api/runs/{runID}/tasks/{taskID}/retry", Tag: "Tasks",
			Summary: "Retry a task", Desc: "Re-queue a single failed task within a run.",
			Params: []apiParam{
				{Name: "runID", In: "path", Required: true, Desc: "Run id.", Example: "etl_daily__2026-07-05T06:00:00Z"},
				{Name: "taskID", In: "path", Required: true, Desc: "Task id.", Example: "load"}},
			Response: map[string]any{"retried": true}},
		{Method: "POST", Path: "/api/runs/{runID}/tasks/{taskID}/mark", Tag: "Tasks",
			Summary: "Mark task state", Desc: "Manually set one task's state (success, failed, or skipped) — an operator override.",
			Params: []apiParam{
				{Name: "runID", In: "path", Required: true, Desc: "Run id.", Example: "etl_daily__2026-07-05T06:00:00Z"},
				{Name: "taskID", In: "path", Required: true, Desc: "Task id.", Example: "load"}},
			Request: map[string]any{"state": "success"}, Response: map[string]any{"marked": true}},
		{Method: "GET", Path: "/api/tasks/{tiID}/log", Tag: "Tasks",
			Summary: "Get a task log", Desc: "Fetch the latest 4 MiB of captured stdout/stderr as plain text. Set download=1 to stream the complete captured file.",
			Params: []apiParam{
				{Name: "tiID", In: "path", Required: true, Desc: "Task instance id (numeric).", Example: "42"},
				{Name: "download", In: "query", Desc: "Set to 1 for a streaming full-file download.", Example: "1"}},
			ResponseDesc: "Plain-text log body.", Response: "2026-07-05T06:00:01Z starting extract\n2026-07-05T06:00:03Z done\n"},

		// ---- Pools ----
		{Method: "GET", Path: "/api/pools", Tag: "Pools",
			Summary: "List pools", Desc: "Return concurrency pools and their slot counts.",
			Response: []any{map[string]any{"name": "default", "slots": 8}}},
		{Method: "POST", Path: "/api/pools/{name}", Tag: "Pools",
			Summary: "Create or resize a pool", Desc: "Upsert a concurrency pool. slots must be a positive integer.",
			Params: []apiParam{
				{Name: "name", In: "path", Required: true, Desc: "Pool name.", Example: "cpu"},
				{Name: "slots", In: "query", Required: true, Desc: "Number of concurrency slots.", Example: "4"}},
			Response: map[string]any{"name": "cpu", "slots": 4}},

		// ---- Variables ----
		{Method: "GET", Path: "/api/variables", Tag: "Variables",
			Summary: "List variables", Desc: "Return all shared variables ({{ var.KEY }}).",
			Response: []any{map[string]any{"key": "warehouse_url", "value": "https://dw.example.com"}}},
		{Method: "POST", Path: "/api/variables/{key}", Tag: "Variables",
			Summary: "Set a variable", Desc: "Create or update a shared variable, referenced in commands as {{ var.KEY }}.",
			Params:  []apiParam{{Name: "key", In: "path", Required: true, Desc: "Variable key.", Example: "warehouse_url"}},
			Request: map[string]any{"value": "https://dw.example.com"}, Response: map[string]any{"ok": true}},
		{Method: "DELETE", Path: "/api/variables/{key}", Tag: "Variables",
			Summary: "Delete a variable", Desc: "Remove a shared variable.",
			Params:   []apiParam{{Name: "key", In: "path", Required: true, Desc: "Variable key.", Example: "warehouse_url"}},
			Response: map[string]any{"deleted": true}},

		// ---- Connections ----
		{Method: "GET", Path: "/api/connections", Tag: "Connections",
			Summary: "List connections", Desc: "Return structured connections ({{ conn.ID.host }}). Passwords are never returned; has_password flags whether one is set.",
			Response: []any{map[string]any{"id": "warehouse", "type": "postgres", "host": "db.example.com", "port": 5432, "has_password": true}}},
		{Method: "POST", Path: "/api/connections/{id}", Tag: "Connections",
			Summary: "Set a connection", Desc: "Create or update a connection. Password is write-only. extra must be valid JSON if present.",
			Params:   []apiParam{{Name: "id", In: "path", Required: true, Desc: "Connection id.", Example: "warehouse"}},
			Request:  map[string]any{"type": "postgres", "host": "db.example.com", "port": 5432, "login": "etl", "password": "s3cret", "extra": "{\"database\":\"analytics\"}"},
			Response: map[string]any{"ok": true}},
		{Method: "DELETE", Path: "/api/connections/{id}", Tag: "Connections",
			Summary: "Delete a connection", Desc: "Remove a connection.",
			Params:   []apiParam{{Name: "id", In: "path", Required: true, Desc: "Connection id.", Example: "warehouse"}},
			Response: map[string]any{"deleted": true}},

		// ---- Projects ----
		{Method: "GET", Path: "/api/projects", Tag: "Projects",
			Summary: "List uploaded projects", Desc: "Return uploaded project directories (name, file count, total size). A shell task's `project` field names one; the scheduler runs its command in a fresh copy of that directory. Upload is multipart (console/CLI), not part of this JSON API.",
			Response: []any{map[string]any{"name": "my_app", "files": 3, "size": 4096}}},
		{Method: "GET", Path: "/api/projects/{name}", Tag: "Projects",
			Summary: "List a project's files", Desc: "Return the file paths and sizes inside one uploaded project.",
			Params:   []apiParam{{Name: "name", In: "path", Required: true, Desc: "Project name.", Example: "my_app"}},
			Response: map[string]any{"name": "my_app", "files": []any{map[string]any{"path": "main.py", "size": 120}}}},
		{Method: "DELETE", Path: "/api/projects/{name}", Tag: "Projects",
			Summary: "Delete a project", Desc: "Remove an uploaded project directory and all its files.",
			Params:   []apiParam{{Name: "name", In: "path", Required: true, Desc: "Project name.", Example: "my_app"}},
			Response: map[string]any{"ok": true}},

		// ---- Tokens ----
		{Method: "GET", Path: "/api/tokens", Tag: "Tokens",
			Summary: "List API tokens", Desc: "Return token metadata (never the secret): id, name, role, prefix, created/last-used times.",
			Response: []any{map[string]any{"id": 1, "name": "ci-bot", "role": "admin", "prefix": "cnv_pat_ab12cd", "created_at": "2026-07-05T12:00:00Z"}}},
		{Method: "POST", Path: "/api/tokens", Tag: "Tokens",
			Summary: "Create an API token", Desc: "Mint a bearer token. The plaintext token is returned ONCE in this response and never again — store it now. role is admin (full) or viewer (read-only).",
			Request:     map[string]any{"name": "ci-bot", "role": "admin"},
			SuccessCode: "201",
			Response:    map[string]any{"id": 1, "name": "ci-bot", "role": "admin", "prefix": "cnv_pat_ab12cd", "token": "cnv_pat_ab12cd34ef...", "created_at": "2026-07-05T12:00:00Z"}},
		{Method: "DELETE", Path: "/api/tokens/{id}", Tag: "Tokens",
			Summary: "Revoke an API token", Desc: "Delete a token by id; it stops working immediately.",
			Params:   []apiParam{{Name: "id", In: "path", Required: true, Desc: "Token id (numeric).", Example: "1"}},
			Response: map[string]any{"ok": true}},

		// ---- Operations ----
		{Method: "GET", Path: "/api/overview", Tag: "Operations",
			Summary: "Dashboard overview", Desc: "Aggregate counts and recent runs for the console dashboard.",
			Response: map[string]any{"dags": 12, "runs_active": 1, "recent": []any{}}},
		{Method: "GET", Path: "/api/audit", Tag: "Operations",
			Summary: "Operations audit log", Desc: "Return the operations audit trail (trigger/cancel/retry/mark/create/delete/pause/token), newest first.",
			Params: []apiParam{
				{Name: "target", In: "query", Desc: "Filter to one dag/run id.", Example: "etl_daily"},
				{Name: "limit", In: "query", Desc: "Max entries (1-500, default 100).", Example: "50"}},
			Response: []any{map[string]any{"id": 10, "ts": "2026-07-05T12:00:00Z", "actor": "admin", "action": "trigger", "target": "etl_daily", "detail": "etl_daily__manual__..."}}},

		// ---- System ----
		{Method: "GET", Path: "/api/info", Tag: "System",
			Summary: "Server info", Desc: "Executor target and tick interval (authenticated).",
			Response: map[string]any{"executor": "local", "tick": "2s"}},
		{Method: "GET", Path: "/api/me", Tag: "System",
			Summary: "Current principal", Desc: "The authenticated user or token principal, and whether auth is enabled.",
			Response: map[string]any{"username": "admin", "role": "admin", "auth": true}},
		{Method: "POST", Path: "/api/login", Tag: "System", NoAuth: true,
			Summary: "Log in (cookie session)", Desc: "Exchange username/password for a session cookie (console use). Machine clients should use an API token instead.",
			Request: map[string]any{"username": "admin", "password": "••••••••"}, Response: map[string]any{"username": "admin", "role": "admin"}},
		{Method: "POST", Path: "/api/logout", Tag: "System",
			Summary: "Log out", Desc: "Revoke the current session cookie.",
			Response: map[string]any{"ok": true}},
		{Method: "GET", Path: "/healthz", Tag: "System", NoAuth: true,
			Summary: "Liveness probe", Desc: "Returns ok when the process is up.",
			ResponseDesc: "The literal text ok.", Response: "ok"},
		{Method: "GET", Path: "/readyz", Tag: "System", NoAuth: true,
			Summary: "Readiness probe", Desc: "Returns ready when the database is reachable.",
			Response: map[string]any{"status": "ready"}},
		{Method: "GET", Path: "/metrics", Tag: "System", NoAuth: true,
			Summary: "Prometheus metrics", Desc: "Store-derived counters and gauges in Prometheus text exposition format.",
			ResponseDesc: "Prometheus text format.", Response: "cronova_up 1\ncronova_dags_total 12\ncronova_runs_active 1\n"},
	}
}

// tagOrder controls the sidebar grouping order in Redoc.
var tagOrder = []struct{ name, desc string }{
	{"DAGs", "Define, inspect, schedule, and delete workflows."},
	{"Runs", "Trigger runs and manage their lifecycle."},
	{"Tasks", "Retry, mark, and inspect individual task instances."},
	{"Pools", "Concurrency pools."},
	{"Variables", "Shared key-value variables ({{ var.KEY }})."},
	{"Connections", "Structured credentials ({{ conn.ID.field }})."},
	{"Tokens", "API tokens for programmatic (Bearer) access."},
	{"Operations", "Dashboard and audit trail."},
	{"System", "Auth, health probes, and metrics."},
}

var (
	specOnce  sync.Once
	specBytes []byte
)

// buildSpec assembles the OpenAPI document from the catalog exactly once.
func buildSpec() []byte {
	specOnce.Do(func() {
		paths := map[string]any{}
		for _, ep := range apiCatalog() {
			item, _ := paths[ep.Path].(map[string]any)
			if item == nil {
				item = map[string]any{}
				paths[ep.Path] = item
			}
			item[strings.ToLower(ep.Method)] = operationFor(ep)
		}

		tags := make([]any, 0, len(tagOrder))
		for _, t := range tagOrder {
			tags = append(tags, map[string]any{"name": t.name, "description": t.desc})
		}

		doc := map[string]any{
			"openapi": "3.0.3",
			"info": map[string]any{
				"title":       "cronova API",
				"version":     "1.0.0",
				"description": apiIntro,
			},
			"servers": []any{map[string]any{"url": docsBaseURL, "description": "Local development server"}},
			"tags":    tags,
			"paths":   paths,
			"components": map[string]any{
				"securitySchemes": map[string]any{
					"bearerAuth": map[string]any{
						"type": "http", "scheme": "bearer",
						"description": "API token from POST /api/tokens, sent as `Authorization: Bearer <token>`. Only required when the server is started with -auth.",
					},
				},
				"schemas": componentSchemas(),
			},
			// Applied globally; endpoints that never need auth override with [].
			"security": []any{map[string]any{"bearerAuth": []any{}}},
		}
		specBytes, _ = json.MarshalIndent(doc, "", "  ")
	})
	return specBytes
}

const apiIntro = "Complete HTTP API for the cronova workflow scheduler — define and trigger workflows, " +
	"manage runs and tasks, and administer pools, variables, connections, and API tokens, so you can " +
	"drive cronova from your own platform.\n\n" +
	"**Authentication.** When the server runs with `-auth`, every `/api/*` endpoint requires an API token " +
	"sent as `Authorization: Bearer <token>`. Create one with `POST /api/tokens` (admin). `viewer` tokens " +
	"may call read (GET) endpoints only. When auth is disabled, all endpoints are open.\n\n" +
	"**Base URL.** Samples use `" + docsBaseURL + "` — replace it with your deployment's origin."

// operationFor builds one OpenAPI operation object (including x-codeSamples).
func operationFor(ep apiEndpoint) map[string]any {
	op := map[string]any{
		"tags":        []any{ep.Tag},
		"summary":     ep.Summary,
		"description": ep.Desc,
		"operationId": operationID(ep),
	}
	if ep.NoAuth {
		op["security"] = []any{} // no auth for this operation
	}
	if len(ep.Params) > 0 {
		params := make([]any, 0, len(ep.Params))
		for _, p := range ep.Params {
			schema := map[string]any{"type": "string"}
			if p.Example != "" {
				schema["example"] = p.Example
			}
			params = append(params, map[string]any{
				"name": p.Name, "in": p.In, "required": p.Required || p.In == "path",
				"description": p.Desc, "schema": schema,
			})
		}
		op["parameters"] = params
	}
	if ep.Request != nil {
		mime := "application/json"
		if ep.RequestType == "yaml" {
			mime = "application/yaml"
		}
		desc := ep.RequestDesc
		if desc == "" {
			desc = "Request body."
		}
		op["requestBody"] = map[string]any{
			"required":    !ep.OptionalBody,
			"description": desc,
			"content":     map[string]any{mime: map[string]any{"example": ep.Request}},
		}
	}
	code := ep.SuccessCode
	if code == "" {
		code = "200"
	}
	respDesc := ep.ResponseDesc
	if respDesc == "" {
		respDesc = "Success."
	}
	respContent := map[string]any{"application/json": map[string]any{"example": ep.Response}}
	if _, isStr := ep.Response.(string); isStr {
		respContent = map[string]any{"text/plain": map[string]any{"example": ep.Response}}
	}
	op["responses"] = map[string]any{
		code:  map[string]any{"description": respDesc, "content": respContent},
		"400": map[string]any{"description": "Invalid request.", "content": errContent()},
		"401": map[string]any{"description": "Missing or invalid credentials.", "content": errContent()},
		"404": map[string]any{"description": "Not found.", "content": errContent()},
	}
	op["x-codeSamples"] = codeSamples(ep)
	return op
}

func errContent() map[string]any {
	return map[string]any{"application/json": map[string]any{
		"schema":  map[string]any{"$ref": "#/components/schemas/Error"},
		"example": map[string]any{"error": "not found"},
	}}
}

// operationID is a stable, unique id derived from method + path.
func operationID(ep apiEndpoint) string {
	s := strings.ToLower(ep.Method) + ep.Path
	s = strings.NewReplacer("/", "_", "{", "", "}", "", ".", "_").Replace(s)
	return strings.Trim(s, "_")
}

// componentSchemas defines the core response object shapes referenced by $ref
// (Redoc renders these as expandable models alongside the examples).
func componentSchemas() map[string]any {
	str := map[string]any{"type": "string"}
	i := map[string]any{"type": "integer"}
	b := map[string]any{"type": "boolean"}
	return map[string]any{
		"Error": map[string]any{
			"type": "object", "properties": map[string]any{"error": str},
		},
		"DAG": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"dag_id": str, "schedule": str, "start_date": map[string]any{"type": "string", "format": "date-time"},
				"catchup": b, "paused": b, "max_active_runs": i, "default_retries": i,
				"trigger_after": map[string]any{"type": "array", "items": str},
				"notify_url":    str, "notify_on": map[string]any{"type": "array", "items": str},
				"sla": i, "dagrun_timeout": i,
				"tasks": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/Task"}},
			},
		},
		"Task": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": str, "type": str, "command": str,
				"deps": map[string]any{"type": "array", "items": str},
				"pool": str, "priority": i, "retries": i, "retry_delay": i,
				"timeout": i, "sla": i, "trigger_rule": str, "conn": str,
			},
		},
		"DagRun": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"run_id": str, "dag_id": str,
				"logical_date": map[string]any{"type": "string", "format": "date-time"},
				"state":        map[string]any{"type": "string", "enum": []any{"queued", "running", "success", "failed", "cancelled", "timed_out"}},
				"trigger_type": str,
				"started_at":   map[string]any{"type": "string", "format": "date-time", "nullable": true},
				"finished_at":  map[string]any{"type": "string", "format": "date-time", "nullable": true},
				"params":       map[string]any{"type": "object", "additionalProperties": str},
			},
		},
		"TaskInstance": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": i, "run_id": str, "task_id": str,
				"state":      str,
				"try_number": i, "max_retries": i, "pool": str, "priority": i,
			},
		},
		"APIToken": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": i, "name": str, "role": map[string]any{"type": "string", "enum": []any{"admin", "viewer"}},
				"prefix": str, "token": map[string]any{"type": "string", "description": "Plaintext — present only in the create response."},
				"created_at":   map[string]any{"type": "string", "format": "date-time"},
				"last_used_at": map[string]any{"type": "string", "format": "date-time", "nullable": true},
			},
		},
	}
}
