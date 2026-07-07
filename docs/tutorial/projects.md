# Run your own scripts & projects

One-line `echo` commands only get you so far — real pipelines are a script, or a whole folder of code and data files. This chapter shows how to upload that code to cronova as a **project** and run it from a task, with a clean isolated copy per attempt.

## Why projects

A `shell` task runs its `command` from the scheduler's working directory, so `python3 main.py` fails unless `main.py` happens to live there. You *could* hardcode absolute paths and deploy your code by hand next to the scheduler — or you can upload it once as a project and let the workflow scheduler stage it for every run.

## Create a small project

Make a tiny app on your machine — one script plus a data file it reads by relative path:

```text
my_app/
├── main.py
└── data/
    └── greeting.txt
```

```python
# my_app/main.py
import os

print("cwd:", os.getcwd())
print("project dir:", os.environ["CRONOVA_PROJECT_DIR"])

with open("data/greeting.txt") as f:
    print(f.read().strip(), "on", os.environ["CRONOVA_LOGICAL_DATE"])
```

```text
hello from my_app
```

The relative `data/greeting.txt` is the point: the script assumes it runs *from the project root*, like it would on your laptop.

## Upload it in the console

Open the console at **http://localhost:8090**, edit a DAG, and open a task in the task editor — the **Project** section is where uploads live. Upload a single script, a whole folder, or a `.zip` (auto-extracted), and give the project a name like `my_app`. Project names allow letters, digits, and `. _ -`; uploads are size-capped (per file and per project) and guarded against path traversal / zip-slip.

**Check it:** list the uploaded projects over the REST API:

```bash
./cronova api GET /api/projects -server http://localhost:8090
```

```json
[{"name": "my_app", "files": 2, "size": 253}]
```

## Reference it from a task

Point a `shell` task at the project with the `project` field. Create `dags/my_app_report.yaml`:

```yaml
dag_id: my_app_report
tasks:
  - id: run_main
    type: shell
    command: python3 main.py     # cwd is a clean copy of my_app, so this resolves
    project: my_app
```

Trigger it and watch:

```bash
./cronova trigger my_app_report
./cronova runs my_app_report
```

**Check it:** in the console, open **my_app_report** → the latest run → **run_main**. The log shows the script ran from a fresh copy of your project — a per-attempt directory under the system temp dir (the exact path varies by OS):

```
cwd: /tmp/cronova-ws-9f8a3c21d4e5/my_app_report__manual_...-run_main
project dir: /tmp/cronova-ws-9f8a3c21d4e5/my_app_report__manual_...-run_main
hello from my_app on 2026-07-07
```

## How project staging works

When a shell task sets `project`, the scheduler stages the code before each attempt:

- **A fresh, isolated copy** of the uploaded project becomes the attempt's working directory (`cwd`). Attempts never interfere with each other, and a retry always starts from a clean copy — never from a half-written state left by the failed attempt.
- The copy's absolute path is exported as **`CRONOVA_PROJECT_DIR`**, so a script can locate its own bundled data files even after `cd`-ing elsewhere.
- File permission bits are preserved, so an executable script stays executable — `./run.sh` works.
- The copy lives under the system temp directory and is **removed when the attempt finalizes**.

!!! warning
    The working directory is **ephemeral** — it's a per-attempt scratch copy, deleted after the attempt ends. Don't leave durable results in `cwd`. Print them to stdout (captured in the task log), or write them somewhere external: a database, object storage, or an absolute path outside the workspace.

## Update your code

Edited `main.py`? Re-upload the changed file — uploads are additive/upsert, so you don't have to re-send the whole folder. Because each attempt copies the *current* project, the change takes effect on the **next run**; attempts already running keep the copy they started with.

!!! tip
    A `shell` task with a project can run **any language on the host** — Python, Node, a Go or Rust binary, `psql`, a JAR. The scheduler is fully decoupled from the task language: upload the code, invoke it with the right interpreter, done.

## Shell tasks only

The `project` field is honored for **`shell`** tasks only. The `python`, `sql`, and `http` types run in-process or with their own execution model, where a staged working directory is meaningless — the [next chapter](task-types.md) covers what each type is for.

## Where projects live

Uploaded projects are plain directories under the server's projects dir — by default `~/.cronova/projects`, overridable with the `-projects` flag or `CRONOVA_PROJECTS` env var. If no projects directory is configured, uploads are disabled and any task referencing a project fails at run time.

## Catch a missing project before it runs

A DAG that references a project which was never uploaded parses fine — and then fails on its first run. Validate first; the dry-run endpoint flags exactly this:

```bash
./cronova api POST /api/dags/validate \
  '{"dag_id":"my_app_report","tasks":[{"id":"run_main","type":"shell","command":"python3 main.py","project":"ghost"}]}' \
  -server http://localhost:8090
```

**Check it:** the response says `"valid": true` but carries a warning:

```json
"warnings": ["task \"run_main\" references project \"ghost\" which is not uploaded yet"]
```

!!! note
    "Why did my project task fail immediately?" is almost always one of two things: the project isn't uploaded, or the server has no projects directory configured. `POST /api/dags/validate` surfaces both as warnings before anything runs.

## What you learned

- Upload a script, folder, or `.zip` as a named **project** in the console's task editor, and attach it to a shell task with `project: my_app`.
- Each attempt runs in a **fresh isolated copy** of the project (its `cwd`, also in `CRONOVA_PROJECT_DIR`); the copy is ephemeral, so durable outputs go to stdout or external storage.
- Re-uploading takes effect on the next run; the projects dir defaults to `~/.cronova/projects` (`-projects` / `CRONOVA_PROJECTS`).
- `POST /api/dags/validate` warns about a referenced project that isn't uploaded — check before the first run.

Next: `shell` is just one of five task types — see what [`python`, `sql`, `jar`, and `http` tasks](task-types.md) can do.
