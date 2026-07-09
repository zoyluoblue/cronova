# cronova CLI 参考

`cronova` 的每一个命令、子命令与标志——这个单一二进制文件既能运行工作流调度器、管理已安装的服务、在终端里操作 DAG，也能通过 REST API 为 AI 智能体提供服务。运行 `cronova <command> -h` 可查看任意命令自身的标志。入门请见[快速开始](GETTING_STARTED.md)；DAG YAML 模式请见 [DAG 参考](DAG_REFERENCE.md)。

```
cronova <command> [args] [flags]
```

命令分为四组：

| 分组 | 命令 | 作用范围 |
|---|---|---|
| [调度器](#scheduler) | `serve`、`cronova-executor` | 本机（长期运行的进程） |
| [服务生命周期](#service-lifecycle) | `start`/`stop`/`restart`/`status`、`init`、`update`、`uninstall`、`version`、`healthcheck` | 宿主机服务管理器（systemd / launchd） |
| [本地操作](#local-operations) | `trigger`、`dags`、`runs`、`backfill`、`prune`、`pools`、`users` | 直接操作 SQLite 数据库（`-db`）——加 `-server` 也可走远程 |
| [远程 / 智能体模式](#remote--agent-mode) | `api`、`get`、`run`、`logs`、`cancel`、`retry`、`mark`、`pause`、`overview`、`tokens`、`mcp` | 运行中服务器的带认证 REST API |

## 调度器

### `cronova serve`

运行调度循环，外加 Web 控制台与 REST API（默认 `http://localhost:8090`）。这就是完整的服务器——cron 解析、补跑、重试、任务执行、UI 与 API 全部在一个进程内。

```bash
cronova serve -db data/cronova.db -dags dags -http :8090
```

| 标志 | 默认值 | 说明 |
|---|---|---|
| `-http` | `:8090` | 控制台 + API 的 HTTP 地址（留空则禁用）。 |
| `-db` | `data/cronova.db` | SQLite 元数据数据库路径。 |
| `-dags` | `dags` | DAG YAML 定义所在目录。 |
| `-logs` | `logs` | 任务日志文件目录。 |
| `-projects` | `~/.cronova/projects` | 已上传[项目文件](tutorial/projects.md)的存放目录。 |
| `-executor` | *（进程内）* | gRPC executor 目标地址，例如 `unix:///tmp/cronova-executor.sock`。留空 = 进程内执行器。 |
| `-tick` | `2s` | 调度循环间隔。 |
| `-retention` | `2160h`（90 天） | 删除早于该窗口的已结束运行**及其日志**；`0` = 永久保留。一次性清理见 [`cronova prune`](#cronova-prune)。 |
| `-auth` | 关闭 | 要求登录才能访问控制台/API（覆盖配置文件）。 |
| `-config` | `cronova.yaml` | YAML 配置文件路径（可选）。 |
| `key_file` / `CRONOVA_KEY_FILE` | `cronova.key` | 仅限配置文件/环境变量（无命令行标志）：用于连接密码 at-rest 加密的密钥文件。首次 `serve` 时自动生成（`0600`）——务必备份；丢失后已存储的密码将无法解读。设为 `none` 可禁用加密（明文存储，启动时会告警）。 |

配置的解析顺序为：内置默认值 ← 配置文件 ← `CRONOVA_*` 环境变量 ← 显式标志。`CRONOVA_WEB_DIR`（仅限开发）会从磁盘加载控制台静态资源，而不是使用内嵌副本。

### `cronova-executor`（独立二进制）

独立、可从崩溃中恢复的任务执行器。先启动它，再用 `serve -executor` 让调度器指向它的 socket——任务可以在调度器重启后存活。参见[架构](ARCHITECTURE.md)。

```bash
cronova-executor -sock /tmp/cronova-executor.sock &
cronova serve -executor unix:///tmp/cronova-executor.sock
```

| 标志 | 默认值 | 说明 |
|---|---|---|
| `-sock` | `/tmp/cronova-executor.sock` | 监听的 Unix socket 路径。 |

## 服务生命周期

这些命令封装了宿主机的服务管理器——Linux 上是 systemd，macOS 上是 launchd——你无需再敲 `systemctl`/`launchctl` 的咒语。

!!! note "自动 sudo"
    写操作类命令（`start`、`stop`、`restart`、`update`、`uninstall`）会自动提权：如果你不是 root，CLI 会透明地通过 `sudo` 重新执行自身（你会看到密码提示）。`CRONOVA_*` 与标准 `*_PROXY` 环境变量会在提权后继续传递。设置 `CRONOVA_NO_SUDO=1` 可退出该行为、自行管理权限——此时命令会失败并给出 `sudo cronova …` 的提示。

### `cronova start` / `stop` / `restart`

控制已安装的服务。`start` 和 `restart` 会确认守护进程确实*持续*运行（而不仅仅是已加载）才报告成功——一个不断崩溃重启的二进制是错误，不是绿灯。

```bash
cronova restart
```

无标志。在未安装服务的主机上，请直接使用 `cronova serve`。

### `cronova status`

显示已安装服务的状态。只读——从不提权。在 Linux 上执行 `systemctl status cronova`；在 macOS 上执行 `launchctl print system/com.cronova`。

```bash
cronova status
```

### `cronova init`

首次安装向导：HTTP 端口、绑定范围（全部网卡还是 `127.0.0.1`）、管理员账号、认证开关——每一项都可按 Enter 接受默认值。它会写入服务器配置（`cronova.yaml`）和一个 `0600` 权限的密钥文件（`cronova.env`），其中包含种子管理员；`serve` 首次启动时会幂等地创建该管理员。重新运行时会把你当前的值显示为默认值。

```bash
cronova init          # interactive
cronova init -yes     # accept defaults / CRONOVA_* env, no prompts
```

| 标志 | 默认值 | 说明 |
|---|---|---|
| `-config` | `cronova.yaml` | 要写入的配置文件（环境变量 `CRONOVA_CONFIG`）。 |
| `-env` | `cronova.env` | 要写入的密钥文件——管理员种子（环境变量 `CRONOVA_ENV_FILE`）。 |
| `-yes` | | 非交互模式：直接接受默认值 / 环境变量，不提示。 |

非交互安装可通过 `CRONOVA_ADMIN_USER`、`CRONOVA_ADMIN_PASSWORD`、`CRONOVA_AUTH`、`CRONOVA_HTTP` 等预置取值。全新安装默认**开启**认证；无法识别的 `CRONOVA_AUTH` 值绝不会悄悄将其关闭。

### `cronova update`

从 GitHub 下载预编译发布版，校验其 SHA256 校验和，原子替换二进制（若安装了 `cronova-executor` 也一并替换），刷新服务定义（unit/plist），然后重启服务。

```bash
cronova update                               # latest release
cronova update v0.2.0                        # pin a tag (re-install / downgrade)
cronova update -proxy http://127.0.0.1:7890  # download through a proxy
```

| 标志 / 环境变量 | 说明 |
|---|---|
| `-proxy <url>` | 下载所用代理：`http(s)://host:port` 或 `socks5://host:port`。 |
| `CRONOVA_UPDATE_PROXY` | 等同于 `-proxy`；也识别 `HTTPS_PROXY` / `ALL_PROXY`。全部都会在 sudo 提权后保留。 |
| `CRONOVA_BASE_URL` | 覆盖下载源（私有镜像 / 测试）。 |

如果重启后的服务无法在新二进制上持续运行，`update` 会**自动回滚**：恢复旧的二进制和服务定义，并重启旧版本——机器绝不会停留在半途而废的更新状态。未固定版本的 `update` 若已是最新版本会以 `already up to date` 直接短路返回；固定版本则总是会被应用。参见[部署 → 更新](DEPLOY.md#updating)。

### `cronova uninstall`

移除服务与二进制文件。配置、数据库、DAG 和日志默认保留——重新安装即可恢复整个部署。

```bash
cronova uninstall            # keeps data (asks for confirmation)
cronova uninstall --purge    # also delete config, DB, DAGs, and logs
cronova uninstall -yes       # skip the confirmation prompt (scripts)
```

| 标志 | 说明 |
|---|---|
| `--purge` | 同时删除配置、数据库、DAG 和日志目录。 |
| `-yes` | 跳过确认提示。 |

### `cronova version`

打印构建版本与平台（即 `update` 会为这台主机获取的发布资产）。

```console
$ cronova version
cronova v0.3.0 darwin/arm64
```

### `cronova healthcheck`

探测服务器的就绪端点，不健康时以非零退出码结束——为 systemd、负载均衡器或 cron 探针提供的免 curl 存活检查。

```bash
cronova healthcheck -http :8090 && echo healthy
```

| 标志 | 默认值 | 说明 |
|---|---|---|
| `-http` | `:8090` | 服务器 HTTP 地址（环境变量 `CRONOVA_HTTP`）。 |
| `-path` | `/readyz` | 要探测的路径。 |

## 本地操作

在持有数据库的机器上运行；它们直接作用于 SQLite 数据库。所有命令都接受 `-db`（默认 `data/cronova.db`），大多数还接受[全局 `-server`/`-token`/`-o` 标志](#global-flags-and-environment)——给出 `-server` 后，同一命令就会改走 REST API（`backfill` 和 `prune` 仅限本地）。

### `cronova trigger`

创建一次 DAG 的手动运行，可附带触发参数（在任务中可作为[模板变量](tutorial/template-variables.md)使用）。

```console
$ cronova trigger example_etl -params '{"day":"2026-01-01"}'
created run example_etl__manual_1783442227904284000 (a running `cronova serve` will execute it)
```

| 标志 | 默认值 | 说明 |
|---|---|---|
| `-params` | | 以字符串值 JSON 对象形式给出的触发参数，例如 `'{"day":"2026-01-01"}'`。 |
| `-db` / `-dags` | `data/cronova.db` / `dags` | 本地数据库与 DAG 目录。 |

该运行会被排入数据库队列；运行中的 `cronova serve` 会拾取并执行它。

### `cronova dags`

列出已注册的 DAG。本地模式下会先从磁盘加载 DAG 目录，因此新添加的 YAML 文件在 `serve` 尚未运行时就会显示出来。

```console
$ cronova dags
DAG_ID             SCHEDULE   CATCHUP  PAUSED  MAX_ACTIVE
downstream_report  (manual)   false    true    1
example_etl        (manual)   false    false   1
ticker             @every 1m  false    true    1
upstream_ingest    (manual)   false    false   1
```

| 标志 | 默认值 | 说明 |
|---|---|---|
| `-db` / `-dags` | `data/cronova.db` / `dags` | 本地数据库与 DAG 目录。 |

### `cronova runs`

显示某个 DAG 最近的运行及各任务状态。

```console
$ cronova runs example_etl -n 3
RUN_ID                                   LOGICAL_DATE          STATE    TRIGGER  TASKS
example_etl__manual_1783442227904284000  2026-07-07T16:37:07Z  success  manual   extract=success transform=success validate=success load=success
example_etl__manual_1783442223878514000  2026-07-07T16:37:03Z  success  manual   extract=success transform=success validate=success load=success
example_etl__manual_1783442199726456000  2026-07-07T16:36:39Z  success  manual   extract=success transform=success validate=success load=success
```

| 标志 | 默认值 | 说明 |
|---|---|---|
| `-n` | `10` | 显示的最近运行数量。 |
| `-db` | `data/cronova.db` | SQLite 数据库路径。 |

远程模式下表格不包含 `TASKS` 列（runs 端点只返回运行本身）；用 `cronova run <run_id>` 查看单次运行的任务状态。底层的 `GET /api/dags/{id}/runs` 端点还接受 `state=`（逗号分隔，例如 `state=failed,cancelled`——未知状态名会被拒绝）与 `offset=` 用于筛选和分页；可通过 [`cronova api`](#cronova-api) 使用。

### `cronova backfill`

为日期窗口内的每个调度周期各排入一次运行——在修复 bug 后重跑历史，或为新添加的 DAG 补齐过去的周期。已存在运行（**任意**状态）的周期会被跳过，因此重复执行回填绝不会重复运行任何东西；`to` 会被收敛到当前时刻（未来的周期属于调度器）；覆盖超过 500 个周期的窗口会被直接拒绝。执行受 DAG 的 `max_active_runs` 节流，与补跑（catchup）完全一致。

```console
$ cronova backfill daily_etl -from 2026-07-01 -to 2026-07-05
backfill daily_etl: created 5 run(s), skipped 0 existing (a running `cronova serve` executes them)
```

| 标志 | 默认值 | 说明 |
|---|---|---|
| `-from` / `-to` | *（必填）* | 窗口起点/终点，`YYYY-MM-DD`，两端均含。 |
| `-db` / `-dags` | `data/cronova.db` / `dags` | 本地数据库与 DAG 目录。 |

DAG 必须带有 `schedule`——回填按调度周期枚举。仅限本地；面向远程服务器请调用 API，它还接受 RFC3339 时间戳：`cronova api POST /api/dags/daily_etl/backfill '{"from":"2026-07-01","to":"2026-07-05"}'`。

### `cronova prune`

删除早于保留窗口的已结束运行——数据库行加上它们的日志目录。它是 [`serve -retention`](#cronova-serve) 的手动对应命令，用于一次性清理，或用于禁用了 retention 的部署。仅限本地；除非给出 `-yes`，否则会先要求确认。

```bash
cronova prune                    # finished runs older than 90 days (asks first)
cronova prune -older-than 720h   # custom window (30 days)
cronova prune -yes               # no confirmation (scripts / cron)
```

| 标志 | 默认值 | 说明 |
|---|---|---|
| `-older-than` | `2160h`（90 天） | 删除早于该窗口的已结束运行（必须为正值）。 |
| `-yes` | | 跳过确认提示。 |
| `-db` / `-logs` | `data/cronova.db` / `logs` | 本地数据库与日志目录。 |

### `cronova pools`

列出[资源池](tutorial/retries-timeouts-pools.md)，或用 `pools set` 创建/调整某个资源池。

```console
$ cronova pools
NAME     SLOTS
default  16
reports  4
$ cronova pools set reports 8
pool "reports" set to 8 slots
```

| 用法 | 说明 |
|---|---|
| `cronova pools` | 列出资源池及其槽位数。 |
| `cronova pools set <name> <slots>` | 创建或调整资源池（`slots` 必须为正整数）。 |

### `cronova users`

管理 Web 控制台账号。仅限本地——账号管理是服务器主机上的操作。

```bash
cronova users list
cronova users add alice -role viewer -password s3cret
cronova users passwd alice          # prompts for the new password
cronova users delete alice
```

| 子命令 | 标志 | 说明 |
|---|---|---|
| `list` | | 列出账号及其角色与创建时间。 |
| `add <name>` | `-role admin\|viewer`（默认 `viewer`）、`-password` | 创建账号。 |
| `passwd <name>` | `-password` | 修改密码——现有会话会被吊销。 |
| `delete <name>` | | 删除账号。 |

省略 `-password` 时，密码会从 stdin 读取（带提示）。`-db` 同样识别 `CRONOVA_DB`。

## 远程 / 智能体模式

通过运行中服务器的**令牌认证、按角色鉴权的 REST API** 来操作——与浏览器控制台走的是同一条路径。脚本、CI 和 AI 智能体正是通过这种方式从任何地方操作 cronova。完整智能体指南：[AI 智能体（MCP）](AGENTS.md)。

### 全局标志与环境变量

每个操作类命令都接受这些标志；也可以将它们设置为环境变量，一次配置整个会话。

| 全局标志 | 环境变量 | 说明 |
|---|---|---|
| `-server <url>` | `CRONOVA_SERVER` | 服务器 URL，例如 `http://localhost:8090`。留空 = 本地数据库。 |
| `-token <token>` | `CRONOVA_TOKEN` | API 令牌（用 [`cronova tokens create`](#cronova-tokens) 签发）。 |
| `-o table\|json` | `CRONOVA_OUTPUT` | 输出格式；脚本与智能体使用 `json`。 |

```bash
export CRONOVA_SERVER=http://localhost:8090 CRONOVA_TOKEN=cnv_pat_…
cronova dags -o json
```

!!! tip "面向脚本的退出码"
    命令在 API 出错时以非零退出码结束（错误内容会先打印出来），因此 `cronova trigger etl && …` 可以在 CI 中安全地串联。

### `cronova api`

对任意 REST 端点的原始透传——无需为每个端点单独提供子命令即可触达完整 API 面的逃生舱口。JSON 响应会被美化打印。

```bash
cronova api GET  /api/dags
cronova api POST /api/dags/etl/trigger '{"params":{"day":"2026-01-01"}}'
```

用法：`cronova api <METHOD> <path> [json-body]`。

### `cronova get`

显示 DAG 定义（`GET /api/dags/{id}`）。

```bash
cronova get example_etl
```

### `cronova run`

显示单次运行及其任务状态（`GET /api/runs/{runID}`）——即 `runs` 命令按任务查看细节的远程对应命令。

```bash
cronova run example_etl__manual_1783442227904284000
```

### `cronova logs`

以纯文本形式获取任务实例的日志。任务实例 ID 可从 `cronova run` 或 Web 控制台的运行详情页获取。

```bash
cronova logs 42
```

### `cronova cancel`

取消一个活跃的运行。

```bash
cronova cancel example_etl__manual_1783442227904284000
```

### `cronova retry`

重试某次运行的失败任务，或单个任务。

```bash
cronova retry example_etl__manual_1783442227904284000            # all failed tasks
cronova retry example_etl__manual_1783442227904284000 transform  # one task
```

### `cronova mark`

运维人员对运行或任务状态的手动覆盖——跳过已知有问题的任务，或在手动修复后把运行强制置为成功。

```bash
cronova mark <run_id> success                # mark the run:  success | failed
cronova mark <run_id> <task_id> skipped      # mark one task: success | failed | skipped
```

| 目标 | 合法状态 |
|---|---|
| 运行（`mark <run_id> <state>`） | `success`、`failed` |
| 任务（`mark <run_id> <task_id> <state>`） | `success`、`failed`、`skipped` |

### `cronova pause`

暂停 DAG 的调度，或用 `-off` 恢复。已暂停的 DAG 会跳过其 cron 调度，但仍可手动触发。

```bash
cronova pause ticker
cronova pause ticker -off   # resume
```

### `cronova overview`

仪表盘摘要——一次调用返回 DAG 数量、活跃运行与资源池使用情况（`GET /api/overview`）。配合 `-o json` 可用于监控脚本。

```bash
cronova overview -o json
```

### `cronova tokens`

签发并管理 API 令牌。**`create` 有意设计为仅限本地**——它直接写入 SQLite 存储，因为第一枚令牌不可能来自它自己所解锁的那个 API（而且令牌管理本就属于服务器主机上的操作）。`list`/`delete` 同样仅限本地。

```console
$ cronova tokens create ci-bot -role admin
created admin token "ci-bot"

  cnv_pat_3JLKxC…

Store it now — it is not shown again. Use it with:
  export CRONOVA_TOKEN=cnv_pat_3JLKxC…

$ cronova tokens list
ID  NAME        ROLE    PREFIX           LAST_USED
3   docs-demo   viewer  cnv_pat_eP6UPK…  never
2   deploy-bot  admin   cnv_pat_FhFJ2V…  never
1   ci-bot      admin   cnv_pat_3JLKxC…  never
```

| 子命令 | 标志 | 说明 |
|---|---|---|
| `create <name>` | `-role admin\|viewer`（默认 `admin`）、`-db` | 签发令牌。明文只显示一次；数据库中只存哈希。 |
| `list` | `-db` | 列出令牌及其角色、前缀与最近使用时间。 |
| `delete <id>` | `-db` | 按 ID 吊销令牌。 |

### `cronova mcp`

通过 stdio 运行一个 [Model Context Protocol](https://modelcontextprotocol.io/) 服务器，把 cronova 的操作以工具形式暴露给 AI 客户端（Claude Code、Claude Desktop、任何 MCP 宿主）。它通过 REST API 与运行中的服务器通信，因此 AI 的权限范围恰好等于其令牌的角色。stdout 承载协议；日志输出到 stderr。

```bash
CRONOVA_TOKEN=cnv_pat_… cronova mcp -read-only
```

| 标志 | 环境变量 | 说明 |
|---|---|---|
| `-server <url>` | `CRONOVA_SERVER` | 服务器 URL（默认 `http://localhost:8090`）。 |
| `-token <token>` | `CRONOVA_TOKEN` | API 令牌——未设置时启动会给出警告。 |
| `-read-only` | | 仅暴露读取（GET）类工具。 |

完整指南、MCP 配置片段与安全注意事项：[AI 智能体（MCP）](AGENTS.md)。

## 常见问题

**本地命令需要服务器在运行吗？** 不需要——`trigger`、`dags`、`runs`、`backfill`、`prune`、`pools`、`users` 和 `tokens` 直接作用于 SQLite 数据库。由 `trigger` 或 `backfill` 排入队列的运行会在 `serve` 运行后被执行。

**如何在另一台机器上运行 CLI？** 设置 `CRONOVA_SERVER` 和 `CRONOVA_TOKEN`（或 `-server`/`-token`）；此后每个操作类命令都走 REST API。令牌签发仍须在服务器主机上进行。

**为什么 `cronova start` 向我索要密码？** 服务类命令会通过 `sudo` 自动提权以访问 systemd/launchd。设置 `CRONOVA_NO_SUDO=1` 可禁用此行为，改由你自己执行 `sudo cronova start`。

**`mark` 可以设置哪些状态？** 运行：`success` 或 `failed`。任务：`success`、`failed` 或 `skipped`。

## 另请参阅

- [快速开始](GETTING_STARTED.md) · [DAG 参考](DAG_REFERENCE.md) · [AI 智能体（MCP）](AGENTS.md) · [部署](DEPLOY.md) · [FAQ](FAQ.md)
