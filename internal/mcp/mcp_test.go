package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/client"
)

func TestBuildToolsAndReadOnly(t *testing.T) {
	full := BuildTools(nil, false)
	if len(full) < 25 {
		t.Fatalf("expected the full tool set, got %d", len(full))
	}
	names := map[string]bool{}
	for _, tool := range full {
		names[tool.Name] = true
	}
	for _, must := range []string{"list_dags", "create_dag", "validate_dag", "trigger_dag", "cancel_run", "mark_task", "get_task_log", "list_projects"} {
		if !names[must] {
			t.Errorf("missing tool %q", must)
		}
	}

	ro := BuildTools(nil, true)
	if len(ro) >= len(full) {
		t.Fatalf("read-only set (%d) should be smaller than full (%d)", len(ro), len(full))
	}
	for _, tool := range ro {
		if tool.ep.Method != "GET" {
			t.Errorf("read-only exposed a mutating tool: %s (%s)", tool.Name, tool.ep.Method)
		}
	}
}

func TestSchemaForRequiredPathAndBody(t *testing.T) {
	var trigger *Tool
	for i := range BuildTools(nil, false) {
		if BuildTools(nil, false)[i].Name == "trigger_dag" {
			tt := BuildTools(nil, false)[i]
			trigger = &tt
			break
		}
	}
	if trigger == nil {
		t.Fatal("trigger_dag not found")
	}
	props, _ := trigger.InputSchema["properties"].(map[string]any)
	if _, ok := props["id"]; !ok {
		t.Error("trigger_dag schema should expose the path param 'id'")
	}
	req, _ := trigger.InputSchema["required"].([]string)
	found := false
	for _, r := range req {
		if r == "id" {
			found = true
		}
	}
	if !found {
		t.Error("path param 'id' should be required")
	}
}

// TestInvokeSplitsArgs verifies path/query/body separation against a live echo.
func TestInvokeSplitsArgs(t *testing.T) {
	var gotPath, gotQuery, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"run_id":"r1"}`))
	}))
	defer srv.Close()

	cli := client.New(srv.URL, "")
	tools := BuildTools(cli, false)
	var trigger *Tool
	for i := range tools {
		if tools[i].Name == "trigger_dag" {
			trigger = &tools[i]
		}
	}
	out, err := trigger.Invoke(context.Background(), map[string]any{
		"id":     "etl",                               // path param
		"params": map[string]any{"day": "2026-01-01"}, // body field
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/dags/etl/trigger" {
		t.Errorf("path = %q", gotPath)
	}
	if gotQuery != "" {
		t.Errorf("query = %q, want empty", gotQuery)
	}
	if !strings.Contains(gotBody, `"params"`) || !strings.Contains(gotBody, "2026-01-01") {
		t.Errorf("body = %q, want the params field", gotBody)
	}
	if !strings.Contains(out, "r1") {
		t.Errorf("result = %q", out)
	}
}

func TestInvokeMissingRequiredPathParam(t *testing.T) {
	tools := BuildTools(client.New("http://unused", ""), false)
	var get *Tool
	for i := range tools {
		if tools[i].Name == "get_dag" {
			get = &tools[i]
		}
	}
	if _, err := get.Invoke(context.Background(), map[string]any{}); err == nil {
		t.Error("get_dag without 'id' should error before calling the API")
	}
}

// TestInvokeSurfacesAPIError: a 4xx becomes a tool error carrying the message.
func TestInvokeSurfacesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"not found"}`))
	}))
	defer srv.Close()
	tools := BuildTools(client.New(srv.URL, ""), false)
	var get *Tool
	for i := range tools {
		if tools[i].Name == "get_dag" {
			get = &tools[i]
		}
	}
	_, err := get.Invoke(context.Background(), map[string]any{"id": "nope"})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected the API error surfaced, got %v", err)
	}
}

// TestBlankLinesIgnored: blank lines between messages must not produce spurious
// parse-error responses.
func TestBlankLinesIgnored(t *testing.T) {
	s := NewServer("cronova", "test", nil)
	in := strings.NewReader("\n{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}\n\n  \n{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"ping\"}\n")
	var out bytes.Buffer
	if err := s.Serve(context.Background(), in, &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected exactly 2 ping responses (blank lines ignored), got %d: %q", len(lines), out.String())
	}
	for _, l := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil || m["error"] != nil {
			t.Errorf("unexpected/error response: %s", l)
		}
	}
}

// TestServerProtocol drives the stdio JSON-RPC loop end to end.
func TestServerProtocol(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"dag_id":"etl"}]`))
	}))
	defer srv.Close()

	s := NewServer("cronova", "test", BuildTools(client.New(srv.URL, ""), false))
	in := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`, // notification: no reply
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_dags","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"nope","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"bogus/method"}`,
	}, "\n") + "\n")

	var out bytes.Buffer
	if err := s.Serve(context.Background(), in, &out); err != nil {
		t.Fatal(err)
	}

	var got []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad response line %q: %v", line, err)
		}
		got = append(got, m)
	}
	// initialize + tools/list + 2 tools/call + 1 error = 5 responses (the
	// notification produced none).
	if len(got) != 5 {
		t.Fatalf("expected 5 responses (notification silent), got %d: %v", len(got), got)
	}
	// initialize
	if res, _ := got[0]["result"].(map[string]any); res["protocolVersion"] != "2024-11-05" {
		t.Errorf("initialize result = %v", got[0])
	}
	// tools/list
	if res, _ := got[1]["result"].(map[string]any); res["tools"] == nil {
		t.Errorf("tools/list missing tools: %v", got[1])
	}
	// tools/call list_dags -> content with the dag
	res3, _ := got[2]["result"].(map[string]any)
	content, _ := res3["content"].([]any)
	if len(content) == 0 || !strings.Contains(content[0].(map[string]any)["text"].(string), "etl") {
		t.Errorf("list_dags call result = %v", got[2])
	}
	// unknown tool -> isError
	res4, _ := got[3]["result"].(map[string]any)
	if res4["isError"] != true {
		t.Errorf("unknown tool should be isError: %v", got[3])
	}
	// unknown method -> JSON-RPC error object
	if got[4]["error"] == nil {
		t.Errorf("bogus method should return a JSON-RPC error: %v", got[4])
	}
}

// TestServeReturnsOnCtxCancel: a cancelled context must unblock Serve even while
// it is parked on a read that never yields a line (the graceful-shutdown fix).
func TestServeReturnsOnCtxCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	pr, pw := io.Pipe()
	defer pw.Close() // pr blocks forever until this writes; it never does
	s := NewServer("cronova", "test", nil)
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, pr, io.Discard) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return on a cancelled context while blocked on read")
	}
}

// TestNotificationNoReply: notifications (no id) should not generate responses
func TestNotificationNoReply(t *testing.T) {
	s := NewServer("cronova", "test", []Tool{})
	in := strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}
`)
	var out bytes.Buffer
	if err := s.Serve(context.Background(), in, &out); err != nil {
		t.Fatal(err)
	}

	output := strings.TrimSpace(out.String())
	if output != "" {
		t.Errorf("notification should not generate a response, got: %q", output)
	}
}

// TestIDEchoVerbatim: ID must be echoed exactly as received
func TestIDEchoVerbatim(t *testing.T) {
	s := NewServer("cronova", "test", []Tool{})

	testCases := []string{
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":"abc","method":"ping"}`,
		`{"jsonrpc":"2.0","id":null,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":true,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":1.5,"method":"ping"}`,
	}

	for _, tc := range testCases {
		in := strings.NewReader(tc + "\n")
		var out bytes.Buffer
		if err := s.Serve(context.Background(), in, &out); err != nil {
			t.Fatal(err)
		}

		// Extract the expected ID from the input
		var inReq map[string]interface{}
		json.Unmarshal([]byte(tc), &inReq)
		expectedID := inReq["id"]

		// Check the response ID
		var outResp map[string]interface{}
		json.Unmarshal(bytes.TrimSpace(out.Bytes()), &outResp)
		actualID := outResp["id"]

		// Compare - they should be equal
		expectedJSON, _ := json.Marshal(expectedID)
		actualJSON, _ := json.Marshal(actualID)
		if string(expectedJSON) != string(actualJSON) {
			t.Errorf("ID not echoed verbatim for %q:\n  expected: %s\n  actual: %s", tc, string(expectedJSON), string(actualJSON))
		}
	}
}
