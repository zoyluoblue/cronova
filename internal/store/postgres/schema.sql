-- cronova PostgreSQL schema. Idempotent: safe to run on every startup.
--
-- Time-valued columns are deliberately TEXT, not timestamptz: the Go store
-- writes RFC3339Nano UTC strings and relies on lexicographic ordering equaling
-- time ordering (same as the SQLite store). Keeping TEXT preserves exact
-- behavioral parity (string equality lookups on logical_date, MAX() over text,
-- range comparisons against Go-formatted cutoffs) across both backends.
-- Boolean-valued columns stay INTEGER because the code writes 0/1.

CREATE TABLE IF NOT EXISTS dags (
    dag_id          TEXT PRIMARY KEY,
    schedule        TEXT,
    timezone        TEXT NOT NULL DEFAULT '', -- IANA zone the cron fields evaluate in ('' = UTC)
    start_date      TEXT,
    catchup         INTEGER NOT NULL DEFAULT 0,
    paused          INTEGER NOT NULL DEFAULT 0,
    max_active_runs INTEGER NOT NULL DEFAULT 1,
    definition_yaml TEXT NOT NULL DEFAULT '',
    owner           TEXT NOT NULL DEFAULT '',
    project         TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL DEFAULT to_char(now() at time zone 'utc', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
    updated_at      TEXT NOT NULL DEFAULT to_char(now() at time zone 'utc', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
    deleted_at      TEXT -- NULL = active; non-null = soft-deleted (archived, recoverable)
);

CREATE TABLE IF NOT EXISTS dag_runs (
    run_id        TEXT PRIMARY KEY,
    dag_id        TEXT NOT NULL REFERENCES dags(dag_id),
    logical_date  TEXT NOT NULL,
    state         TEXT NOT NULL,
    trigger_type  TEXT NOT NULL,
    started_at    TEXT,
    finished_at   TEXT,
    params          TEXT NOT NULL DEFAULT '', -- JSON map of trigger-time params (recorded per run)
    definition_yaml TEXT NOT NULL DEFAULT '', -- immutable DAG definition used by this run
    definition_hash TEXT NOT NULL DEFAULT '', -- SHA-256 of definition_yaml
    priority        INTEGER NOT NULL DEFAULT 0, -- orders competing runs at dispatch (higher first)
    parent_run_id   TEXT NOT NULL DEFAULT '',
    UNIQUE (dag_id, logical_date)
);

CREATE TABLE IF NOT EXISTS task_instances (
    id            BIGSERIAL PRIMARY KEY,
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
    started_at    TEXT,
    finished_at   TEXT,
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
    id          BIGSERIAL PRIMARY KEY,
    source      TEXT NOT NULL,
    event_key   TEXT NOT NULL,
    payload     TEXT NOT NULL DEFAULT '',
    consumed    INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL DEFAULT to_char(now() at time zone 'utc', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
);
CREATE INDEX IF NOT EXISTS idx_events_pending ON events(source, consumed, id);

-- Operations audit trail: who (actor) did what (action) to which target, when.
-- actor is a username, or 'anonymous' when auth is disabled.
CREATE TABLE IF NOT EXISTS audit_log (
    id       BIGSERIAL PRIMARY KEY,
    ts       TEXT NOT NULL DEFAULT to_char(now() at time zone 'utc', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
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
    id           BIGSERIAL PRIMARY KEY,
    name         TEXT NOT NULL,
    role         TEXT NOT NULL DEFAULT 'admin',
    token_hash   TEXT NOT NULL UNIQUE,
    prefix       TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL DEFAULT to_char(now() at time zone 'utc', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
    last_used_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_token_hash ON api_tokens(token_hash);

-- Console/API accounts. Passwords are PBKDF2-HMAC-SHA256 hashes (never plaintext). role is
-- 'admin' (full access) or 'viewer' (read-only). Auth is opt-in (auth.enabled).
CREATE TABLE IF NOT EXISTS users (
    id            BIGSERIAL PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL DEFAULT 'viewer',
    created_at    TEXT NOT NULL DEFAULT to_char(now() at time zone 'utc', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
    updated_at    TEXT NOT NULL DEFAULT to_char(now() at time zone 'utc', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
);

-- Opaque server-side sessions (DB-backed so they survive restart and can be
-- revoked on logout). Only a prefixed SHA-256 digest of the cookie token is stored.
CREATE TABLE IF NOT EXISTS sessions (
    token       TEXT PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users(id),
    created_at  TEXT NOT NULL DEFAULT to_char(now() at time zone 'utc', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
    expires_at  TEXT NOT NULL
);

-- UI-managed shared configuration. Variables are plain key-value (referenced in
-- task commands as {{ var.KEY }}); connections hold structured credentials
-- ({{ conn.ID.host }} etc.). Passwords are stored as-is and NEVER returned by the
-- API (write-only, masked in the UI). The store encrypts it with AES-256-GCM
-- when key_file is configured; key_file: none is the explicit plaintext mode.
CREATE TABLE IF NOT EXISTS workers (
    worker_id      TEXT PRIMARY KEY,
    name           TEXT NOT NULL DEFAULT '',
    labels         TEXT NOT NULL DEFAULT '{}', -- JSON string map; "group" = routing group
    state          TEXT NOT NULL DEFAULT 'offline', -- online | offline | lost
    draining       BOOLEAN NOT NULL DEFAULT FALSE,
    version        TEXT NOT NULL DEFAULT '',
    active_tasks   INTEGER NOT NULL DEFAULT 0,
    last_heartbeat TEXT,
    created_at     TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS worker_join_tokens (
    token_hash TEXT PRIMARY KEY, -- sha256 hex; one-time
    created_by TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT '',
    expires_at TEXT NOT NULL,
    used_at    TEXT -- non-null = consumed
);

CREATE TABLE IF NOT EXISTS alert_groups (
    name       TEXT PRIMARY KEY,
    channels   TEXT NOT NULL DEFAULT '[]', -- JSON array of {url, format}
    updated_at TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS variables (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT to_char(now() at time zone 'utc', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
);

CREATE TABLE IF NOT EXISTS connections (
    id         TEXT PRIMARY KEY,
    type       TEXT NOT NULL DEFAULT '',
    host       TEXT NOT NULL DEFAULT '',
    port       INTEGER NOT NULL DEFAULT 0,
    login      TEXT NOT NULL DEFAULT '',
    password   TEXT NOT NULL DEFAULT '',
    extra      TEXT NOT NULL DEFAULT '', -- JSON map of extra fields ({{ conn.ID.extra.KEY }})
    updated_at TEXT NOT NULL DEFAULT to_char(now() at time zone 'utc', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
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
    expires_at TEXT NOT NULL
);

-- Per-DAG inbound-webhook secrets: POST /api/hooks/{dag}/{secret} triggers the
-- DAG without a bearer token (the secret IS the credential; only its SHA-256
-- is stored, prefix is for display). One hook per DAG; setting rotates it.
CREATE TABLE IF NOT EXISTS dag_hooks (
    dag_id      TEXT PRIMARY KEY,
    secret_hash TEXT NOT NULL,
    prefix      TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT to_char(now() at time zone 'utc', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
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
    id      BIGSERIAL PRIMARY KEY,
    dag_id  TEXT NOT NULL,
    hash    TEXT NOT NULL,
    yaml    TEXT NOT NULL,
    ts      TEXT NOT NULL DEFAULT to_char(now() at time zone 'utc', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
);
CREATE INDEX IF NOT EXISTS idx_dag_versions ON dag_versions(dag_id, id DESC);
