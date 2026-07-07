// Package mcp is a minimal Model Context Protocol server that exposes cronova's
// operations as MCP tools over stdio, so an AI client (Claude Code/Desktop, any
// MCP host) can drive cronova natively. It is a thin, dependency-free JSON-RPC
// layer: tools are DERIVED from the same api.Catalog() that drives the OpenAPI
// spec, and every call goes through the token-authenticated REST client — no
// privileged side channel.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// protocolVersion is the MCP revision we advertise if the client doesn't request
// one. We otherwise echo the client's requested version for compatibility.
const protocolVersion = "2024-11-05"

// Server serves MCP over a single stdio stream.
type Server struct {
	name    string
	version string
	tools   []Tool
	byName  map[string]*Tool

	mu  sync.Mutex // serializes writes to out
	enc *json.Encoder
}

// NewServer builds a server exposing tools (see BuildTools).
func NewServer(name, version string, tools []Tool) *Server {
	s := &Server{name: name, version: version, tools: tools, byName: map[string]*Tool{}}
	for i := range s.tools {
		s.byName[s.tools[i].Name] = &s.tools[i]
	}
	return s
}

// --- JSON-RPC 2.0 framing (newline-delimited over stdio) ---

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // absent => notification
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Serve runs the read/dispatch/write loop until in is exhausted or ctx is done.
// The blocking read runs in a goroutine so a ctx cancellation (SIGINT/SIGTERM)
// returns promptly even while parked waiting for the next line; the leaked read
// goroutine dies with the process, which is exiting anyway.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	s.enc = json.NewEncoder(out)
	r := bufio.NewReader(in)
	lines := make(chan []byte)
	errc := make(chan error, 1)
	go func() {
		for {
			line, err := r.ReadBytes('\n')
			if len(line) > 0 {
				select {
				case lines <- line:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				errc <- err
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line := <-lines:
			s.dispatch(ctx, line)
		case err := <-errc:
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func (s *Server) dispatch(ctx context.Context, line []byte) {
	if len(bytes.TrimSpace(line)) == 0 {
		return // blank/whitespace line between messages — ignore, don't error
	}
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		s.send(rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "parse error"}})
		return
	}
	result, rerr := s.handle(ctx, req)
	if req.ID == nil {
		return // notification: never reply
	}
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	if rerr != nil {
		resp.Error = rerr
	} else {
		resp.Result = result
	}
	s.send(resp)
}

func (s *Server) send(resp rpcResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.enc.Encode(resp) // Encoder appends the delimiting newline
}

func (s *Server) handle(ctx context.Context, req rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		ver := p.ProtocolVersion
		if ver == "" {
			ver = protocolVersion
		}
		return map[string]any{
			"protocolVersion": ver,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": s.name, "version": s.version},
		}, nil

	case "notifications/initialized", "notifications/cancelled":
		return nil, nil // notifications; no reply (ID absent anyway)

	case "ping":
		return map[string]any{}, nil

	case "tools/list":
		list := make([]map[string]any, 0, len(s.tools))
		for i := range s.tools {
			t := &s.tools[i]
			list = append(list, map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"inputSchema": t.InputSchema,
			})
		}
		return map[string]any{"tools": list}, nil

	case "tools/call":
		return s.callTool(ctx, req.Params)

	default:
		return nil, &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
}

// callTool executes a tool and returns an MCP tool result. A tool-level failure
// (bad args, API error) is returned as a result with isError=true — the model
// sees the message — not as a protocol error.
func (s *Server) callTool(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid params"}
	}
	tool, ok := s.byName[call.Name]
	if !ok {
		return toolError(fmt.Sprintf("unknown tool %q", call.Name)), nil
	}
	args := map[string]any{}
	if len(call.Arguments) > 0 {
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return toolError("arguments must be a JSON object"), nil
		}
	}
	text, err := tool.Invoke(ctx, args)
	if err != nil {
		return toolError(err.Error()), nil
	}
	return toolText(text), nil
}

func toolText(text string) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}
}

func toolError(msg string) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": msg}}, "isError": true}
}
