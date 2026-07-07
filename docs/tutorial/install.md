# Install cronova

cronova is a self-hosted **workflow scheduler** that ships as one static Go binary — scheduler, web console, REST API, and CLI included, with an embedded SQLite database. In this chapter you install it, start it, and open the console for the first time.

There are three ways to get the `cronova` binary. For this tutorial, use the plain binary and run it from a working directory — no root, no service, easy to throw away.

## Option 1: Prebuilt release (recommended for the tutorial)

Grab the latest release (currently **v0.2.1**) from the [Releases page](https://github.com/zoyluoblue/cronova/releases). Binaries are published for Linux and macOS, `amd64` and `arm64`, as `cronova_<os>_<arch>.tar.gz`:

```bash
mkdir cronova-tutorial && cd cronova-tutorial
curl -fsSLO https://github.com/zoyluoblue/cronova/releases/latest/download/cronova_darwin_arm64.tar.gz
tar -xzf cronova_darwin_arm64.tar.gz
chmod +x cronova
```

Swap the filename for your platform: `cronova_linux_amd64.tar.gz`, `cronova_linux_arm64.tar.gz`, or `cronova_darwin_amd64.tar.gz`. Each release also attaches a `SHA256SUMS` file if you want to verify the download.

!!! tip
    The tarball is more than the binary: it also unpacks a `dags/` folder with runnable [example DAGs](https://github.com/zoyluoblue/cronova/tree/main/dags), a `cronova.yaml.example` config template, and the standalone `cronova-executor`. Starting from the tarball means the console won't be empty on first launch.

## Option 2: Build from source

With **Go 1.26+** installed:

```bash
git clone https://github.com/zoyluoblue/cronova
cd cronova
go build -o cronova ./cmd/cronova
```

This builds the scheduler, web console, and CLI into one static binary. It is CGO-free (pure-Go SQLite), so no C toolchain is required.

!!! note
    A plain `go build` reports its version as `dev` — that's expected. Release binaries carry the real version tag.

## Option 3: One-line installer (native service)

For a real deployment on a Linux or macOS box, one command does everything:

```bash
curl -fsSL https://raw.githubusercontent.com/zoyluoblue/cronova/main/deploy/bootstrap.sh | sudo bash
```

It detects your OS and CPU architecture, downloads the matching prebuilt release, verifies its SHA256, installs cronova as a native service (systemd on Linux, launchd on macOS), and runs an interactive setup wizard for the port, bind scope, admin account, and auth.

A service install manages its own lifecycle afterwards: `cronova update` upgrades in place (with automatic rollback if the new binary doesn't stay up), and `cronova uninstall` removes it. The full production guide is in [Deployment](../DEPLOY.md).

For the rest of the tutorial, stick with the plain binary from Option 1 or 2.

## Check it: `cronova version`

From the directory with your binary:

```bash
./cronova version
```

You'll see the build version and platform, in the form `cronova <version> <os>/<arch>`:

```
cronova v0.2.1 darwin/arm64
```

If that prints, the install is done.

## Start the scheduler and open the console

`cronova serve` runs the scheduling loop **and** the web console + REST API in one process:

```bash
./cronova serve
```

By default it works relative to the current directory: DAG YAML files load from `./dags` (created if missing), the SQLite database lives at `data/cronova.db`, and task logs go to `logs/`. That's why running it from a dedicated working directory is the cleanest way to follow along.

Now open **<http://localhost:8090>** in your browser. You'll see the cronova console — the DAG list, run history, task states, and one-click manual triggers. If you installed from the release tarball, the bundled example DAGs (like `example_etl` and `ticker`) already appear in the list.

You can check the same thing from a second terminal with the CLI:

```bash
./cronova dags
```

Each loaded DAG is listed with its schedule — proof the scheduler is up and reading your DAG directory.

!!! warning
    Authentication is **off** by default, and a fresh `serve` prints a warning about it: anyone who can reach port 8090 can trigger or delete DAGs. That's fine on your laptop for this tutorial; before exposing cronova to a network, enable login — see [Enabling login](../GETTING_STARTED.md#enabling-login).

Stop the server anytime with ++ctrl+c++ — your DAGs and the database stay on disk, ready for the next `./cronova serve`.

## What you learned

- Three ways to install cronova: a prebuilt release binary, `go build` from source, or the one-line `curl | sudo bash` installer that sets up a native service.
- `./cronova version` confirms the binary works and prints `cronova <version> <os>/<arch>`.
- `./cronova serve` runs the scheduler, web console, and REST API in one process, with everything (DAGs, DB, logs) relative to your working directory — console at <http://localhost:8090>.

Next up: write and trigger your first DAG — [First DAG](first-dag.md).
