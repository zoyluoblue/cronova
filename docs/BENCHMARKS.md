# Benchmarks

Reproducible scheduler-throughput numbers from the in-repo harness
([`internal/scheduler/bench_test.go`](https://github.com/zoyluoblue/cronova/blob/main/internal/scheduler/bench_test.go)).
The harness drives the REAL scheduling path — store, admission, global
priority dispatch, pool accounting, process execution (`sh -c true`),
finalization — with no HTTP in the loop.

```bash
# SQLite (embedded, default)
CRONOVA_BENCH=1 go test ./internal/scheduler/ -run TestThroughputBench -v

# PostgreSQL
docker run -d --name cronova-pg -e POSTGRES_PASSWORD=test -e POSTGRES_DB=cronova_test -p 55433:5432 postgres:17-alpine
CRONOVA_BENCH=1 CRONOVA_TEST_PG_DSN='postgres://postgres:test@127.0.0.1:55433/cronova_test?sslmode=disable' \
  go test ./internal/scheduler/ -run TestThroughputBench -v
```

Knobs: `CRONOVA_BENCH_DAGS` (default 20) × `CRONOVA_BENCH_RUNS` (25) runs,
each a chain of `CRONOVA_BENCH_TASKS` (3) trivial shell tasks.

## Results (2026-08-08)

Apple Silicon MacBook Pro, macOS 25.4; scheduler tick 10 ms, 200 active runs /
64 concurrent tasks; PostgreSQL 17.5 in Docker on the same machine. Workload:
20 DAGs × 25 runs = **500 runs / 1500 task executions**, drained from a cold
queue.

| Backend | Wall time | Runs/s | Tasks/s | Run p50 | Run p99 |
|---|---|---|---|---|---|
| SQLite (embedded) | 4.8 s | **103.5** | **310.6** | 1.72 s | 2.32 s |
| PostgreSQL 17 (Docker, same host) | 10.2 s | **49.1** | **147.4** | 3.88 s | 4.60 s |

Reading the numbers honestly:

- **Throughput is scheduler-side capacity**, not task capacity: every task
  here is `true` (~0 ms). With real tasks, wall time is dominated by the tasks
  themselves; these figures bound how fast cronova can *admit, dispatch, and
  finalize* work. 100 runs/s ≈ 8.6M runs/day on SQLite — orders of magnitude
  above the target deployment size.
- **Run p50/p99 include queue wait**: 500 runs contend for 200 active-run
  slots, so median "duration" is mostly waiting for admission, not overhead.
  A single idle-system run completes in one or two ticks.
- **SQLite beats PostgreSQL here** because the store runs in-process with a
  single writer and zero network hops, while every PG statement crosses a
  Docker network boundary. PostgreSQL is not the "faster" choice — it is the
  **multi-instance** choice: cross-machine HA (`-standby` takeover) and a
  metadata store that lives apart from the scheduler host.
- Numbers vary with hardware and settings; treat them as one honest data
  point and re-run the harness on your own machine (that is what it is for).

## Distributed workers

Dial-in workers move task *execution* off the scheduler host, but assignments
still flow through the scheduler's dispatch loop, so scheduler-side throughput
bounds the fleet as a whole; what workers add is execution capacity and
isolation (N hosts running your actual workloads). The
`docker compose --profile full` topology (PostgreSQL + hub + 2 workers) is the
reproducible test bed for distributed scenarios — see docs/DEPLOY.md
"Distributed workers".
