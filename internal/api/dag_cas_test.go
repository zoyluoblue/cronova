package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/zoyluo/cronova/internal/model"
)

// TestDagSaveCAS: a build carrying expected_hash only lands when the stored
// definition still matches — after a concurrent save changes the hash, the
// stale writer gets 409 dag_conflict and its spec never reaches the engine
// (plan1 LB-01: source must never be silently rewritten).
func TestDagSaveCAS(t *testing.T) {
	h, st, trig, _ := setup(t) // seeds DAG "etl"

	spec := func(cmd, expected string) string {
		m := map[string]any{
			"dag_id": "etl",
			"tasks":  []any{map[string]any{"id": "t", "type": "shell", "command": cmd}},
		}
		if expected != "" {
			m["expected_hash"] = expected
		}
		b, _ := json.Marshal(m)
		return string(b)
	}

	// Load the CAS token the editor would hold.
	var detail struct {
		DefinitionHash string `json:"definition_hash"`
	}
	rec := do(h, "GET", "/api/dags/etl", "", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil || detail.DefinitionHash == "" {
		t.Fatalf("definition_hash missing from GET: %s", rec.Body.String())
	}

	// Fresh hash: accepted and forwarded to the engine.
	if rec := do(h, "POST", "/api/dags/build", spec("echo v2", detail.DefinitionHash), nil); rec.Code != http.StatusOK {
		t.Fatalf("fresh-hash save = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(trig.createdYML, "echo v2") {
		t.Fatalf("fresh save did not reach the engine: %q", trig.createdYML)
	}

	// A concurrent editor changes the stored definition (hash flips).
	if err := st.UpsertDAG(context.Background(), &model.DAG{
		DagID: "etl", DefinitionYAML: "dag_id: etl\ntasks:\n  - id: other\n    command: \"echo concurrent\"\n",
		MaxActiveRuns: 1,
	}); err != nil {
		t.Fatal(err)
	}

	// The first editor's now-STALE hash must be refused, and the engine must
	// not see the stale spec.
	trig.createdYML = ""
	rec = do(h, "POST", "/api/dags/build", spec("echo v3-stale", detail.DefinitionHash), nil)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "dag_conflict") {
		t.Fatalf("stale-hash save = %d (%s), want 409 dag_conflict", rec.Code, rec.Body.String())
	}
	if trig.createdYML != "" {
		t.Fatalf("stale save leaked through to the engine: %q", trig.createdYML)
	}

	// Legacy path (no expected_hash) still last-write-wins for CLI/GitOps.
	if rec := do(h, "POST", "/api/dags/build", spec("echo v4", ""), nil); rec.Code != http.StatusOK {
		t.Fatalf("legacy save = %d", rec.Code)
	}
}
