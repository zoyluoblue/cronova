package api

import "testing"

// TestActionCoverageClosure is the plan3 R11B-04 gate in miniature: every
// public endpoint must carry exactly one valid classification, so no surface
// can ship unclassified (an unclassified surface would be invisible to the
// AI policy layer — the exact gap plan3 forbids).
func TestActionCoverageClosure(t *testing.T) {
	valid := map[string]bool{
		ClassDirectRead: true, ClassDirectDraft: true, ClassPrepareApprove: true,
		ClassHumanOnly: true, ClassInternalOnly: true,
	}
	eps := Catalog()
	if len(eps) == 0 {
		t.Fatal("empty catalog can never pass coverage (plan5 §0.2)")
	}
	for _, ep := range eps {
		if !valid[ep.Classification] {
			t.Errorf("unclassified surface: %s %s (%q)", ep.Method, ep.Path, ep.Classification)
		}
	}
	// Spot-check the security-critical overrides.
	byKey := map[string]string{}
	for _, ep := range eps {
		byKey[ep.Method+" "+ep.Path] = ep.Classification
	}
	if byKey["POST /api/worker-tokens"] != ClassHumanOnly {
		t.Errorf("worker-token minting must be HUMAN_ONLY, got %q", byKey["POST /api/worker-tokens"])
	}
	if byKey["POST /api/workers/join"] != ClassInternalOnly {
		t.Errorf("worker join must be INTERNAL_ONLY, got %q", byKey["POST /api/workers/join"])
	}
	if byKey["GET /api/dags"] != ClassDirectRead {
		t.Errorf("GET /api/dags must be DIRECT_READ, got %q", byKey["GET /api/dags"])
	}
}
