---
title: cronova — 轻量级自托管工作流调度器
hide:
  - navigation
  - toc
---

# cronova

**一个单二进制 Go 程序的轻量级自托管工作流调度器 —— 开源的 Airflow / Azkaban 替代品，一条命令即可安装。**

[![Release](https://img.shields.io/github/v/release/zoyluoblue/cronova?sort=semver&logo=github)](https://github.com/zoyluoblue/cronova/releases/latest)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](https://github.com/zoyluoblue/cronova/blob/main/LICENSE)
[![GitHub stars](https://img.shields.io/github/stars/zoyluoblue/cronova?logo=github&color=1f6feb)](https://github.com/zoyluoblue/cronova/stargazers)

cronova 用于调度 **DAG** —— 带依赖、重试、补跑（catchup）和资源池的任务集合 —— 并以**一个静态二进制**加**内嵌 SQLite** 数据库的形式交付。无需 JVM、无需 Python 运行时、无需外部数据库、无需消息中间件。

```bash
# Install the scheduler + web console + native service on Linux or macOS:
curl -fsSL https://raw.githubusercontent.com/zoyluoblue/cronova/main/deploy/bootstrap.sh | sudo bash
```

[开始教程](tutorial/index.md){ .md-button .md-button--primary }
[快速上手](GETTING_STARTED.md){ .md-button }

![cronova web console — visual task editor with drag-and-drop template variable pills](img/task-editor.png)

<div class="grid cards" markdown>

-   :material-package-variant-closed:{ .lg .middle } **单二进制，零依赖**

    ---

    纯 Go 构建，内嵌数据库，只需一个进程。`curl | bash` 一键安装，
    `cronova update` 升级，`cronova uninstall` 卸载。

    [:octicons-arrow-right-24: 快速上手](GETTING_STARTED.md)

-   :material-graph:{ .lg .middle } **Airflow 风格的 DAG，用 YAML 编写**

    ---

    任务依赖、cron / `@every` 调度、跨 DAG 触发、
    补跑/回填、重试与超时、资源池、触发规则。

    [:octicons-arrow-right-24: DAG 与任务参考](DAG_REFERENCE.md)

-   :material-language-python:{ .lg .middle } **多语言任务 + 工程上传**

    ---

    任务以子进程方式运行 —— shell、Python、SQL、JAR 或 HTTP。
    整个工程文件夹拖拽上传，即可在隔离副本中运行。

    [:octicons-arrow-right-24: 运行你自己的脚本](GETTING_STARTED.md#5-run-your-own-scripts-and-projects)

-   :material-robot:{ .lg .middle } **AI 原生（内置 MCP）**

    ---

    内置 Model Context Protocol 服务器和远程 JSON CLI，让 AI
    智能体通过同一套令牌鉴权的 API 管理工作流。

    [:octicons-arrow-right-24: AI 智能体（MCP）](AGENTS.md)

</div>

## cronova 是什么？

cronova 是一个用 Go 编写的**开源自托管工作流调度器**（作业调度器 / 任务编排器 / DAG 调度器）。它按 cron 或时间间隔触发调度 DAG，用宿主机自带的解释器以子进程方式运行每个任务，并提供 Web 控制台、REST API、CLI，以及面向 AI 智能体的 MCP 端点。你可以把它理解为**带依赖、重试、回填和可观测性的 cron 替代品**，或是一个装进单个二进制里的**轻量级 Airflow 替代品**。

## 30 秒跑起一个 DAG

```bash
go build -o cronova ./cmd/cronova   # or grab a prebuilt release
./cronova serve                     # console at http://localhost:8090
./cronova trigger example_etl       # run a DAG now
./cronova runs example_etl          # watch task states
```

## 进一步了解

- **[教程](tutorial/index.md)** —— 循序渐进的路线：安装 → 第一个 DAG → 调度 → 变量 → 工程 → 跨 DAG。
- **[控制台指南](console/index.md)** —— Web 控制台每个页面：仪表盘、DAG 编辑、可视化任务编辑器、运行与实时日志、资源池、变量、审计、API 令牌。
- **[快速上手](GETTING_STARTED.md)** —— 单页速通路径。
- **[部署](DEPLOY.md)** —— systemd / launchd 服务、更新、可从崩溃中恢复的执行器。
- **[对比](COMPARISON.md)** —— cronova vs. Airflow、Azkaban、Dagster、Prefect 与 cron。
- **[FAQ](FAQ.md)** —— 常见问题解答。
- **[GitHub](https://github.com/zoyluoblue/cronova)** —— 源码、发布版本、issue。⭐ 欢迎 Star！
