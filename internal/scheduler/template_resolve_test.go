package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/model"
)

// TestRenderCommandDotted checks the pure resolver: base vars, params.*, and
// unknown dotted names are left intact (not blanked).
func TestRenderCommandDotted(t *testing.T) {
	m := map[string]string{"logical_date": "2026-06-09", "params.day": "mon", "var.host": "db1"}
	resolve := func(k string) (string, bool) { v, ok := m[k]; return v, ok }
	got := renderCommand("d={{ logical_date }} p={{ params.day }} h={{ var.host }} miss={{ var.nope }}", resolve)
	want := "d=2026-06-09 p=mon h=db1 miss={{ var.nope }}"
	if got != want {
		t.Errorf("renderCommand = %q, want %q", got, want)
	}
}

func TestConnField(t *testing.T) {
	// extra mixes a string and a number — a non-string value must not poison the
	// lookup of sibling string keys, and scalars resolve to their literal text.
	c := &model.Connection{ID: "mysql", Type: "mysql", Host: "h", Port: 3306, Login: "u", Password: "p", Extra: `{"schema":"prod","timeout":30,"tls":true}`}
	cases := map[string]string{"host": "h", "port": "3306", "login": "u", "user": "u", "password": "p", "type": "mysql", "extra.schema": "prod", "extra.timeout": "30", "extra.tls": "true"}
	for field, want := range cases {
		if got, ok := connField(c, field); !ok || got != want {
			t.Errorf("connField(%q) = %q,%v want %q", field, got, ok, want)
		}
	}
	if _, ok := connField(c, "nope"); ok {
		t.Error("unknown field should be not-found")
	}
	if _, ok := connField(c, "extra.missing"); ok {
		t.Error("missing extra key should be not-found")
	}
}

// TestTemplateResolveEndToEnd runs a real task whose command references a
// variable, a connection field, and a trigger param — the whole pipeline.
func TestTemplateResolveEndToEnd(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	if err := s.store.UpsertVariable(ctx, &model.Variable{Key: "greeting", Value: "hola"}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.UpsertConnection(ctx, &model.Connection{ID: "db", Host: "dbhost", Port: 5432, Password: "s3cret"}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out")
	dag := &model.DAG{
		DagID: "tvars", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{{
			ID:      "t",
			Command: "echo v={{ var.greeting }} h={{ conn.db.host }} pw={{ conn.db.password }} p={{ params.who }} > " + out,
			Pool:    model.DefaultPoolName,
		}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, err := s.TriggerManual(ctx, "tvars", map[string]string{"who": "world"})
	if err != nil {
		t.Fatal(err)
	}
	if run := s.driveToTerminal(t, ctx, runID, 20); run.State != model.RunSuccess {
		t.Fatalf("run = %s, want success", run.State)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "v=hola h=dbhost pw=s3cret p=world\n"; got != want {
		t.Errorf("resolved command output = %q, want %q", got, want)
	}
	// and the run persisted its params
	got, err := s.store.GetDagRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Params["who"] != "world" {
		t.Errorf("run params = %v, want who=world", got.Params)
	}
}
