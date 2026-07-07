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
		if !e.IsDir() {
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

	projDir := filepath.Join(s.projectsDir, name)
	existing := int64(0)
	if _, sz := dirStats(projDir); sz > 0 {
		existing = sz
	}

	var files []*multipart.FileHeader
	var paths []string // optional, parallel to files: project-root-relative paths
	if r.MultipartForm != nil {
		files = r.MultipartForm.File["file"]
		paths = r.MultipartForm.Value["path"]
	}

	// Zip archive: a single *.zip part is extracted rather than stored verbatim.
	if len(files) == 1 && isZipName(files[0].Filename) {
		n, err := s.extractZipUpload(projDir, files[0], existing)
		if err != nil {
			writeUploadErr(w, err)
			return
		}
		s.audit(r, "project.upload", name+" (zip)", fmt.Sprintf("%d files", n))
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "project": name, "files": n})
		return
	}

	// One or more file parts (single file or folder), else an inline script.
	var written int
	if len(files) > 0 {
		var incoming int64
		for _, fh := range files {
			incoming += fh.Size
			if fh.Size > maxProjectFileSize {
				httpErr(w, http.StatusRequestEntityTooLarge, "a file exceeds the size limit")
				return
			}
		}
		if existing+incoming > maxProjectSize {
			httpErr(w, http.StatusRequestEntityTooLarge, "project exceeds total size limit")
			return
		}
		if err := os.MkdirAll(projDir, 0o755); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		for i, fh := range files {
			// The multipart layer strips directories from fh.Filename (a security
			// default), so a folder upload carries each file's project-relative
			// path in a parallel `path` field. Fall back to the (base) filename.
			rel := fh.Filename
			if i < len(paths) && paths[i] != "" {
				rel = paths[i]
			}
			f, err := fh.Open()
			if err != nil {
				httpErr(w, http.StatusInternalServerError, "read upload failed")
				return
			}
			err = writeProjectFile(projDir, rel, f)
			_ = f.Close()
			if err != nil {
				writeUploadErr(w, err)
				return
			}
			written++
		}
	} else {
		filename := filepath.Base(r.FormValue("filename"))
		if !safeFileName(filename) {
			httpErr(w, http.StatusBadRequest, "invalid or missing filename")
			return
		}
		content := r.FormValue("content")
		if existing+int64(len(content)) > maxProjectSize {
			httpErr(w, http.StatusRequestEntityTooLarge, "project exceeds total size limit")
			return
		}
		if err := os.MkdirAll(projDir, 0o755); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := writeProjectFile(projDir, filename, strings.NewReader(content)); err != nil {
			writeUploadErr(w, err)
			return
		}
		written = 1
	}

	s.audit(r, "project.upload", name, fmt.Sprintf("%d file(s)", written))
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
func (s *Server) extractZipUpload(projDir string, fh *multipart.FileHeader, existing int64) (int, error) {
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
	if existing+total > maxProjectSize {
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

// DELETE /api/projects/{name} — remove a project directory.
func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	dir, ok := s.projectDir(w, r)
	if !ok {
		return
	}
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
