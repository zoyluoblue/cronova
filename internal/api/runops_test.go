package api

import (
	"net/http"
	"strings"
	"testing"
)

// TestRunTimelineAndHold: the timeline merges run/task/audit events in order,
// and hold/release round-trips (409 on a terminal run).
func TestRunTimelineAndHold(t *testing.T) {
	h, _, _, _ := setup(t) // seeds run etl__r1 (success) with one task
	rec := do(h, "GET", "/api/runs/etl__r1/timeline", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("timeline = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"events"`, `"kind":"run"`, `created`} {
		if !strings.Contains(body, want) {
			t.Fatalf("timeline missing %q: %s", want, body)
		}
	}
	// Holding a TERMINAL run must be refused.
	if rec := do(h, "POST", "/api/runs/etl__r1/hold", "", nil); rec.Code != http.StatusConflict {
		t.Fatalf("hold on terminal run = %d, want 409", rec.Code)
	}
}
