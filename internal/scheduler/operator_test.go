package scheduler

import (
	"testing"

	"github.com/zoyluo/cronova/internal/model"
)

// TestResolveHTTPSpec: url, header values, and body are templated with the same
// resolver as shell commands; the input spec is not mutated.
func TestResolveHTTPSpec(t *testing.T) {
	resolve := func(k string) (string, bool) {
		v, ok := map[string]string{
			"conn.api.host": "api.example.com",
			"var.TOKEN":     "secret123",
		}[k]
		return v, ok
	}
	in := model.HTTPSpec{
		Method:  "POST",
		URL:     "https://{{ conn.api.host }}/ingest",
		Headers: map[string]string{"Authorization": "Bearer {{ var.TOKEN }}", "X-Static": "z"},
		Body:    `{"tok":"{{ var.TOKEN }}"}`,
	}
	out := resolveHTTPSpec(in, resolve)

	if out.URL != "https://api.example.com/ingest" {
		t.Errorf("url = %q", out.URL)
	}
	if out.Headers["Authorization"] != "Bearer secret123" || out.Headers["X-Static"] != "z" {
		t.Errorf("headers = %v", out.Headers)
	}
	if out.Body != `{"tok":"secret123"}` {
		t.Errorf("body = %q", out.Body)
	}
	// input untouched (out.Headers is a fresh map)
	if in.URL != "https://{{ conn.api.host }}/ingest" || in.Headers["Authorization"] != "Bearer {{ var.TOKEN }}" {
		t.Error("input spec was mutated")
	}
}
