# Graph, pools, variables, audit & API tokens

The operations pages of the cronova web console: the cross-DAG dependency graph, global concurrency pools, shared variables and connections, the operator audit trail, and API tokens for machine access. Everything here lives in the sidebar below your DAG list, at `http://localhost:8090`.

## Cross-DAG graph (`#/graph`)

The **Graph** page draws every [`trigger_after`](../tutorial/cross-dag.md) relationship in your installation as one picture — which DAGs fire which other DAGs when they finish.

![cross-DAG graph](../img/graph.png)

How to read it:

| Element | Meaning |
|---|---|
| Arrow | Points in the *trigger-after* direction: upstream DAG → the DAG it triggers on completion. |
| Node color | Tinted by that DAG's latest run state (green success, red failed, blue running, …). Neutral means no runs yet. |
| Solid node | A known DAG — **click it to open that DAG's page** ([Working with a DAG](dag.md)). |
| Dashed node | An unknown DAG: something references it in `trigger_after`, but no DAG with that id exists. Dashed nodes are not clickable. |

Navigate large graphs by **dragging to pan** and zooming with ++ctrl++ / ++cmd++ + mouse wheel — a plain wheel still scrolls the page. The overlay buttons in the corner zoom in (`+`), zoom out (`−`), and fit the whole graph to the viewport (`⤢`).

If no DAG declares `trigger_after`, the page shows an empty state instead of a graph.

## Pools (`#/pools`)

**Pools** are named sets of global concurrency slots, shared across all DAGs and runs. A task occupies one slot of its pool while it executes; when a pool is full, further tasks queue until a slot frees up.

![resource pools](../img/console/pools.png)

The table lists every pool:

| Column | Description |
|---|---|
| Name | Pool id, referenced from task YAML. |
| Slots | Maximum concurrent tasks — an editable number field (minimum 1). |
| Save | Applies the new slot count for that row. |

- **Create a pool**: type a name and a slot count in the toolbar below the table (default 4) and click **Create**.
- **Resize a pool**: change the number in its Slots field and click **Save**.

Tasks opt in with the `pool:` field; every task defaults to the `default` pool, and `priority` decides who wins a contended slot. See [DAG & Task Reference](../DAG_REFERENCE.md#resource-pools) for the YAML side and the [pools tutorial](../tutorial/retries-timeouts-pools.md) for a worked example. You can also manage pools from the CLI with `cronova pools set` ([CLI Reference](../CLI.md)).

## Variables & Connections (`#/resources`)

The **Variables & Connections** page holds configuration shared across tasks, in two tabs. Reference values in task commands as `{{ var.KEY }}` and `{{ conn.ID.field }}`, or pass `{{ params.KEY }}` at trigger time — the [template variables tutorial](../tutorial/variables-connections-params.md) covers all three.

![variables and connections](../img/console/resources.png)

### Variables

Plain key/value pairs, edited inline:

| Column | Description |
|---|---|
| Key | Variable name. Letters, digits, `_`, `.` and `-` only. |
| Value | Editable text field — change it and click **Save** on the same row. |
| Actions | **Save** the row, or **✕** to delete (with confirmation). |

Add a new variable with the key + value inputs below the table and **Add variable**. Use it anywhere templates render, e.g. `Authorization: Bearer {{ var.TOKEN }}`.

### Connections

Named endpoint credentials — databases, APIs, hosts. The list shows each connection's id, type, host:port, login, and whether a password is set (`••••••`); **Edit** opens the dialog, **✕** deletes.

**New connection** opens a dialog with these fields:

| Field | Notes |
|---|---|
| Connection ID | e.g. `mysql_prod`. Fixed after creation (same charset rule as variable keys). |
| Type | Free text, e.g. `mysql`. |
| Host / Port / Login | Endpoint address and user. |
| Password | **Write-only.** On edit it starts blank; leave it blank to keep the stored secret. |
| Extra (JSON) | Arbitrary extra fields as a JSON object, e.g. `{"schema":"prod"}`. |

!!! warning "Passwords are never displayed back"
    The console (and the API) never return a stored connection password — the list only shows *whether* one is set. To rotate a secret, type a new value; to keep it, leave the field blank.

In templates, read connection fields as `{{ conn.ID.host }}`, `.port`, `.login` (alias `.user`), `.password`, `.type`, or `{{ conn.ID.extra.KEY }}` for Extra-JSON keys. `sql` tasks consume a connection directly via their `conn:` field — see [DAG & Task Reference](../DAG_REFERENCE.md).

## Audit (`#/audit`)

The **Audit** page is the operations log: who did what to which DAG or run, and when. It lists the latest 200 entries.

![audit trail](../img/console/audit.png)

| Column | Description |
|---|---|
| Time | When the action happened. |
| Actor | The signed-in username, or `anonymous` when authentication is off. |
| Action | What was done (see below). |
| Target | The DAG id, run id, or token affected, plus a detail suffix (e.g. `task=success` for a mark). |

Recorded actions: **trigger**, **cancel**, **retry run**, **retry task**, **mark task**, **mark run**, **create DAG**, **delete DAG**, **pause**, **unpause**, **create token**, **revoke token**, and project uploads/deletes.

!!! note "Auto-saved edits are not logged"
    The [task editor](task-editor.md) auto-saves on every debounced keystroke, so routine edits to an existing DAG are deliberately *not* audited — only the creation of a genuinely new DAG is. The trail stays meaningful instead of drowning in save events.

## API (`#/api`)

The **API & Integration** page is where you connect other systems to cronova: interactive API docs plus API tokens for machine access.

![API tokens](../img/console/api-tokens.png)

### API reference

- **Open API reference →** opens the interactive docs at `/docs` — a self-contained Redoc page with built-in `curl` / Go / Python / Java samples and an in-page language switcher.
- **OpenAPI spec** serves the raw document at `/openapi.json`, ready to feed into codegen or an HTTP client.

Driving cronova from an AI agent instead? cronova ships a built-in MCP server — see [AI Agents (MCP)](../AGENTS.md).

### API tokens

Tokens are machine credentials. Call any endpoint with the header `Authorization: Bearer <token>`.

| Column | Description |
|---|---|
| Name | Free-form label, e.g. `ci-bot`. |
| Role | **Admin (read-write)** or **Viewer (GET only)**. |
| Prefix | The first characters of the token — the list never shows the full value. |
| Created / Last used | Creation time and last authenticated call (`Never used` until then). |

To **create a token**, enter a name, pick a role, and click **Create token**. To **revoke** one, click **✕** on its row and confirm — revocation is immediate. When authentication is enabled, only admin users can create or revoke tokens.

!!! warning "The token value is shown once"
    The plaintext token appears in a dialog immediately after creation — copy it and store it securely. It is never retrievable again; the list shows only the prefix. If you lose it, revoke the token and create a new one.

## Common questions

**Why is a node dashed in the DAG graph?**
A DAG lists it in `trigger_after`, but no DAG with that id exists (deleted or misspelled). Fix the reference in the upstream DAG's settings, or create the missing DAG.

**Where do I set which pool a task uses?**
In the task's YAML (`pool: reports`) via the [task editor](task-editor.md), not on the Pools page — the Pools page only defines the pools and their slot counts.

**Are variables a safe place for secrets?**
Variable values are displayed in plain text in the console. For credentials, prefer a connection's password field, which is write-only and never echoed back.

**Can a viewer token trigger a DAG run?**
No. Viewer tokens are read-only (GET requests only); triggering, retrying, and editing require an admin token.

## Next steps

- Back to the console overview: [Console](index.md) · [Dashboard & creating DAGs](dashboard.md)
- Operate individual runs: [Runs, logs & recovery](runs-logs.md)
- The full YAML surface: [DAG & Task Reference](../DAG_REFERENCE.md)
