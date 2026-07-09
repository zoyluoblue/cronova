package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/zoyluo/cronova/internal/model"
)

func TestOpenAPISpecEndpoint(t *testing.T) {
	h, _, _, _ := setup(t)
	rec, _ := get(t, h, "GET", "/openapi.json")
	if rec.Code != 200 {
		t.Fatalf("/openapi.json = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age") {
		t.Errorf("Cache-Control = %q, want a cacheable spec", cc)
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("spec endpoint served invalid JSON: %v", err)
	}
	paths, _ := doc["paths"].(map[string]any)
	if len(paths) == 0 {
		t.Fatal("served spec has no paths")
	}
}

func TestDocsPageEndpoint(t *testing.T) {
	h, _, _, _ := setup(t)
	rec, body := get(t, h, "GET", "/docs")
	if rec.Code != 200 {
		t.Fatalf("/docs = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	page := body.(string)
	for _, want := range []string{"Redoc.init", "/openapi.json", "cronova API reference"} {
		if !strings.Contains(page, want) {
			t.Errorf("/docs page missing %q", want)
		}
	}
}

// The reference surfaces (spec, docs UI, metrics) live outside /api/ and must
// stay reachable without credentials even when auth is enabled.
func TestDocsEndpointsPublicUnderAuth(t *testing.T) {
	h := authServer(t, model.RoleAdmin)
	for _, p := range []string{"/openapi.json", "/docs", "/metrics"} {
		if rec := do(h, "GET", p, "", nil); rec.Code != http.StatusOK {
			t.Errorf("unauthenticated %s = %d, want 200", p, rec.Code)
		}
	}
}

func TestCatalogProjection(t *testing.T) {
	eps := Catalog()
	if len(eps) != len(apiCatalog()) {
		t.Fatalf("Catalog len = %d, want %d (must mirror the internal catalog)", len(eps), len(apiCatalog()))
	}
	byKey := map[string]Endpoint{}
	for _, e := range eps {
		if e.Method == "" || e.Path == "" || e.Tag == "" || e.Summary == "" {
			t.Errorf("incomplete endpoint projection: %+v", e)
		}
		byKey[e.Method+" "+e.Path] = e
	}

	cases := []struct {
		name  string
		key   string
		check func(t *testing.T, e Endpoint)
	}{
		{"raw YAML create keeps its body type", "POST /api/dags", func(t *testing.T, e Endpoint) {
			if !e.HasBody || e.BodyType != "yaml" {
				t.Errorf("POST /api/dags = hasBody %v type %q, want yaml body", e.HasBody, e.BodyType)
			}
		}},
		{"json body type is defaulted", "POST /api/dags/build", func(t *testing.T, e Endpoint) {
			if !e.HasBody || e.BodyType != "json" {
				t.Errorf("POST /api/dags/build = hasBody %v type %q, want json body", e.HasBody, e.BodyType)
			}
		}},
		{"bodyless GET stays bodyless", "GET /api/dags", func(t *testing.T, e Endpoint) {
			if e.HasBody || e.BodyExample != nil {
				t.Errorf("GET /api/dags should have no body: %+v", e)
			}
		}},
		{"path params are projected", "GET /api/dags/{id}", func(t *testing.T, e Endpoint) {
			if len(e.Params) != 1 || e.Params[0].Name != "id" || e.Params[0].In != "path" || !e.Params[0].Required {
				t.Errorf("params = %+v, want one required path param id", e.Params)
			}
		}},
		{"trigger body is optional", "POST /api/dags/{id}/trigger", func(t *testing.T, e Endpoint) {
			if !e.OptionalBody {
				t.Error("trigger body should be optional")
			}
		}},
		{"health probe never needs auth", "GET /healthz", func(t *testing.T, e Endpoint) {
			if !e.NoAuth {
				t.Error("healthz should be NoAuth")
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, ok := byKey[tc.key]
			if !ok {
				t.Fatalf("catalog missing %s", tc.key)
			}
			tc.check(t, e)
		})
	}
}
