package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/store/sqlite"
)

// projectsServer builds a handler with project uploads enabled (auth off) and
// returns it plus the on-disk projects dir.
func projectsServer(t *testing.T) (http.Handler, string) {
	t.Helper()
	st, err := sqlite.New(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	projDir := t.TempDir()
	srv := New(st, &stubTrigger{}, t.TempDir(), nil, Info{})
	srv.SetProjectsDir(projDir)
	return srv.Handler(), projDir
}

func postFile(t *testing.T, h http.Handler, project, filename, content string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write([]byte(content))
	mw.Close()
	req := httptest.NewRequest("POST", "/api/projects/"+project, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func postInline(t *testing.T, h http.Handler, project, filename, content string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("filename", filename)
	mw.WriteField("content", content)
	mw.Close()
	req := httptest.NewRequest("POST", "/api/projects/"+project, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestUploadSingleFile(t *testing.T) {
	h, projDir := projectsServer(t)
	rec := postFile(t, h, "myproj", "main.py", "print('hi')\n")
	if rec.Code != http.StatusOK {
		t.Fatalf("upload = %d, body=%s", rec.Code, rec.Body)
	}
	got, err := os.ReadFile(filepath.Join(projDir, "myproj", "main.py"))
	if err != nil || string(got) != "print('hi')\n" {
		t.Fatalf("stored file = %q, err=%v", got, err)
	}
	// executable bit set so `./main.py` works too
	if fi, _ := os.Stat(filepath.Join(projDir, "myproj", "main.py")); fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("uploaded file should be executable, mode=%v", fi.Mode())
	}
}

func TestUploadInline(t *testing.T) {
	h, projDir := projectsServer(t)
	rec := postInline(t, h, "snip", "run.sh", "echo hello\n")
	if rec.Code != http.StatusOK {
		t.Fatalf("inline upload = %d, body=%s", rec.Code, rec.Body)
	}
	got, _ := os.ReadFile(filepath.Join(projDir, "snip", "run.sh"))
	if string(got) != "echo hello\n" {
		t.Fatalf("stored inline = %q", got)
	}
}

func TestListAndGetAndDeleteProject(t *testing.T) {
	h, _ := projectsServer(t)
	postFile(t, h, "p", "a.py", "A")
	postFile(t, h, "p", "b.py", "BB")

	rec, body := get(t, h, "GET", "/api/projects")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d", rec.Code)
	}
	arr, _ := body.([]any)
	if len(arr) != 1 {
		t.Fatalf("expected 1 project, got %v", body)
	}
	if m, _ := arr[0].(map[string]any); m["name"] != "p" || m["files"].(float64) != 2 {
		t.Errorf("project info = %v, want name=p files=2", m)
	}

	rec, body = get(t, h, "GET", "/api/projects/p")
	if rec.Code != http.StatusOK {
		t.Fatalf("getProject = %d", rec.Code)
	}
	if m, _ := body.(map[string]any); len(m["files"].([]any)) != 2 {
		t.Errorf("expected 2 files, got %v", body)
	}

	rec, _ = get(t, h, "DELETE", "/api/projects/p")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete = %d", rec.Code)
	}
	rec, _ = get(t, h, "GET", "/api/projects/p")
	if rec.Code != http.StatusNotFound {
		t.Errorf("after delete, get = %d, want 404", rec.Code)
	}
}

func TestUploadRejectsBadProjectName(t *testing.T) {
	h, _ := projectsServer(t)
	// URL-safe path segments that still fail the name rule reach the handler and
	// must be rejected with 400. (Traversal like ".." / "a/b" is stopped earlier
	// by the router — it never reaches the handler.)
	for _, bad := range []string{"a~b", "a:b", "a,b", "a;b"} {
		rec := postFile(t, h, bad, "x.py", "x")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("project name %q -> %d, want 400", bad, rec.Code)
		}
	}
}

func TestUploadFilenameCannotEscape(t *testing.T) {
	h, projDir := projectsServer(t)
	// a traversal filename is reduced to its base name and stays inside the project
	rec := postFile(t, h, "p", "../../evil.py", "x")
	if rec.Code != http.StatusOK {
		t.Fatalf("upload = %d, body=%s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(projDir, "p", "evil.py")); err != nil {
		t.Errorf("file should be stored as basename inside the project: %v", err)
	}
	// nothing was written outside the projects dir
	if _, err := os.Stat(filepath.Join(projDir, "evil.py")); err == nil {
		t.Error("traversal escaped the project directory")
	}
}

func TestUploadTooLarge(t *testing.T) {
	h, _ := projectsServer(t)
	big := strings.Repeat("x", maxProjectFileSize+1024)
	rec := postFile(t, h, "p", "big.bin", big)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize upload = %d, want 413", rec.Code)
	}
}

// postFiles uploads many file parts, each key a project-relative path. It sends a
// parallel `path` field per file (as the browser folder-upload flow does), since
// the multipart layer strips directories from the filename itself.
func postFiles(t *testing.T, h http.Handler, project string, files map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for relpath, content := range files {
		fw, err := mw.CreateFormFile("file", filepath.Base(relpath))
		if err != nil {
			t.Fatal(err)
		}
		fw.Write([]byte(content))
		mw.WriteField("path", relpath) // parallel path, matched by index
	}
	mw.Close()
	req := httptest.NewRequest("POST", "/api/projects/"+project, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// postZip uploads a single .zip part built from entries (name -> content).
func postZip(t *testing.T, h http.Handler, project string, entries map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	for name, content := range entries {
		fw, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		fw.Write([]byte(content))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "archive.zip")
	fw.Write(zbuf.Bytes())
	mw.Close()
	req := httptest.NewRequest("POST", "/api/projects/"+project, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestUploadFolder(t *testing.T) {
	h, projDir := projectsServer(t)
	rec := postFiles(t, h, "app", map[string]string{
		"main.py":       "print('hi')\n",
		"pkg/util.py":   "X = 1\n",
		"data/seed.txt": "seed\n",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("folder upload = %d, body=%s", rec.Code, rec.Body)
	}
	for rel, want := range map[string]string{"main.py": "print('hi')\n", "pkg/util.py": "X = 1\n", "data/seed.txt": "seed\n"} {
		got, err := os.ReadFile(filepath.Join(projDir, "app", filepath.FromSlash(rel)))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, err=%v", rel, got, err)
		}
	}
}

func TestUploadZipExtracts(t *testing.T) {
	h, projDir := projectsServer(t)
	rec := postZip(t, h, "z", map[string]string{
		"main.py":      "print(1)\n",
		"sub/hello.py": "print(2)\n",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("zip upload = %d, body=%s", rec.Code, rec.Body)
	}
	if b, _ := os.ReadFile(filepath.Join(projDir, "z", "main.py")); string(b) != "print(1)\n" {
		t.Errorf("main.py = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(projDir, "z", "sub", "hello.py")); string(b) != "print(2)\n" {
		t.Errorf("sub/hello.py = %q", b)
	}
}

func TestUploadZipSlipRejected(t *testing.T) {
	h, projDir := projectsServer(t)
	rec := postZip(t, h, "z", map[string]string{
		"ok.py":            "1\n",
		"../../escape.txt": "pwned\n", // zip-slip attempt
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("zip-slip upload = %d, want 400", rec.Code)
	}
	// nothing escaped the projects dir
	for _, p := range []string{
		filepath.Join(projDir, "escape.txt"),
		filepath.Join(filepath.Dir(projDir), "escape.txt"),
	} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("zip-slip escaped to %s", p)
		}
	}
}

func TestUploadFolderTraversalRejected(t *testing.T) {
	h, projDir := projectsServer(t)
	rec := postFiles(t, h, "app", map[string]string{"../evil.py": "x"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("traversal folder upload = %d, want 400", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(projDir), "evil.py")); err == nil {
		t.Error("traversal escaped the projects dir")
	}
}

func TestUploadFolderFailureLeavesExistingProjectUntouched(t *testing.T) {
	h, projDir := projectsServer(t)
	if rec := postFile(t, h, "app", "stable.txt", "old"); rec.Code != http.StatusOK {
		t.Fatalf("seed upload = %d", rec.Code)
	}
	rec := postFiles(t, h, "app", map[string]string{
		"stable.txt": "new",
		"../bad.txt": "must fail",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("mixed upload = %d, want 400; body=%s", rec.Code, rec.Body)
	}
	got, err := os.ReadFile(filepath.Join(projDir, "app", "stable.txt"))
	if err != nil || string(got) != "old" {
		t.Fatalf("committed project changed after failed upload: got=%q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(projDir, "app", "bad.txt")); !os.IsNotExist(err) {
		t.Fatalf("failed upload left bad.txt behind: %v", err)
	}
}

// TestSpecToYAMLCarriesProject: the build path (UI spec -> canonical YAML) must
// keep a shell task's project. Regression for the hand-rolled spec structs that
// silently dropped it.
func TestSpecToYAMLCarriesProject(t *testing.T) {
	yml, err := specToYAML(dagSpec{
		DagID: "p", MaxActiveRuns: 1,
		Tasks: []taskSpec{{ID: "run", Type: "shell", Command: "python3 main.py", Project: "my_app"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(yml), "project: my_app") {
		t.Fatalf("specToYAML dropped project:\n%s", yml)
	}
}

// TestGetDAGCarriesProject: the read path (stored YAML -> editor JSON) must return
// the task's project so the console can show it selected.
func TestGetDAGCarriesProject(t *testing.T) {
	h, st, _, _ := setup(t)
	yaml := "dag_id: pr\ntasks:\n  - id: run\n    type: shell\n    command: \"python3 main.py\"\n    project: my_app\n"
	if err := st.UpsertDAG(context.Background(), &model.DAG{DagID: "pr", DefinitionYAML: yaml, MaxActiveRuns: 1, StartDate: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	_, body := get(t, h, "GET", "/api/dags/pr")
	tasks := body.(map[string]any)["tasks"].([]any)
	if m := tasks[0].(map[string]any); m["project"] != "my_app" {
		t.Fatalf("getDAG task project = %v, want my_app", m["project"])
	}
}

// TestValidateFlagsMissingProject: a DAG that references an un-uploaded project
// still parses (valid=true) but validate warns about it, so an author/AI knows
// before the first run.
func TestValidateFlagsMissingProject(t *testing.T) {
	h, projDir := projectsServer(t)
	if err := os.MkdirAll(filepath.Join(projDir, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"dag_id":"d","start_date":"2026-01-01","tasks":[
		{"id":"a","type":"shell","command":"echo hi","project":"real"},
		{"id":"b","type":"shell","command":"echo hi","project":"ghost"}]}`
	req := httptest.NewRequest("POST", "/api/dags/validate", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("bad response: %v (%s)", err, rec.Body)
	}
	if m["valid"] != true {
		t.Fatalf("valid = %v, want true (structure is fine): %s", m["valid"], rec.Body)
	}
	warns, _ := m["warnings"].([]any)
	joined := fmt.Sprint(warns...)
	if !strings.Contains(joined, "ghost") {
		t.Errorf("expected a warning about the missing project 'ghost', got %v", warns)
	}
	if strings.Contains(joined, `"real"`) || strings.Contains(joined, "project \"real\"") {
		t.Errorf("the existing project 'real' should not warn: %v", warns)
	}
}

func TestUploadDisabledWhenNoProjectsDir(t *testing.T) {
	st, _ := sqlite.New(filepath.Join(t.TempDir(), "p.db"))
	t.Cleanup(func() { _ = st.Close() })
	_ = st.Migrate(context.Background())
	h := New(st, &stubTrigger{}, t.TempDir(), nil, Info{}).Handler() // no SetProjectsDir
	rec := postFile(t, h, "p", "a.py", "x")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("upload with no projects dir = %d, want 503", rec.Code)
	}
	rec, _ = get(t, h, "GET", "/api/projects")
	if rec.Code != http.StatusOK {
		t.Errorf("list with no projects dir should be 200 empty, got %d", rec.Code)
	}
}
