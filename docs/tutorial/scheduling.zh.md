# 调度与补跑

到目前为止，你都是手动触发 DAG。本章将为它配置**调度（schedule）**，学习*逻辑日期*——回填背后的思维模型——并通过 `catchup`、`max_active_runs` 和暂停来控制运行的堆积方式。

## `schedule` 字段

DAG 的 `schedule` 接受两种形式：**cron 表达式**或**时间间隔**。

### Cron 表达式

标准的 5 字段 cron 语法，和你在 `crontab` 里写的完全一样。创建 `dags/daily_report.yaml`：

```yaml
dag_id: daily_report
schedule: "0 2 * * *"        # every day at 02:00
start_date: 2026-07-01
tasks:
  - id: build
    type: shell
    command: echo "reporting for {{ logical_date }}"
```

调度器在每个 tick（默认每 `2s`，可通过 `serve -tick` 调整）评估所有 DAG 的调度，一旦某个调度边界已经越过，就创建一次运行。

验证一下——列出你的 DAG：

```bash
./cronova dags
```

```text
DAG_ID        SCHEDULE   CATCHUP  PAUSED  MAX_ACTIVE
daily_report  0 2 * * *  false    false   1
```

同样的调度信息也会显示在控制台 [http://localhost:8090](http://localhost:8090) 中对应 DAG 的旁边。

!!! note

    调度器以 **UTC** 时间工作：cron 边界按 UTC 时间触发，每次运行的逻辑日期
    也以 UTC 记录。`"0 2 * * *"` 表示 UTC 时间 02:00，而不是本地时间 02:00。

### 使用 `@every` 的时间间隔

如果只是想"每 N 秒/分钟/小时跑一次"，不必推算 cron，直接用时间间隔：

```yaml
dag_id: ticker
schedule: "@every 30s"
start_date: 2026-06-01
tasks:
  - id: heartbeat
    type: shell
    command: echo "tick at $CRONOVA_LOGICAL_DATETIME"
```

等待一分钟，然后验证：

```bash
./cronova runs ticker -n 3
```

```text
RUN_ID                     LOGICAL_DATE          STATE    TRIGGER   TASKS
ticker__20260707T091530Z   2026-07-07T09:15:30Z  success  schedule  heartbeat=success
ticker__20260707T091500Z   2026-07-07T09:15:00Z  success  schedule  heartbeat=success
ticker__20260707T091430Z   2026-07-07T09:14:30Z  success  schedule  heartbeat=success
```

新的运行每 30 秒出现一次，每条的 `TRIGGER` 都是 `schedule`。仓库中附带了这个 DAG 的可运行版本：[`dags/ticker.yaml`](https://github.com/zoyluoblue/cronova/blob/main/dags/ticker.yaml)。

### 不设 schedule = 仅手动触发

省略 `schedule`（或设为 `""`），DAG 就永远不会按时钟运行——它在 `cronova dags` 中显示为 `(manual)`，只有当你在控制台触发它、执行 `cronova trigger <dag_id>`，或用 `trigger_after` 将它挂接到另一个 DAG（后续章节介绍）时才会运行。

## `start_date`：时间的起点

`start_date` 是该 DAG 可被调度的最早逻辑日期。单独来看它作用不大——但它是 catchup 向前回溯的锚点，所以请给每个有调度的 DAG 都设置一个。

```yaml
start_date: 2026-07-01
```

## 逻辑日期

这是所有基于 DAG 的工作流调度器（包括 cronova）最核心的思维模型：

**每次运行都携带一个 `logical_date`——它是这次运行所*代表*的时间周期，而不是实际执行时的墙上时钟时间。**

7 月 6 日的 02:00 运行，其 `logical_date = 2026-07-06`，即使调度器当晚宕机、直到 7 月 7 日才执行这次运行，也是如此。你的任务应该读取逻辑日期，而不是询问系统时钟：

```yaml
tasks:
  - id: build
    type: shell
    command: python report.py --date {{ logical_date }}      # via template
  - id: notify
    type: shell
    command: echo "built report for $CRONOVA_LOGICAL_DATE"   # via env var
    deps: [build]
```

`{{ logical_date }}` 渲染为 `YYYY-MM-DD`；`{{ logical_datetime }}` 给出完整的 RFC 3339 时间戳。二者也会以环境变量 `CRONOVA_LOGICAL_DATE` 和 `CRONOVA_LOGICAL_DATETIME` 的形式注入到任务环境中。就连运行 id 也内嵌了它：`daily_report__20260706T020000Z`。

只知道"现在"的任务会让回填失去意义——每次重放的运行都会处理*今天*的数据。而以 `{{ logical_date }}` 为键的任务无论何时执行，都能处理正确的时间周期。这正是下一节能够成立的原因。

## `catchup`：回填错过的周期

`catchup` 决定 DAG 未运行期间越过的调度边界如何处理——无论是因为调度器宕机、DAG 刚刚创建，还是它的 `start_date` 在过去：

- `catchup: false`（默认）——错过的周期直接跳过；只有未来的边界会创建运行。
- `catchup: true`——调度器会遍历从 `start_date` 到现在的每一个边界，为每个错过的周期创建一次运行，各自带有自己的逻辑日期。

试一下。今天是 2026-07-07；把 DAG 的日期设到一周前并开启 catchup：

```yaml
dag_id: daily_report
schedule: "0 2 * * *"
start_date: 2026-07-01
catchup: true
tasks:
  - id: build
    type: shell
    command: echo "reporting for {{ logical_date }}"
```

验证一下——保存文件后几秒内：

```bash
./cronova runs daily_report -n 10
```

```text
RUN_ID                           LOGICAL_DATE          STATE    TRIGGER   TASKS
daily_report__20260707T020000Z   2026-07-07T02:00:00Z  running  schedule  build=running
daily_report__20260706T020000Z   2026-07-06T02:00:00Z  success  schedule  build=success
daily_report__20260705T020000Z   2026-07-05T02:00:00Z  success  schedule  build=success
daily_report__20260704T020000Z   2026-07-04T02:00:00Z  success  schedule  build=success
...
```

每个错过的日期对应一次运行，从最早的开始，各自处理"属于自己"的那一天。在控制台打开这个 DAG，观察运行历史逐步填满；每次运行的日志都会输出不同的 `{{ logical_date }}`。

回填被刻意做了节流：调度器每个 tick 最多创建一次新运行，并且永远不会超过 `max_active_runs`，所以即使对一个 `start_date` 在一年前的 DAG 开启 catchup，得到的也是平稳的消化过程，而不是洪水式涌入。

!!! warning

    补跑（以及重试）的前提是任务**幂等**：对同一个逻辑日期运行两次必须产生相同
    的结果。如果任务是追加写而不是覆盖写，回填就会造成数据重复。

## `max_active_runs`

`max_active_runs` 限制*这个 DAG* 同时在途的运行数量。默认值为 `1`（`0` 会被视为 `1`），这意味着回填会严格按顺序、一次只执行一个周期——当第 N+1 天依赖第 N 天的产出时，这通常正是你想要的。

如果各周期相互独立、你希望回填更快，可以调高它：

```yaml
max_active_runs: 3
```

验证一下：回填期间，`./cronova runs daily_report` 会同时显示最多三条处于 `running` 状态的运行。

## 暂停 DAG

暂停会让调度器停止创建新运行，且无需改动 YAML。在控制台切换该 DAG 的暂停开关，或者使用 CLI——`pause` 会调用运行中服务器的 REST API，所以先把 CLI 指向它：

```bash
export CRONOVA_SERVER=http://localhost:8090
./cronova pause daily_report
```

验证一下：

```bash
./cronova dags
```

```text
DAG_ID        SCHEDULE   CATCHUP  PAUSED  MAX_ACTIVE
daily_report  0 2 * * *  true     true    1
```

`PAUSED` 现在是 `true`，不会再出现新的调度运行。恢复方式：

```bash
./cronova pause daily_report -off
```

如果你启用了认证，还需要设置 `CRONOVA_TOKEN`（用 `cronova tokens create` 签发一个——参见 [CLI 参考](../CLI.md)）。

!!! tip

    `paused` *不是* YAML 字段——它是运维状态，由控制台、CLI 或 API 管理，
    并且在 DAG 文件重新加载后依然保留。你的 DAG 定义始终是对工作流的纯粹
    描述；暂停始终是一个运维动作。

## 本章小结

- `schedule` 接受 cron 表达式（`"0 2 * * *"`）或时间间隔（`"@every 30s"`）；省略它则 DAG 仅手动触发。时间均为 UTC。
- 每次运行都有一个**逻辑日期**——它所代表的时间周期——可通过 `{{ logical_date }}` / `CRONOVA_LOGICAL_DATE` 获取。
- `catchup: true` 会从 `start_date` 起为每个错过的周期回填一次运行，并受 `max_active_runs`（默认 `1`）节流。
- 在控制台或用 `cronova pause <dag_id> [-off]` 暂停和恢复调度——这是运维状态，不是 YAML。

下一步：在[依赖与触发规则](dependencies.md)中把工作流扩展为多个任务，并精确控制每个任务的触发时机。
