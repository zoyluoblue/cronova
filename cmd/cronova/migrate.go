package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/zoyluo/cronova/internal/store/postgres"
	"github.com/zoyluo/cronova/internal/store/sqlite"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// cmdMigrateStore moves a single-node SQLite metadata store into PostgreSQL —
// the plan4 R18 "single → cluster" data relocation, with the plan5 §9
// dependency-closure verification: per-table counts must match exactly, and
// orphaned references (runs without their DAG, task instances without their
// run) must be zero on BOTH sides before the migration reports success.
//
//	cronova migrate-store -from data/cronova.db -to postgres://user:pw@host/db
//
// Preconditions (refused otherwise): the source has no active (queued or
// running) runs — stop `cronova serve` and drain first; live migration of
// in-flight work is out of scope by design. The target database must be empty
// (its cronova tables contain no rows) so a half-migrated target cannot be
// silently merged into. Sessions and one-time join tokens are NOT copied:
// sessions are ephemeral logins and join tokens are single-use bootstrap
// credentials — both are invalid in a new incarnation on purpose.
func cmdMigrateStore(args []string) error {
	fs := flag.NewFlagSet("migrate-store", flag.ExitOnError)
	from := fs.String("from", "data/cronova.db", "source SQLite database path")
	to := fs.String("to", "", "target PostgreSQL DSN (postgres://...)")
	report := fs.String("report", "", "write the dependency-closure record JSON here (default: stdout)")
	_ = fs.Parse(args)
	if *to == "" || !strings.HasPrefix(*to, "postgres") {
		return fmt.Errorf("usage: cronova migrate-store -from <sqlite.db> -to postgres://...")
	}
	if _, err := os.Stat(*from); err != nil {
		return fmt.Errorf("source database: %w", err)
	}
	ctx := context.Background()

	// Normalize the source to the CURRENT schema first (the ALTER ladder runs
	// on store open) — a raw copy from a pre-upgrade file would miss columns.
	srcStore, err := sqlite.New(*from)
	if err != nil {
		return err
	}
	if err := srcStore.Migrate(ctx); err != nil {
		srcStore.Close()
		return fmt.Errorf("normalize source schema: %w", err)
	}
	srcStore.Close()

	src, err := sql.Open("sqlite", *from+"?_time_format=sqlite")
	if err != nil {
		return err
	}
	defer src.Close()

	// Preflight 1: no active runs on the source.
	var active int
	if err := src.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM dag_runs WHERE state IN ('queued','running')`).Scan(&active); err != nil {
		return fmt.Errorf("source preflight: %w (is this a cronova database?)", err)
	}
	if active > 0 {
		return fmt.Errorf("source has %d active (queued/running) runs — stop the scheduler and let them finish or cancel them first", active)
	}

	dst, err := sql.Open("pgx", *to)
	if err != nil {
		return err
	}
	defer dst.Close()

	// tables lists what is copied, in foreign-key order. Column lists are the
	// shared portable schema (both stores are column-parallel by design).
	type table struct {
		name string
		cols []string
	}
	tables := []table{
		{"dags", []string{"dag_id", "schedule", "timezone", "start_date", "catchup", "paused", "max_active_runs", "definition_yaml", "owner", "project", "created_at", "updated_at", "deleted_at"}},
		{"dag_runs", []string{"run_id", "dag_id", "logical_date", "state", "trigger_type", "started_at", "finished_at", "params", "definition_yaml", "definition_hash", "priority", "parent_run_id", "held"}},
		{"task_instances", []string{"run_id", "task_id", "state", "try_number", "max_retries", "pool", "priority", "definition_hash", "executor_ref", "log_path", "started_at", "finished_at"}},
		{"pools", []string{"name", "slots"}},
		{"dag_dependencies", []string{"upstream_dag", "downstream_dag"}},
		{"events", []string{"source", "event_key", "payload", "consumed", "created_at"}},
		{"audit_log", []string{"ts", "actor", "action", "target", "detail"}},
		{"api_tokens", []string{"name", "role", "token_hash", "prefix", "created_at", "last_used_at", "expires_at", "dag_id"}},
		{"users", []string{"username", "password_hash", "role", "created_at", "updated_at"}},
		{"variables", []string{"key", "value", "updated_at"}},
		{"connections", []string{"id", "type", "host", "port", "login", "password", "extra", "updated_at"}},
		{"alert_groups", []string{"name", "channels", "updated_at"}},
		{"workers", []string{"worker_id", "name", "labels", "state", "draining", "version", "active_tasks", "last_heartbeat", "created_at"}},
		{"dag_versions", []string{"dag_id", "hash", "yaml", "ts"}},
		{"task_outputs", []string{"run_id", "task_id", "output"}},
		{"dag_hooks", []string{"dag_id", "secret_hash", "prefix", "created_at"}},
	}

	// Preflight 2: target must be empty — checked BEFORE the schema is
	// created, so a table that does not exist yet counts as empty and the
	// store's own seeding (the default pool) cannot trip the check.
	for _, tb := range tables {
		var n int
		err := dst.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+tb.name).Scan(&n)
		if err != nil {
			if strings.Contains(err.Error(), "does not exist") {
				continue // fresh database — fine
			}
			return fmt.Errorf("target preflight %s: %w", tb.name, err)
		}
		if n > 0 {
			return fmt.Errorf("target table %s already has %d rows — migrate into an EMPTY database (merging is refused by design)", tb.name, n)
		}
	}

	// Now create the schema via the real store's migration (also seeds the
	// default pool — absorbed below by the pools upsert).
	pgStore, err := postgres.New(*to)
	if err != nil {
		return err
	}
	if err := pgStore.Migrate(ctx); err != nil {
		pgStore.Close()
		return fmt.Errorf("prepare target schema: %w", err)
	}
	pgStore.Close()

	// Copy, one transaction on the target so a failure leaves nothing behind.
	tx, err := dst.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit

	counts := map[string]int{}
	for _, tb := range tables {
		colList := strings.Join(tb.cols, ", ")
		rows, err := src.QueryContext(ctx, `SELECT `+colList+` FROM `+tb.name)
		if err != nil {
			// A pre-upgrade source may lack the newest tables; treat a missing
			// table as empty rather than failing the whole migration.
			if strings.Contains(err.Error(), "no such table") {
				counts[tb.name] = 0
				continue
			}
			return fmt.Errorf("read %s: %w", tb.name, err)
		}
		ph := make([]string, len(tb.cols))
		for i := range ph {
			ph[i] = fmt.Sprintf("$%d", i+1)
		}
		insert := `INSERT INTO ` + tb.name + ` (` + colList + `) VALUES (` + strings.Join(ph, ",") + `)`
		if tb.name == "pools" {
			// Migrate seeds the default pool; the source's row wins.
			insert += ` ON CONFLICT (name) DO UPDATE SET slots=EXCLUDED.slots`
		}
		n := 0
		for rows.Next() {
			vals := make([]any, len(tb.cols))
			ptrs := make([]any, len(tb.cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				return fmt.Errorf("scan %s: %w", tb.name, err)
			}
			if _, err := tx.ExecContext(ctx, insert, vals...); err != nil {
				rows.Close()
				return fmt.Errorf("insert %s row %d: %w", tb.name, n+1, err)
			}
			n++
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate %s: %w", tb.name, err)
		}
		counts[tb.name] = n
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	// Dependency-closure verification (plan5 §9): counts equal, orphans zero.
	rec := map[string]any{"source": *from, "target_tables": map[string]any{}, "counts": counts}
	mismatches := 0
	for _, tb := range tables {
		var n int
		if err := dst.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+tb.name).Scan(&n); err != nil {
			return fmt.Errorf("verify %s: %w", tb.name, err)
		}
		if n != counts[tb.name] {
			mismatches++
			fmt.Fprintf(os.Stderr, "MISMATCH %s: source %d, target %d\n", tb.name, counts[tb.name], n)
		}
	}
	orphans := map[string]int{}
	orphanQ := map[string]string{
		"runs_without_dag":     `SELECT COUNT(*) FROM dag_runs r LEFT JOIN dags d ON r.dag_id=d.dag_id WHERE d.dag_id IS NULL`,
		"tasks_without_run":    `SELECT COUNT(*) FROM task_instances t LEFT JOIN dag_runs r ON t.run_id=r.run_id WHERE r.run_id IS NULL`,
		"outputs_without_run":  `SELECT COUNT(*) FROM task_outputs o LEFT JOIN dag_runs r ON o.run_id=r.run_id WHERE r.run_id IS NULL`,
		"versions_without_dag": `SELECT COUNT(*) FROM dag_versions v LEFT JOIN dags d ON v.dag_id=d.dag_id WHERE d.dag_id IS NULL`,
	}
	orphanTotal := 0
	for name, q := range orphanQ {
		var n int
		if err := dst.QueryRowContext(ctx, q).Scan(&n); err != nil {
			return fmt.Errorf("orphan check %s: %w", name, err)
		}
		orphans[name] = n
		orphanTotal += n
	}
	rec["orphans"] = orphans
	rec["mismatches"] = mismatches

	out, _ := json.MarshalIndent(rec, "", "  ")
	if *report != "" {
		if err := os.WriteFile(*report, out, 0o644); err != nil {
			return err
		}
	} else {
		fmt.Println(string(out))
	}
	if mismatches > 0 || orphanTotal > 0 {
		return fmt.Errorf("migration verification FAILED: %d count mismatches, %d orphaned references — do not point a scheduler at this target", mismatches, orphanTotal)
	}
	fmt.Fprintf(os.Stderr, "migration verified: %d tables, counts equal, zero orphans.\n", len(tables))
	fmt.Fprintln(os.Stderr, "next: start cronova with CRONOVA_DB set to the PostgreSQL DSN, run `cronova healthcheck`, and keep the SQLite file as the rollback copy until satisfied. Sessions and join tokens were intentionally not copied (log in again; mint new join tokens).")
	return nil
}
