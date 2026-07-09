package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/zoyluo/cronova/internal/model"
)

// Per-task project staging. When a shell task sets Project, the scheduler copies
// that uploaded project directory to a fresh per-attempt workspace under the OS
// temp dir and runs the command there (cwd = workspace), so `python3 main.py`
// resolves and each run gets a clean, isolated copy. The workspace is removed
// when the attempt finalizes (runTask's defer). This assumes the executor shares
// a filesystem with the scheduler (in-process, or a same-host/shared-mount gRPC
// executor) — the same assumption the log path already makes.

// validProjectName restricts a project name to one safe path segment so it can
// never escape the projects dir via traversal (it comes from user-authored YAML).
var validProjectName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// workspaceRoot is where this scheduler's per-attempt copies live. It honors
// Options.WorkspaceDir (tests; multi-instance isolation — see cmdServe, which
// derives a per-DB dir so two cronovas on one host can't GC each other's
// workspaces), defaulting to a subdir of the OS temp dir.
func (s *Scheduler) workspaceRoot() string {
	if s.opts.WorkspaceDir != "" {
		return s.opts.WorkspaceDir
	}
	return filepath.Join(os.TempDir(), "cronova-workspaces")
}

// stageProject copies projects/<name> to a fresh workspace keyed by the task ref
// and returns the workspace path. A pre-existing workspace (a retry) is replaced
// so the copy is always clean.
func (s *Scheduler) stageProject(name, ref string) (string, error) {
	if s.opts.ProjectsDir == "" {
		return "", fmt.Errorf("no projects directory configured (set -projects / CRONOVA_PROJECTS)")
	}
	if name == "." || name == ".." || !validProjectName.MatchString(name) {
		return "", fmt.Errorf("invalid project name %q", name)
	}
	src := filepath.Join(s.opts.ProjectsDir, name)
	info, err := os.Stat(src)
	if err != nil {
		return "", fmt.Errorf("project %q not found under %s", name, s.opts.ProjectsDir)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project %q is not a directory", name)
	}

	ws := filepath.Join(s.workspaceRoot(), sanitizeRef(ref))
	if err := os.RemoveAll(ws); err != nil {
		return "", fmt.Errorf("clear workspace: %w", err)
	}
	if err := copyTree(src, ws); err != nil {
		_ = os.RemoveAll(ws)
		return "", fmt.Errorf("stage project: %w", err)
	}
	return ws, nil
}

// sanitizeRef turns a task ref ("run_id/task_id") into a single safe dir name.
//
// The character map is many-to-one ("data.load" and "data-load" both fold to
// "data-load"), so two distinct refs could otherwise share one workspace dir and
// clobber or GC each other. To keep the name INJECTIVE we append a short hash of
// the ORIGINAL ref whenever folding actually changed something. gcWorkspaces
// derives its keep-set through this same function, so the suffix stays consistent
// on both sides.
func sanitizeRef(ref string) string {
	safe := legacySanitizeRef(ref)
	if safe == ref {
		return safe // no folding: already unambiguous
	}
	sum := sha256.Sum256([]byte(ref))
	return safe + "-" + hex.EncodeToString(sum[:4])
}

// legacySanitizeRef is the pre-injective-fix mapping (no hash suffix). It exists
// ONLY so gcWorkspaces can also keep workspaces staged by an older binary: after
// an in-place upgrade, a recovered still-running task's workspace carries this old
// name and must not be swept as an orphan (which would delete a live task's cwd).
func legacySanitizeRef(ref string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, ref)
}

// copyTree recursively copies src into dst, preserving file permission bits (so
// executable scripts stay executable). Non-regular files (symlinks, devices) are
// skipped — a project is expected to be plain source, and copying symlinks would
// risk escaping the workspace.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		}
		if !d.Type().IsRegular() {
			return nil // skip symlinks/devices/etc.
		}
		return copyFile(path, target)
	})
}

// gcWorkspaces removes leftover per-attempt workspaces whose task is no longer
// running. The normal path removes its own workspace (runTask's defer), but a
// scheduler shutdown mid-run deliberately keeps it (so a recovered task can
// still read it), and that recovered task finalizes OUTSIDE runTask — no defer
// fires, so the dir would live forever. The sweep keeps every workspace owned
// by a currently-running task instance and removes the rest.
//
// minAge guards the periodic sweep against a launch race: a workspace is staged
// moments BEFORE its task row flips to running, so a freshly-staged dir could
// look orphaned. The boot-time sweep (before dispatch starts) passes 0.
func (s *Scheduler) gcWorkspaces(ctx context.Context, minAge time.Duration) {
	root := s.workspaceRoot()
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) == 0 {
		return // nothing to sweep (or root missing)
	}
	// Keep any workspace whose task might still have a live process: TaskRunning,
	// but ALSO TaskQueued-with-a-ref — a ref is assigned at launch, so a queued
	// row with one may be a process whose "mark running" write hasn't landed yet
	// (or a recovery that couldn't update the row). Only sweep workspaces of tasks
	// that are terminal / never launched.
	keep := map[string]bool{}
	for _, st := range []model.TaskState{model.TaskRunning, model.TaskQueued} {
		tis, err := s.store.ListTaskInstancesByState(ctx, st)
		if err != nil {
			s.log.Error("workspace gc: list task instances", "state", st, "err", err)
			return // fail safe: skip the sweep rather than risk deleting a live workspace
		}
		for _, ti := range tis {
			if ti.ExecutorRef != "" {
				keep[sanitizeRef(ti.ExecutorRef)] = true
				// Also keep the pre-upgrade (un-hashed) name so a task reattached across
				// a binary upgrade — whose workspace still carries the old name and is
				// NOT re-staged by recovery — is never swept out from under itself.
				keep[legacySanitizeRef(ti.ExecutorRef)] = true
			}
		}
	}
	cutoff := time.Now().Add(-minAge)
	removed := 0
	for _, e := range entries {
		if !e.IsDir() || keep[e.Name()] {
			continue
		}
		if minAge > 0 {
			info, err := e.Info()
			if err != nil || info.ModTime().After(cutoff) {
				continue // too fresh — may belong to a task mid-launch
			}
		}
		if err := os.RemoveAll(filepath.Join(root, e.Name())); err == nil {
			removed++
		}
	}
	if removed > 0 {
		s.log.Info("workspace gc", "removed", removed)
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
