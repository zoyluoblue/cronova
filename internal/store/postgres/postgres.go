// Package postgres is the PostgreSQL-backed implementation of store.Store.
//
// It is a direct port of store/sqlite and keeps its storage conventions:
// timestamps are RFC3339Nano UTC strings in TEXT columns (lexicographic
// ordering == time ordering, and exact-string equality lookups on
// logical_date keep working), and booleans are INTEGER 0/1. Behavioral parity
// with the SQLite store matters more than idiomatic Postgres types — the two
// backends must be interchangeable under the same scheduler logic.
//
// Unlike the SQLite store there is no single-connection invariant: Postgres
// handles concurrent connections natively (pool of 10). List* methods still
// fully materialize their results before returning so callers always receive
// detached data, keeping the code shape identical across backends.
package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/secrets"
	"github.com/zoyluo/cronova/internal/store"
)

//go:embed schema.sql
var schemaSQL string

const timeLayout = time.RFC3339Nano

// Store is a PostgreSQL-backed store.Store.
type Store struct {
	db *sql.DB
	// cipher seals/opens connection passwords at rest; nil = plaintext
	// (encryption not configured). Set via SetSecretCipher before serving.
	cipher *secrets.Cipher
}

// SetSecretCipher enables at-rest encryption of connection passwords: writes
// seal the password, reads open it (legacy plaintext rows pass through). Call
// once at startup, before the store is shared.
func (s *Store) SetSecretCipher(c *secrets.Cipher) { s.cipher = c }

var _ store.Store = (*Store)(nil)

// New opens a PostgreSQL database using a pgx connection string / URL
// (e.g. "postgres://user:pass@host:5432/cronova?sslmode=require").
func New(dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	// A small pool is plenty: cronova's write paths are short transactions and
	// the scheduler tick is single-threaded per instance.
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxIdleTime(5 * time.Minute)
	if err := db.PingContext(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Migrate(ctx context.Context) error {
	if err := s.execMultiStatement(ctx, schemaSQL); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	// Idempotent column additions for DBs created before the column existed.
	// CREATE TABLE IF NOT EXISTS won't alter an existing table; Postgres has
	// ADD COLUMN IF NOT EXISTS, so no duplicate-column-error dance is needed.
	for _, alter := range []string{
		`ALTER TABLE dags ADD COLUMN IF NOT EXISTS deleted_at TEXT`,
		`ALTER TABLE dags ADD COLUMN IF NOT EXISTS timezone TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE dag_runs ADD COLUMN IF NOT EXISTS params TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE dag_runs ADD COLUMN IF NOT EXISTS definition_yaml TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE dag_runs ADD COLUMN IF NOT EXISTS definition_hash TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE task_instances ADD COLUMN IF NOT EXISTS definition_hash TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE api_tokens ADD COLUMN IF NOT EXISTS expires_at TEXT`,
		`ALTER TABLE api_tokens ADD COLUMN IF NOT EXISTS dag_id TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := s.db.ExecContext(ctx, alter); err != nil {
			return fmt.Errorf("migrate (%s): %w", alter, err)
		}
	}
	if err := s.migrateSessionTokenHashes(ctx); err != nil {
		return fmt.Errorf("migrate session tokens: %w", err)
	}
	// Older releases created events as an unused, non-unique reserved table.
	// Collapse any manually-inserted duplicates before enforcing idempotent keys.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE id NOT IN (SELECT MIN(id) FROM events GROUP BY source, event_key)`); err != nil {
		return fmt.Errorf("deduplicate events: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_events_source_key ON events(source, event_key)`); err != nil {
		return fmt.Errorf("index events: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO pools(name, slots) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		model.DefaultPoolName, 16,
	); err != nil {
		return fmt.Errorf("seed default pool: %w", err)
	}
	return nil
}

// execMultiStatement runs a multi-statement SQL script. pgx's default extended
// query protocol only accepts one statement per Exec, so we drop down to the
// underlying *pgx.Conn, whose no-argument Exec uses the simple protocol and
// runs the whole script in one round trip (like SQLite's Exec did).
func (s *Store) execMultiStatement(ctx context.Context, script string) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(driverConn any) error {
		pc, ok := driverConn.(*stdlib.Conn)
		if !ok {
			return fmt.Errorf("unexpected driver connection type %T", driverConn)
		}
		_, err := pc.Conn().Exec(ctx, script)
		return err
	})
}

// --- helpers ---

type scanner interface{ Scan(dest ...any) error }

func fmtTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func fmtNullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(timeLayout)
}

// parseLoose parses the few timestamp formats we persist: RFC3339(Nano) for
// values we write (schema defaults use to_char in the same shape), and the
// space-separated form for defensive compatibility with hand-edited rows.
func parseLoose(s string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func nsToTime(ns sql.NullString) *time.Time {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	t := parseLoose(ns.String)
	if t.IsZero() {
		return nil
	}
	return &t
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// isUniqueErr reports whether err is a UNIQUE / PRIMARY KEY constraint
// violation (SQLSTATE 23505; a foreign-key violation is 23503 and must not map
// to ErrAlreadyExists).
func isUniqueErr(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// placeholderList returns "$start,$start+1,...,$start+n-1" for building
// dynamic IN (...) lists (Postgres uses positional $N, not ?).
func placeholderList(start, n int) string {
	ph := make([]string, n)
	for i := 0; i < n; i++ {
		ph[i] = "$" + strconv.Itoa(start+i)
	}
	return strings.Join(ph, ",")
}

// --- DAGs ---

func (s *Store) UpsertDAG(ctx context.Context, d *model.DAG) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO dags (dag_id, schedule, timezone, start_date, catchup, paused, max_active_runs, definition_yaml, owner, project, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT(dag_id) DO UPDATE SET
  schedule=excluded.schedule, timezone=excluded.timezone, start_date=excluded.start_date, catchup=excluded.catchup,
  max_active_runs=excluded.max_active_runs, definition_yaml=excluded.definition_yaml,
  updated_at=excluded.updated_at, deleted_at=NULL`,
		// paused/owner/project are operational state, not part of the YAML
		// definition: preserve the existing row's values on re-register (a DAG
		// edit or a restart) so a save/reload never silently un-pauses a DAG.
		// deleted_at is cleared: creating/registering a dag_id makes it active
		// (re-creating a previously soft-deleted id revives it).
		d.DagID, d.Schedule, d.Timezone, fmtTime(d.StartDate), boolToInt(d.Catchup), boolToInt(d.Paused),
		d.MaxActiveRuns, d.DefinitionYAML, d.Owner, d.Project, fmtTime(time.Now().UTC()))
	if err != nil {
		return fmt.Errorf("upsert dag %q: %w", d.DagID, err)
	}
	return nil
}

const dagCols = `dag_id, schedule, timezone, start_date, catchup, paused, max_active_runs, definition_yaml, owner, project, created_at, updated_at, deleted_at`

func scanDAG(sc scanner) (*model.DAG, error) {
	var d model.DAG
	var startStr, createdStr, updatedStr string
	var catchup, paused int
	var deletedNS sql.NullString
	err := sc.Scan(&d.DagID, &d.Schedule, &d.Timezone, &startStr, &catchup, &paused, &d.MaxActiveRuns,
		&d.DefinitionYAML, &d.Owner, &d.Project, &createdStr, &updatedStr, &deletedNS)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d.Catchup = catchup != 0
	d.Paused = paused != 0
	d.StartDate = parseLoose(startStr)
	d.CreatedAt = parseLoose(createdStr)
	d.UpdatedAt = parseLoose(updatedStr)
	d.DeletedAt = nsToTime(deletedNS)
	return &d, nil
}

func (s *Store) GetDAG(ctx context.Context, dagID string) (*model.DAG, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+dagCols+` FROM dags WHERE dag_id=$1`, dagID)
	return scanDAG(row)
}

func (s *Store) ListDAGs(ctx context.Context) ([]*model.DAG, error) {
	// Active DAGs only — soft-deleted (archived) DAGs are hidden from every list.
	rows, err := s.db.QueryContext(ctx, `SELECT `+dagCols+` FROM dags WHERE deleted_at IS NULL ORDER BY dag_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.DAG
	for rows.Next() {
		d, err := scanDAG(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SoftDeleteDAG archives a DAG: it sets deleted_at so the DAG disappears from
// every list, while its row (with definition_yaml) and run history are kept for
// audit/recovery. Returns ErrNotFound if no such DAG.
func (s *Store) SoftDeleteDAG(ctx context.Context, dagID string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE dags SET deleted_at=$1, updated_at=$1 WHERE dag_id=$2 AND deleted_at IS NULL`,
		fmtTime(time.Now().UTC()), dagID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound // missing or already deleted
	}
	return nil
}

func (s *Store) SetDAGPaused(ctx context.Context, dagID string, paused bool) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE dags SET paused=$1, updated_at=$2 WHERE dag_id=$3`,
		boolToInt(paused), fmtTime(time.Now().UTC()), dagID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// --- DAG runs ---

const runCols = `run_id, dag_id, logical_date, state, trigger_type, started_at, finished_at, params, definition_yaml, definition_hash`

func marshalParams(p map[string]string) string {
	if len(p) == 0 {
		return ""
	}
	b, _ := json.Marshal(p)
	return string(b)
}

func unmarshalParams(s string) map[string]string {
	if s == "" {
		return nil
	}
	var m map[string]string
	if json.Unmarshal([]byte(s), &m) != nil {
		return nil
	}
	return m
}

func scanRun(sc scanner) (*model.DagRun, error) {
	var r model.DagRun
	var logStr, state, trig string
	var startNS, finNS sql.NullString
	var params string
	err := sc.Scan(&r.RunID, &r.DagID, &logStr, &state, &trig, &startNS, &finNS, &params,
		&r.DefinitionYAML, &r.DefinitionHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.LogicalDate = parseLoose(logStr)
	r.State = model.RunState(state)
	r.TriggerType = model.TriggerType(trig)
	r.StartedAt = nsToTime(startNS)
	r.FinishedAt = nsToTime(finNS)
	r.Params = unmarshalParams(params)
	return &r, nil
}

func (s *Store) CreateDagRun(ctx context.Context, r *model.DagRun) error {
	// Guard against the delete race: only insert if the DAG is still active. A
	// soft-delete (DeleteDAG) and a concurrent run-creation (createDueRuns /
	// triggerDownstreams) are not atomic across statements, so without this a run
	// could be created for a just-archived DAG and then executed. The INSERT...
	// SELECT inserts zero rows when deleted_at IS NOT NULL.
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO dag_runs (`+runCols+`)
		 SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10 WHERE EXISTS (SELECT 1 FROM dags WHERE dag_id=$11 AND deleted_at IS NULL)`,
		r.RunID, r.DagID, fmtTime(r.LogicalDate), string(r.State), string(r.TriggerType),
		fmtNullTime(r.StartedAt), fmtNullTime(r.FinishedAt), marshalParams(r.Params),
		r.DefinitionYAML, r.DefinitionHash, r.DagID)
	if err != nil {
		if isUniqueErr(err) {
			return store.ErrAlreadyExists
		}
		return fmt.Errorf("create dag_run %q: %w", r.RunID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound // DAG missing or soft-deleted -> no run created
	}
	return nil
}

func (s *Store) CreateDagRunBounded(ctx context.Context, r *model.DagRun, global int) error {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO dag_runs (`+runCols+`)
		 SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10
		 WHERE EXISTS (SELECT 1 FROM dags WHERE dag_id=$11 AND deleted_at IS NULL)
		   AND (SELECT COUNT(*) FROM dag_runs WHERE state='queued') < $12`,
		r.RunID, r.DagID, fmtTime(r.LogicalDate), string(r.State), string(r.TriggerType),
		fmtNullTime(r.StartedAt), fmtNullTime(r.FinishedAt), marshalParams(r.Params),
		r.DefinitionYAML, r.DefinitionHash, r.DagID, global)
	if err != nil {
		if isUniqueErr(err) {
			return store.ErrAlreadyExists
		}
		return fmt.Errorf("create bounded dag_run %q: %w", r.RunID, err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	d, derr := s.GetDAG(ctx, r.DagID)
	if derr != nil || d.DeletedAt != nil {
		return store.ErrNotFound
	}
	return model.ErrQueueFull
}

func (s *Store) CreateManualDagRunBounded(ctx context.Context, r *model.DagRun, perDAG, global int) error {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO dag_runs (`+runCols+`)
		 SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10
		 WHERE EXISTS (SELECT 1 FROM dags WHERE dag_id=$11 AND deleted_at IS NULL)
		   AND (SELECT COUNT(*) FROM dag_runs WHERE dag_id=$12 AND state IN ('queued','running')) < $13
		   AND (SELECT COUNT(*) FROM dag_runs WHERE state='queued') < $14`,
		r.RunID, r.DagID, fmtTime(r.LogicalDate), string(r.State), string(r.TriggerType),
		fmtNullTime(r.StartedAt), fmtNullTime(r.FinishedAt), marshalParams(r.Params),
		r.DefinitionYAML, r.DefinitionHash,
		r.DagID, r.DagID, perDAG, global)
	if err != nil {
		if isUniqueErr(err) {
			return store.ErrAlreadyExists
		}
		return fmt.Errorf("create bounded dag_run %q: %w", r.RunID, err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	d, derr := s.GetDAG(ctx, r.DagID)
	if derr != nil || d.DeletedAt != nil {
		return store.ErrNotFound
	}
	return model.ErrQueueFull
}

func (s *Store) GetDagRun(ctx context.Context, runID string) (*model.DagRun, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+runCols+` FROM dag_runs WHERE run_id=$1`, runID)
	return scanRun(row)
}

func (s *Store) GetDagRunByLogicalDate(ctx context.Context, dagID string, logicalDate time.Time) (*model.DagRun, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+runCols+` FROM dag_runs WHERE dag_id=$1 AND logical_date=$2`,
		dagID, fmtTime(logicalDate))
	return scanRun(row)
}

func (s *Store) ListDagRuns(ctx context.Context, dagID string, limit int) ([]*model.DagRun, error) {
	return s.ListDagRunsPage(ctx, dagID, nil, limit, 0)
}

// LatestScheduledRun returns the newest schedule-triggered run (by logical
// date) regardless of how many manual/backfill runs exist — the scheduler's
// catchup anchor must never be crowded out of a windowed listing.
func (s *Store) LatestScheduledRun(ctx context.Context, dagID string) (*model.DagRun, error) {
	r, err := scanRun(s.db.QueryRowContext(ctx,
		`SELECT `+runCols+` FROM dag_runs WHERE dag_id=$1 AND trigger_type=$2
		 ORDER BY logical_date DESC LIMIT 1`, dagID, string(model.TriggerSchedule)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return r, err
}

// ListDagRunsPage lists a DAG's runs newest-first, optionally filtered to the
// given states, with limit/offset paging (offset enables the console's
// "load more" over long histories).
func (s *Store) ListDagRunsPage(ctx context.Context, dagID string, states []model.RunState, limit, offset int) ([]*model.DagRun, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	q := `SELECT ` + runCols + ` FROM dag_runs WHERE dag_id=$1`
	args := []any{dagID}
	if len(states) > 0 {
		ph := make([]string, len(states))
		for i, st := range states {
			args = append(args, string(st))
			ph[i] = "$" + strconv.Itoa(len(args))
		}
		q += ` AND state IN (` + strings.Join(ph, ",") + `)`
	}
	args = append(args, limit)
	q += ` ORDER BY logical_date DESC LIMIT $` + strconv.Itoa(len(args))
	args = append(args, offset)
	q += ` OFFSET $` + strconv.Itoa(len(args))
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.DagRun
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) ListDagRunsByState(ctx context.Context, state model.RunState) ([]*model.DagRun, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+runCols+` FROM dag_runs WHERE state=$1 ORDER BY logical_date`, string(state))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.DagRun
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RecentRuns returns the most recent runs across all live (non-soft-deleted)
// DAGs, newest first, ordered by when they actually ran (started_at, falling
// back to logical_date). Powers the dashboard activity timeline.
func (s *Store) RecentRuns(ctx context.Context, limit int) ([]*model.DagRun, error) {
	if limit <= 0 {
		limit = 20
	}
	// order by parsed epoch, not raw text: our RFC3339Nano timestamps trim
	// trailing fractional zeros, so a whole-second value ("…05Z") and a
	// sub-second one ("…05.3Z") don't compare lexicographically — casting to
	// timestamptz normalizes both to a numeric instant so same-second runs sort
	// correctly (the Postgres analogue of SQLite's strftime('%s')). NULLIF
	// guards the cast against empty strings; NULLS LAST matches SQLite's DESC
	// ordering for never-started runs.
	rows, err := s.db.QueryContext(ctx,
		`SELECT r.run_id, r.dag_id, r.logical_date, r.state, r.trigger_type, r.started_at, r.finished_at, r.params,
		        r.definition_yaml, r.definition_hash
		 FROM dag_runs r JOIN dags d ON r.dag_id=d.dag_id
		 WHERE d.deleted_at IS NULL
		 ORDER BY COALESCE(EXTRACT(EPOCH FROM NULLIF(r.started_at,'')::timestamptz),
		                   EXTRACT(EPOCH FROM NULLIF(r.logical_date,'')::timestamptz)) DESC,
		          r.started_at DESC NULLS LAST LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.DagRun
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) UpdateDagRunState(ctx context.Context, runID string, state model.RunState, startedAt, finishedAt *time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE dag_runs SET state=$1, started_at=$2, finished_at=$3 WHERE run_id=$4`,
		string(state), fmtNullTime(startedAt), fmtNullTime(finishedAt), runID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// UpdateDagRunSuccess makes the state transition and dependency publication one
// transaction. ON CONFLICT re-arms the event when an operator changes a run
// away from success and later marks it successful again.
func (s *Store) UpdateDagRunSuccess(ctx context.Context, runID string, startedAt, finishedAt *time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE dag_runs SET state=$1, started_at=$2, finished_at=$3 WHERE run_id=$4`,
		string(model.RunSuccess), fmtNullTime(startedAt), fmtNullTime(finishedAt), runID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO events(source, event_key, payload, consumed)
VALUES ($1, $2, '', 0)
ON CONFLICT(source, event_key) DO UPDATE SET
    consumed=0,
    created_at=$3`, model.EventSourceDependency, runID, fmtTime(time.Now().UTC())); err != nil {
		return fmt.Errorf("publish dependency event for run %q: %w", runID, err)
	}
	return tx.Commit()
}

func (s *Store) UpdateDagRunDefinition(ctx context.Context, runID, definitionYAML, definitionHash string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE dag_runs SET definition_yaml=$1, definition_hash=$2 WHERE run_id=$3`,
		definitionYAML, definitionHash, runID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) CountActiveRuns(ctx context.Context, dagID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM dag_runs WHERE dag_id=$1 AND state IN ('queued','running')`, dagID).
		Scan(&n)
	return n, err
}

// --- task instances ---

const tiCols = `id, run_id, task_id, state, try_number, max_retries, pool, priority, definition_hash, executor_ref, log_path, started_at, finished_at`

func scanTI(sc scanner) (*model.TaskInstance, error) {
	var ti model.TaskInstance
	var state string
	var startNS, finNS sql.NullString
	err := sc.Scan(&ti.ID, &ti.RunID, &ti.TaskID, &state, &ti.TryNumber, &ti.MaxRetries,
		&ti.Pool, &ti.Priority, &ti.DefinitionHash, &ti.ExecutorRef, &ti.LogPath, &startNS, &finNS)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	ti.State = model.TaskState(state)
	ti.StartedAt = nsToTime(startNS)
	ti.FinishedAt = nsToTime(finNS)
	return &ti, nil
}

func (s *Store) CreateTaskInstance(ctx context.Context, ti *model.TaskInstance) error {
	// RETURNING instead of LastInsertId: the pgx driver does not implement
	// LastInsertId (Postgres has no connection-level "last rowid" concept).
	var id int64
	err := s.db.QueryRowContext(ctx, `
INSERT INTO task_instances (run_id, task_id, state, try_number, max_retries, pool, priority, definition_hash, executor_ref, log_path, started_at, finished_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`,
		ti.RunID, ti.TaskID, string(ti.State), ti.TryNumber, ti.MaxRetries, ti.Pool, ti.Priority,
		ti.DefinitionHash, ti.ExecutorRef, ti.LogPath, fmtNullTime(ti.StartedAt), fmtNullTime(ti.FinishedAt)).Scan(&id)
	if err != nil {
		if isUniqueErr(err) {
			return store.ErrAlreadyExists
		}
		return fmt.Errorf("create task_instance %s/%s: %w", ti.RunID, ti.TaskID, err)
	}
	ti.ID = id
	return nil
}

func (s *Store) GetTaskInstance(ctx context.Context, id int64) (*model.TaskInstance, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+tiCols+` FROM task_instances WHERE id=$1`, id)
	return scanTI(row)
}

func (s *Store) ListTaskInstances(ctx context.Context, runID string) ([]*model.TaskInstance, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+tiCols+` FROM task_instances WHERE run_id=$1 ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.TaskInstance
	for rows.Next() {
		ti, err := scanTI(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ti)
	}
	return out, rows.Err()
}

func (s *Store) ListTaskInstancesByState(ctx context.Context, state model.TaskState) ([]*model.TaskInstance, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+tiCols+` FROM task_instances WHERE state=$1 ORDER BY priority DESC, id`, string(state))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.TaskInstance
	for rows.Next() {
		ti, err := scanTI(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ti)
	}
	return out, rows.Err()
}

func (s *Store) UpdateTaskInstance(ctx context.Context, ti *model.TaskInstance) error {
	res, err := s.db.ExecContext(ctx, `
UPDATE task_instances SET state=$1, try_number=$2, max_retries=$3, pool=$4, priority=$5, definition_hash=$6, executor_ref=$7, log_path=$8, started_at=$9, finished_at=$10
WHERE id=$11`,
		string(ti.State), ti.TryNumber, ti.MaxRetries, ti.Pool, ti.Priority, ti.DefinitionHash, ti.ExecutorRef, ti.LogPath,
		fmtNullTime(ti.StartedAt), fmtNullTime(ti.FinishedAt), ti.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

const terminalTaskStates = `'success','failed','upstream_failed','skipped','cancelled','timed_out'`

// UpdateTaskInstanceGuarded applies the update only if the row still carries
// expectRef AND is not already terminal — an optimistic CAS. It lets a polling
// goroutine finalize a task WITHOUT clobbering a concurrent CancelRun (which makes
// the row terminal) or a retry (which clears/rewrites executor_ref). Returns
// whether the write applied.
func (s *Store) UpdateTaskInstanceGuarded(ctx context.Context, ti *model.TaskInstance, expectRef string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
UPDATE task_instances SET state=$1, try_number=$2, max_retries=$3, pool=$4, priority=$5, definition_hash=$6, executor_ref=$7, log_path=$8, started_at=$9, finished_at=$10
WHERE id=$11 AND executor_ref=$12 AND state NOT IN (`+terminalTaskStates+`)`,
		string(ti.State), ti.TryNumber, ti.MaxRetries, ti.Pool, ti.Priority, ti.DefinitionHash, ti.ExecutorRef, ti.LogPath,
		fmtNullTime(ti.StartedAt), fmtNullTime(ti.FinishedAt), ti.ID, expectRef)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// --- cross-DAG dependencies ---

func (s *Store) ReplaceDagDependencies(ctx context.Context, downstream string, upstreams []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM dag_dependencies WHERE downstream_dag=$1`, downstream); err != nil {
		return err
	}
	for _, up := range upstreams {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO dag_dependencies (upstream_dag, downstream_dag) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			up, downstream); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListDownstreams(ctx context.Context, upstream string) ([]string, error) {
	return s.queryStrings(ctx, `SELECT downstream_dag FROM dag_dependencies WHERE upstream_dag=$1 ORDER BY downstream_dag`, upstream)
}

func (s *Store) ListUpstreams(ctx context.Context, downstream string) ([]string, error) {
	return s.queryStrings(ctx, `SELECT upstream_dag FROM dag_dependencies WHERE downstream_dag=$1 ORDER BY upstream_dag`, downstream)
}

func (s *Store) ListPendingEvents(ctx context.Context, source string, limit int) ([]*model.Event, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, source, event_key, payload, consumed, created_at
FROM events
WHERE source=$1 AND consumed=0
ORDER BY id
LIMIT $2`, source, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Event
	for rows.Next() {
		var (
			e        model.Event
			consumed int
			created  string
		)
		if err := rows.Scan(&e.ID, &e.Source, &e.EventKey, &e.Payload, &consumed, &created); err != nil {
			return nil, err
		}
		e.Consumed = consumed != 0
		e.CreatedAt = parseLoose(created)
		out = append(out, &e)
	}
	return out, rows.Err()
}

func (s *Store) ConsumeEvent(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE events SET consumed=1 WHERE id=$1`, id)
	return err
}

// PublishEvent durably records an event. (source, event_key) is the
// idempotency key: re-publishing an existing key — consumed or not — is a
// no-op, so an external caller retrying a delivery cannot double-trigger.
func (s *Store) PublishEvent(ctx context.Context, source, key, payload string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
INSERT INTO events(source, event_key, payload, consumed) VALUES ($1, $2, $3, 0)
ON CONFLICT(source, event_key) DO NOTHING`, source, key, payload)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// --- inbound webhook secrets (per-DAG) ---

// SetDagHook stores (replaces) the hook secret hash for a DAG.
func (s *Store) SetDagHook(ctx context.Context, dagID, secretHash, prefix string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO dag_hooks(dag_id, secret_hash, prefix, created_at) VALUES ($1,$2,$3,$4)
ON CONFLICT(dag_id) DO UPDATE SET secret_hash=excluded.secret_hash, prefix=excluded.prefix, created_at=excluded.created_at`,
		dagID, secretHash, prefix, fmtTime(time.Now().UTC()))
	return err
}

// GetDagHook returns the stored secret hash + display prefix, or ErrNotFound.
func (s *Store) GetDagHook(ctx context.Context, dagID string) (secretHash, prefix string, createdAt time.Time, err error) {
	var created string
	err = s.db.QueryRowContext(ctx,
		`SELECT secret_hash, prefix, created_at FROM dag_hooks WHERE dag_id=$1`, dagID).
		Scan(&secretHash, &prefix, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", time.Time{}, store.ErrNotFound
	}
	if err != nil {
		return "", "", time.Time{}, err
	}
	return secretHash, prefix, parseLoose(created), nil
}

// DeleteDagHook removes a DAG's hook secret (the URL stops working immediately).
func (s *Store) DeleteDagHook(ctx context.Context, dagID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM dag_hooks WHERE dag_id=$1`, dagID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) queryStrings(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// --- pools ---

func (s *Store) UpsertPool(ctx context.Context, p *model.Pool) error {
	if p == nil || p.Slots < 1 || p.Slots > model.MaxPoolSlots {
		return fmt.Errorf("pool slots must be between 1 and %d", model.MaxPoolSlots)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO pools(name, slots) VALUES ($1, $2) ON CONFLICT(name) DO UPDATE SET slots=excluded.slots`,
		p.Name, p.Slots)
	return err
}

func (s *Store) GetPool(ctx context.Context, name string) (*model.Pool, error) {
	var p model.Pool
	err := s.db.QueryRowContext(ctx, `SELECT name, slots FROM pools WHERE name=$1`, name).
		Scan(&p.Name, &p.Slots)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Store) ListPools(ctx context.Context) ([]*model.Pool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, slots FROM pools ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Pool
	for rows.Next() {
		var p model.Pool
		if err := rows.Scan(&p.Name, &p.Slots); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

func (s *Store) CountRunningInPool(ctx context.Context, pool string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM task_instances WHERE pool=$1 AND state IN ('queued','running')`, pool).
		Scan(&n)
	return n, err
}

func (s *Store) CountActiveTaskInstances(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM task_instances WHERE state IN ('queued','running')`).Scan(&n)
	return n, err
}

// ---- auth: users + sessions ----

const userCols = `id, username, password_hash, role, created_at`

func scanUser(sc scanner) (*model.User, error) {
	var u model.User
	var role, created string
	if err := sc.Scan(&u.ID, &u.Username, &u.PasswordHash, &role, &created); err != nil {
		return nil, err
	}
	u.Role = model.Role(role)
	u.CreatedAt = parseLoose(created)
	return &u, nil
}

func (s *Store) CreateUser(ctx context.Context, u *model.User) error {
	// RETURNING instead of LastInsertId (unsupported by the pgx driver).
	return s.db.QueryRowContext(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1,$2,$3) RETURNING id`,
		u.Username, u.PasswordHash, string(u.Role)).Scan(&u.ID)
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (*model.User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE username=$1`, username))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return u, err
}

func (s *Store) GetUserByID(ctx context.Context, id int64) (*model.User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return u, err
}

func (s *Store) ListUsers(ctx context.Context) ([]*model.User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) UpdateUserPassword(ctx context.Context, id int64, passwordHash string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET password_hash=$1, updated_at=$2 WHERE id=$3`,
		passwordHash, fmtTime(time.Now().UTC()), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=$1`, id) // revoke on password change
	return err
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=$1`, id); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) CreateSession(ctx context.Context, se *model.Session) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token, user_id, expires_at) VALUES ($1,$2,$3)`,
		hashSessionToken(se.Token), se.UserID, fmtTime(se.ExpiresAt))
	return err
}

func (s *Store) GetSession(ctx context.Context, token string) (*model.Session, error) {
	var se model.Session
	var created, expires string
	hashed := hashSessionToken(token)
	err := s.db.QueryRowContext(ctx,
		`SELECT user_id, created_at, expires_at FROM sessions WHERE token=$1`, hashed).
		Scan(&se.UserID, &created, &expires)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	se.CreatedAt = parseLoose(created)
	se.ExpiresAt = parseLoose(expires)
	se.Token = token
	if !se.ExpiresAt.After(time.Now()) { // expired: prune + treat as absent
		_, _ = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token=$1`, hashed)
		return nil, store.ErrNotFound
	}
	return &se, nil
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token=$1`, hashSessionToken(token))
	return err
}

func hashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// migrateSessionTokenHashes upgrades pre-hardening rows that stored the raw
// cookie token. The prefix makes this idempotent across every startup.
func (s *Store) migrateSessionTokenHashes(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT token FROM sessions WHERE token NOT LIKE 'sha256:%'`)
	if err != nil {
		return err
	}
	var legacy []string
	for rows.Next() {
		var token string
		if err := rows.Scan(&token); err != nil {
			_ = rows.Close()
			return err
		}
		legacy = append(legacy, token)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, token := range legacy {
		if _, err := s.db.ExecContext(ctx, `UPDATE sessions SET token=$1 WHERE token=$2`, hashSessionToken(token), token); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < $1`, fmtTime(time.Now()))
	return err
}

// ---- variables + connections (UI-managed config) ----

func scanVariable(sc scanner) (*model.Variable, error) {
	var v model.Variable
	var upd string
	if err := sc.Scan(&v.Key, &v.Value, &upd); err != nil {
		return nil, err
	}
	v.UpdatedAt = parseLoose(upd)
	return &v, nil
}

func (s *Store) ListVariables(ctx context.Context) ([]*model.Variable, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value, updated_at FROM variables ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Variable
	for rows.Next() {
		v, err := scanVariable(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) GetVariable(ctx context.Context, key string) (*model.Variable, error) {
	v, err := scanVariable(s.db.QueryRowContext(ctx, `SELECT key, value, updated_at FROM variables WHERE key=$1`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return v, err
}

func (s *Store) UpsertVariable(ctx context.Context, v *model.Variable) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO variables (key, value, updated_at) VALUES ($1,$2,$3)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		v.Key, v.Value, fmtTime(time.Now().UTC()))
	return err
}

func (s *Store) DeleteVariable(ctx context.Context, key string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM variables WHERE key=$1`, key)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

const connCols = `id, type, host, port, login, password, extra, updated_at`

func scanConnection(sc scanner) (*model.Connection, error) {
	var c model.Connection
	var upd string
	if err := sc.Scan(&c.ID, &c.Type, &c.Host, &c.Port, &c.Login, &c.Password, &c.Extra, &upd); err != nil {
		return nil, err
	}
	c.UpdatedAt = parseLoose(upd)
	return &c, nil
}

func (s *Store) ListConnections(ctx context.Context) ([]*model.Connection, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+connCols+` FROM connections ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Connection
	for rows.Next() {
		c, err := scanConnection(rows)
		if err != nil {
			return nil, err
		}
		if err := s.openConnSecret(c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetConnection(ctx context.Context, id string) (*model.Connection, error) {
	c, err := scanConnection(s.db.QueryRowContext(ctx, `SELECT `+connCols+` FROM connections WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := s.openConnSecret(c); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Store) UpsertConnection(ctx context.Context, c *model.Connection) error {
	password := c.Password
	if s.cipher != nil {
		sealed, err := s.cipher.Encrypt(password)
		if err != nil {
			return fmt.Errorf("encrypt connection password: %w", err)
		}
		password = sealed
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO connections (id, type, host, port, login, password, extra, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		 ON CONFLICT(id) DO UPDATE SET type=excluded.type, host=excluded.host, port=excluded.port,
		   login=excluded.login, password=excluded.password, extra=excluded.extra, updated_at=excluded.updated_at`,
		c.ID, c.Type, c.Host, c.Port, c.Login, password, c.Extra, fmtTime(time.Now().UTC()))
	return err
}

// openConnSecret decrypts a connection's password in place (no-op without a
// cipher; legacy plaintext passes through the cipher unchanged).
func (s *Store) openConnSecret(c *model.Connection) error {
	if s.cipher == nil || c == nil {
		return nil
	}
	plain, err := s.cipher.Decrypt(c.Password)
	if err != nil {
		return fmt.Errorf("connection %q: %w", c.ID, err)
	}
	c.Password = plain
	return nil
}

// MigrateConnectionSecrets seals any legacy plaintext passwords in place — a
// one-time upgrade run at startup once encryption is enabled. Idempotent:
// already sealed rows are skipped.
func (s *Store) MigrateConnectionSecrets(ctx context.Context) (int, error) {
	if s.cipher == nil {
		return 0, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, password FROM connections WHERE password != ''`)
	if err != nil {
		return 0, err
	}
	type row struct{ id, password string }
	var todo []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.password); err != nil {
			rows.Close()
			return 0, err
		}
		if !secrets.IsEncrypted(r.password) {
			todo = append(todo, r)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, r := range todo {
		sealed, err := s.cipher.Encrypt(r.password)
		if err != nil {
			return 0, err
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE connections SET password=$1 WHERE id=$2`, sealed, r.id); err != nil {
			return 0, err
		}
	}
	return len(todo), nil
}

func (s *Store) DeleteConnection(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM connections WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// CountRunsByState returns the all-time run count grouped by state (for /metrics).
func (s *Store) CountRunsByState(ctx context.Context) (map[model.RunState]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT state, COUNT(*) FROM dag_runs GROUP BY state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[model.RunState]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[model.RunState(st)] = n
	}
	return out, rows.Err()
}

// PruneRuns deletes finished runs older than cutoff plus their task instances,
// in one transaction, and returns the (dag_id, run_id) pairs it removed so the
// caller can delete the runs' log directories. Only terminal states with a
// recorded finished_at qualify — an in-flight or still-queued run is never pruned.
func (s *Store) PruneRuns(ctx context.Context, cutoff time.Time) ([]*model.DagRun, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Each DAG's newest schedule-triggered run is exempt no matter how old: it
	// is the catchup anchor (see scheduler.scheduleAnchor). Pruning it would
	// reset a sparse-schedule catchup DAG to start_date and re-execute history.
	rows, err := tx.QueryContext(ctx,
		`SELECT run_id, dag_id FROM dag_runs
		 WHERE finished_at IS NOT NULL AND finished_at < $1
		   AND state IN ($2,$3,$4,$5)
		   AND run_id NOT IN (
		     SELECT r2.run_id FROM dag_runs r2
		     WHERE r2.trigger_type = $6
		       AND r2.logical_date = (
		         SELECT MAX(r3.logical_date) FROM dag_runs r3
		         WHERE r3.dag_id = r2.dag_id AND r3.trigger_type = $7))`,
		fmtTime(cutoff.UTC()),
		string(model.RunSuccess), string(model.RunFailed),
		string(model.RunCancelled), string(model.RunTimedOut),
		string(model.TriggerSchedule), string(model.TriggerSchedule))
	if err != nil {
		return nil, err
	}
	var pruned []*model.DagRun
	for rows.Next() {
		r := &model.DagRun{}
		if err := rows.Scan(&r.RunID, &r.DagID); err != nil {
			rows.Close()
			return nil, err
		}
		pruned = append(pruned, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(pruned) == 0 {
		return nil, tx.Commit()
	}

	// Delete in chunks: Postgres allows far more bind vars than SQLite, but the
	// chunking keeps statement size bounded and the code shape identical.
	const chunk = 500
	for i := 0; i < len(pruned); i += chunk {
		end := i + chunk
		if end > len(pruned) {
			end = len(pruned)
		}
		ids := make([]any, 0, end-i)
		for _, r := range pruned[i:end] {
			ids = append(ids, r.RunID)
		}
		eventArgs := make([]any, 0, len(ids)+1)
		eventArgs = append(eventArgs, model.EventSourceDependency)
		eventArgs = append(eventArgs, ids...)
		if _, err := tx.ExecContext(ctx, `DELETE FROM events WHERE source=$1 AND event_key IN (`+placeholderList(2, len(ids))+`)`, eventArgs...); err != nil {
			return nil, fmt.Errorf("prune dependency events: %w", err)
		}
		in := placeholderList(1, len(ids))
		if _, err := tx.ExecContext(ctx, `DELETE FROM task_instances WHERE run_id IN (`+in+`)`, ids...); err != nil {
			return nil, fmt.Errorf("prune task_instances: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM dag_runs WHERE run_id IN (`+in+`)`, ids...); err != nil {
			return nil, fmt.Errorf("prune dag_runs: %w", err)
		}
	}
	return pruned, tx.Commit()
}

// RecordAudit appends one entry to the operations audit trail.
func (s *Store) RecordAudit(ctx context.Context, e *model.AuditEntry) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_log (ts, actor, action, target, detail) VALUES ($1,$2,$3,$4,$5)`,
		fmtTime(time.Now().UTC()), e.Actor, e.Action, e.Target, e.Detail)
	return err
}

// ListAudit returns audit entries newest-first (by id); target != "" filters to
// one dag/run. limit is clamped to [1,500] (default 100).
func (s *Store) ListAudit(ctx context.Context, target string, limit, offset int) ([]*model.AuditEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	var (
		rows *sql.Rows
		err  error
	)
	if target != "" {
		rows, err = s.db.QueryContext(ctx, `SELECT id, ts, actor, action, target, detail FROM audit_log WHERE target=$1 ORDER BY id DESC LIMIT $2 OFFSET $3`, target, limit, offset)
	} else {
		rows, err = s.db.QueryContext(ctx, `SELECT id, ts, actor, action, target, detail FROM audit_log ORDER BY id DESC LIMIT $1 OFFSET $2`, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.AuditEntry
	for rows.Next() {
		var e model.AuditEntry
		var ts string
		if err := rows.Scan(&e.ID, &ts, &e.Actor, &e.Action, &e.Target, &e.Detail); err != nil {
			return nil, err
		}
		e.TS = parseLoose(ts)
		out = append(out, &e)
	}
	return out, rows.Err()
}

func (s *Store) PruneAudit(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM audit_log WHERE ts < $1`, fmtTime(cutoff))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CreateAPIToken inserts a token, storing only its hash. The plaintext is never
// persisted (the caller returns it once in the create response).
func (s *Store) CreateAPIToken(ctx context.Context, t *model.APIToken, hash string) error {
	// RETURNING instead of LastInsertId (unsupported by the pgx driver).
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO api_tokens (name, role, token_hash, prefix, created_at, expires_at, dag_id) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		t.Name, string(t.Role), hash, t.Prefix, fmtTime(time.Now().UTC()), fmtNullTime(t.ExpiresAt), t.DagID).Scan(&t.ID)
	if err != nil {
		return err
	}
	t.CreatedAt = time.Now().UTC()
	return nil
}

// scanAPIToken decodes one api_tokens row (shared by List and GetByHash).
func scanAPIToken(row scanner) (*model.APIToken, error) {
	t := &model.APIToken{}
	var role, created string
	var lastUsed, expires sql.NullString
	if err := row.Scan(&t.ID, &t.Name, &role, &t.Prefix, &created, &lastUsed, &expires, &t.DagID); err != nil {
		return nil, err
	}
	t.Role = model.Role(role)
	t.CreatedAt = parseLoose(created)
	if lastUsed.Valid && lastUsed.String != "" {
		lt := parseLoose(lastUsed.String)
		t.LastUsedAt = &lt
	}
	if expires.Valid && expires.String != "" {
		et := parseLoose(expires.String)
		t.ExpiresAt = &et
	}
	return t, nil
}

// ListAPITokens returns all tokens newest-first (never the hash or plaintext).
func (s *Store) ListAPITokens(ctx context.Context) ([]*model.APIToken, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, role, prefix, created_at, last_used_at, expires_at, dag_id FROM api_tokens ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.APIToken
	for rows.Next() {
		t, err := scanAPIToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetAPITokenByHash resolves an incoming bearer token's hash to its record, or
// ErrNotFound. Used on every Bearer-authenticated request.
func (s *Store) GetAPITokenByHash(ctx context.Context, hash string) (*model.APIToken, error) {
	t, err := scanAPIToken(s.db.QueryRowContext(ctx,
		`SELECT id, name, role, prefix, created_at, last_used_at, expires_at, dag_id FROM api_tokens WHERE token_hash=$1`, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

// TouchAPIToken records the token's most recent use (best-effort, throttled by
// the caller).
func (s *Store) TouchAPIToken(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE api_tokens SET last_used_at=$1 WHERE id=$2`, fmtTime(time.Now().UTC()), id)
	return err
}

// DeleteAPIToken revokes a token by id. Returns ErrNotFound if absent.
func (s *Store) DeleteAPIToken(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM api_tokens WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}
