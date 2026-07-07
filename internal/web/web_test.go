package web

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// By default FS() serves the assets embedded into the binary.
func TestFSDefaultsToEmbedded(t *testing.T) {
	t.Setenv("CRONOVA_WEB_DIR", "") // empty => embedded path
	if _, err := fs.Stat(FS(), "base.js"); err != nil {
		t.Fatalf("embedded FS should contain base.js: %v", err)
	}
}

// CRONOVA_WEB_DIR points FS() at a directory on disk (dev hot-reload).
func TestFSDiskOverride(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("disk"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CRONOVA_WEB_DIR", dir)
	b, err := fs.ReadFile(FS(), "marker.txt")
	if err != nil || string(b) != "disk" {
		t.Fatalf("disk override should serve marker.txt: b=%q err=%v", b, err)
	}
}

// A CRONOVA_WEB_DIR that is not an existing directory is ignored — FS() falls
// back to the embedded assets rather than serving nothing.
func TestFSDiskOverrideIgnoredWhenMissing(t *testing.T) {
	t.Setenv("CRONOVA_WEB_DIR", filepath.Join(t.TempDir(), "nope"))
	if _, err := fs.Stat(FS(), "base.js"); err != nil {
		t.Fatalf("missing dir should fall back to embedded: %v", err)
	}
}
