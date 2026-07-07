# 任务类型：shell、python、sql、jar、http

cronova **DAG** 中的每个任务都有一个 `type`，它告诉工作流调度器*如何*执行该任务。本章将逐一介绍全部五种类型——`shell`、`python`、`sql`、`jar` 和 `http`——每种类型都配有一个最小可运行示例，并说明哪些类型需要在主机上安装工具，哪些已内置于二进制文件、无需额外依赖。

## `shell` — 运行任意命令

`shell` 任务通过 `sh -c` 把 `command` 作为操作系统子进程运行，因此管道、重定向、`$(...)` 替换和环境变量都与你在终端里的行为完全一致。

创建 `dags/type_shell.yaml`（不写 `schedule`，所以它只在你手动触发时运行）：

```yaml
dag_id: type_shell
tasks:
  - id: hello
    type: shell
    command: echo "hello from $(uname -s) at $CRONOVA_LOGICAL_DATETIME"
```

触发并查看结果：

```bash
cronova trigger type_shell
cronova runs type_shell
```

`hello` 任务会进入 **success** 状态。在控制台 [http://localhost:8090](http://localhost:8090) 打开这次运行并点击该任务——日志会显示类似 `hello from Darwin at 2026-07-07T09:00:00Z` 的内容。

!!! note
    `shell` 是**默认**类型——前面章节里你写的每个任务其实都是 shell 任务。`type: shell` 完全可以省略。

shell 任务可以调用主机上安装的任何东西：Python 脚本、Node CLI、`psql`、编译好的二进制文件。它也是唯一支持[上一章](projects.md)中 `project` 字段的类型。

## `python` — 内联 Python 代码

`python` 任务把 Python **代码**写在 `command` 里，并用服务 `PATH` 上的 `python3`（找不到时回退到 `python`）运行。多行代码请使用 YAML 块标量（`|`）。代码是作为参数直接传给解释器的——不经过 shell——所以你永远不需要转义引号。

创建 `dags/type_python.yaml`：

```yaml
dag_id: type_python
tasks:
  - id: crunch
    type: python
    command: |
      import os, platform
      print("python", platform.python_version())
      print("processing", os.environ["CRONOVA_LOGICAL_DATE"])
```

`CRONOVA_*` 运行变量同样存在于环境中，和 shell 任务一样。

触发并查看：

```bash
cronova trigger type_python
cronova runs type_python
```

任务日志会显示解释器版本和逻辑日期。任务的结果就是解释器的退出码，因此未捕获的异常会让任务失败——如果配置了重试，也会随之触发重试。如果服务 `PATH` 上没有解释器，日志会显示 `python: no python3/python interpreter on PATH`，任务失败。

## `sql` — 查询数据库，无需客户端工具

`sql` 任务把查询写在 `command` 里，并通过 `conn` 指定一个[连接](variables-connections-params.md)。cronova 使用编译进二进制文件的原生驱动自行打开数据库——连接的*类型*决定使用 PostgreSQL、MySQL/MariaDB 还是 SQLite——因此不需要安装 `psql` 或 `mysql` 客户端。

```yaml
dag_id: type_sql
tasks:
  - id: count_events
    type: sql
    conn: warehouse
    command: "SELECT count(*) FROM events WHERE day = '{{ logical_date }}'"
```

这里 `warehouse` 是你在控制台创建的连接的 id，查询和其他 `command` 一样按每次运行做模板渲染。

任务日志会显示结果：返回行的语句（`SELECT`、`WITH`、`SHOW` 等）会以制表符分隔的形式记录列名和行（前 100 行），后跟行数；其他语句则记录 `(N rows affected)`。

!!! tip
    你可以在零基础设施的情况下试用 `sql` 任务：创建一个类型为 `sqlite` 的连接，并把它的 **host** 设为一个文件路径（对 SQLite 来说，host 存放的是数据库文件）。让 `conn` 指向它，配上 `command: "SELECT 1 AS ok"` 并触发——日志会显示：

    ```
    ok
    1
    (1 rows)
    ```

## `jar` — 运行 Java 程序

`jar` 任务运行一条 `java -jar …` 命令行。它的执行方式与 `shell` 任务的 shell 语义完全相同——参数、引号、环境变量和 `{{ }}` 模板的行为都一致——这个类型的意义在于标明该任务是一个 Java 作业。它需要服务 `PATH` 上有 JRE/JDK。

```yaml
dag_id: type_jar
tasks:
  - id: report
    type: jar
    command: "java -jar /opt/jobs/report.jar --date {{ logical_date }}"
```

验证方式相同：触发后在任务日志中查看程序的 stdout。JVM 以非零退出码结束会导致任务失败。如果任务立即失败且日志中出现类似 "command not found" 的行，说明 *服务* 看到的 `PATH` 上没有 `java`——请在该环境下用 `java -version` 验证。

## `http` — 调用 API，无需 curl

`http` 任务完全不使用 `command`。你在任务的 `http:` 键下描述请求，cronova 用进程内 HTTP 客户端执行它——不需要安装任何东西，而且会自动跟随重定向。

| 字段 | 默认值 | 含义 |
|---|---|---|
| `method` | `GET` | HTTP 方法 |
| `url` | —（必填） | 请求 URL；支持 `{{ }}` 模板 |
| `headers` | — | 请求头映射；值支持模板 |
| `body` | — | 请求体；支持模板 |
| `expected_status` | 任意 2xx | 视为成功的状态码，例如 `[200, 201]` |

最小示例——创建 `dags/type_http.yaml`：

```yaml
dag_id: type_http
tasks:
  - id: ping
    type: http
    http:
      url: https://example.com
```

触发后在控制台打开任务日志，你会看到一份请求/响应记录：

```
> GET https://example.com
< 200 OK (142ms)
<!doctype html>
…
```

如果状态码不在接受范围内，日志会以 `http: unexpected status 503 (want 2xx)` 结尾，任务失败——同样，这正是驱动重试的机制。

一个更贴近实际的调用会在 URL、请求头和请求体中组合使用模板：

```yaml
  - id: ingest
    type: http
    http:
      method: POST
      url: "https://{{ conn.api.host }}/ingest"
      headers:
        Authorization: "Bearer {{ var.TOKEN }}"
      body: '{"date": "{{ logical_date }}"}'
      expected_status: [200, 201]
```

host 来自连接，token 来自受管变量——因此没有任何机密信息出现在 YAML 文件里。

## 内置执行 vs. 依赖主机工具

五种类型可以清晰地分成两组：

| 类型 | 运行方式 | `command` 存放的内容 | 主机上需要 |
|---|---|---|---|
| `shell` | 操作系统子进程（`sh -c`） | 任意 shell 命令 | 命令所调用的工具 |
| `python` | 操作系统子进程（`python3`） | Python 代码 | 服务 `PATH` 上的 `python3` |
| `sql` | 进程内（原生驱动） | SQL 查询；`conn` 选择连接 | 无额外依赖 |
| `jar` | 操作系统子进程（`java`） | 一条 `java -jar …` 命令 | `PATH` 上的 JRE/JDK |
| `http` | 进程内 HTTP 客户端 | —（使用 `http:` 配置） | 无额外依赖 |

`sql` 和 `http` 内置在 cronova 二进制文件中，在裸机上即可工作。`shell`、`python` 和 `jar`——以及 shell 任务调用的任何东西——都需要在调度器运行的机器上安装相应工具。

!!! warning
    当 cronova 作为 systemd 或 launchd 服务运行时，任务继承的是**服务的** `PATH`，它通常比你交互式 shell 的 `PATH` 短得多。一条在你终端里能运行的命令，在服务下仍可能以 `command not found` 失败——修复方法见[部署](../DEPLOY.md)。

每种类型逐字段的完整 schema 见 [DAG 参考](../DAG_REFERENCE.md)。

## 本章小结

- 每个任务都有一个 `type`；`shell` 是默认类型，通过 `sh -c` 运行 `command`。
- `python` 用 `python3` 运行内联代码，`jar` 以 shell 语义运行一条 `java -jar` 命令——两者都需要服务 `PATH` 上有对应运行时。
- `sql`（一个 `conn` id 加一条查询）和 `http`（包含 `method`、`url`、`headers`、`body`、`expected_status` 的 `http:` 配置）内置于二进制文件，查询、URL、请求头和请求体中均可使用模板。

下一章：用[重试、超时与资源池](retries-timeouts-pools.md)让任务更健壮。
