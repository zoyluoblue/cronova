package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestBuildSpecValid checks the generated OpenAPI document is well-formed JSON
// with the expected top-level shape.
func TestBuildSpecValid(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(buildSpec(), &doc); err != nil {
		t.Fatalf("spec is not valid JSON: %v", err)
	}
	if doc["openapi"] != "3.0.3" {
		t.Errorf("openapi version = %v, want 3.0.3", doc["openapi"])
	}
	paths, ok := doc["paths"].(map[string]any)
	if !ok || len(paths) == 0 {
		t.Fatal("spec has no paths")
	}
	// Every cataloged endpoint must appear under its path+method.
	for _, ep := range apiCatalog() {
		item, ok := paths[ep.Path].(map[string]any)
		if !ok {
			t.Errorf("missing path %s", ep.Path)
			continue
		}
		if _, ok := item[strings.ToLower(ep.Method)]; !ok {
			t.Errorf("missing %s %s", ep.Method, ep.Path)
		}
	}
}

// TestEveryOperationHasFourSamples ensures the in-page language switcher always
// has curl/Go/Python/Java.
func TestEveryOperationHasFourSamples(t *testing.T) {
	want := map[string]bool{"cURL": true, "Go": true, "Python": true, "Java": true}
	for _, ep := range apiCatalog() {
		samples := codeSamples(ep)
		if len(samples) != 4 {
			t.Fatalf("%s %s: got %d samples, want 4", ep.Method, ep.Path, len(samples))
		}
		got := map[string]bool{}
		for _, s := range samples {
			m := s.(map[string]any)
			label := m["label"].(string)
			got[label] = true
			src, _ := m["source"].(string)
			if strings.TrimSpace(src) == "" {
				t.Errorf("%s %s: empty %s sample", ep.Method, ep.Path, label)
			}
		}
		for lang := range want {
			if !got[lang] {
				t.Errorf("%s %s: missing %s sample", ep.Method, ep.Path, lang)
			}
		}
	}
}

// TestSamplesReflectMethodAndAuth spot-checks that generated samples embed the
// right verb, URL, auth header, and body.
func TestSamplesReflectMethodAndAuth(t *testing.T) {
	var trigger, health apiEndpoint
	for _, ep := range apiCatalog() {
		if ep.Path == "/api/dags/{id}/trigger" {
			trigger = ep
		}
		if ep.Path == "/healthz" {
			health = ep
		}
	}
	// Authenticated POST with a body.
	curl := sampleCurl(trigger)
	if !strings.Contains(curl, "-X POST") || !strings.Contains(curl, "Authorization: Bearer") {
		t.Errorf("trigger curl missing method/auth:\n%s", curl)
	}
	if !strings.Contains(curl, "etl_daily/trigger") {
		t.Errorf("trigger curl missing filled path:\n%s", curl)
	}
	py := samplePython(trigger)
	if !strings.Contains(py, "requests.post(") || !strings.Contains(py, "data=payload") {
		t.Errorf("trigger python malformed:\n%s", py)
	}
	java := sampleJava(trigger)
	if !strings.Contains(java, "BodyPublishers.ofString") {
		t.Errorf("trigger java missing body publisher:\n%s", java)
	}
	// Unauthenticated GET: no auth header, simplest curl form.
	hc := sampleCurl(health)
	if strings.Contains(hc, "Authorization") {
		t.Errorf("healthz curl should not include auth:\n%s", hc)
	}
	hgo := sampleGo(health)
	if strings.Contains(hgo, "token :=") {
		t.Errorf("healthz Go should not declare a token:\n%s", hgo)
	}
}

// TestJavaSamplesCompilable guards against emitting a bare `.POST` / `.PUT` for
// bodyless POST/PUT endpoints — java.net.http has no such no-arg overload, so it
// must be `.POST(HttpRequest.BodyPublishers.noBody())`.
func TestJavaSamplesCompilable(t *testing.T) {
	var checked int
	for _, ep := range apiCatalog() {
		if ep.Method != "POST" && ep.Method != "PUT" {
			continue
		}
		if _, hasBody := sampleBody(ep); hasBody {
			continue // body-bearing case appends BodyPublishers.ofString(...)
		}
		checked++
		java := sampleJava(ep)
		verb := "." + ep.Method
		// The verb must be immediately followed by "(" — never a newline (bare call).
		idx := strings.Index(java, verb+"\n")
		if idx >= 0 {
			t.Errorf("%s %s: Java sample emits a bare %s (won't compile):\n%s", ep.Method, ep.Path, verb, java)
		}
		if !strings.Contains(java, verb+"(HttpRequest.BodyPublishers.noBody())") {
			t.Errorf("%s %s: bodyless Java sample must use BodyPublishers.noBody():\n%s", ep.Method, ep.Path, java)
		}
	}
	if checked == 0 {
		t.Fatal("no bodyless POST/PUT endpoints exercised — test is not covering the fix")
	}
}

// TestNoAuthOperationsMarkedInSpec verifies public endpoints override security.
func TestNoAuthOperationsMarkedInSpec(t *testing.T) {
	for _, ep := range apiCatalog() {
		if !ep.NoAuth {
			continue
		}
		op := operationFor(ep)
		sec, ok := op["security"].([]any)
		if !ok || len(sec) != 0 {
			t.Errorf("%s %s: NoAuth endpoint should have empty security, got %v", ep.Method, ep.Path, op["security"])
		}
	}
}
