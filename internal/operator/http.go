// Package operator implements typed task operators — native actions a task can
// perform instead of a shell command. Operators run inside a `cronova run-op`
// subprocess dispatched by the scheduler, so they reuse the executor's normal
// launch/probe/cancel/log machinery unchanged.
package operator

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zoyluo/cronova/internal/model"
)

// maxBodyLog caps how much response body is echoed to the task log.
const maxBodyLog = 1 << 20 // 1 MiB

// RunHTTP performs the request described by spec, writing a human-readable
// transcript (request line, response status, body) to out. It returns a process
// exit code: 0 when the response status is accepted (see statusAccepted), else 1.
// A malformed request or transport error is a task failure (code 1), not a Go
// error — the error is reported in the log, and the non-zero code drives retry.
// The request is bounded by ctx (the scheduler kills the subprocess on timeout).
func RunHTTP(ctx context.Context, spec model.HTTPSpec, out io.Writer) int {
	method := strings.ToUpper(strings.TrimSpace(spec.Method))
	if method == "" {
		method = http.MethodGet
	}
	url := strings.TrimSpace(spec.URL)
	if url == "" {
		fmt.Fprintln(out, "http: url is empty")
		return 1
	}
	var body io.Reader
	if spec.Body != "" {
		body = strings.NewReader(spec.Body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		fmt.Fprintf(out, "http: build request: %v\n", err)
		return 1
	}
	for k, v := range spec.Headers {
		req.Header.Set(k, v)
	}
	fmt.Fprintf(out, "> %s %s\n", method, url)

	start := time.Now()
	// No SSRF guard here (unlike notify webhooks): an http task is a deliberate
	// author action with the same trust as a shell task, which can already curl
	// anything. The client follows redirects by default.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(out, "http: request failed: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyLog))
	fmt.Fprintf(out, "< %s (%s)\n", resp.Status, time.Since(start).Round(time.Millisecond))
	if len(data) > 0 {
		out.Write(data)
		if data[len(data)-1] != '\n' {
			fmt.Fprintln(out)
		}
	}
	if !statusAccepted(resp.StatusCode, spec.ExpectedStatus) {
		fmt.Fprintf(out, "http: unexpected status %d (want %s)\n", resp.StatusCode, expectedLabel(spec.ExpectedStatus))
		return 1
	}
	return 0
}

// statusAccepted reports whether code is a success. With no expected list, any
// 2xx counts; otherwise code must appear in the list.
func statusAccepted(code int, expected []int) bool {
	if len(expected) == 0 {
		return code >= 200 && code < 300
	}
	for _, e := range expected {
		if e == code {
			return true
		}
	}
	return false
}

func expectedLabel(expected []int) string {
	if len(expected) == 0 {
		return "2xx"
	}
	parts := make([]string, len(expected))
	for i, e := range expected {
		parts[i] = fmt.Sprintf("%d", e)
	}
	return strings.Join(parts, ",")
}
