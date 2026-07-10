package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultSocketPathUsesPrivatePerUserDirectory(t *testing.T) {
	path := defaultSocketPath()
	if !filepath.IsAbs(path) || filepath.Base(path) != "executor.sock" {
		t.Fatalf("default socket path = %q", path)
	}
	if !strings.Contains(filepath.Base(filepath.Dir(path)), "cronova-") {
		t.Fatalf("default socket directory is not per-user: %q", path)
	}
}

func TestListenExecutorSocketPermissions(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "cnve")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	dir := filepath.Join(root, "p")
	sock := filepath.Join(dir, "executor.sock")
	lis, cleanup, err := listenExecutorSocket(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	defer lis.Close()

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("socket directory mode = %o, want 700", got)
	}
	sockInfo, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if got := sockInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("socket mode = %o, want 600", got)
	}
}

func TestListenExecutorSocketRejectsPublicDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "public")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := listenExecutorSocket(filepath.Join(dir, "executor.sock")); err == nil {
		t.Fatal("expected a public socket directory to be rejected")
	}
}
