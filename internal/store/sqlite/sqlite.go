// Package sqlite is the SQLite-backed implementation of store.Store.
//
// It uses the pure-Go modernc.org/sqlite driver (no CGO) so cronova stays a
// single static binary. The connection runs with a rollback journal (DELETE
// mode, for cross-process safety — see New) and a busy timeout, and access is
// serialized via MaxOpenConns(1) (see docs/ARCHITECTURE.md §6).
//
// INVARIANT (single connection): never issue a write while a query's *sql.Rows
// is still open — with one connection that self-deadlocks (the open cursor
// holds the only conn, and busy_timeout does not apply to same-connection
// contention). Every List* method below fully materializes its result into a
// slice before returning, so callers always receive detached data. Any new
// method must preserve this: read rows to completion (or Close) before writing.
package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/store"
	sqlitelib "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

const timeLayout = time.RFC3339Nano

// Store is a SQLite-backed store.Store.
type Store struct {
	db *sql.DB
}

var _ store.Store = (*Store)(nil)

// New opens (or creates) a SQLite database at path.
func New(path string) (*Store, error) {
	// Rollback journal (DELETE), not WAL: the pure-Go modernc driver emulates
	// WAL shared-memory per-process, so WAL does NOT coordinate across OS
	// processes (the `cronova` CLI and a running `cronova serve` would not see
	// each other's writes). DELETE mode uses real OS file locks, which are
	// multi-process safe. We give up nothing: with MaxOpenConns(1) below, access
	// is already serialized within a process, so WAL's concurrent-read advantage
	// was unused. busy_timeout lets cross-process lock contention retry.
	dsn := fmt.Sprintf(
		"file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(DELETE)&_pragma=foreign_keys(on)",
		path,
	)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Serialize all access through a single connection. Simple and correct for
	// single-machine v1; revisit when moving to a client/server DB.
	db.SetMaxOpenConns(1)
	if err := db.PingContext(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	// Idempotent column additions for DBs created before the column existed.
	// CREATE TABLE IF NOT EXISTS won't alter an existing table; ADD COLUMN errors
	// with "duplicate column name" if already present, which we ignore.
	for _, alter := range []string{
		`ALTER TABLE dags ADD COLUMN deleted_at DATETIME`,
		`ALTER TABLE dag_runs ADD COLUMN params TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := s.db.ExecContext(ctx, alter); err != nil && !isDuplicateColumnErr(err) {
			return fmt.Errorf("migrate (%s): %w", alter, err)
		}
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO pools(name, slots) VALUES (?, ?)`,
		model.DefaultPoolName, 16,
	); err != nil {
		return fmt.Errorf("seed default pool: %w", err)
	}
	return nil
}

// isDuplicateColumnErr reports whether err is SQLite's "duplicate column name"
// (returned by ADD COLUMN when the column already exists), making the ALTER a
// safe no-op on already-migrated databases.
func isDuplicateColumnErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate column name")
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
// values we write, and SQLite's CURRENT_TIMESTAMP format for defaults.
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
// violation (vs. e.g. a foreign-key violation, which is also a constraint).
func isUniqueErr(err error) bool {
	var se *sqlitelib.Error
	if errors.As(err, &se) {
		c := se.Code()
		return c == 2067 || c == 1555 // SQLITE_CONSTRAINT_UNIQUE / _PRIMARYKEY
	}
	return false
}

// --- DAGs ---

func (s *Store) UpsertDAG(ctx context.Context, d *model.DAG) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO dags (dag_id, schedule, start_date, catchup, paused, max_active_runs, definition_yaml, owner, project, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,CURRENT_TIMESTAMP)
ON CONFLICT(dag_id) DO UPDATE SET
  schedule=excluded.schedule, start_date=excluded.start_date, catchup=excluded.catchup,
  max_active_runs=excluded.max_active_runs, definition_yaml=excluded.definition_yaml,
  updated_at=CURRENT_TIMESTAMP, deleted_at=NULL`,
		// paused/owner/project are operational state, not part of the YAML
		// definition: preserve the existing row's values on re-register (a DAG
		// edit or a restart) so a save/reload never silently un-pauses a DAG.
		// deleted_at is cleared: creating/registering a dag_id makes it active
		// (re-creating a previously soft-deleted id revives it).
		d.DagID, d.Schedule, fmtTime(d.StartDate), boolToInt(d.Catchup), boolToInt(d.Paused),
		d.MaxActiveRuns, d.DefinitionYAML, d.Owner, d.Project)
	if err != nil {
		return fmt.Errorf("upsert dag %q: %w", d.DagID, err)
	}
	return nil
}

const dagCols = `dag_id, schedule, start_date, catchup, paused, max_active_runs, definition_yaml, owner, project, created_at, updated_at, deleted_at`

func scanDAG(sc scanner) (*model.DAG, error) {
	var d model.DAG
	var startStr, createdStr, updatedStr string
	var catchup, paused int
	var deletedNS sql.NullString
	err := sc.Scan(&d.DagID, &d.Schedule, &startStr, &catchup, &paused, &d.MaxActiveRuns,
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
	row := s.db.QueryRowContext(ctx, `SELECT `+dagCols+` FROM dags WHERE dag_id=?`, dagID)
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
		`UPDATE dags SET deleted_at=CURRENT_TIMESTAMP, updated_at=CURRENT_TIMESTAMP WHERE dag_id=? AND deleted_at IS NULL`,
		dagID)
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
		`UPDATE dags SET paused=?, updated_at=CURRENT_TIMESTAMP WHERE dag_id=?`,
		boolToInt(paused), dagID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// --- DAG runs ---

const runCols = `run_id, dag_id, logical_date, state, trigger_type, started_at, finished_at, params`

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
	err := sc.Scan(&r.RunID, &r.DagID, &logStr, &state, &trig, &startNS, &finNS, &params)
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
		 SELECT ?,?,?,?,?,?,?,? WHERE EXISTS (SELECT 1 FROM dags WHERE dag_id=? AND deleted_at IS NULL)`,
		r.RunID, r.DagID, fmtTime(r.LogicalDate), string(r.State), string(r.TriggerType),
		fmtNullTime(r.StartedAt), fmtNullTime(r.FinishedAt), marshalParams(r.Params), r.DagID)
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

func (s *Store) GetDagRun(ctx context.Context, runID string) (*model.DagRun, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+runCols+` FROM dag_runs WHERE run_id=?`, runID)
	return scanRun(row)
}

func (s *Store) GetDagRunByLogicalDate(ctx context.Context, dagID string, logicalDate time.Time) (*model.DagRun, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+runCols+` FROM dag_runs WHERE dag_id=? AND logical_date=?`,
		dagID, fmtTime(logicalDate))
	return scanRun(row)
}

func (s *Store) ListDagRuns(ctx context.Context, dagID string, limit int) ([]*model.DagRun, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+runCols+` FROM dag_runs WHERE dag_id=? ORDER BY logical_date DESC LIMIT ?`,
		dagID, limit)
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
		`SELECT `+runCols+` FROM dag_runs WHERE state=? ORDER BY logical_date`, string(state))
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
	// sub-second one ("…05.3Z") don't compare lexicographically — strftime('%s')
	// normalizes both to a numeric instant so same-second runs sort correctly.
	rows, err := s.db.QueryContext(ctx,
		`SELECT r.run_id, r.dag_id, r.logical_date, r.state, r.trigger_type, r.started_at, r.finished_at, r.params
		 FROM dag_runs r JOIN dags d ON r.dag_id=d.dag_id
		 WHERE d.deleted_at IS NULL
		 ORDER BY COALESCE(CAST(strftime('%s', r.started_at) AS INTEGER), CAST(strftime('%s', r.logical_date) AS INTEGER)) DESC,
		          r.started_at DESC LIMIT ?`, limit)
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
		`UPDATE dag_runs SET state=?, started_at=?, finished_at=? WHERE run_id=?`,
		string(state), fmtNullTime(startedAt), fmtNullTime(finishedAt), runID)
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
		`SELECT COUNT(*) FROM dag_runs WHERE dag_id=? AND state IN ('queued','running')`, dagID).
		Scan(&n)
	return n, err
}

// --- task instances ---

const tiCols = `id, run_id, task_id, state, try_number, max_retries, pool, priority, executor_ref, log_path, started_at, finished_at`

func scanTI(sc scanner) (*model.TaskInstance, error) {
	var ti model.TaskInstance
	var state string
	var startNS, finNS sql.NullString
	err := sc.Scan(&ti.ID, &ti.RunID, &ti.TaskID, &state, &ti.TryNumber, &ti.MaxRetries,
		&ti.Pool, &ti.Priority, &ti.ExecutorRef, &ti.LogPath, &startNS, &finNS)
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
	res, err := s.db.ExecContext(ctx, `
INSERT INTO task_instances (run_id, task_id, state, try_number, max_retries, pool, priority, executor_ref, log_path, started_at, finished_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		ti.RunID, ti.TaskID, string(ti.State), ti.TryNumber, ti.MaxRetries, ti.Pool, ti.Priority,
		ti.ExecutorRef, ti.LogPath, fmtNullTime(ti.StartedAt), fmtNullTime(ti.FinishedAt))
	if err != nil {
		if isUniqueErr(err) {
			return store.ErrAlreadyExists
		}
		return fmt.Errorf("create task_instance %s/%s: %w", ti.RunID, ti.TaskID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	ti.ID = id
	return nil
}

func (s *Store) GetTaskInstance(ctx context.Context, id int64) (*model.TaskInstance, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+tiCols+` FROM task_instances WHERE id=?`, id)
	return scanTI(row)
}

func (s *Store) ListTaskInstances(ctx context.Context, runID string) ([]*model.TaskInstance, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+tiCols+` FROM task_instances WHERE run_id=? ORDER BY id`, runID)
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
		`SELECT `+tiCols+` FROM task_instances WHERE state=? ORDER BY priority DESC, id`, string(state))
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
UPDATE task_instances SET state=?, try_number=?, max_retries=?, pool=?, priority=?, executor_ref=?, log_path=?, started_at=?, finished_at=?
WHERE id=?`,
		string(ti.State), ti.TryNumber, ti.MaxRetries, ti.Pool, ti.Priority, ti.ExecutorRef, ti.LogPath,
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
UPDATE task_instances SET state=?, try_number=?, max_retries=?, pool=?, priority=?, executor_ref=?, log_path=?, started_at=?, finished_at=?
WHERE id=? AND executor_ref=? AND state NOT IN (`+terminalTaskStates+`)`,
		string(ti.State), ti.TryNumber, ti.MaxRetries, ti.Pool, ti.Priority, ti.ExecutorRef, ti.LogPath,
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
	if _, err := tx.ExecContext(ctx, `DELETE FROM dag_dependencies WHERE downstream_dag=?`, downstream); err != nil {
		return err
	}
	for _, up := range upstreams {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO dag_dependencies (upstream_dag, downstream_dag) VALUES (?, ?)`,
			up, downstream); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListDownstreams(ctx context.Context, upstream string) ([]string, error) {
	return s.queryStrings(ctx, `SELECT downstream_dag FROM dag_dependencies WHERE upstream_dag=? ORDER BY downstream_dag`, upstream)
}

func (s *Store) ListUpstreams(ctx context.Context, downstream string) ([]string, error) {
	return s.queryStrings(ctx, `SELECT upstream_dag FROM dag_dependencies WHERE downstream_dag=? ORDER BY upstream_dag`, downstream)
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
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO pools(name, slots) VALUES (?, ?) ON CONFLICT(name) DO UPDATE SET slots=excluded.slots`,
		p.Name, p.Slots)
	return err
}

func (s *Store) GetPool(ctx context.Context, name string) (*model.Pool, error) {
	var p model.Pool
	err := s.db.QueryRowContext(ctx, `SELECT name, slots FROM pools WHERE name=?`, name).
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
		`SELECT COUNT(*) FROM task_instances WHERE pool=? AND state IN ('queued','running')`, pool).
		Scan(&n)
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
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES (?,?,?)`,
		u.Username, u.PasswordHash, string(u.Role))
	if err != nil {
		return err
	}
	u.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (*model.User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE username=?`, username))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return u, err
}

func (s *Store) GetUserByID(ctx context.Context, id int64) (*model.User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id=?`, id))
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
		`UPDATE users SET password_hash=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, passwordHash, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, id) // revoke on password change
	return err
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, id); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id=?`, id)
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
		`INSERT INTO sessions (token, user_id, expires_at) VALUES (?,?,?)`,
		se.Token, se.UserID, fmtTime(se.ExpiresAt))
	return err
}

func (s *Store) GetSession(ctx context.Context, token string) (*model.Session, error) {
	var se model.Session
	var created, expires string
	err := s.db.QueryRowContext(ctx,
		`SELECT token, user_id, created_at, expires_at FROM sessions WHERE token=?`, token).
		Scan(&se.Token, &se.UserID, &created, &expires)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	se.CreatedAt = parseLoose(created)
	se.ExpiresAt = parseLoose(expires)
	if !se.ExpiresAt.After(time.Now()) { // expired: prune + treat as absent
		_, _ = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token=?`, token)
		return nil, store.ErrNotFound
	}
	return &se, nil
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token=?`, token)
	return err
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, fmtTime(time.Now()))
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
	v, err := scanVariable(s.db.QueryRowContext(ctx, `SELECT key, value, updated_at FROM variables WHERE key=?`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return v, err
}

func (s *Store) UpsertVariable(ctx context.Context, v *model.Variable) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO variables (key, value, updated_at) VALUES (?,?,CURRENT_TIMESTAMP)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=CURRENT_TIMESTAMP`,
		v.Key, v.Value)
	return err
}

func (s *Store) DeleteVariable(ctx context.Context, key string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM variables WHERE key=?`, key)
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
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetConnection(ctx context.Context, id string) (*model.Connection, error) {
	c, err := scanConnection(s.db.QueryRowContext(ctx, `SELECT `+connCols+` FROM connections WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return c, err
}

func (s *Store) UpsertConnection(ctx context.Context, c *model.Connection) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO connections (id, type, host, port, login, password, extra, updated_at)
		 VALUES (?,?,?,?,?,?,?,CURRENT_TIMESTAMP)
		 ON CONFLICT(id) DO UPDATE SET type=excluded.type, host=excluded.host, port=excluded.port,
		   login=excluded.login, password=excluded.password, extra=excluded.extra, updated_at=CURRENT_TIMESTAMP`,
		c.ID, c.Type, c.Host, c.Port, c.Login, c.Password, c.Extra)
	return err
}

func (s *Store) DeleteConnection(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM connections WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}
