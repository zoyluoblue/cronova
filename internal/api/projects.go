package api

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/projectfs"
)

// Uploaded projects are plain directories under the server's projects dir; a
// shell task's `project` field names one, and the scheduler stages a copy as the
// task's working directory (see internal/scheduler/project.go). These endpoints
// manage the files. Writes (POST/DELETE) are admin-gated by withAuth.
//
// M2 scope: single-file and inline uploads. Folder/zip upload is M3.

const (
	maxProjectFileSize = 10 << 20 // per uploaded file
	maxProjectSize     = 50 << 20 // per project total (a few small files)
)

// projectNameRe matches the scheduler's rule: one safe path segment, so a name
// can never traverse out of the projects dir.
var projectNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func validProjectName(name string) bool {
	return name != "." && name != ".." && projectNameRe.MatchString(name)
}

// safeFileName accepts a single path component (no separators, not dot/dotdot).
// Callers filepath.Base the input first; this rejects the residual bad cases.
func safeFileName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	return !strings.ContainsAny(name, `/\`)
}

type projectInfo struct {
	Name  string `json:"name"`
	Files int    `json:"files"`
	Size  int64  `json:"size"`
}

// GET /api/projects — list uploaded projects.
func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	if s.projectsDir == "" {
		writeJSON(w, http.StatusOK, []projectInfo{})
		return
	}
	lock := projectfs.Lock(s.projectsDir)
	lock.RLock()
	defer lock.RUnlock()
	entries, err := os.ReadDir(s.projectsDir)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, []projectInfo{})
			return
		}
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []projectInfo{}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".upload-") || strings.HasPrefix(e.Name(), ".previous-") {
			continue
		}
		files, size := dirStats(filepath.Join(s.projectsDir, e.Name()))
		out = append(out, projectInfo{Name: e.Name(), Files: files, Size: size})
	}
	writeJSON(w, http.StatusOK, out)
}

type projectFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// GET /api/projects/{name} — list the files in one project.
func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	dir, ok := s.projectDir(w, r)
	if !ok {
		return
	}
	lock := projectfs.Lock(s.projectsDir)
	lock.RLock()
	defer lock.RUnlock()
	if _, err := os.Stat(dir); err != nil {
		httpErr(w, http.StatusNotFound, "project not found")
		return
	}
	files := []projectFile{}
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		if info, err := d.Info(); err == nil {
			files = append(files, projectFile{Path: filepath.ToSlash(rel), Size: info.Size()})
		}
		return nil
	})
	writeJSON(w, http.StatusOK, map[string]any{"name": filepath.Base(dir), "files": files})
}

// POST /api/projects/{name} — upload into a project. multipart/form-data, one of:
//   - a single `file` part (single-file upload),
//   - many `file` parts whose filenames are project-root-relative paths (a folder
//     upload via <input webkitdirectory>),
//   - a single `file` part named *.zip (auto-extracted, zip-slip guarded),
//   - `filename` + `content` fields (an inline script).
//
// Uploads are additive (upsert files). Admin-gated by withAuth.
func (s *Server) uploadProject(w http.ResponseWriter, r *http.Request) {
	if s.projectsDir == "" {
		httpErr(w, http.StatusServiceUnavailable, "project uploads are disabled (no projects dir configured)")
		return
	}
	name := r.PathValue("name")
	if !validProjectName(name) {
		httpErr(w, http.StatusBadRequest, "invalid project name (allowed: letters, digits, . _ -)")
		return
	}
	// Cap the whole request so a folder upload can't exhaust memory/disk.
	r.Body = http.MaxBytesReader(w, r.Body, maxProjectSize+(1<<20))
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		httpErr(w, http.StatusRequestEntityTooLarge, "upload too large or malformed")
		return
	}

	var files []*multipart.FileHeader
	var paths []string // optional, parallel to files: project-root-relative paths
	if r.MultipartForm != nil {
		files = r.MultipartForm.File["file"]
		paths = r.MultipartForm.Value["path"]
	}

	lock := projectfs.Lock(s.projectsDir)
	lock.Lock()
	defer lock.Unlock()
	if err := os.MkdirAll(s.projectsDir, 0o755); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	projDir := filepath.Join(s.projectsDir, name)
	staging, err := os.MkdirTemp(s.projectsDir, ".upload-"+name+"-")
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "create upload staging failed")
		return
	}
	defer os.RemoveAll(staging)
	if info, statErr := os.Stat(projDir); statErr == nil && info.IsDir() {
		if err := copyProjectTree(projDir, staging); err != nil {
			httpErr(w, http.StatusInternalServerError, "stage existing project failed")
			return
		}
	}

	var written int
	isZip := len(files) == 1 && isZipName(files[0].Filename)
	if isZip {
		written, err = extractZipUpload(staging, files[0])
	} else if len(files) > 0 {
		for i, fh := range files {
			if fh.Size > maxProjectFileSize {
				err = errTooBig
				break
			}
			rel := fh.Filename
			if i < len(paths) && paths[i] != "" {
				rel = paths[i]
			}
			f, openErr := fh.Open()
			if openErr != nil {
				err = openErr
				break
			}
			err = writeProjectFile(staging, rel, f)
			_ = f.Close()
			if err != nil {
				break
			}
			written++
		}
	} else {
		filename := r.FormValue("filename")
		if !safeFileName(filename) {
			httpErr(w, http.StatusBadRequest, "invalid or missing filename")
			return
		}
		err = writeProjectFile(staging, filename, strings.NewReader(r.FormValue("content")))
		written = 1
	}
	if err != nil {
		writeUploadErr(w, err)
		return
	}
	if _, size := dirStats(staging); size > maxProjectSize {
		writeUploadErr(w, errTooBig)
		return
	}
	if err := commitProjectTree(projDir, staging); err != nil {
		httpErr(w, http.StatusInternalServerError, "commit upload failed")
		return
	}

	target := name
	if isZip {
		target += " (zip)"
	}
	s.audit(r, "project.upload", target, fmt.Sprintf("%d file(s)", written))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "project": name, "files": written})
}

// errBadPath signals a rejected (traversal) path; errTooBig a size-limit breach.
var (
	errBadPath = fmt.Errorf("unsafe path in upload")
	errTooBig  = fmt.Errorf("upload exceeds size limit")
)

func writeUploadErr(w http.ResponseWriter, err error) {
	switch {
	case err == errBadPath:
		httpErr(w, http.StatusBadRequest, "upload contains an unsafe path")
	case err == errTooBig:
		httpErr(w, http.StatusRequestEntityTooLarge, "a file exceeds the size limit")
	default:
		httpErr(w, http.StatusInternalServerError, "write failed")
	}
}

func isZipName(name string) bool {
	return strings.EqualFold(filepath.Ext(filepath.Base(name)), ".zip")
}

// cleanRelPath turns an upload-supplied path (either separator, possibly a
// webkitdirectory relative path) into a safe project-relative path. Absolute
// paths and any component that escapes the project root are REJECTED (errBadPath)
// rather than silently rewritten, so a crafted archive/upload cannot plant files.
func cleanRelPath(name string) (string, error) {
	name = strings.ReplaceAll(name, `\`, "/")
	if name == "" || path.IsAbs(name) {
		return "", errBadPath
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errBadPath
	}
	return filepath.FromSlash(clean), nil
}

// writeProjectFile writes src to projDir/<relPath> (creating parent dirs),
// rejecting traversal and capping the file size. Files are stored executable so
// both `python3 main.py` and `./main.py` work.
func writeProjectFile(projDir, relPath string, src io.Reader) error {
	rel, err := cleanRelPath(relPath)
	if err != nil {
		return err
	}
	dst := filepath.Join(projDir, rel)
	if !withinDir(projDir, dst) {
		return errBadPath
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(src, maxProjectFileSize+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(dst)
		return err
	}
	if n > maxProjectFileSize {
		_ = os.Remove(dst)
		return errTooBig
	}
	return nil
}

// extractZipUpload extracts a .zip part into projDir with zip-slip protection,
// per-entry and total size caps. Returns the number of files written.
func extractZipUpload(projDir string, fh *multipart.FileHeader) (int, error) {
	f, err := fh.Open()
	if err != nil {
		return 0, err
	}
	defer f.Close()
	zr, err := zip.NewReader(f, fh.Size)
	if err != nil {
		return 0, errBadPath // not a valid zip
	}
	// Pre-check total uncompressed size (guards against a zip bomb).
	var total int64
	for _, e := range zr.File {
		if e.FileInfo().IsDir() {
			continue
		}
		if int64(e.UncompressedSize64) > maxProjectFileSize {
			return 0, errTooBig
		}
		total += int64(e.UncompressedSize64)
	}
	if total > maxProjectSize {
		return 0, errTooBig
	}
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		return 0, err
	}
	n := 0
	for _, e := range zr.File {
		if e.FileInfo().IsDir() || !e.Mode().IsRegular() { // skip dirs, symlinks, devices
			continue
		}
		rc, err := e.Open()
		if err != nil {
			return n, err
		}
		err = writeProjectFile(projDir, e.Name, rc)
		_ = rc.Close()
		if err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// copyProjectTree copies only plain directories and regular files. Uploaded
// projects never preserve symlinks or device nodes across a version swap.
func copyProjectTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		err = writeProjectFile(dst, filepath.ToSlash(rel), in)
		_ = in.Close()
		return err
	})
}

// commitProjectTree swaps staging into place. Callers hold the project root's
// write lock, so readers observe either the old complete tree or the new one.
func commitProjectTree(dst, staging string) error {
	backup, err := os.MkdirTemp(filepath.Dir(dst), ".previous-"+filepath.Base(dst)+"-")
	if err != nil {
		return err
	}
	if err := os.Remove(backup); err != nil {
		return err
	}
	hadOld := false
	if _, err := os.Stat(dst); err == nil {
		if err := os.Rename(dst, backup); err != nil {
			return err
		}
		hadOld = true
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(staging, dst); err != nil {
		if hadOld {
			_ = os.Rename(backup, dst)
		}
		return err
	}
	if hadOld {
		_ = os.RemoveAll(backup)
	}
	return nil
}

// DELETE /api/projects/{name} — remove a project directory.
func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	dir, ok := s.projectDir(w, r)
	if !ok {
		return
	}
	lock := projectfs.Lock(s.projectsDir)
	lock.Lock()
	defer lock.Unlock()
	if _, err := os.Stat(dir); err != nil {
		httpErr(w, http.StatusNotFound, "project not found")
		return
	}
	if err := os.RemoveAll(dir); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "project.delete", filepath.Base(dir), "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// projectDir validates the {name} param and returns the on-disk dir. It writes
// the error response and returns ok=false when invalid/disabled.
func (s *Server) projectDir(w http.ResponseWriter, r *http.Request) (string, bool) {
	if s.projectsDir == "" {
		httpErr(w, http.StatusServiceUnavailable, "project uploads are disabled (no projects dir configured)")
		return "", false
	}
	name := r.PathValue("name")
	if !validProjectName(name) {
		httpErr(w, http.StatusBadRequest, "invalid project name")
		return "", false
	}
	return filepath.Join(s.projectsDir, name), true
}

// withinDir reports whether target resolves to a path inside base.
func withinDir(base, target string) bool {
	rel, err := filepath.Rel(base, filepath.Clean(target))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// projectWarnings flags tasks whose `project` names something missing or
// unconfigured — a DAG can parse fine yet fail at run time because the referenced
// project was never uploaded. validate surfaces these so an author (esp. an AI)
// gets the signal up front instead of at the first run.
func (s *Server) projectWarnings(tasks []model.Task) []string {
	var warns []string
	for _, t := range tasks {
		if t.Project == "" {
			continue
		}
		switch {
		case !validProjectName(t.Project):
			warns = append(warns, fmt.Sprintf("task %q: invalid project name %q", t.ID, t.Project))
		case s.projectsDir == "":
			warns = append(warns, fmt.Sprintf("task %q references project %q, but this server has no projects dir configured", t.ID, t.Project))
		default:
			if fi, err := os.Stat(filepath.Join(s.projectsDir, t.Project)); err != nil || !fi.IsDir() {
				warns = append(warns, fmt.Sprintf("task %q references project %q which is not uploaded yet", t.ID, t.Project))
			}
		}
	}
	return warns
}

// dirStats returns the file count and total byte size of a tree (regular files).
func dirStats(dir string) (files int, size int64) {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			files++
			size += info.Size()
		}
		return nil
	})
	return files, size
}
