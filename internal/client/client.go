// Package client is a small typed HTTP client for the cronova REST API. It is
// the shared surface behind both the CLI's remote mode and the MCP server, so an
// AI agent reaches cronova through the SAME token-authenticated, role-gated API
// a browser does — no privileged side door.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxResponseBytes caps how much of a response we buffer, so a hostile or
// misbehaving server can't OOM the (long-lived) MCP process. Defense-in-depth
// on top of the client's overall request timeout; matches the caps the rest of
// the codebase applies to bodies it reads.
const maxResponseBytes = 32 << 20

// Client talks to a running cronova server's /api over HTTP with a Bearer token.
type Client struct {
	baseURL string
	token   string
	hc      *http.Client
}

// New builds a client for baseURL (e.g. "http://localhost:8090"); token may be
// empty when the server runs with auth disabled.
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		hc:      &http.Client{Timeout: 60 * time.Second},
	}
}

// BaseURL reports the server root (for messages).
func (c *Client) BaseURL() string { return c.baseURL }

// Options configure a single Call.
type Options struct {
	Path        map[string]string // {name} substitutions in the path template
	Query       map[string]string // query params (empty values are dropped)
	Body        []byte            // request body; nil = none
	ContentType string            // defaults to application/json when Body is set
	Accept      string            // e.g. "text/plain" for a task log
}

// Result is a raw API response.
type Result struct {
	Status      int
	Body        []byte
	ContentType string
}

// OK reports a 2xx status.
func (r *Result) OK() bool { return r.Status >= 200 && r.Status < 300 }

// APIError is returned for a non-2xx response, carrying the server's error text.
type APIError struct {
	Status  int
	Message string
	Method  string
	Path    string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	return fmt.Sprintf("%s %s: %d %s", e.Method, e.Path, e.Status, msg)
}

// Call performs an API request and returns the raw result. A non-2xx status is
// returned as an *APIError (with the Result still populated for inspection).
func (c *Client) Call(ctx context.Context, method, path string, o Options) (*Result, error) {
	for k, v := range o.Path {
		path = strings.ReplaceAll(path, "{"+k+"}", url.PathEscape(v))
	}
	u := c.baseURL + path
	if len(o.Query) > 0 {
		q := url.Values{}
		for k, v := range o.Query {
			if v != "" {
				q.Set(k, v)
			}
		}
		if enc := q.Encode(); enc != "" {
			u += "?" + enc
		}
	}

	var body io.Reader
	if o.Body != nil {
		body = bytes.NewReader(o.Body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if o.Body != nil {
		ct := o.ContentType
		if ct == "" {
			ct = "application/json"
		}
		req.Header.Set("Content-Type", ct)
	}
	if o.Accept != "" {
		req.Header.Set("Accept", o.Accept)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	res := &Result{Status: resp.StatusCode, Body: b, ContentType: resp.Header.Get("Content-Type")}
	if !res.OK() {
		msg := strings.TrimSpace(string(b))
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(b, &e) == nil && e.Error != "" {
			msg = e.Error
		}
		return res, &APIError{Status: res.Status, Message: msg, Method: method, Path: path}
	}
	return res, nil
}

// CallJSON is Call plus JSON decoding of the body into out (out may be nil to
// discard). It returns the *Result for status/raw access.
func (c *Client) CallJSON(ctx context.Context, method, path string, o Options, out any) (*Result, error) {
	res, err := c.Call(ctx, method, path, o)
	if err != nil {
		return res, err
	}
	if out != nil && len(res.Body) > 0 {
		if err := json.Unmarshal(res.Body, out); err != nil {
			return res, fmt.Errorf("decode %s %s response: %w", method, path, err)
		}
	}
	return res, nil
}

// jsonBody marshals v to a JSON body for Options.Body.
func jsonBody(v any) ([]byte, error) { return json.Marshal(v) }
