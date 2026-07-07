package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/zoyluo/cronova/internal/api"
	"github.com/zoyluo/cronova/internal/client"
)

// defaultMaxOut caps a tool result so a huge response (e.g. a long log) can't
// blow the model's context; the text is truncated with a marker.
const defaultMaxOut = 100 << 10

// toolNames is the curated allow-list: which catalog endpoints are exposed as
// tools, and under what (LLM-friendly) name. Everything else (login/logout, the
// raw-YAML DAG create, token management, /metrics, health) is deliberately NOT
// exposed. Descriptions, input schemas, and dispatch all derive from the catalog
// entry, so only the names live here.
var toolNames = map[string]string{
	"GET /api/dags":                               "list_dags",
	"POST /api/dags/build":                        "create_dag",
	"POST /api/dags/validate":                     "validate_dag",
	"GET /api/dags/{id}":                          "get_dag",
	"DELETE /api/dags/{id}":                       "delete_dag",
	"POST /api/dags/{id}/pause":                   "pause_dag",
	"GET /api/dags/{id}/runs":                     "list_dag_runs",
	"GET /api/dag-graph":                          "get_dag_graph",
	"GET /api/schedule/preview":                   "preview_schedule",
	"POST /api/dags/{id}/trigger":                 "trigger_dag",
	"GET /api/runs/{runID}":                       "get_run",
	"POST /api/runs/{runID}/cancel":               "cancel_run",
	"POST /api/runs/{runID}/retry":                "retry_run",
	"POST /api/runs/{runID}/mark":                 "mark_run",
	"POST /api/runs/{runID}/tasks/{taskID}/retry": "retry_task",
	"POST /api/runs/{runID}/tasks/{taskID}/mark":  "mark_task",
	"GET /api/tasks/{tiID}/log":                   "get_task_log",
	"GET /api/pools":                              "list_pools",
	"POST /api/pools/{name}":                      "set_pool",
	"GET /api/variables":                          "list_variables",
	"POST /api/variables/{key}":                   "set_variable",
	"DELETE /api/variables/{key}":                 "delete_variable",
	"GET /api/connections":                        "list_connections",
	"POST /api/connections/{id}":                  "set_connection",
	"DELETE /api/connections/{id}":                "delete_connection",
	"GET /api/projects":                           "list_projects",
	"GET /api/projects/{name}":                    "get_project",
	"DELETE /api/projects/{name}":                 "delete_project",
	"GET /api/overview":                           "get_overview",
	"GET /api/audit":                              "list_audit",
}

// Tool is one MCP tool bound to an API endpoint + a client.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
	ep          api.Endpoint
	cli         *client.Client
	maxOut      int
}

// BuildTools derives the tool set from the API catalog. In readOnly mode only
// GET (non-mutating) tools are exposed.
func BuildTools(cli *client.Client, readOnly bool) []Tool {
	var tools []Tool
	for _, ep := range api.Catalog() {
		name, ok := toolNames[ep.Method+" "+ep.Path]
		if !ok {
			continue
		}
		if readOnly && ep.Method != "GET" {
			continue
		}
		tools = append(tools, Tool{
			Name:        name,
			Description: toolDesc(ep),
			InputSchema: schemaFor(ep),
			ep:          ep,
			cli:         cli,
			maxOut:      defaultMaxOut,
		})
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	return tools
}

// ToolNames returns the sorted names that would be exposed (for the CLI banner).
func ToolNames(readOnly bool) []string {
	tools := BuildTools(nil, readOnly)
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	return names
}

func toolDesc(ep api.Endpoint) string {
	d := ep.Summary
	if ep.Desc != "" {
		d += " — " + ep.Desc
	}
	if ep.Method != "GET" {
		d += " (mutating)"
	}
	return d
}

// schemaFor builds a JSON Schema: path/query params + request-body fields (from
// the catalog's example body) are flattened into top-level properties. Invoke
// re-splits them into path substitution, query params, and JSON body.
func schemaFor(ep api.Endpoint) map[string]any {
	props := map[string]any{}
	var required []string
	for _, p := range ep.Params {
		desc := p.Desc
		if p.Example != "" {
			desc += fmt.Sprintf(" (e.g. %q)", p.Example)
		}
		props[p.Name] = map[string]any{"type": "string", "description": strings.TrimSpace(desc)}
		if p.Required {
			required = append(required, p.Name)
		}
	}
	if ex, ok := ep.BodyExample.(map[string]any); ok {
		for k, v := range ex {
			if _, exists := props[k]; exists {
				continue
			}
			props[k] = map[string]any{"type": jsonType(v), "description": "request body field"}
		}
	} else if ep.HasBody {
		props["body"] = map[string]any{"type": "object", "description": "request body"}
	}
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func jsonType(v any) string {
	switch v.(type) {
	case bool:
		return "boolean"
	case float64, float32, int, int64:
		return "number"
	case []any, []string:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "string"
	}
}

// Invoke executes the tool: it splits args into path params, query params, and a
// JSON body, calls the API, and returns the response text (truncated). An API
// error is returned as an error so the caller marks the tool result isError.
func (t *Tool) Invoke(ctx context.Context, args map[string]any) (string, error) {
	kind := map[string]string{}
	for _, p := range t.ep.Params {
		kind[p.Name] = p.In
	}
	pathParams := map[string]string{}
	query := map[string]string{}
	body := map[string]any{}
	for k, v := range args {
		switch kind[k] {
		case "path":
			pathParams[k] = toStr(v)
		case "query":
			query[k] = toStr(v)
		default:
			body[k] = v
		}
	}
	for _, p := range t.ep.Params {
		if p.In == "path" && p.Required && pathParams[p.Name] == "" {
			return "", fmt.Errorf("missing required argument %q", p.Name)
		}
	}

	opts := client.Options{Path: pathParams, Query: query}
	if raw, ok := body["body"]; ok && len(body) == 1 {
		b, _ := json.Marshal(raw) // generic {body: ...} passthrough
		opts.Body = b
	} else if len(body) > 0 {
		b, _ := json.Marshal(body)
		opts.Body = b
	}

	res, err := t.cli.Call(ctx, t.ep.Method, t.ep.Path, opts)
	out := ""
	if res != nil {
		out = string(res.Body)
	}
	if err != nil {
		var ae *client.APIError
		if errors.As(err, &ae) {
			if strings.TrimSpace(out) == "" {
				out = ae.Error()
			}
			return "", fmt.Errorf("%s", strings.TrimSpace(out))
		}
		return "", err
	}
	return truncate(out, t.maxOut), nil
}

func toStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func truncate(s string, max int) string {
	if max > 0 && len(s) > max {
		return s[:max] + fmt.Sprintf("\n…[truncated %d bytes]", len(s)-max)
	}
	return s
}
