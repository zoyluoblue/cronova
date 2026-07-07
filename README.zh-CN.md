<div align="center">

# cronova

**单二进制、自托管的工作流调度器 —— 一条命令即可安装的开源 [Apache Airflow](https://airflow.apache.org/) / Azkaban 替代方案。**

[![Release](https://img.shields.io/github/v/release/zoyluoblue/cronova?sort=semver&logo=github)](https://github.com/zoyluoblue/cronova/releases/latest)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/github/go-mod/go-version/zoyluoblue/cronova?logo=go)](go.mod)
[![Platforms](https://img.shields.io/badge/platform-linux%20%7C%20macOS%20·%20amd64%20%7C%20arm64-informational)](docs/DEPLOY.md)
[![GitHub stars](https://img.shields.io/github/stars/zoyluoblue/cronova?logo=github&color=1f6feb)](https://github.com/zoyluoblue/cronova/stargazers)

[English](README.md) · **简体中文**

<sub>⭐ 觉得有用?<b><a href="https://github.com/zoyluoblue/cronova">在 GitHub 上给 cronova 点个 Star</a></b> —— 帮更多人发现一种更轻的工作流运行方式。</sub>

</div>

cronova 是一款对标 Airflow / Azkaban 的**工作流调度器与任务编排系统(job scheduler / task orchestrator)**,面向想要 DAG 调度、又**不想背运维包袱**的团队。它是**一个静态 Go 二进制**、内置 **SQLite** 数据库 —— 无需 JVM、无需 Python 运行时、无需外部数据库、无需消息队列、无需容器。一条命令装到任意 Linux / macOS 机器上,定义你的流水线,打开内置 Web 控制台即可。

<div align="center">
  <img src="docs/img/task-editor.png" alt="cronova Web 控制台 —— 支持拖拽模板变量药丸的可视化任务编辑器(多语言工作流调度器)" width="900">
  <br><em>控制台任务编辑器:用「点击 / 拖拽变量药丸」拼出命令(内置 / 变量 / 连接 / 参数)。</em>
</div>

```bash
# 一条命令在 Linux 或 macOS 上安装:调度器 + Web 控制台 + 系统服务
curl -fsSL https://raw.githubusercontent.com/zoyluoblue/cronova/main/deploy/bootstrap.sh | sudo bash
```

## 为什么选 cronova?

- 🟢 **单二进制、零依赖。** 纯 Go 构建(`modernc.org/sqlite`,无 CGO)、内置数据库、单进程。`curl | bash` 装、`cronova update` 升级、`cronova uninstall` 卸载 —— 没有 Airflow 那套要伺候的技术栈。
- 🗂️ **Airflow / Azkaban 式 DAG。** 声明式 YAML DAG、依赖边、cron / `@every` 定时、跨 DAG 触发、catchup / 回填、任务级重试与超时、资源池、触发规则 —— 你熟悉的那些编排原语。
- 🌐 **多语言任务 + 工程上传。** 每个任务以子进程运行,所以任务可用 **shell、Python、SQL、JAR、HTTP** —— 主机上任意语言。在控制台里拖拽一个脚本、整个工程文件夹或 `.zip`,cronova 会在隔离的干净副本里运行它。
- 🤖 **AI 原生。** 内置 **[MCP(Model Context Protocol)](https://modelcontextprotocol.io/) server** 与远程 JSON CLI,让 AI 智能体(Claude 及任意 MCP 客户端)通过同一套「令牌鉴权 + 角色控制」的 API 列出、创建、校验、触发、查看 DAG。
- 🛡️ **崩溃可恢复执行。** 任务可跑在解耦的 gRPC executor 里,重启或升级调度器都不会杀掉正在运行的任务 —— 恢复时重新挂接在跑任务,不会重复执行。
- 🖥️ **开箱即用的 Web 控制台。** DAG 仪表盘、运行历史、任务状态、**实时日志(SSE)**、手动触发、变量与连接、审计流水、可视化命令编辑器 —— 全部进程内提供。含 REST API + OpenAPI。

## cronova 是什么?

**cronova 是一款开源、自托管的工作流调度器**(即 job scheduler / task orchestrator / DAG 调度器),用 Go 编写。它按 cron 或间隔触发调度 **DAG**(任务的有向无环图),用主机自带的解释器把每个任务作为子进程运行,并提供 Web 控制台、REST API、CLI,以及给 AI 智能体用的 MCP 端点。可以把它理解为**「带依赖、重试、回填与可观测性的 cron 替代」**,或**装在一个二进制里的轻量 Airflow 替代**。

## 快速开始

```bash
# 1. 构建(Go 1.26+)—— 或从 Releases 下预编译二进制
go build -o cronova ./cmd/cronova

# 2. 启动调度器 + Web 控制台(进程内 executor)
./cronova serve                 # 控制台在 http://localhost:8090

# 3. 用 CLI 驱动(另开一个终端)
./cronova dags                  # 列出 ./dags 里的 DAG
./cronova trigger example_etl   # 立即运行一个 DAG
./cronova runs example_etl      # 运行历史 + 任务状态
```

打开 **http://localhost:8090** 进入控制台 —— DAG 列表、运行历史、任务状态、实时日志、一键手动触发。

<div align="center">
  <img src="docs/img/dashboard.png" alt="cronova 仪表盘 —— 自托管工作流调度器,展示 DAG、运行历史、成功率与调度" width="900">
</div>

## cronova vs. Airflow vs. Azkaban vs. cron

| | **cronova** | Apache Airflow | Azkaban | 原生 cron |
|---|:---:|:---:|:---:|:---:|
| 安装 | **单二进制 / `curl \| bash`** | Python 栈 + DB + broker | JVM + MySQL | 系统自带 |
| 运行依赖 | **无**(内置 SQLite) | Python、Postgres、Redis/Celery | Java、MySQL | 无 |
| DAG 与依赖 | ✅ | ✅ | ✅ | ❌ |
| cron + 间隔 + 跨 DAG 触发 | ✅ | ✅ | 部分 | 仅 cron |
| catchup / 回填 | ✅ | ✅ | ❌ | ❌ |
| 重试、超时、资源池 | ✅ | ✅ | 部分 | ❌ |
| 崩溃恢复(不重复执行) | ✅ | ✅ | 部分 | ❌ |
| 多语言任务(shell/Python/SQL/JAR/HTTP) | ✅ | ✅(operators) | 偏 JVM | 任意(无编排) |
| Web 控制台 + 实时日志 | ✅ | ✅ | ✅ | ❌ |
| REST API + OpenAPI | ✅ | ✅ | 部分 | ❌ |
| AI 智能体 / MCP 集成 | ✅ **内置** | ❌ | ❌ | ❌ |
| 体积 | **单进程、数十 MB** | 重 | 重(JVM) | 极小 |

cronova 瞄准的是裸 `crontab` 与完整 Airflow 部署之间的甜点区:**真正的 DAG 编排,几乎没有运维负担。**

## 定义一个 DAG

在 `./dags/` 放一个 YAML 文件(可运行示例见 [`dags/`](dags/)):

```yaml
dag_id: daily_etl
schedule: "0 2 * * *"        # cron;或 "@every 30s";留空则仅手动
start_date: 2026-06-01
catchup: true                # 回填错过的周期
max_active_runs: 1
default_retries: 2
tasks:
  - id: extract
    type: shell
    command: "python extract.py --date {{ logical_date }}"
    pool: default
  - id: transform
    command: "python transform.py --date {{ logical_date }}"
    deps: [extract]
  - id: load
    command: "psql -f load.sql"
    deps: [transform]
    retries: 3
    timeout: 1800
trigger_after:               # 可选:在另一个 DAG 成功后运行
  - dag_id: upstream_ingest
```

**模板变量**可用于任意 command、URL、header、body 或 query:
`{{ logical_date }}`、`{{ logical_datetime }}`、`{{ run_id }}`、`{{ dag_id }}`、`{{ task_id }}`、`{{ try_number }}`(同时注入为 `CRONOVA_*` 环境变量),以及 UI 管理的 `{{ var.KEY }}`、`{{ conn.ID.host }}`、`{{ params.KEY }}`。在控制台里你**不用手打 `{{ }}`** —— 可视化编辑器把每个变量渲染成**彩色药丸**,分组调色板支持**点击或拖拽**插入。

### 运行你自己的脚本 / 工程

在控制台(任务编辑器 → **工程**)上传单个脚本、整个工程文件夹或 `.zip`,再让 shell 任务指向它:

```yaml
tasks:
  - id: run_main
    type: shell
    command: python3 main.py     # 以「工程的干净副本」为工作目录运行
    project: my_app
```

每次尝试都会拿到该工程的**全新隔离副本**作为工作目录(`CRONOVA_PROJECT_DIR` 指向它),因此重新上传下次运行即生效、各次尝试互不干扰。详见 [docs/GETTING_STARTED.md](docs/GETTING_STARTED.md)。

## AI 智能体(MCP + 远程 CLI)

让 AI 通过**与人类同一套「令牌鉴权 + 角色控制」的 API** 编排 cronova —— 既可作为原生 **MCP 工具**,也可用**远程 JSON CLI**:

```bash
cronova tokens create my-agent -role admin     # 本地一次性签发令牌
cronova mcp                                     # 走 stdio 的 MCP server(Claude 等)

export CRONOVA_SERVER=http://localhost:8090 CRONOVA_TOKEN=cnv_pat_…
cronova dags -o json                            # 远程 CLI,JSON 输出
cronova api POST /api/dags/validate '{"dag_id":"x","tasks":[…]}'   # dry-run 校验
```

`cronova mcp` 暴露约 30 个由 API catalog 派生的工具(`list_dags`、`create_dag`、`validate_dag`、`trigger_dag`、`get_task_log`、`retry_task` …);`-read-only` 只暴露读操作。指南 + MCP 配置:**[docs/AGENTS.md](docs/AGENTS.md)**。

## 生产部署

cronova 是**调度器,不是运行时**:它用**主机自带的解释器**(`sh`、`python3`、`java`、`psql` …)把每个任务作为子进程启动(Azkaban 风格)—— 因此它以单个静态二进制部署到 **systemd(Linux)** 或 **launchd(macOS)**,无需容器或内置运行时。

```bash
cronova start | stop | restart | status   # 管理服务(自动 sudo 提权)
cronova update                             # 拉取并安装最新 release,然后重启
cronova update v0.2.1                      # 固定 / 降级到指定版本
cronova uninstall [--purge]                # 移除服务 + 二进制(--purge 连数据一起删)
```

一键安装脚本会跑一个交互式向导(端口、绑定范围、管理员账号、鉴权)。完整指南、服务 `PATH` 坑、崩溃可恢复 executor:**[docs/DEPLOY.md](docs/DEPLOY.md)**。

<div align="center">
  <img src="docs/img/graph.png" alt="cronova DAG 关系图 —— 在 Web 控制台可视化跨 DAG 的触发依赖" width="820">
</div>

## 文档

| 指南 | 内容 |
|---|---|
| [快速上手](docs/GETTING_STARTED.md) | 安装、第一个 DAG、工程、模板变量 |
| [DAG 参考](docs/DAG_REFERENCE.md) | 每个 DAG/任务字段、任务类型、触发、资源池 |
| [CLI 参考](docs/CLI.md) | 每条 `cronova` 命令与参数 |
| [AI 智能体(MCP)](docs/AGENTS.md) | MCP server、远程 CLI、令牌、安全 |
| [部署](docs/DEPLOY.md) | systemd/launchd、更新、崩溃可恢复 executor |
| [架构](docs/ARCHITECTURE.md) | 设计取舍、执行模型、图示 |
| [cronova vs Airflow](docs/COMPARISON.md) | 何时选 cronova、逐项对比 |
| [FAQ](docs/FAQ.md) | 常见问题解答 |

## 常见问题(FAQ)

**cronova 是 Airflow 的替代品吗?**
是 —— 面向想要 DAG 调度(依赖、重试、catchup、资源池、Web UI、REST API),又不想跑 Python 栈、独立数据库和消息队列的团队。cronova 是一个二进制 + 内置数据库。对于超大规模、重插件生态的数据平台,Airflow 仍是更丰富的选择。

**cronova 需要数据库、JVM 或 Python 吗?**
不需要。调度器和 Web 控制台是单个 Go 二进制 + 内置 **SQLite**。只有当**你的任务**要用到 Python/Java/psql 时,主机上才需要装它们。

**任务能用什么语言写?**
任意。任务类型为 `shell`、`python`、`sql`、`jar`、`http`;shell 任务可以调用主机上的任何东西(Node、Go、Rust 二进制 …)。框架(Go)与任务语言完全解耦。

**cronova 和 cron 有什么不同?**
cron 按时钟跑孤立命令。cronova 跑 **DAG**:带依赖、重试、超时、回填、并发资源池、跨 DAG 触发,还有带日志的 Web 控制台和 API —— 也就是你围着 cron 手搓的那些东西。

**AI 智能体能控制 cronova 吗?**
能。它内置 **MCP server**(`cronova mcp`)和远程 JSON CLI,AI 智能体可通过与人类同一套鉴权、角色受控的 API 管理工作流。

**支持哪些平台?**
Linux 与 macOS,amd64 与 arm64 均可。预编译二进制见 [Releases](https://github.com/zoyluoblue/cronova/releases)。

**是否生产可用 / 崩溃安全?**
把任务跑在解耦的 gRPC executor 里,调度器就能在不杀掉在跑任务的前提下重启或升级;恢复时重新挂接在跑任务,不会重复执行。

## 开发

```bash
go test -race ./...      # 全量测试

# 改了 proto/ 后重新生成 gRPC 代码(需要:buf + protoc-gen-go[-grpc])
buf generate

# UI 开发:从磁盘直接提供控制台静态资源(改完刷新,无需重编)
CRONOVA_WEB_DIR=internal/web/static go run ./cmd/cronova serve
```

欢迎贡献 —— 架构与设计说明见 [docs/](docs/)。

## 许可证

[MIT](LICENSE) © cronova authors。

---

<div align="center">

### ⭐ 觉得 cronova 有用?

在 **[GitHub 上点个 Star](https://github.com/zoyluoblue/cronova)** —— 这是支持项目最简单的方式,也能帮更多人发现一种更轻的工作流运行方式。

<sub>cronova —— 自托管<b>工作流调度器</b> · <b>Airflow 替代</b> · DAG 编排 · 单 Go 二进制 · 面向 AI 智能体的 MCP。</sub>

</div>
