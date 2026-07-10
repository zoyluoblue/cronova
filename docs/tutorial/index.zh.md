# 教程

本教程将手把手带你学习 cronova——一款单二进制、可自托管的**工作流调度器**，也是 Airflow 的开源替代方案——从安装二进制文件开始，一步步搭建出带重试、资源池和跨 DAG 依赖的生产级流水线。

各章节层层递进，但每一章也可以独立阅读。如果你只关心某个特定主题，直接跳转即可。

## 你将构建什么

你将逐章打磨一条小型 **ETL 风格流水线**。它最初只是 YAML 文件里的一个 `echo` 任务，随后逐步加上真正的 **cron** 调度与回填、extract → transform → load 依赖链、按日期模板化的命令、来自托管连接的密钥、你自己上传的 Python 工程、重试与资源池——最终再挂上一个下游报表 **DAG**，在流水线成功时自动运行。

一切都在本地运行：一个 `cronova` 二进制文件、一个内嵌的 SQLite 数据库，以及位于 **http://localhost:8090** 的 Web 控制台。无需外部数据库、消息中间件或容器。

## 你需要准备什么

- 一台 **Linux 或 macOS** 机器——笔记本电脑即可。预编译二进制（最新版本：**v0.2.1**）覆盖 amd64 和 arm64。
- 一个终端。
- **Go 1.26.5+**，*仅*在你选择从源码构建时才需要。使用预编译发行版或一行安装脚本完全不需要工具链。

!!! tip
    该二进制文件不依赖 CGO（纯 Go 实现的 SQLite），因此无需编译或链接任何东西——下载、`chmod +x`、运行即可。

## 章节目录

1. **[安装 cronova](install.md)**——获取二进制文件（预编译发行版、一行安装脚本或 `go build`），运行 `cronova serve`，打开控制台。
2. **[你的第一个 DAG](first-dag.md)**——在 `./dags` 目录下用 YAML 文件编写一个 DAG，触发它，并在控制台和 CLI 中观察运行。
3. **[调度](scheduling.md)**——cron 表达式与 `@every` 间隔、`start_date`、`catchup` 回填，以及*逻辑日期*的含义。
4. **[任务依赖](dependencies.md)**——用 `deps` 把任务串联起来，并通过 `all_success`、`one_failed` 等触发规则控制任务何时触发。
5. **[模板变量](template-variables.md)**——把 `{{ logical_date }}`、`{{ run_id }}` 等变量注入命令，或以 `CRONOVA_*` 环境变量的形式读取。
6. **[变量、连接与参数](variables-connections-params.md)**——借助 `{{ var.KEY }}`、`{{ conn.ID.field }}` 和按次运行的 `{{ params.KEY }}`，让密钥和配置远离 YAML。
7. **[工程：运行你自己的代码](projects.md)**——上传一个脚本或整个代码库，并在每次尝试时于全新的隔离工作目录中运行。
8. **[任务类型](task-types.md)**——除 `shell` 之外的 `python`、`sql`、`jar` 和 `http` 任务，以及各自的适用场景。
9. **[重试、超时与资源池](retries-timeouts-pools.md)**——通过 `retries`、`retry_delay`、`timeout`、SLA 和全局并发资源池，让流水线更具韧性。
10. **[跨 DAG 依赖](cross-dag.md)**——用 `trigger_after` 把整条 DAG 链接起来，并在成功或失败时收到 webhook 通知。

!!! note
    本教程覆盖日常使用的字段和命令。完整的 schema 见 [DAG 参考](../DAG_REFERENCE.md)，全部命令和参数见 [CLI 参考](../CLI.md)。可直接运行的示例 DAG 位于仓库的 [`dags/`](https://github.com/zoyluoblue/cronova/tree/main/dags) 目录。

## 如何阅读

每一章都遵循同样的节奏：一段简短讲解、一个可直接运行的小片段（一段 YAML 或几条 shell 命令），以及一个**验证时刻**——用确切的 CLI 输出或控制台变化来证明它生效了。建议跟着敲一遍，每一步只需一两分钟。

## 本章小结

- 本教程把一条 ETL 风格流水线从单个任务逐步演进为带调度、依赖感知、重试加固的工作流。
- 你只需要一台 Linux/macOS 机器——仅从源码构建时才需要 Go 1.26.5+。
- 十个章节带你从安装走到跨 DAG 编排，更深入的内容均有参考文档。

下一步：在[安装 cronova](install.md) 中把二进制文件跑起来。
