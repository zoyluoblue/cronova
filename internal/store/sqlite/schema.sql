-- cronova SQLite schema. Idempotent: safe to run on every startup.

CREATE TABLE IF NOT EXISTS dags (
    dag_id          TEXT PRIMARY KEY,
    schedule        TEXT,
    timezone        TEXT NOT NULL DEFAULT '', -- IANA zone the cron fields evaluate in ('' = UTC)
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

-- priority: orders competing runs at dispatch (higher first; trigger-time).
CREATE TABLE IF NOT EXISTS dag_runs (
    run_id        TEXT PRIMARY KEY,
    dag_id        TEXT NOT NULL REFERENCES dags(dag_id),
    logical_date  DATETIME NOT NULL,
    state         TEXT NOT NULL,
    trigger_type  TEXT NOT NULL,
    started_at    DATETIME,
    finished_at   DATETIME,
    params          TEXT NOT NULL DEFAULT '', -- JSON map of trigger-time params (recorded per run)
    definition_yaml TEXT NOT NULL DEFAULT '', -- immutable DAG definition used by this run
    definition_hash TEXT NOT NULL DEFAULT '', -- SHA-256 of definition_yaml
    priority        INTEGER NOT NULL DEFAULT 0,
    parent_run_id   TEXT NOT NULL DEFAULT '', -- sub-workflow parent run ('' = top-level)
    held            INTEGER NOT NULL DEFAULT 0, -- operator hold: no new task dispatch
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
    definition_hash TEXT NOT NULL DEFAULT '', -- definition used by the most recent attempt
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

-- Durable scheduler events. Dependency events use the upstream run_id as their
-- key; Migrate adds a unique (source,event_key) index after deduplicating the
-- previously-reserved table used by older releases.
CREATE TABLE IF NOT EXISTS events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    source      TEXT NOT NULL,
    event_key   TEXT NOT NULL,
    payload     TEXT NOT NULL DEFAULT '',
    consumed    INTEGER NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_events_pending ON events(source, consumed, id);

-- Operations audit trail: who (actor) did what (action) to which target, when.
-- actor is a username, or 'anonymous' when auth is disabled.
CREATE TABLE IF NOT EXISTS audit_log (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    ts       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    actor    TEXT NOT NULL DEFAULT '',
    action   TEXT NOT NULL,
    target   TEXT NOT NULL DEFAULT '',
    detail   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit_log(ts DESC);
CREATE INDEX IF NOT EXISTS idx_audit_target ON audit_log(target);

-- API tokens for machine/programmatic access (Authorization: Bearer <token>).
-- Only the SHA-256 hash of the token is stored; the plaintext is shown once at
-- creation. role is 'admin' (full) or 'viewer' (read-only), same as users.
CREATE TABLE IF NOT EXISTS api_tokens (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL,
    role         TEXT NOT NULL DEFAULT 'admin',
    token_hash   TEXT NOT NULL UNIQUE,
    prefix       TEXT NOT NULL DEFAULT '',
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_token_hash ON api_tokens(token_hash);

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
-- revoked on logout). Only a prefixed SHA-256 digest of the cookie token is stored.
CREATE TABLE IF NOT EXISTS sessions (
    token       TEXT PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users(id),
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at  DATETIME NOT NULL
);

-- UI-managed shared configuration. Variables are plain key-value (referenced in
-- task commands as {{ var.KEY }}); connections hold structured credentials
-- ({{ conn.ID.host }} etc.). Passwords are stored as-is and NEVER returned by the
-- API (write-only, masked in the UI). The store encrypts it with AES-256-GCM
-- when key_file is configured; key_file: none is the explicit plaintext mode.
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

-- Named alert fan-out: a DAG's notify.group resolves here to N channels
-- (webhook or mailto: + format), so channel lists live in one place instead of
-- one URL pasted into every DAG.
CREATE TABLE IF NOT EXISTS alert_groups (
    name       TEXT PRIMARY KEY,
    channels   TEXT NOT NULL DEFAULT '[]', -- JSON array of {url, format}
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Remote workers joined via one-time join tokens. The "group" key inside
-- labels is the routing group tasks target with worker_group:.
CREATE TABLE IF NOT EXISTS workers (
    worker_id      TEXT PRIMARY KEY,
    name           TEXT NOT NULL DEFAULT '',
    labels         TEXT NOT NULL DEFAULT '{}', -- JSON string map
    state          TEXT NOT NULL DEFAULT 'offline', -- online | offline | lost
    draining       INTEGER NOT NULL DEFAULT 0, -- 1 = no new assignments
    version        TEXT NOT NULL DEFAULT '',
    active_tasks   INTEGER NOT NULL DEFAULT 0,
    last_heartbeat DATETIME,
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- One-time worker join tokens, hashed at rest (sha256 hex).
CREATE TABLE IF NOT EXISTS worker_join_tokens (
    token_hash TEXT PRIMARY KEY,
    created_by TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME NOT NULL,
    used_at    DATETIME -- non-null = consumed
);

CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_ti_state   ON task_instances(state);
CREATE INDEX IF NOT EXISTS idx_ti_run     ON task_instances(run_id);
CREATE INDEX IF NOT EXISTS idx_ti_pool    ON task_instances(pool, state);
CREATE INDEX IF NOT EXISTS idx_runs_state ON dag_runs(state);
CREATE INDEX IF NOT EXISTS idx_runs_dag   ON dag_runs(dag_id);

-- Single-row scheduler lease: prevents two `cronova serve` processes from
-- scheduling against the same database (which would double-dispatch tasks).
-- The holder renews expires_at on a heartbeat; a stale lease left by a crashed
-- holder is taken over on the next start.
CREATE TABLE IF NOT EXISTS scheduler_lease (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    holder     TEXT NOT NULL,
    expires_at DATETIME NOT NULL
);

-- Per-DAG inbound-webhook secrets: POST /api/hooks/{dag}/{secret} triggers the
-- DAG without a bearer token (the secret IS the credential; only its SHA-256
-- is stored, prefix is for display). One hook per DAG; setting rotates it.
CREATE TABLE IF NOT EXISTS dag_hooks (
    dag_id      TEXT PRIMARY KEY,
    secret_hash TEXT NOT NULL,
    prefix      TEXT NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Task outputs (XCom-equivalent): a small JSON string-map a task emits by
-- writing to the file named in $CRONOVA_OUTPUT. Collected at success-finalize;
-- downstream commands read fields via {{ ti.<task_id>.<key> }} templates.
CREATE TABLE IF NOT EXISTS task_outputs (
    run_id  TEXT NOT NULL,
    task_id TEXT NOT NULL,
    output  TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (run_id, task_id)
);

-- Append-only DAG definition history: one row per distinct definition (same
-- consecutive hash is skipped). Runs link to a version via definition_hash;
-- restore re-registers an old version through the normal validated path.
CREATE TABLE IF NOT EXISTS dag_versions (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    dag_id  TEXT NOT NULL,
    hash    TEXT NOT NULL,
    yaml    TEXT NOT NULL,
    ts      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_dag_versions ON dag_versions(dag_id, id DESC);
