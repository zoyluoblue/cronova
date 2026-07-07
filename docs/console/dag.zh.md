# 操作单个 DAG

DAG 页面（`#/dag/<id>`）是你在 cronova 网页控制台中运维单个工作流的地方：触发和监控运行、编辑任务和依赖、调整调度与安全设置。此页面上的一切改动都会自动保存——没有保存按钮。

在[仪表盘](dashboard.md)上点击任意一行即可打开该页面。页面由一个 hero 头部和三个标签页组成——**Runs**、**Structure** 和 **Settings**——每个标签页都可以直接通过链接访问（`#/dag/<id>/structure`、`#/dag/<id>/settings`）。

## Hero 头部

页面顶部汇总了 DAG 的概况，并承载其主要操作。

**实时信息**（根据最近 25 次运行重新计算）：

| 信息 | 显示内容 |
|---|---|
| Last run | 最近一次运行的状态徽章、开始时间和耗时——若尚无运行则显示 "No runs yet" |
| Schedule | 一段通俗描述加原始表达式（例如 "Runs every 5 min · @every 5m"），或 "manual trigger only" |
| Success rate | 最近运行中的 `succeeded/finished` 比例（例如 `8/10 success`） |

**操作：**

| 控件 | 作用 |
|---|---|
| ▶ Trigger run | 立即将一次手动运行加入队列 |
| ⋯（Trigger with params） | 打开一个键/值对话框，然后带参数触发 |
| Pause / Resume | 切换 `paused` 状态——已暂停的 DAG 不会被调度，但仍可手动触发 |
| ⧉ Duplicate | 以新 id 复制整个 DAG（相同的定义，全新的运行历史） |
| YAML | 打开 [YAML 抽屉](#yaml-抽屉) |

在 DAG id 旁边你还会看到一个保存状态徽章（**Saved** / **Saving…** / **Fix errors to save** / **Save failed**）。此页面上的每次编辑——无论结构还是设置——都会在短暂防抖后经过校验并写入服务器；校验错误会显示在标签页下方的横幅中，并在修复之前阻止保存。

!!! note
    当 DAG 没有任何任务时，两个触发按钮都会被禁用——新创建的空壳 DAG 会直接打开 **Structure** 标签页，方便你添加第一个任务。

## 触发一次运行

点击 **▶ Trigger run** 立即将一次运行加入队列。控制台会先刷写所有待保存的编辑（确保这次运行使用你的最新定义），显示 "Triggered — run queued" 提示，随后新运行会以触发类型 `manual` 出现在 **Runs** 标签页顶部。

若要传入运行时参数，请改为点击 **⋯** 按钮。对话框中可以添加键/值行（空行会被忽略）：

- 每个键会以 `CRONOVA_PARAM_*` 环境变量的形式注入到任务环境中。
- 命令可以通过 `{{ params.key }}` 模板变量引用这些值。

完整的模板机制参见[变量、连接与参数](../tutorial/variables-connections-params.md)。

## Runs 标签页

![DAG runs 标签页](../img/console/dag-runs.png)

默认标签页列出该 DAG 最近的 25 次运行，最新的在最前：

| 列 | 内容 |
|---|---|
| logical date | 该运行的逻辑日期（它所代表的数据区间） |
| state | 状态徽章：`queued`、`running`、`success`、`failed`、`cancelled`、`timed out` 等 |
| trigger | 运行的触发方式：`scheduled`、`manual` 或 `dependency`（跨 DAG） |
| started | 实际开始时间 |
| duration | 已耗时（运行中的运行会实时更新） |

点击某一行可打开该运行的详情页，查看每个任务的状态和日志——参见[运行、日志与恢复](runs-logs.md)。

**行内操作**显示在最后一列（只读的 viewer 会话中隐藏）：

- **✕ Cancel run**——对 `queued` 或 `running` 状态的运行可用；先弹出确认，然后终止它。
- **↻ Retry failed**——对 `failed`、`cancelled` 或 `timed out` 状态的运行可用；原地重新入队。

只要有任何运行处于 `queued` 或 `running` 状态，该标签页就会每 3 秒轮询一次服务器，自动刷新列表和 hero 信息。一旦全部结束，轮询会自行停止，因此闲置的 DAG 页面不会产生任何后台请求。

## Structure 标签页

![DAG structure 标签页——任务与依赖图](../img/console/dag-structure.png)

Structure 标签页展示 DAG 的依赖图和任务列表。标签页药丸上显示任务数量。

### 依赖图

依赖图将每个任务渲染为一个节点，边表示依赖关系。你可以对其平移和缩放，而最核心的交互是——**通过点击编辑边**：

1. 点击上游任务的节点（它成为待选中状态）。
2. 点击下游任务的节点。

如果该边不存在则会被添加；如果已存在则会被移除。连续点击同一节点两次会取消选择。每条新边都会先进行环检测——会造成依赖环的连接将被拒绝，并弹出 "Dependency cycle detected" 提示，任何内容都不会被保存。

### 任务表格

依赖图下方，每个任务占一行：

| 列 | 内容 |
|---|---|
| id | 任务 id（等宽字体） |
| type | 任务类型（`shell`、`python`、`http` 等） |
| command | 命令摘要（点击可复制完整命令） |
| pool | 任务运行所在的并发资源池 |
| trigger rule | 任务相对于上游的触发时机（`all success`、`one failed` 等） |
| deps | 上游任务 id |

点击某一行会在[任务编辑器](task-editor.md)中打开该任务。每行还有：

- **⧉ Duplicate**——以 `<id>_copy` 为名克隆该任务，并保留相同的依赖。
- **✕ Remove**——经确认对话框后删除该任务；其他任务 `deps` 中对它的引用会被自动清理。

**+ Add task** 会创建一个新任务（`task_1`、`task_2` 等）并直接带你进入任务编辑器填写命令。所有任务字段参见 [DAG 与任务参考](../DAG_REFERENCE.md)，触发规则的行为参见[依赖与触发规则](../tutorial/dependencies.md)。

## Settings 标签页

![DAG settings 标签页](../img/console/dag-settings.png)

设置项以单行摘要的形式列出——点击某一行可就地展开编辑器，修改值后点击 **Done**。改动在输入时即刻保存。

| 设置 | 控制内容 |
|---|---|
| Schedule | Manual / Interval / Cron——与新建 DAG 对话框相同的三种模式，带有 cron 预设、实时的 "Next: …" 触发时间预览和开始日期。Catchup 复选框已展示但目前禁用（"coming soon"）。 |
| Max active runs | 该 DAG 允许同时执行多少次运行 |
| Default retries | 应用于未自行设置重试次数的任务的重试次数 |
| SLA (soft) | 从运行开始计时的秒数；若届时运行尚未完成则触发告警，但运行继续执行。`0` 表示关闭。需要配置通知 webhook。 |
| Run timeout (hard) | 从运行开始计时的秒数；超时后运行被强制失败，运行中的任务会被杀掉，运行以 `timed_out` 结束。`0` 表示关闭。 |
| Upstream DAGs | 选择其他 DAG，它们成功后会自动触发此 DAG——参见[跨 DAG 触发与通知](../tutorial/cross-dag.md) |
| Notifications | 一个 webhook URL（兼容 Slack/Feishu/Discord）加 **Failure** 与 **Success** 事件选项；当运行以选中的状态结束时会以 POST 方式发送一段 JSON 载荷。只有设置了 URL 之后才能选择事件。 |

调度语法（`@every 5m`、五段式 cron）参见[调度与补跑](../tutorial/scheduling.md)。

### 危险区域：删除 DAG

在 Settings 标签页底部，**Delete** 在确认对话框后将 DAG 归档：它会从各列表中消失并停止被调度，但运行历史会保留，之后仍可恢复。当 DAG 存在活跃运行时删除会被拒绝（HTTP 409）——请先取消这些运行。

!!! warning
    cronova 中的删除是归档而非清除——历史数据会保留。但该 DAG 会立即停止调度，包括通过跨 DAG 触发依赖它的所有下游 DAG。

## YAML 抽屉

点击 hero 中的 **YAML**，即可看到控制台表单所写入的 DAG 原样内容——与你在 CLI 中管理的是同一份 YAML。在抽屉中你可以 **Copy** 将其复制到剪贴板，或 **Download** 下载为 `<dag_id>.yaml`。这是用于代码评审、版本控制或迁移 DAG 定义的出口通道——格式记录在 [DAG 与任务参考](../DAG_REFERENCE.md)中。

## 复制 DAG

点击 hero 中的 **⧉ Duplicate**，输入新的 DAG id（默认为 `<id>_copy`）并确认。完整的定义——任务、依赖、调度、设置——会被复制到新 id 下，并附带全新的空运行历史，随后控制台会打开新 DAG 的页面。

!!! tip
    Duplicate 是预演高风险改动最快的方式：复制 DAG，在副本上编辑并手动触发，等副本运行通过后再把改动移植回去。

## 常见问题

**保存按钮在哪里？**
没有保存按钮。每次编辑都会在短暂防抖后自动保存；留意 DAG id 旁的徽章——**Saved** 表示服务器已持有你的最新版本。如果显示 **Fix errors to save**，请先解决标签页下方列出的错误。

**暂停 DAG 会终止其正在进行的运行吗？**
不会。暂停只停止后续调度。已入队或正在运行的运行会继续执行；如有需要，请在 Runs 标签页中逐个取消。

**可以重新运行一次成功的运行吗？**
无法通过行内操作实现——重试只出现在 `failed`、`cancelled` 和 `timed out` 状态的运行上。若要全新执行一次，请触发新的运行；若要重跑某次运行中的个别任务，请打开运行详情页（[运行、日志与恢复](runs-logs.md)）。

**如何在两个任务之间添加依赖？**
既可以在 Structure 标签页的依赖图中先点上游再点下游，也可以在[任务编辑器](task-editor.md)中打开下游任务并切换其上游选项。两条路径都会进行环检测。
