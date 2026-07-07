package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCallPathQueryBodyAndAuth(t *testing.T) {
	var gotPath, gotQuery, gotAuth, gotCT, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath() // encoded form, so %20 is visible
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "secret")
	res, err := c.Call(context.Background(), "POST", "/api/dags/{id}/trigger", Options{
		Path:  map[string]string{"id": "etl daily"}, // space must be path-escaped
		Query: map[string]string{"limit": "5", "empty": ""},
		Body:  []byte(`{"params":{"day":"x"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() {
		t.Fatalf("status = %d", res.Status)
	}
	if gotPath != "/api/dags/etl%20daily/trigger" {
		t.Errorf("path = %q, want escaped", gotPath)
	}
	if gotQuery != "limit=5" { // empty value dropped
		t.Errorf("query = %q, want limit=5", gotQuery)
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotCT != "application/json" {
		t.Errorf("content-type = %q", gotCT)
	}
	if gotBody != `{"params":{"day":"x"}}` {
		t.Errorf("body = %q", gotBody)
	}
}

func TestCallAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"error":"run is still active"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	res, err := c.Call(context.Background(), "POST", "/api/runs/x/retry", Options{})
	if err == nil {
		t.Fatal("expected an error for 409")
	}
	var ae *APIError
	if !errors.As(err, &ae) {
		t.Fatalf("want *APIError, got %T", err)
	}
	if ae.Status != http.StatusConflict || ae.Message != "run is still active" {
		t.Errorf("APIError = %+v", ae)
	}
	if res == nil || res.Status != http.StatusConflict {
		t.Error("Result should be populated even on error")
	}
}

func TestCallJSONDecodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"run_id":"r1"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	var out struct {
		RunID string `json:"run_id"`
	}
	if _, err := c.CallJSON(context.Background(), "GET", "/api/x", Options{}, &out); err != nil {
		t.Fatal(err)
	}
	if out.RunID != "r1" {
		t.Errorf("decoded = %+v", out)
	}
}

func TestNoTokenNoAuthHeader(t *testing.T) {
	var hadAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadAuth = r.Header["Authorization"]
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	if _, err := New(srv.URL, "").Call(context.Background(), "GET", "/api/dags", Options{}); err != nil {
		t.Fatal(err)
	}
	if hadAuth {
		t.Error("no Authorization header should be sent when token is empty")
	}
}
