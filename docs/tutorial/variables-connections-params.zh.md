# 变量、连接与参数

把主机名、密码、日期硬编码进 DAG YAML，是工作流调度器把密钥泄漏到 git 里的常见方式。本章讲解 cronova 由 UI 管理的三个命名空间——`{{ var.KEY }}`、`{{ conn.ID.FIELD }}` 和 `{{ params.KEY }}`——让你的 DAG 文件保持干净，密钥也不会混进去。

上一章你已经认识了 `{{ logical_date }}` 这样的内置运行变量。这三个命名空间不同：它们的值存放在 cronova 的数据库里，而不是 YAML 中，并且都在控制台里管理。

| 命名空间 | 存放什么 | 在哪里管理 |
|---|---|---|
| `{{ var.KEY }}` | 共享配置值 | 控制台 → **Variables & Connections** |
| `{{ conn.ID.FIELD }}` | 外部系统的凭据 | 控制台 → **Variables & Connections** |
| `{{ params.KEY }}` | 触发时按次传入的值 | `cronova trigger -params` 或控制台 |

## 共享变量：`{{ var.KEY }}`

**变量**是所有 DAG 共享的具名值——一个 API 基础 URL、一个 bucket 名称、一个 webhook。在控制台里改一次，所有引用它的任务在下一次运行时就会拿到新值。

打开控制台 **http://localhost:8090**，进入 **Variables & Connections** 页面。添加一个变量，key 为 `greeting`，值为 `hello from a shared variable`。名称允许字母、数字和 `_ . -`。

现在在 DAG 中引用它。创建 `dags/use_vars.yaml`：

```yaml
dag_id: use_vars
tasks:
  - id: show
    type: shell
    command: echo "{{ var.greeting }}"
```

触发并查看结果：

```bash
./cronova trigger use_vars
./cronova runs use_vars
```

在控制台点进这次运行，打开 `show` 任务的日志——它打印出 `hello from a shared variable`。`{{ var.greeting }}` 占位符在派发时被替换，值是在那一刻从存储中取出的。

在控制台里修改这个变量的值再触发一次：新的运行会打印新值。无需改 YAML，无需重载。

## 连接：`{{ conn.ID.FIELD }}`

**连接**把一个外部系统的凭据——数据库、API、数据仓库——打包在一个 id 之下。在同一个 **Variables & Connections** 页面上，创建一个 id 为 `api` 的连接并填写各字段。

每个连接都有一组固定字段可供引用：

| 字段 | 引用方式 |
|---|---|
| host | `{{ conn.api.host }}` |
| port | `{{ conn.api.port }}` |
| login | `{{ conn.api.login }}`（别名：`{{ conn.api.user }}`） |
| password | `{{ conn.api.password }}` |
| type | `{{ conn.api.type }}` |
| 任意额外 JSON 字段 | `{{ conn.api.extra.KEY }}` |

任何支持模板的地方都可以使用——shell 命令，或 `http` 任务的 URL、请求头和请求体：

```yaml
dag_id: call_api
tasks:
  - id: fetch
    type: shell
    command: 'curl -s -u {{ conn.api.login }}:{{ conn.api.password }} "https://{{ conn.api.host }}/status"'
  - id: ingest
    type: http
    deps: [fetch]
    http:
      method: POST
      url: "https://{{ conn.api.host }}/ingest"
      headers: { Authorization: "Bearer {{ var.TOKEN }}" }
      body: '{"date":"{{ logical_date }}"}'
      expected_status: [200, 201]
```

!!! note

    对于 `sql` 任务，通常根本不需要模板化这些字段。改为设置任务级的
    `conn: warehouse` 字段——连接的 `type` 决定驱动
    （postgres / mysql / sqlite），cronova 会为你构建 DSN。参见
    [DAG Reference](../DAG_REFERENCE.md#task-types)。

## 按次运行参数：`{{ params.KEY }}`

变量和连接是共享状态。**参数**恰恰相反：是你手动触发时为*某一次具体运行*传入的值——要重新处理的日期、客户 id、dry-run 开关。

创建 `dags/daily_report.yaml`（没有 `schedule`，所以只能手动触发）：

```yaml
dag_id: daily_report
tasks:
  - id: build
    type: shell
    command: echo "building report for {{ params.day }} (env says $CRONOVA_PARAM_DAY)"
```

以 JSON 对象的形式带参数触发：

```bash
./cronova trigger daily_report -params '{"day":"2026-01-01"}'
```

在控制台查看 `build` 任务的日志：两种形式都打印出 `2026-01-01`。每个参数都能通过两种方式获取——既是 `{{ params.KEY }}` 模板，*也*是 `CRONOVA_PARAM_<KEY>` 环境变量（key 转为大写），所以只读环境变量的脚本同样能用。

在控制台里，Trigger 旁边的 **⋯** 按钮会打开 **Trigger with params** 对话框——一个键值对表单，不用写 JSON 就能做同样的事。

!!! tip

    在控制台的任务编辑器里，你从来不需要手打 `{{ }}` 花括号。每个
    引用都渲染成带颜色的**胶囊（pill）**，还有一个分组的候选面板
    （内置 · 变量 · 连接 · 参数），点击或拖拽即可插入。

## 密钥如何被隔离

cronova 对 `var.*` 和 `conn.*` 采用惰性解析，在服务端、派发时进行——并且**只在任务显式引用它们时**才解析。它们绝不会被批量注入到每个任务的环境变量中，因此连接密码不会泄漏到无关任务的 env（或其日志里的 `env` 输出）中。只有内置运行变量和触发参数会成为 `CRONOVA_*` 环境变量。

!!! warning

    模板会在命令运行前被替换进命令文本，所以执行
    `echo {{ conn.api.password }}` 的任务会把密钥打印进自己的日志。
    请在真正消费密钥的地方引用它（认证参数、请求头）——不要
    echo 它。

## 本章小结

- `{{ var.KEY }}` 和 `{{ conn.ID.FIELD }}` 从控制台的 **Variables & Connections** 页面获取共享配置和凭据——不再写进 DAG YAML。
- 连接字段包括 `host`、`port`、`login`/`user`、`password`、`type` 和 `extra.KEY`；`sql` 任务改用任务级的 `conn:` id。
- `cronova trigger <dag_id> -params '{"day":"…"}'` 传入按次运行的值，可通过 `{{ params.KEY }}` 或 `CRONOVA_PARAM_<KEY>` 读取。
- 变量和连接只在被引用时才解析——密钥绝不会注入到每个任务的环境中。

**下一步：**把一个真实代码库接入任务——上传一次，然后作为[工程](projects.md)运行。
