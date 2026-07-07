package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/executor"
	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/store/sqlite"
)

// newProjectAt writes a small project tree under projectsDir/<name> and returns
// the projects dir. main.py is executable; there's a nested file too.
func newProjectAt(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	proj := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(proj, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "main.py"), []byte("print('hi')\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "pkg", "util.py"), []byte("X = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestStageProject(t *testing.T) {
	projects := newProjectAt(t, "myproj")
	s := &Scheduler{opts: Options{ProjectsDir: projects, WorkspaceDir: t.TempDir()}}

	ws, err := s.stageProject("myproj", "run_1/task_1")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(ws)

	// files copied, content intact
	if b, err := os.ReadFile(filepath.Join(ws, "main.py")); err != nil || string(b) != "print('hi')\n" {
		t.Fatalf("main.py = %q, err=%v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(ws, "pkg", "util.py")); err != nil || string(b) != "X = 1\n" {
		t.Fatalf("pkg/util.py = %q, err=%v", b, err)
	}
	// executable bit preserved
	if fi, err := os.Stat(filepath.Join(ws, "main.py")); err != nil || fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("main.py should stay executable, mode=%v err=%v", fi.Mode(), err)
	}
	// workspace is under the temp workspace root, not the projects dir
	if filepath.Dir(filepath.Dir(ws)) == projects {
		t.Errorf("workspace %s should not be inside projects dir", ws)
	}
}

func TestStageProjectFreshCopyPerAttempt(t *testing.T) {
	projects := newProjectAt(t, "p")
	s := &Scheduler{opts: Options{ProjectsDir: projects, WorkspaceDir: t.TempDir()}}

	ws1, err := s.stageProject("p", "run_1/task_1")
	if err != nil {
		t.Fatal(err)
	}
	// a task that dirtied its workspace must not leak into the next attempt
	if err := os.WriteFile(filepath.Join(ws1, "scratch.txt"), []byte("dirty"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws2, err := s.stageProject("p", "run_1/task_1") // same ref => same path, re-staged clean
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(ws2)
	if ws1 != ws2 {
		t.Fatalf("same ref should map to same workspace: %s vs %s", ws1, ws2)
	}
	if _, err := os.Stat(filepath.Join(ws2, "scratch.txt")); !os.IsNotExist(err) {
		t.Error("re-stage should have wiped the previous attempt's scratch file")
	}
}

func TestStageProjectRejectsBadNames(t *testing.T) {
	s := &Scheduler{opts: Options{ProjectsDir: t.TempDir()}}
	for _, bad := range []string{"", ".", "..", "../etc", "a/b", "foo/../bar", "/abs", "a b"} {
		if _, err := s.stageProject(bad, "r/t"); err == nil {
			t.Errorf("stageProject(%q) should be rejected", bad)
		}
	}
}

func TestStageProjectMissing(t *testing.T) {
	s := &Scheduler{opts: Options{ProjectsDir: t.TempDir()}}
	if _, err := s.stageProject("nope", "r/t"); err == nil {
		t.Error("staging a nonexistent project should error")
	}
}

func TestStageProjectNoDirConfigured(t *testing.T) {
	s := &Scheduler{opts: Options{ProjectsDir: ""}}
	if _, err := s.stageProject("x", "r/t"); err == nil {
		t.Error("staging with no projects dir should error")
	}
}

// TestProjectAttachRunsInWorkspace is the M1 end-to-end proof: a shell task with
// Project set runs with cwd = a staged copy of the project. The command reads a
// file that exists ONLY inside the project, so a successful run proves both that
// the files were staged and that cwd points at them.
func TestProjectAttachRunsInWorkspace(t *testing.T) {
	projects := t.TempDir()
	proj := filepath.Join(projects, "hello")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "marker.txt"), []byte("HELLO_FROM_PROJECT\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := sqlite.New(filepath.Join(t.TempDir(), "sched.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := New(st, executor.NewLocal(), Options{
		LogDir:       filepath.Join(t.TempDir(), "logs"),
		WorkspaceDir: t.TempDir(),
		ProjectsDir:  projects,
		Tick:         10 * time.Millisecond,
		PollInterval: 5 * time.Millisecond,
	})

	ctx := context.Background()
	dag := &model.DAG{
		DagID: "proj", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		// cat succeeds only if cwd is the staged copy; also assert the env var is set.
		Tasks: []model.Task{{
			ID:          "run",
			Command:     `cat marker.txt && test -d "$CRONOVA_PROJECT_DIR"`,
			Project:     "hello",
			Pool:        model.DefaultPoolName,
			TriggerRule: model.RuleAllSuccess,
		}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, err := s.TriggerManual(ctx, "proj", nil)
	if err != nil {
		t.Fatal(err)
	}
	run := s.driveToTerminal(t, ctx, runID, 60)
	if run.State != model.RunSuccess {
		t.Fatalf("project-attached run = %s, want success (cwd should be the staged copy)", run.State)
	}
}

// TestProjectAttachMissingProjectFails: referencing an absent project fails the
// task cleanly (staging error), not a phantom success.
func TestProjectAttachMissingProjectFails(t *testing.T) {
	st, err := sqlite.New(filepath.Join(t.TempDir(), "sched.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := New(st, executor.NewLocal(), Options{
		LogDir:       filepath.Join(t.TempDir(), "logs"),
		WorkspaceDir: t.TempDir(),
		ProjectsDir:  t.TempDir(), // empty projects dir
		Tick:         10 * time.Millisecond,
		PollInterval: 5 * time.Millisecond,
	})
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "proj2", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{{ID: "run", Command: "echo hi", Project: "does-not-exist", Pool: model.DefaultPoolName, TriggerRule: model.RuleAllSuccess}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, err := s.TriggerManual(ctx, "proj2", nil)
	if err != nil {
		t.Fatal(err)
	}
	run := s.driveToTerminal(t, ctx, runID, 60)
	if run.State != model.RunFailed {
		t.Fatalf("run with a missing project = %s, want failed", run.State)
	}
}

// TestGCWorkspaces: orphaned workspaces are swept; a running task's workspace
// survives; the age guard protects freshly-staged dirs during a periodic sweep.
func TestGCWorkspaces(t *testing.T) {
	st, err := sqlite.New(filepath.Join(t.TempDir(), "gc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	wsRoot := t.TempDir()
	s := New(st, executor.NewLocal(), Options{
		LogDir:       filepath.Join(t.TempDir(), "logs"),
		WorkspaceDir: wsRoot,
	})

	// A run + a RUNNING task instance whose ref owns one workspace.
	if err := st.UpsertDAG(ctx, &model.DAG{DagID: "g", DefinitionYAML: "dag_id: g\ntasks:\n  - id: a\n    command: x\n", MaxActiveRuns: 1, StartDate: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDagRun(ctx, &model.DagRun{RunID: "g__r1", DagID: "g", LogicalDate: time.Now().UTC(), State: model.RunRunning, TriggerType: model.TriggerManual}); err != nil {
		t.Fatal(err)
	}
	ti := &model.TaskInstance{RunID: "g__r1", TaskID: "a", State: model.TaskRunning, Pool: "default", ExecutorRef: "g__r1/a", LogPath: "x.log"}
	if err := st.CreateTaskInstance(ctx, ti); err != nil {
		t.Fatal(err)
	}
	// A queued task WITH a ref: launched but its "mark running" write may not have
	// landed — its workspace must also be protected.
	qti := &model.TaskInstance{RunID: "g__r1", TaskID: "q", State: model.TaskQueued, Pool: "default", ExecutorRef: "g__r1/q", LogPath: "q.log"}
	if err := st.CreateTaskInstance(ctx, qti); err != nil {
		t.Fatal(err)
	}

	mk := func(name string) string {
		d := filepath.Join(wsRoot, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		return d
	}
	live := mk(sanitizeRef("g__r1/a"))   // owned by the running task
	queued := mk(sanitizeRef("g__r1/q")) // owned by a queued-with-ref (launch window)
	orphan := mk("g__r0-a")              // stale leftover
	fresh := mk("g__r2-b")               // just staged, task not yet marked running

	// Boot sweep (no age guard): orphan AND fresh go (dispatch hasn't started),
	// live stays.
	s.gcWorkspaces(ctx, 0)
	if _, err := os.Stat(live); err != nil {
		t.Error("running task's workspace must survive gc")
	}
	if _, err := os.Stat(queued); err != nil {
		t.Error("queued-with-ref task's workspace must survive gc (launch window)")
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Error("orphan workspace should be removed")
	}
	if _, err := os.Stat(fresh); !os.IsNotExist(err) {
		t.Error("boot sweep removes everything unowned")
	}

	// Periodic sweep (age guard): a fresh unowned dir is kept, an old one goes.
	fresh2 := mk("g__r3-c")
	old := mk("g__r4-d")
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	s.gcWorkspaces(ctx, time.Hour)
	if _, err := os.Stat(fresh2); err != nil {
		t.Error("age guard must keep a freshly-staged workspace")
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("aged orphan should be removed by the periodic sweep")
	}
	if _, err := os.Stat(live); err != nil {
		t.Error("running task's workspace must still survive")
	}
}

func TestSanitizeRef(t *testing.T) {
	cases := map[string]string{
		"run_1/task_1":    "run_1-task_1",
		"a/b/c":           "a-b-c",
		"weird:ref name!": "weird-ref-name-",
		"keep_-azAZ09":    "keep_-azAZ09",
	}
	for in, want := range cases {
		if got := sanitizeRef(in); got != want {
			t.Errorf("sanitizeRef(%q) = %q, want %q", in, got, want)
		}
	}
}
