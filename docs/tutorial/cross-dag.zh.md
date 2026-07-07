# 跨 DAG 触发与通知

到目前为止，所有的依赖都存在于单个 DAG *内部*。在最后一章里，你将用 `trigger_after` 把整条 DAG 串接起来，在控制台的 **DAG Graph** 中查看结果，并配置一个 `notify` webhook——让失败的运行主动通知你，而不是等着被发现。

## 任务依赖 vs. DAG 依赖

`deps` 连接的是同一个 DAG 单次运行内的各个任务。`trigger_after` 则高一个层级：它在某个 DAG 成功之后运行**整个下游 DAG**。当一条流水线的终点正是另一条流水线的起点时——比如一个团队负责的数据摄取 DAG 和另一个团队负责的报表 DAG——这正是最自然的形态，而无需把它们合并成一个庞大的工作流。

cronova 在仓库的 [`dags/`](https://github.com/zoyluoblue/cronova/tree/main/dags) 目录中以可运行示例的形式提供了这个模式的两半：`upstream_ingest.yaml` 和 `downstream_report.yaml`。

## 上游 DAG

上游是一个完全普通的 DAG——它内部没有任何东西知道下游的存在：

```yaml
dag_id: upstream_ingest
# manual/scheduled upstream; downstream_report runs after this succeeds
start_date: 2026-06-01
max_active_runs: 1
tasks:
  - id: ingest
    type: shell
    command: "echo ingesting for $CRONOVA_LOGICAL_DATE && sleep 1"
```

它没有 `schedule`，所以只在被触发时运行——这对本次演练很方便，但改用 cron 调度也完全一样。

## 下游 DAG：`trigger_after`

下游在*自己*这一侧声明依赖：

```yaml
dag_id: downstream_report
start_date: 2026-06-01
max_active_runs: 1
default_retries: 1
# runs automatically once upstream_ingest succeeds for the same logical_date
trigger_after:
  - dag_id: upstream_ingest
tasks:
  - id: build_report
    type: shell
    command: "echo building report from $CRONOVA_LOGICAL_DATE data"
    pool: reports        # configure size with: cronova pools set reports <n>
    retries: 2
    timeout: 600
```

有两点值得注意：

- **没有 `schedule`。**调度为空的 DAG 只会通过手动触发或 `trigger_after` 运行——上游*就是*它的调度。
- **`trigger_after` 指明了上游。**每当 `upstream_ingest` 有一次运行以 `success` 结束，调度器就会为**相同的逻辑日期**创建一次 `downstream_report` 的运行。

由于逻辑日期会随之传递，补跑的上游运行会触发针对*那个*时间段的报表——而不是墙上时钟的"现在"。Catchup（补跑）与跨 DAG 触发天然可以组合使用。

!!! tip
    `trigger_after` 接受一个列表，因此一个下游可以汇聚多个上游。只有当列出的**每一个**上游都对该逻辑日期有一次成功运行时它才会触发，并且仍然遵守下游自身的 `max_active_runs`。

## 触发这条链路

在 `cronova serve` 运行的情况下，触发上游：

```bash
./cronova trigger upstream_ingest
```

等几秒钟（ingest 任务会 sleep 一秒，调度器每 2 秒 tick 一次），然后查看两个 DAG：

```bash
./cronova runs upstream_ingest
./cronova runs downstream_report
```

上游显示一次普通的手动运行：

```
RUN_ID                             LOGICAL_DATE          STATE    TRIGGER  TASKS
upstream_ingest__20260707T091502Z  2026-07-07T09:15:02Z  success  manual   ingest=success
```

而下游显示了一次**你从未触发过**的运行：

```
RUN_ID                               LOGICAL_DATE          STATE    TRIGGER     TASKS
downstream_report__20260707T091502Z  2026-07-07T09:15:02Z  success  dependency  build_report=success
```

`TRIGGER` 列显示为 `dependency`，且 `LOGICAL_DATE` 与上游运行完全一致——这就是跨 DAG 触发在起作用。

## 在 DAG Graph 中查看

打开控制台 **http://localhost:8090**，点击导航中的 **Graph**。**DAG Graph** 视图会绘制 DAG 之间的触发依赖——你会看到一条从 `upstream_ingest` 指向 `downstream_report` 的边。当你的工作流调度器发展到几十个 DAG 时，这张图就是你一眼回答"什么之后运行什么？"的方式。

## 用 `notify` 配置 Webhook 通知

一条静默失败的流水线比没有流水线更糟。`notify` 是一个 DAG 级别的字段：当运行以你列出的状态结束时，指定的 URL 会收到一个 JSON `POST`。把它加到任意 DAG 上：

```yaml
dag_id: downstream_report
# … tasks as above …
notify:
  url: https://hooks.slack.com/services/T000/B000/XXXX
  on: [failure]
```

`on` 接受 `failure` 和/或 `success`——`failure` 同时涵盖已取消和超时的运行，因此任何非正常结束都会告警。URL 必须是 `http(s)`。

payload 长这样：

```json
{
  "text": "cronova · downstream_report · run downstream_report__20260707T091502Z finished: failed (tasks: [build_report])",
  "dag_id": "downstream_report",
  "run_id": "downstream_report__20260707T091502Z",
  "state": "failed",
  "logical_date": "2026-07-07T09:15:02Z",
  "started_at": "2026-07-07T09:15:04Z",
  "finished_at": "2026-07-07T09:15:07Z",
  "duration_ms": 3000,
  "failed_tasks": ["build_report"]
}
```

`text` 字段是现成的人类可读摘要，因此 Slack、飞书或 Discord 的**incoming webhook** URL 无需任何胶水代码即可直接渲染它；结构化字段则供你自己的服务端点使用。

想看到它实际触发，可以临时把 `build_report` 的命令改成 `exit 1`，再次触发 `upstream_ingest`，然后观察 `cronova serve` 的日志：你会看到一行带 run id 的 `notify sent`（如果投递失败则是 `notify non-2xx` / `notify post`）。投递是异步且尽力而为的——它永远不会阻塞调度循环。

如果你在 DAG 上设置了 `sla` 或 `dagrun_timeout`，超限也会通过同一个 webhook 上报——配置阈值本身就是启用开关，与 `on` 无关。

!!! warning
    出站 webhook 做了 SSRF 加固：cronova 会拒绝解析到私有或内部地址的 URL（localhost、RFC 1918 网段、link-local、云元数据地址），并且从不跟随重定向。请用公网端点测试——真实的 Slack/飞书 webhook 或托管的请求检查服务——而不是 `http://localhost` 上的接收端。

## 你学到了什么

- `trigger_after` 串接整条 DAG：一旦列出的每个上游都有**同一逻辑日期的成功运行**，下游就会自动运行，在 `cronova runs` 中显示为 `TRIGGER=dependency`。
- 没有 `schedule` 的 DAG 只会通过手动触发或 `trigger_after` 运行，控制台的 **Graph** 视图会可视化所有跨 DAG 的边。
- `notify` 在 `failure` 和/或 `success` 时 POST 一个 JSON payload——附带可直接用于 Slack 的 `text` 摘要——异步投递并内置 SSRF 防护。

## 下一步

整个教程到此结束——你已经从一个单独的 `echo` 任务，一路走到了带调度、感知依赖、重试加固、跨 DAG 且带告警的流水线，而这一切只需一个二进制文件和一个内嵌的 SQLite 数据库。接下来可以：

- **[部署](../DEPLOY.md)**——把 cronova 安装为 systemd/launchd 服务，切换到可从崩溃中恢复的 gRPC 执行器（正在运行的任务可以在调度器重启或升级后存活），并用 `cronova update` 保持更新。
- **[AI Agents (MCP)](../AGENTS.md)**——让 AI Agent 通过内置的 MCP 服务器和远程 JSON CLI 来列出、创建、校验和触发 DAG。
- **[DAG 与任务参考](../DAG_REFERENCE.md)**——详尽的 schema：每个 DAG 和任务字段、全部五种任务类型、触发规则和资源池。
- **[CLI 参考](../CLI.md)**——每一条命令和标志，从 `serve` 到 `tokens create`。
