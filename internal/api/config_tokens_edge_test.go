package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/zoyluo/cronova/internal/auth"
)

func TestVariablesEdgeCases(t *testing.T) {
	h, _, _, _ := setup(t)
	cases := []struct {
		name     string
		method   string
		path     string
		body     string
		wantCode int
	}{
		{"empty list is [] not null", "GET", "/api/variables", "", 200},
		{"malformed body", "POST", "/api/variables/ok_key", `{"value":`, 400},
		{"delete missing key", "DELETE", "/api/variables/never_set", "", 404},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(h, tc.method, tc.path, tc.body, nil)
			if rec.Code != tc.wantCode {
				t.Fatalf("code = %d, want %d (body %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.method == "GET" && strings.TrimSpace(rec.Body.String()) != "[]" {
				t.Errorf("empty list body = %q, want []", rec.Body.String())
			}
		})
	}
}

func TestConnectionsEdgeCases(t *testing.T) {
	h, _, _, _ := setup(t)
	cases := []struct {
		name     string
		method   string
		path     string
		body     string
		wantCode int
	}{
		{"empty list is [] not null", "GET", "/api/connections", "", 200},
		{"invalid id charset", "POST", "/api/connections/bad@id", `{"type":"mysql"}`, 400},
		{"malformed body", "POST", "/api/connections/ok_id", `{"type":`, 400},
		{"delete missing connection", "DELETE", "/api/connections/never_set", "", 404},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(h, tc.method, tc.path, tc.body, nil)
			if rec.Code != tc.wantCode {
				t.Fatalf("code = %d, want %d (body %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.method == "GET" && strings.TrimSpace(rec.Body.String()) != "[]" {
				t.Errorf("empty list body = %q, want []", rec.Body.String())
			}
		})
	}
}

func TestCreateTokenValidation(t *testing.T) {
	h, _, _, _ := setup(t)
	cases := []struct {
		name     string
		body     string
		wantCode int
	}{
		{"malformed body", `{"name":`, 400},
		{"missing name", `{"role":"admin"}`, 400},
		{"blank name", `{"name":"   "}`, 400},
		{"unknown role", `{"name":"x","role":"root"}`, 400},
		{"role defaults to admin", `{"name":"defaulted"}`, 201},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(h, "POST", "/api/tokens", tc.body, nil)
			if rec.Code != tc.wantCode {
				t.Fatalf("code = %d, want %d (body %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantCode != 201 {
				return
			}
			var m map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
				t.Fatal(err)
			}
			if m["role"] != "admin" {
				t.Errorf("defaulted role = %v, want admin", m["role"])
			}
			plaintext, _ := m["token"].(string)
			prefix, _ := m["prefix"].(string)
			if !strings.HasPrefix(plaintext, auth.APITokenPrefix) {
				t.Errorf("plaintext %q missing the %q prefix", plaintext, auth.APITokenPrefix)
			}
			if prefix == "" || !strings.HasPrefix(plaintext, prefix) {
				t.Errorf("display prefix %q is not a prefix of the token", prefix)
			}
		})
	}
}

func TestTokenListAndDeleteEdgeCases(t *testing.T) {
	h, _, _, _ := setup(t)
	// empty list is [] not null
	rec, body := get(t, h, "GET", "/api/tokens")
	if rec.Code != 200 {
		t.Fatalf("list = %d", rec.Code)
	}
	if toks, ok := body.([]any); !ok || len(toks) != 0 {
		t.Errorf("empty token list = %v, want []", body)
	}
	// delete validation
	if rec := do(h, "DELETE", "/api/tokens/notanumber", "", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("non-numeric id = %d, want 400", rec.Code)
	}
	if rec := do(h, "DELETE", "/api/tokens/424242", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown id = %d, want 404", rec.Code)
	}
}

func TestProjectEndpointsDisabledWithoutDir(t *testing.T) {
	h, _, _, _ := setup(t) // setup never calls SetProjectsDir
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/projects/x"},
		{"DELETE", "/api/projects/x"},
	} {
		if rec := do(h, tc.method, tc.path, "", nil); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s = %d, want 503 when uploads are disabled", tc.method, tc.path, rec.Code)
		}
	}
}

func TestProjectNameValidationAndMissingDelete(t *testing.T) {
	h, _ := projectsServer(t)
	if rec := do(h, "GET", "/api/projects/bad@name", "", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid name get = %d, want 400", rec.Code)
	}
	if rec := do(h, "DELETE", "/api/projects/bad@name", "", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid name delete = %d, want 400", rec.Code)
	}
	if rec := do(h, "DELETE", "/api/projects/ghost", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("missing project delete = %d, want 404", rec.Code)
	}
}
