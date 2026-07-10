# cronova CLI Reference

Every `cronova` command, subcommand, and flag — the single binary that runs the workflow scheduler, manages the installed service, operates DAGs from the terminal, and serves AI agents over the REST API. Run `cronova <command> -h` for any command's own flags. For first steps see [Getting Started](GETTING_STARTED.md); for the DAG YAML schema see the [DAG Reference](DAG_REFERENCE.md).

```
cronova <command> [args] [flags]
```

Commands fall into four groups:

| Group | Commands | Where they act |
|---|---|---|
| [Scheduler](#scheduler) | `serve`, `cronova-executor` | This machine (long-running processes) |
| [Service lifecycle](#service-lifecycle) | `start`/`stop`/`restart`/`status`, `init`, `update`, `uninstall`, `version`, `healthcheck` | The host service manager (systemd / launchd) |
| [Local operations](#local-operations) | `trigger`, `dags`, `runs`, `backfill`, `prune`, `pools`, `users` | The SQLite DB directly (`-db`) — or remote with `-server` |
| [Remote / agent mode](#remote--agent-mode) | `api`, `get`, `run`, `logs`, `cancel`, `retry`, `mark`, `pause`, `overview`, `tokens`, `mcp` | A running server's authenticated REST API |

## Scheduler

### `cronova serve`

Run the scheduling loop plus the web console and REST API (default `http://localhost:8090`). This is the whole server — cron parsing, catchup, retries, task execution, UI, and API in one process.

```bash
cronova serve -db data/cronova.db -dags dags -http 127.0.0.1:8090
```

| Flag | Default | Description |
|---|---|---|
| `-http` | `127.0.0.1:8090` | HTTP address for the console + API (empty to disable). |
| `-db` | `data/cronova.db` | SQLite metadata database path. |
| `-dags` | `dags` | Directory of DAG YAML definitions. |
| `-logs` | `logs` | Directory for task log files. |
| `-projects` | `~/.cronova/projects` | Directory for uploaded [project files](tutorial/projects.md). |
| `-executor` | *(in-process)* | Absolute Unix-socket executor target. Empty = in-process executor; TCP targets are rejected. |
| `-tick` | `2s` | Scheduling-loop interval. |
| `-retention` | `2160h` (90 days) | Delete finished runs **and their logs** older than this; `0` = keep forever. See [`cronova prune`](#cronova-prune) for one-off cleanups. |
| `-auth` | off | Require login for the console/API (overrides config). |
| `-allow-unauthenticated-remote` | off | **Dangerous:** permit auth-off serving on a non-loopback address. |
| `-config` | `cronova.yaml` | Path to a YAML config file (optional). |
| `key_file` / `CRONOVA_KEY_FILE` | `cronova.key` | Config/env only (no flag): key file that encrypts connection passwords at rest. Auto-generated (`0600`) on first `serve` — back it up; losing it makes stored passwords unreadable. `none` disables encryption (plaintext, with a startup warning). |

Settings resolve in order: built-in defaults ← config file ← `CRONOVA_*` environment ← explicit flags. `CRONOVA_WEB_DIR` (dev only) serves the console assets from disk instead of the embedded copies.

### `cronova-executor` (separate binary)

The standalone, crash-recoverable task executor. Run it first, then point the scheduler at its socket with `serve -executor` — tasks survive a scheduler restart. See [Architecture](ARCHITECTURE.md).

```bash
cronova-executor &
cronova serve -executor "unix:///tmp/cronova-$(id -u)/executor.sock"
```

| Flag | Default | Description |
|---|---|---|
| `-sock` | `/tmp/cronova-<uid>/executor.sock` | Unix socket path. Its parent must be private (`0700`); the socket is forced to `0600`. |

The executor API has no separate credentials. Filesystem ownership is its trust
boundary, so cronova accepts only absolute Unix sockets and refuses a public
socket directory or any TCP target.

## Service lifecycle

These wrap the host service manager — systemd on Linux, launchd on macOS — so you never type `systemctl`/`launchctl` incantations.

!!! note "Auto-sudo"
    Mutating commands (`start`, `stop`, `restart`, `update`, `uninstall`) auto-elevate: if you are not root, the CLI transparently re-executes itself under `sudo` (you get the password prompt). `CRONOVA_*` and standard `*_PROXY` environment variables are forwarded across the escalation. Set `CRONOVA_NO_SUDO=1` to opt out and manage privileges yourself — the command then fails with a `sudo cronova …` hint instead.

### `cronova start` / `stop` / `restart`

Control the installed service. `start` and `restart` confirm the daemon actually *stays* running (not just that it loaded) before reporting success — a crash-looping binary is an error, not a green light.

```bash
cronova restart
```

No flags. On a host without an installed service, use `cronova serve` directly.

### `cronova status`

Show the installed service's status. Read-only — never escalates. On Linux it runs `systemctl status cronova`; on macOS, `launchctl print system/com.cronova`.

```bash
cronova status
```

### `cronova init`

First-time setup wizard: HTTP port, bind scope (all interfaces vs. `127.0.0.1`), admin account, and auth on/off — each with an Enter-to-accept default. Writes the server config (`cronova.yaml`) and a `0600` secrets file (`cronova.env`) with the seed admin; `serve` creates that admin idempotently on first start. Re-running shows your current values as the defaults.

```bash
cronova init          # interactive
cronova init -yes     # accept defaults / CRONOVA_* env, no prompts
```

| Flag | Default | Description |
|---|---|---|
| `-config` | `cronova.yaml` | Config file to write (env `CRONOVA_CONFIG`). |
| `-env` | `cronova.env` | Secrets file to write — the admin seed (env `CRONOVA_ENV_FILE`). |
| `-yes` | | Non-interactive: accept defaults / env without prompting. |

Non-interactive installs preset values with `CRONOVA_ADMIN_USER`, `CRONOVA_ADMIN_PASSWORD`, `CRONOVA_AUTH`, `CRONOVA_HTTP`, etc. A fresh install defaults auth to **on**; an unrecognized `CRONOVA_AUTH` value never silently disables it.

### `cronova update`

Download a prebuilt release from GitHub, require and verify its SHA256 checksum, atomically swap the binary (and `cronova-executor`, if installed), refresh the service definition (unit/plist), and restart the service. Missing checksum metadata aborts the update.

```bash
cronova update                               # latest release
cronova update v0.2.0                        # pin a tag (re-install / downgrade)
cronova update -proxy http://127.0.0.1:7890  # download through a proxy
```

| Flag / Env | Description |
|---|---|
| `-proxy <url>` | Proxy for the download: `http(s)://host:port` or `socks5://host:port`. |
| `CRONOVA_UPDATE_PROXY` | Same as `-proxy`; also honors `HTTPS_PROXY` / `ALL_PROXY`. All survive the sudo escalation. |
| `CRONOVA_BASE_URL` | Override the download origin (private mirror / testing). |

If the restarted service does not stay up on the new binary, `update` **rolls back automatically**: the previous binaries and service definition are restored and the old version is restarted — the box is never left on a half-applied update. An unpinned `update` that is already current short-circuits with `already up to date`; a pinned version is always applied. See [Deployment → Updating](DEPLOY.md#updating).

### `cronova uninstall`

Remove the service and binaries. Config, database, DAGs, and logs are kept by default — re-installing brings the deployment back.

```bash
cronova uninstall            # keeps data (asks for confirmation)
cronova uninstall --purge    # also delete config, DB, DAGs, and logs
cronova uninstall -yes       # skip the confirmation prompt (scripts)
```

| Flag | Description |
|---|---|
| `--purge` | Also delete config, database, DAG, and log directories. |
| `-yes` | Skip the confirmation prompt. |

### `cronova version`

Print the build version and platform (the release asset `update` would fetch for this host).

```console
$ cronova version
cronova v0.3.0 darwin/arm64
```

### `cronova healthcheck`

Probe the server's readiness endpoint and exit non-zero if unhealthy — a curl-free liveness check for systemd, load balancers, or cron probes.

```bash
cronova healthcheck -http 127.0.0.1:8090 && echo healthy
```

| Flag | Default | Description |
|---|---|---|
| `-http` | `127.0.0.1:8090` | Server HTTP address (env `CRONOVA_HTTP`). |
| `-path` | `/readyz` | Path to probe. |

## Local operations

Run on the machine that holds the database; they act directly on the SQLite DB. All accept `-db` (default `data/cronova.db`), and most also take the [global `-server`/`-token`/`-o` flags](#global-flags-and-environment) — give `-server` and the same command goes over the REST API instead (`backfill` and `prune` are local-only).

### `cronova trigger`

Create a manual run of a DAG, optionally with trigger params (available in tasks as [template variables](tutorial/template-variables.md)).

```console
$ cronova trigger example_etl -params '{"day":"2026-01-01"}'
created run example_etl__manual_1783442227904284000 (a running `cronova serve` will execute it)
```

| Flag | Default | Description |
|---|---|---|
| `-params` | | Trigger params as a JSON object of string values, e.g. `'{"day":"2026-01-01"}'`. |
| `-db` / `-dags` | `data/cronova.db` / `dags` | Local DB and DAG directory. |

The run is queued in the database; a running `cronova serve` picks it up and executes it.

### `cronova dags`

List registered DAGs. In local mode it loads the DAG directory from disk first, so freshly added YAML files show up even before `serve` runs.

```console
$ cronova dags
DAG_ID             SCHEDULE   CATCHUP  PAUSED  MAX_ACTIVE
downstream_report  (manual)   false    true    1
example_etl        (manual)   false    false   1
ticker             @every 1m  false    true    1
upstream_ingest    (manual)   false    false   1
```

| Flag | Default | Description |
|---|---|---|
| `-db` / `-dags` | `data/cronova.db` / `dags` | Local DB and DAG directory. |

### `cronova runs`

Show a DAG's recent runs with per-task states.

```console
$ cronova runs example_etl -n 3
RUN_ID                                   LOGICAL_DATE          STATE    TRIGGER  TASKS
example_etl__manual_1783442227904284000  2026-07-07T16:37:07Z  success  manual   extract=success transform=success validate=success load=success
example_etl__manual_1783442223878514000  2026-07-07T16:37:03Z  success  manual   extract=success transform=success validate=success load=success
example_etl__manual_1783442199726456000  2026-07-07T16:36:39Z  success  manual   extract=success transform=success validate=success load=success
```

| Flag | Default | Description |
|---|---|---|
| `-n` | `10` | Number of recent runs to show. |
| `-db` | `data/cronova.db` | SQLite database path. |

In remote mode the table omits the `TASKS` column (the runs endpoint returns runs only); use `cronova run <run_id>` for one run's task states. The underlying `GET /api/dags/{id}/runs` endpoint also accepts `state=` (comma-separated, e.g. `state=failed,cancelled` — unknown names are rejected) and `offset=` for filtering and paging; reach them with [`cronova api`](#cronova-api).

### `cronova backfill`

Enqueue one queued run per schedule period in a date window — re-run history after a bug fix, or load past periods for a newly added DAG. Periods that already have a run (in **any** state) are skipped, so re-running a backfill never double-runs anything; `to` is clamped to now (future periods belong to the scheduler); a window covering more than 500 periods is rejected outright. Execution is throttled by the DAG's `max_active_runs`, exactly like catchup.

```console
$ cronova backfill daily_etl -from 2026-07-01 -to 2026-07-05
backfill daily_etl: created 5 run(s), skipped 0 existing (a running `cronova serve` executes them)
```

| Flag | Default | Description |
|---|---|---|
| `-from` / `-to` | *(required)* | Window start / end, `YYYY-MM-DD`, both inclusive. |
| `-db` / `-dags` | `data/cronova.db` / `dags` | Local DB and DAG directory. |

The DAG must have a `schedule` — backfill enumerates schedule periods. Local-only; against a remote server call the API, which also accepts RFC3339 timestamps: `cronova api POST /api/dags/daily_etl/backfill '{"from":"2026-07-01","to":"2026-07-05"}'`.

### `cronova prune`

Delete finished runs — DB rows plus their log directories — older than a retention window. The manual counterpart of [`serve -retention`](#cronova-serve), for one-off cleanups or deployments that run with retention disabled. Local-only; asks for confirmation unless `-yes`.

```bash
cronova prune                    # finished runs older than 90 days (asks first)
cronova prune -older-than 720h   # custom window (30 days)
cronova prune -yes               # no confirmation (scripts / cron)
```

| Flag | Default | Description |
|---|---|---|
| `-older-than` | `2160h` (90 days) | Delete finished runs older than this (must be positive). |
| `-yes` | | Skip the confirmation prompt. |
| `-db` / `-logs` | `data/cronova.db` / `logs` | Local DB and log directory. |

### `cronova pools`

List [resource pools](tutorial/retries-timeouts-pools.md), or create/resize one with `pools set`.

```console
$ cronova pools
NAME     SLOTS
default  16
reports  4
$ cronova pools set reports 8
pool "reports" set to 8 slots
```

| Usage | Description |
|---|---|
| `cronova pools` | List pools and their slot counts. |
| `cronova pools set <name> <slots>` | Create or resize a pool (`slots` must be a positive integer). |

### `cronova users`

Manage web console accounts. Local-only — account admin is a server-host operation.

```bash
cronova users list
cronova users add alice -role viewer -password s3cret
cronova users passwd alice          # prompts for the new password
cronova users delete alice
```

| Subcommand | Flags | Description |
|---|---|---|
| `list` | | List accounts with role and creation time. |
| `add <name>` | `-role admin\|viewer` (default `viewer`), `-password` | Create an account. |
| `passwd <name>` | `-password` | Change a password — existing sessions are revoked. |
| `delete <name>` | | Remove an account. |

When `-password` is omitted, the password is read from stdin (prompted). `-db` also honors `CRONOVA_DB`.

## Remote / agent mode

Drive a running server over its **token-authenticated, role-gated REST API** — the same path the browser console uses. This is how scripts, CI, and AI agents operate cronova from anywhere. Full agent guide: [AI Agents (MCP)](AGENTS.md).

### Global flags and environment

Every operational command accepts these; set them once as environment variables for a session.

| Global flag | Env | Description |
|---|---|---|
| `-server <url>` | `CRONOVA_SERVER` | Server URL, e.g. `http://localhost:8090`. Empty = local DB. |
| `-token <token>` | `CRONOVA_TOKEN` | API token (mint with [`cronova tokens create`](#cronova-tokens)). |
| `-o table\|json` | `CRONOVA_OUTPUT` | Output format; `json` for scripting and agents. |

```bash
export CRONOVA_SERVER=http://localhost:8090 CRONOVA_TOKEN=cnv_pat_…
cronova dags -o json
```

!!! tip "Exit codes for scripts"
    Commands exit non-zero on API errors (the error body is printed first), so `cronova trigger etl && …` chains safely in CI.

### `cronova api`

Raw passthrough to any REST endpoint — the escape hatch that exposes the full API surface without a per-endpoint subcommand. JSON responses are pretty-printed.

```bash
cronova api GET  /api/dags
cronova api POST /api/dags/etl/trigger '{"params":{"day":"2026-01-01"}}'
```

Usage: `cronova api <METHOD> <path> [json-body]`.

### `cronova get`

Show a DAG definition (`GET /api/dags/{id}`).

```bash
cronova get example_etl
```

### `cronova run`

Show one run and its task states (`GET /api/runs/{runID}`) — the remote counterpart to `runs`' per-task detail.

```bash
cronova run example_etl__manual_1783442227904284000
```

### `cronova logs`

Fetch a task instance's log as plain text. Get the task instance ID from `cronova run` or the run detail page in the web console.

```bash
cronova logs 42
```

### `cronova cancel`

Cancel an active run.

```bash
cronova cancel example_etl__manual_1783442227904284000
```

### `cronova retry`

Retry a run's failed tasks, or a single task.

```bash
cronova retry example_etl__manual_1783442227904284000            # all failed tasks
cronova retry example_etl__manual_1783442227904284000 transform  # one task
```

### `cronova mark`

Operator override of a run or task state — skip a known-bad task, force a run green after a manual fix.

```bash
cronova mark <run_id> success                # mark the run:  success | failed
cronova mark <run_id> <task_id> skipped      # mark one task: success | failed | skipped
```

| Target | Valid states |
|---|---|
| Run (`mark <run_id> <state>`) | `success`, `failed` |
| Task (`mark <run_id> <task_id> <state>`) | `success`, `failed`, `skipped` |

### `cronova pause`

Pause a DAG's scheduling, or resume it with `-off`. Paused DAGs skip their cron schedule but can still be triggered manually.

```bash
cronova pause ticker
cronova pause ticker -off   # resume
```

### `cronova overview`

Dashboard summary — DAG counts, active runs, and pool usage in one call (`GET /api/overview`). Pair with `-o json` for monitoring scripts.

```bash
cronova overview -o json
```

### `cronova tokens`

Provision and manage API tokens. **`create` is local-only by design** — it writes directly to the SQLite store, because the first token cannot come from the API it unlocks (and token admin stays a server-host operation). `list`/`delete` are local too.

```console
$ cronova tokens create ci-bot -role admin
created admin token "ci-bot"

  cnv_pat_3JLKxC…

Store it now — it is not shown again. Use it with:
  export CRONOVA_TOKEN=cnv_pat_3JLKxC…

$ cronova tokens list
ID  NAME        ROLE    PREFIX           LAST_USED
3   docs-demo   viewer  cnv_pat_eP6UPK…  never
2   deploy-bot  admin   cnv_pat_FhFJ2V…  never
1   ci-bot      admin   cnv_pat_3JLKxC…  never
```

| Subcommand | Flags | Description |
|---|---|---|
| `create <name>` | `-role admin\|viewer` (default `admin`), `-db` | Mint a token. The plaintext is shown once; only a hash is stored. |
| `list` | `-db` | List tokens with role, prefix, and last-used time. |
| `delete <id>` | `-db` | Revoke a token by ID. |

### `cronova mcp`

Run a [Model Context Protocol](https://modelcontextprotocol.io/) server over stdio, exposing cronova's operations as tools for AI clients (Claude Code, Claude Desktop, any MCP host). It talks to a running server through the REST API, so the AI's reach is exactly its token's role. Stdout carries the protocol; logs go to stderr.

```bash
CRONOVA_TOKEN=cnv_pat_… cronova mcp -read-only
```

| Flag | Env | Description |
|---|---|---|
| `-server <url>` | `CRONOVA_SERVER` | Server URL (default `http://localhost:8090`). |
| `-token <token>` | `CRONOVA_TOKEN` | API token — warns at startup if unset. |
| `-read-only` | | Expose only the read (GET) tools. |

Full guide, MCP config snippet, and security notes: [AI Agents (MCP)](AGENTS.md).

## Common questions

**Do local commands need the server running?** No — `trigger`, `dags`, `runs`, `backfill`, `prune`, `pools`, `users`, and `tokens` act on the SQLite DB directly. Runs queued by `trigger` or `backfill` execute once `serve` is running.

**How do I run the CLI from another machine?** Set `CRONOVA_SERVER` and `CRONOVA_TOKEN` (or `-server`/`-token`); every operational command then goes over the REST API. Token minting stays on the server host.

**Why did `cronova start` ask for my password?** Service commands auto-elevate via `sudo` to reach systemd/launchd. Set `CRONOVA_NO_SUDO=1` to disable this and run `sudo cronova start` yourself.

**What states can `mark` set?** Runs: `success` or `failed`. Tasks: `success`, `failed`, or `skipped`.

## See also

- [Getting Started](GETTING_STARTED.md) · [DAG Reference](DAG_REFERENCE.md) · [AI Agents (MCP)](AGENTS.md) · [Deployment](DEPLOY.md) · [FAQ](FAQ.md)
