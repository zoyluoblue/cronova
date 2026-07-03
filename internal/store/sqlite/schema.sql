-- cronova SQLite schema. Idempotent: safe to run on every startup.

CREATE TABLE IF NOT EXISTS dags (
    dag_id          TEXT PRIMARY KEY,
    schedule        TEXT,
    start_date      DATETIME,
    catchup         INTEGER NOT NULL DEFAULT 0,
    paused          INTEGER NOT NULL DEFAULT 0,
    max_active_runs INTEGER NOT NULL DEFAULT 1,
    definition_yaml TEXT NOT NULL DEFAULT '',
    owner           TEXT NOT NULL DEFAULT '',
    project         TEXT NOT NULL DEFAULT '',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at      DATETIME -- NULL = active; non-null = soft-deleted (archived, recoverable)
);

CREATE TABLE IF NOT EXISTS dag_runs (
    run_id        TEXT PRIMARY KEY,
    dag_id        TEXT NOT NULL REFERENCES dags(dag_id),
    logical_date  DATETIME NOT NULL,
    state         TEXT NOT NULL,
    trigger_type  TEXT NOT NULL,
    started_at    DATETIME,
    finished_at   DATETIME,
    params        TEXT NOT NULL DEFAULT '', -- JSON map of trigger-time params (recorded per run)
    UNIQUE (dag_id, logical_date)
);

CREATE TABLE IF NOT EXISTS task_instances (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id        TEXT NOT NULL REFERENCES dag_runs(run_id),
    task_id       TEXT NOT NULL,
    state         TEXT NOT NULL,
    try_number    INTEGER NOT NULL DEFAULT 0,
    max_retries   INTEGER NOT NULL DEFAULT 0,
    pool          TEXT NOT NULL DEFAULT 'default',
    priority      INTEGER NOT NULL DEFAULT 0,
    executor_ref  TEXT NOT NULL DEFAULT '',
    log_path      TEXT NOT NULL DEFAULT '',
    started_at    DATETIME,
    finished_at   DATETIME,
    UNIQUE (run_id, task_id)
);

CREATE TABLE IF NOT EXISTS pools (
    name   TEXT PRIMARY KEY,
    slots  INTEGER NOT NULL
);

-- No FK to dags: a DAG may declare trigger_after on an upstream that is loaded
-- later (or not at all). A dangling upstream simply never fires the downstream.
CREATE TABLE IF NOT EXISTS dag_dependencies (
    upstream_dag    TEXT NOT NULL,
    downstream_dag  TEXT NOT NULL,
    PRIMARY KEY (upstream_dag, downstream_dag)
);

CREATE TABLE IF NOT EXISTS events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    source      TEXT NOT NULL,
    event_key   TEXT NOT NULL,
    payload     TEXT NOT NULL DEFAULT '',
    consumed    INTEGER NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Console/API accounts. Passwords are PBKDF2-HMAC-SHA256 hashes (never plaintext). role is
-- 'admin' (full access) or 'viewer' (read-only). Auth is opt-in (auth.enabled).
CREATE TABLE IF NOT EXISTS users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL DEFAULT 'viewer',
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Opaque server-side sessions (DB-backed so they survive restart and can be
-- revoked on logout). token is a random 256-bit value stored in an httpOnly cookie.
CREATE TABLE IF NOT EXISTS sessions (
    token       TEXT PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users(id),
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at  DATETIME NOT NULL
);

-- UI-managed shared configuration. Variables are plain key-value (referenced in
-- task commands as {{ var.KEY }}); connections hold structured credentials
-- ({{ conn.ID.host }} etc.). Passwords are stored as-is and NEVER returned by the
-- API (write-only, masked in the UI) — protect the DB file with filesystem perms.
CREATE TABLE IF NOT EXISTS variables (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS connections (
    id         TEXT PRIMARY KEY,
    type       TEXT NOT NULL DEFAULT '',
    host       TEXT NOT NULL DEFAULT '',
    port       INTEGER NOT NULL DEFAULT 0,
    login      TEXT NOT NULL DEFAULT '',
    password   TEXT NOT NULL DEFAULT '',
    extra      TEXT NOT NULL DEFAULT '', -- JSON map of extra fields ({{ conn.ID.extra.KEY }})
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_ti_state   ON task_instances(state);
CREATE INDEX IF NOT EXISTS idx_ti_run     ON task_instances(run_id);
CREATE INDEX IF NOT EXISTS idx_ti_pool    ON task_instances(pool, state);
CREATE INDEX IF NOT EXISTS idx_runs_state ON dag_runs(state);
CREATE INDEX IF NOT EXISTS idx_runs_dag   ON dag_runs(dag_id);
