# Connecting an AI agent to cronova

cronova exposes its operations to AI agents two ways, both over the **same
token-authenticated, role-gated REST API** a browser uses — an agent never gets
a privileged side door. Its reach is exactly its token's role (`admin` = full
CRUD + operate; `viewer` = read-only).

1. **MCP server** (`cronova mcp`) — native tools for Claude Code / Claude Desktop
   or any MCP host.
2. **Remote CLI** (`cronova <verb> -server … -token …` or `cronova api …`) — for
   agents that shell out and read JSON.

Both need a running `cronova serve` and an API token.

---

## 1. Mint a token

Run this **on the cronova server host** (it writes to the local DB — the bootstrap
path, since you need a token to reach the API):

```bash
cronova tokens create my-agent -role admin      # or -role viewer for read-only
#   cnv_pat_XXXXXXXXXXXXXXXXXXXX     ← shown once; store it
cronova tokens list
cronova tokens delete <id>
```

Give the agent the printed token as `CRONOVA_TOKEN`.

---

## 2. MCP server (recommended for Claude)

`cronova mcp` speaks the Model Context Protocol over stdio and exposes ~30 tools
(`list_dags`, `create_dag`, `validate_dag`, `trigger_dag`, `get_run`,
`get_task_log`, `cancel_run`, `retry_task`, `mark_run`, `set_variable`,
`list_projects`, …) — all **derived from the same catalog as the OpenAPI spec**,
so they can never drift from the API.

Register it with your MCP client. Claude Code (`~/.claude/mcp.json` or project
`.mcp.json`) / Claude Desktop:

```json
{
  "mcpServers": {
    "cronova": {
      "command": "cronova",
      "args": ["mcp"],
      "env": {
        "CRONOVA_SERVER": "http://localhost:8090",
        "CRONOVA_TOKEN": "cnv_pat_XXXXXXXXXXXXXXXXXXXX"
      }
    }
  }
}
```

Flags: `cronova mcp -server URL -token T -read-only`. **`-read-only` exposes only
the read (GET) tools** — a safe default for an observe-only agent (belt-and-suspenders
on top of a `viewer` token). STDOUT carries the protocol; logs go to STDERR.

The agent can then, in plain language, "list failing runs of etl_daily", "show
the log of the failed task", "retry it", or "validate this DAG then create it".

---

## 3. Remote CLI (for shell-out agents / scripts)

Point any command at a server with `-server`/`-token` (or `CRONOVA_SERVER` /
`CRONOVA_TOKEN`) and ask for JSON with `-o json`:

```bash
export CRONOVA_SERVER=http://localhost:8090 CRONOVA_TOKEN=cnv_pat_…

cronova dags -o json                          # list DAGs
cronova get etl_daily -o json                 # one DAG definition
cronova trigger etl_daily -params '{"day":"2026-01-01"}' -o json
cronova runs etl_daily -o json                # recent runs
cronova run <run_id>                          # a run + task states
cronova logs <task_instance_id>               # a task's log (text)
cronova cancel <run_id>
cronova retry <run_id> [task_id]
cronova mark <run_id> [task_id] success       # operator override
cronova pause etl_daily        (-off to resume)
cronova overview                              # dashboard summary
```

### The `api` escape hatch

Every endpoint is reachable directly — the full surface without a per-verb
subcommand:

```bash
cronova api GET  /api/dags
cronova api POST /api/dags/validate '{"dag_id":"x","tasks":[{"id":"a","type":"shell","command":"echo hi"}]}'
cronova api POST /api/dags/build    '{"dag_id":"x","tasks":[{"id":"a","type":"shell","command":"echo hi"}]}'
```

A non-2xx prints the error body and exits non-zero. The machine-readable OpenAPI
spec is at `GET /openapi.json` (human docs at `/docs`).

---

## Authoring loop (validate before create)

`validate_dag` (MCP) / `POST /api/dags/validate` runs the **same** cycle / dep /
cron / id checks as create but **persists nothing** and returns structured
feedback — so an agent can iterate on a generated DAG before committing it:

```bash
cronova api POST /api/dags/validate '{"dag_id":"x","tasks":[…]}'
#  {"valid": false, "error": "dependency cycle detected: [a b a]", "canonical_yaml": "…"}
```

---

## Security notes

- **Least privilege:** hand read-only agents a `viewer` token (and/or run
  `cronova mcp -read-only`). Writes (create/delete/trigger/mark/…) require an
  `admin` token — enforced server-side, the same gate as the console.
- **Tokens are minted locally**, never via the API/MCP — an agent can't escalate
  by creating itself a stronger token.
- **HTTPS in production:** put cronova behind TLS and use an `https://` server URL;
  the token is a bearer credential.
- **Audit:** create/trigger/cancel/mark/… land in the audit log (`GET /api/audit`)
  with the token's identity.
```
