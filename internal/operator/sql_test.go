package operator

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zoyluo/cronova/internal/model"
)

func TestRunSQLSqlite(t *testing.T) {
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "t.db")
	run := func(q string) (string, int) {
		var out bytes.Buffer
		code := RunSQL(ctx, SQLSpec{Driver: "sqlite", DSN: dsn, Query: q}, &out)
		return out.String(), code
	}

	if _, code := run("CREATE TABLE x(a INTEGER, b TEXT)"); code != 0 {
		t.Fatalf("create exit = %d", code)
	}
	if log, code := run("INSERT INTO x VALUES (1,'one'),(2,'two')"); code != 0 || !strings.Contains(log, "2 rows affected") {
		t.Fatalf("insert exit=%d log=%q", code, log)
	}
	log, code := run("SELECT a, b FROM x ORDER BY a")
	if code != 0 {
		t.Fatalf("select exit = %d; log:\n%s", code, log)
	}
	for _, want := range []string{"a\tb", "1\tone", "2\ttwo", "(2 rows)"} {
		if !strings.Contains(log, want) {
			t.Errorf("select log missing %q:\n%s", want, log)
		}
	}
	// a bad query is a task failure (exit 1), reported in the log
	if log, code := run("SELECT * FROM nope"); code != 1 || !strings.Contains(log, "sql:") {
		t.Fatalf("bad query exit=%d log=%q", code, log)
	}
	// a dispatch-time error (carried in spec.Err) fails cleanly
	var out bytes.Buffer
	if code := RunSQL(ctx, SQLSpec{Err: "connection \"db\" not found"}, &out); code != 1 || !strings.Contains(out.String(), "not found") {
		t.Fatalf("err spec exit=%d out=%q", code, out.String())
	}
}

func TestLeadingVerb(t *testing.T) {
	cases := map[string]string{
		"SELECT 1":                      "SELECT",
		"  select 1":                    "SELECT",
		"-- note\nSELECT 1":             "SELECT",
		"/* c */ SELECT 1":              "SELECT",
		"/* c */SELECT 1":               "SELECT",
		"(SELECT 1) UNION (SELECT 2)":   "SELECT",
		"-- x\nWITH c AS (SELECT 1)...": "WITH",
		"INSERT INTO t VALUES (1)":      "INSERT",
		"update t set a=1":              "UPDATE",
		"":                              "",
	}
	for q, want := range cases {
		if got := leadingVerb(q); got != want {
			t.Errorf("leadingVerb(%q) = %q, want %q", q, got, want)
		}
	}
}

// TestRunSQLCommentedSelect: a comment/paren-prefixed SELECT must return its rows,
// not be misrouted to Exec (which silently discards the result set).
func TestRunSQLCommentedSelect(t *testing.T) {
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "c.db")
	base := SQLSpec{Driver: "sqlite", DSN: dsn}
	run := func(q string) string {
		var out bytes.Buffer
		s := base
		s.Query = q
		RunSQL(ctx, s, &out)
		return out.String()
	}
	run("CREATE TABLE t(a INTEGER)")
	run("INSERT INTO t VALUES (7),(8)")
	for _, q := range []string{"-- daily\nSELECT a FROM t ORDER BY a", "/* note */ SELECT a FROM t ORDER BY a"} {
		log := run(q)
		if strings.Contains(log, "rows affected") || !strings.Contains(log, "(2 rows)") || !strings.Contains(log, "7") {
			t.Fatalf("commented SELECT misrouted (result discarded) for %q:\n%s", q, log)
		}
	}
}

func TestBuildDSN(t *testing.T) {
	pgDrv, pgDSN, err := BuildDSN(&model.Connection{Type: "postgres", Host: "h", Port: 5432, Login: "u", Password: "p@ss", Extra: `{"database":"db","sslmode":"disable"}`})
	if err != nil || pgDrv != "pgx" {
		t.Fatalf("pg driver=%q err=%v", pgDrv, err)
	}
	// password special char must be percent-encoded, db + sslmode present
	if !strings.Contains(pgDSN, "u:p%40ss@h:5432/db") || !strings.Contains(pgDSN, "sslmode=disable") {
		t.Fatalf("pg dsn = %q", pgDSN)
	}
	myDrv, myDSN, err := BuildDSN(&model.Connection{Type: "mysql", Host: "h", Port: 3306, Login: "u", Password: "p", Extra: `{"database":"db"}`})
	if err != nil || myDrv != "mysql" || !strings.Contains(myDSN, "@tcp(h:3306)/db") {
		t.Fatalf("mysql driver=%q dsn=%q err=%v", myDrv, myDSN, err)
	}
	liteDrv, liteDSN, err := BuildDSN(&model.Connection{Type: "sqlite", Host: "/data/app.db"})
	if err != nil || liteDrv != "sqlite" || liteDSN != "/data/app.db" {
		t.Fatalf("sqlite driver=%q dsn=%q err=%v", liteDrv, liteDSN, err)
	}
	if _, _, err := BuildDSN(&model.Connection{Type: "oracle"}); err == nil {
		t.Fatal("unsupported type should error")
	}
}
