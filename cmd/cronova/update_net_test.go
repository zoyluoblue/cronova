package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestFetchReleaseAgainstRealGitHub downloads the real latest release for this
// host from GitHub, verifies it against the published SHA256SUMS, extracts the
// binary, and runs it — exercising the EXACT `cronova update` download path
// against production assets (no local mirror). Gated on network + ~10MB, so it is
// opt-in:
//
//	CRONOVA_NET_TEST=1 go test -run RealGitHub -count=2 ./cmd/cronova/
func TestFetchReleaseAgainstRealGitHub(t *testing.T) {
	if os.Getenv("CRONOVA_NET_TEST") != "1" {
		t.Skip("set CRONOVA_NET_TEST=1 to run the real-GitHub download+verify test")
	}
	base := releaseBaseURL("latest")
	asset := releaseAsset()

	bins, ver, err := fetchRelease(base, asset, "") // download + SHA256 verify + untar
	if err != nil {
		t.Fatalf("fetchRelease(%s/%s): %v", base, asset, err)
	}
	if len(bins["cronova"]) < 1<<20 {
		t.Fatalf("cronova binary missing or too small: %d bytes", len(bins["cronova"]))
	}
	t.Logf("verified %s from GitHub: cronova=%d bytes, VERSION=%q", asset, len(bins["cronova"]), ver)

	// The downloaded binary is this host's platform — write it out and run it, so
	// we prove the whole chain end to end, not just the checksum.
	bin := filepath.Join(t.TempDir(), "cronova-dl")
	if err := os.WriteFile(bin, bins["cronova"], 0o755); err != nil {
		t.Fatal(err)
	}
	// Run -h (present in every release; `version` was added later) to prove the
	// cross-compiled binary actually executes on this host — not just that the
	// bytes downloaded and verified.
	out, _ := exec.Command(bin, "-h").CombinedOutput() // -h path exits 0; ignore code across versions
	if !strings.Contains(string(out), "cronova") || !strings.Contains(string(out), "scheduler") {
		t.Fatalf("downloaded binary did not run as cronova; output:\n%s", out)
	}
	t.Logf("downloaded binary executes on this host (%d bytes, valid cronova usage banner)", len(bins["cronova"]))
}
