# 你的第一个 DAG

本章将带你编写第一个 **DAG**——一个由依赖关系连接起来的两个 shell 任务组成的工作流——然后用 cronova 调度器运行它，并分别从 CLI 和 Web 控制台观察它成功执行。

## 创建 DAG 文件

DAG（有向无环图）是一组带依赖边的任务，以单个 YAML 文件的形式定义在 `./dags/` 目录中。每个 shell 任务都作为操作系统子进程运行。

创建 `dags/hello.yaml`：

```yaml
dag_id: hello
tasks:
  - id: greet
    type: shell
    command: echo "hello from cronova"
  - id: report
    type: shell
    command: echo "run $CRONOVA_RUN_ID finished greeting"
    deps: [greet]
```

这就是一个完整、可运行的工作流。下面我们逐行讲解。

### `dag_id`

```yaml
dag_id: hello
```

DAG 的唯一标识符。它是必填项，也是你在其他所有地方使用的名称——`cronova trigger hello`、控制台的 DAG 列表、运行历史。

### `tasks`

```yaml
tasks:
  - id: greet
```

任务列表——同样是必填项。每个条目都有一个 `id`，且必须在 DAG 内唯一。

### `type`

```yaml
    type: shell
```

任务类型。`shell` 通过 `sh -c` 将 `command` 作为操作系统子进程运行，所以任何能在终端里敲出来的命令在这里都能用。还有四种类型——`python`、`sql`、`jar` 和 `http`——将在教程后续章节介绍。

!!! tip

    `shell` 是默认的 `type`，所以这两行完全可以省略。
    这里显式写出，是为了让文件读起来毫无歧义。

### `command`

```yaml
    command: echo "run $CRONOVA_RUN_ID finished greeting"
```

任务执行的内容。注意 `$CRONOVA_RUN_ID`：cronova 会把运行信息以 `CRONOVA_*` 环境变量的形式注入每个任务的环境——运行 id、DAG id、任务 id、逻辑日期等等。你的脚本无需任何额外配置就能直接使用它们。

### `deps`

```yaml
    deps: [greet]
```

依赖边——正是它让这组任务成为一张图。`report` 会等待 `greet`，并且（默认情况下）只在 `greet` **成功**之后才运行。文件加载时会对边做环检测，意外形成的循环会被直接拒绝，而不会让工作流挂起。

## 启动调度器

`cronova serve` 在一个进程内同时运行调度循环、REST API 和 Web 控制台：

```bash
cronova serve
```

它会加载 `./dags` 下的所有 `*.yaml` 和 `*.yml` 文件。格式有误的文件会被记录日志并跳过——绝不会拖垮调度器。

**验证一下**——在另一个终端执行：

```bash
cronova dags
```

```
DAG_ID  SCHEDULE  CATCHUP  PAUSED  MAX_ACTIVE
hello   (manual)  false    false   1
```

`hello` 已经注册成功。你也可以打开控制台 **http://localhost:8090**——DAG 列表中会显示 `hello`，信息与命令行一致。

## 触发一次运行

```bash
cronova trigger hello
```

```
created run hello__manual_1783468804512345600 (a running `cronova serve` will execute it)
```

!!! note

    `cronova trigger` 只负责**创建**运行——它向数据库写入一条运行记录后立即返回。
    正在运行的 `cronova serve` 会在下一个调度周期（默认每 `2s` 一次）拾取它，
    因此执行几乎是立即开始的，只是不发生在 `trigger` 命令内部。

## 观察运行过程

```bash
cronova runs hello
```

```
RUN_ID                              LOGICAL_DATE          STATE    TRIGGER  TASKS
hello__manual_1783468804512345600   2026-07-07T08:15:04Z  success  manual   greet=success report=success
```

这次运行先执行了 `greet`，再执行 `report`——正是 `deps` 规定的顺序。如果你在触发后足够快地执行 `cronova runs hello`，还能捕捉到状态的推进过程：`queued` → `running` → `success`。

### 在控制台里做同样的事

打开 **http://localhost:8090**，你刚才做的每一步在控制台里都有对应操作：

- **DAG 列表**中显示 `hello`，支持一键手动触发——无需终端。
- 点进 `hello` 可查看**运行历史**以及每次运行中各任务的状态。
- 点击某个任务实例可以**实时查看日志**——你会在 `greet` 的日志里看到 `hello from cronova`，以及 `report` 打印出的真实运行 id。

## 定时调度 vs. 手动触发

你可能已经注意到 `hello.yaml` 没有 `schedule` 字段。这是有意为之：**省略 `schedule` 会让 DAG 变为仅手动触发**——只有在你触发时（通过 CLI、控制台或 API）才会运行，绝不会自行执行。这也是 `cronova dags` 的 SCHEDULE 列显示 `(manual)` 的原因。

要让 cronova 自动运行它，只需加一行——cron 表达式或时间间隔均可：

```yaml
dag_id: hello
schedule: "@every 1m"    # or a cron expression like "0 2 * * *"
tasks:
  # ...unchanged...
```

保存文件后，`cronova dags` 会显示 `SCHEDULE=@every 1m`——调度器每分钟创建一次新的运行。调度、`start_date` 以及补跑/回填将在下一章专门讲解。

!!! warning

    只要 `serve` 在运行，`@every 1m` 的调度就会持续不断地产生运行。
    做完实验后，请删掉 `schedule` 这一行，或者在控制台暂停该 DAG
    （也可以执行 `cronova pause hello`）。

## 本章小结

- 一个 DAG 就是 `./dags` 下的一个 YAML 文件：必填的 `dag_id` 加上 `tasks` 列表，用 `deps` 画出依赖边。
- `cronova serve` 同时运行调度器和位于 http://localhost:8090 的控制台；`cronova dags` 列出已加载的 DAG。
- `cronova trigger <dag_id>` 创建一次运行，`serve` 在下一个调度周期执行它；`cronova runs <dag_id>` 和控制台的运行历史展示每个任务的状态。
- 没有 `schedule` 字段即为仅手动触发；加上它就把 DAG 交给了调度器。

完整的逐字段 schema 参见 [DAG 与任务参考](../DAG_REFERENCE.md)。**下一步：**给 `hello` 配上真正的调度——cron 表达式、时间间隔、`start_date` 和补跑——见[调度](scheduling.md)。
