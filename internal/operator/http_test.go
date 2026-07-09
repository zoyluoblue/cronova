package operator

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoyluo/cronova/internal/model"
)

func TestRunHTTPSuccess(t *testing.T) {
	var gotMethod, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("pong"))
	}))
	defer srv.Close()

	var out bytes.Buffer
	code := RunHTTP(context.Background(), model.HTTPSpec{
		Method: "post", URL: srv.URL, Body: "hello",
		Headers: map[string]string{"Authorization": "Bearer tok"},
	}, &out)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; log:\n%s", code, out.String())
	}
	if gotMethod != "POST" || gotAuth != "Bearer tok" || gotBody != "hello" {
		t.Fatalf("server saw method=%q auth=%q body=%q", gotMethod, gotAuth, gotBody)
	}
	if !strings.Contains(out.String(), "pong") || !strings.Contains(out.String(), "200") {
		t.Fatalf("log missing response:\n%s", out.String())
	}
}

func TestRedactURLUserinfo(t *testing.T) {
	// A password in the URL userinfo is masked in the echoed request line.
	// (Connection-password substitutions are redacted separately by the executor.)
	got := redactURL("https://admin:hunter2@example.com/api")
	if strings.Contains(got, "hunter2") {
		t.Fatalf("userinfo password leaked: %q", got)
	}
	if !strings.Contains(got, "admin") {
		t.Fatalf("username should be preserved: %q", got)
	}
	// A URL without userinfo is returned unchanged.
	if got := redactURL("https://example.com/hook?x=1"); got != "https://example.com/hook?x=1" {
		t.Fatalf("plain URL altered: %q", got)
	}
}

func TestRunHTTPUnexpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	var out bytes.Buffer
	// default expectation is 2xx → 500 fails
	if code := RunHTTP(context.Background(), model.HTTPSpec{URL: srv.URL}, &out); code != 1 {
		t.Fatalf("exit = %d, want 1 for 500", code)
	}
	// explicit expected_status accepting 500 → success
	if code := RunHTTP(context.Background(), model.HTTPSpec{URL: srv.URL, ExpectedStatus: []int{500}}, &out); code != 0 {
		t.Fatalf("exit = %d, want 0 when 500 is expected", code)
	}
}

func TestRunHTTPTransportError(t *testing.T) {
	var out bytes.Buffer
	// a closed port → connection refused → task failure (exit 1), not a panic
	if code := RunHTTP(context.Background(), model.HTTPSpec{URL: "http://127.0.0.1:1/nope"}, &out); code != 1 {
		t.Fatalf("exit = %d, want 1 on transport error", code)
	}
	if code := RunHTTP(context.Background(), model.HTTPSpec{URL: ""}, &out); code != 1 {
		t.Fatalf("exit = %d, want 1 on empty url", code)
	}
}
