# 重试、超时与资源池

真实的流水线总会出问题：API 断开连接、查询卡死、十个重活同时压到一台机器上。本章将通过自动**重试**、按次尝试的**超时**、软性 **SLA**、运行级硬截止时间以及全局并发**资源池**，让你的工作流具备韧性——正是这些能力把工作流调度器和普通 cron 任务区分开来。

## 一个不稳定的任务

我们先故意造一个会失败的任务。每次尝试都有一个尝试序号——模板里是 `{{ try_number }}`，环境变量里是 `CRONOVA_TRY_NUMBER`——从 `1` 开始，每次重试递增。我们用它来模拟一个第三次才成功的抓取任务。

创建 `dags/flaky_pipeline.yaml`：

```yaml
dag_id: flaky_pipeline
tasks:
  - id: fetch
    type: shell
    command: |
      if [ "$CRONOVA_TRY_NUMBER" -lt 3 ]; then
        echo "attempt $CRONOVA_TRY_NUMBER: connection reset by peer" >&2
        exit 1
      fi
      echo "attempt $CRONOVA_TRY_NUMBER: fetched 1200 rows"
    retries: 3
    retry_delay: 5
```

两个新的任务字段：

- `retries: 3` —— 失败后最多重试 3 次。任务总共获得 **`retries` + 1 次尝试**（这里是 4 次）。
- `retry_delay: 5` —— 两次尝试之间等待 5 秒。两者的默认值都是 `0`。

这里没有配置 `schedule`，所以这个 DAG 只在你手动触发时运行。现在就触发它：

```bash
cronova trigger flaky_pipeline
```

然后查看运行情况：

```bash
cronova runs flaky_pipeline
```

前两次尝试期间，你会看到任务在 `running` 和 `up_for_retry` 之间循环——`up_for_retry` 是失败的尝试在等待 `retry_delay` 期间所处的状态：

```
RUN_ID                                  LOGICAL_DATE          STATE    TRIGGER  TASKS
flaky_pipeline__manual_1751871234...    2026-07-07T00:00:00Z  running  manual   fetch=up_for_retry
```

大约十秒之后（两次失败 × 5 秒延迟），再执行一次——第三次尝试成功，运行随之完成：

```
RUN_ID                                  LOGICAL_DATE          STATE    TRIGGER  TASKS
flaky_pipeline__manual_1751871234...    2026-07-07T00:00:00Z  success  manual   fetch=success
```

在控制台 [http://localhost:8090](http://localhost:8090) 打开这次运行并点击 `fetch` 任务：日志会展示每一次尝试——`attempt 1: connection reset by peer`、`attempt 2: …`，最后是 `attempt 3: fetched 1200 rows`。任务的尝试序号按每次尝试分别存储，你随时可以看到一个任务经历了多少波折才成功。

!!! tip
    自动重试处理的是瞬时故障。对于已经以失败**结束**的运行，请使用运维命令 `cronova retry <run_id> [task_id]` 只重跑失败的任务——参见 [CLI 参考](../CLI.md)。

## DAG 级默认值

给每个任务都写一遍 `retries` 太重复了。可以在 DAG 级别统一设置默认值：

```yaml
dag_id: flaky_pipeline
default_retries: 2
default_retry_delay: 30
tasks:
  - id: fetch
    ...            # inherits: 2 retries, 30s apart
  - id: load
    retries: 5     # tasks can still override the default
    ...
```

`default_retries` 和 `default_retry_delay` 会应用到所有未自行设置 `retries` / `retry_delay` 的任务上。两者默认都是 `0`。

## 超时：终止卡死的尝试

重试只有在尝试确实*失败*时才有用。一个挂起的进程——连接卡住、锁一直不释放——否则会永远运行下去。`timeout` 给每次尝试设定时间上限。添加第二个任务：

```yaml
  - id: transform
    type: shell
    command: "sleep 120"
    deps: [fetch]
    timeout: 5
```

`timeout: 5` 给每次尝试 5 秒时间。超时时，cronova 会杀掉**整个进程组**——不只是顶层的 shell，还包括它派生的所有子进程——确保没有任何东西留在后台继续运行。默认值 `0` 表示不限制。

再次触发这个 DAG，几秒后在控制台查看 `transform` 的日志。`sleep` 永远不会跑完；日志会以这一行结尾：

```
=== killed: timeout after 5s ===
```

被终止的尝试以退出码 `124` 结束，按普通失败处理——如果任务还有剩余的 `retries`，它会进入 `up_for_retry`，并在下一次尝试中获得全新的计时。重试次数用尽后，任务最终标记为 `failed`，其下游任务变为 `upstream_failed`。

## SLA：只告警的软截止时间

有时你并不想终止任何东西——只想在事情跑慢时*知道*一声。这就是 `sla`，在任务和 DAG 两个级别都可以设置，且始终**从运行开始时刻**计时：

```yaml
dag_id: flaky_pipeline
sla: 600                 # alert if the whole run exceeds 10 minutes
notify:
  - url: https://hooks.example.com/cronova
    on: [failure]
tasks:
  - id: transform
    sla: 300             # alert if this task hasn't finished 5 minutes into the run
    ...
```

当一次运行（或一个尚未完成的任务）越过其 `sla` 时，cronova 会记录一条警告，并向该 DAG 的 `notify:` webhook 发送 `sla_miss`（或 `task_sla_miss`）载荷。运行会**继续进行**——SLA 纯粹是一种告警，每次运行或每个任务至多触发一次。设置阈值本身就是启用开关：SLA 告警会发送到任何已配置的 webhook，不受 `on:` 列表限制（`on:` 只控制运行结束时的成功/失败告警）。

!!! note
    任务级 `sla` 是从**运行开始**计算的截止时间，而不是从任务自身启动时算起。如果上游任务耗尽了时间预算，下游任务可能一行代码还没执行就已经错过 SLA——而这恰恰是你想收到的告警。

## dagrun_timeout：硬性终止

`sla` 只是警告；`dagrun_timeout` 会动手。它是运行级的硬截止时间，同样以从运行开始起的秒数计：

```yaml
dag_id: flaky_pipeline
sla: 600
dagrun_timeout: 1800     # kill the whole run after 30 minutes
```

超过期限时，cronova 会杀掉所有正在运行的任务，把所有未完成的任务标记为 `timed_out`，将整次运行最终标记为 `timed_out`，并且——如果配置了 `notify:` webhook——发送一条失败告警（同样不受 `on:` 限制）。默认值 `0` 表示不限制。

一个好的实践是把两者搭配使用：`sla` 设为你*预期*的时长，`dagrun_timeout` 设为你无法*容忍*的时长。

## 资源池：全局并发上限

重试和超时保护的是单个任务。**资源池**保护的是共享资源——比如一个最多能承受 4 个并发报表查询的数据库、一个有严格速率限制的 API——并且作用范围横跨*所有* DAG。资源池是一组具名的全局槽位；任务在运行期间占用其所属资源池的一个槽位。

用 CLI 创建一个资源池：

```bash
cronova pools set reports 4
```

```
pool "reports" set to 4 slots
```

然后通过 `pool:` 把任务指向它，并用 `priority:` 排定优先级：

```yaml
  - id: build_report
    type: shell
    command: "python report.py --date {{ logical_date }}"
    deps: [transform]
    pool: reports
    priority: 10
```

无论有多少个 DAG 运行处于活跃状态，`reports` 池中最多同时执行 4 个任务。当等待的任务多于空闲槽位时，`priority` 更高的任务优先（默认值为 `0`）。

所有未设置 `pool:` 的任务使用内置的 `default` 池，其初始容量为 16 个槽位。你可以随时查看现有资源池并调整任意池的容量：

```bash
cronova pools
```

```
NAME     SLOTS
default  16
reports  4
```

!!! warning
    如果任务引用了一个尚不存在的资源池，cronova 会自动以默认的 16 个槽位创建它，以避免死锁——但这多半不是你想要的上限。请在上线 DAG *之前*先用 `cronova pools set` 创建好资源池。

## 本章要点

- `retries` / `retry_delay`（以及 DAG 级的 `default_retries` / `default_retry_delay`）让任务获得 `retries + 1` 次尝试，`try_number` 逐次递增，`up_for_retry` 状态衔接重试间隔。
- `timeout` 杀掉卡死尝试的整个进程组；`sla`（任务级或 DAG 级）是从运行开始计时、仅告警的软截止时间；`dagrun_timeout` 会硬性终止整次运行。
- 资源池在全局范围限制并发：先 `cronova pools set reports 4`，再在任务上配置 `pool:` + `priority:`；其余任务共享 16 槽位的 `default` 池。

下一章：在[跨 DAG 依赖](cross-dag.md)中用 `trigger_after` 和 webhook 通知把整条工作流串联起来。
