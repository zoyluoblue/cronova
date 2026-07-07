# 仪表盘与创建 DAG

DAG 仪表盘是 cronova Web 控制台的主页（默认地址 `http://localhost:8090`）——单个页面展示所有工作流定义、它们最近的健康状况以及下一次计划运行。本页逐一介绍仪表盘的各个元素，并演示如何通过起步模板、cron 调度或粘贴 YAML 来创建新 DAG。

![cronova dashboard — DAG list with stats, sparklines and schedules](../img/console/dashboard.png)

在全新实例上，仪表盘显示"No DAGs yet"的引导页面，只有一个创建按钮。一旦存在至少一个 DAG，就会呈现完整布局：统计卡片、活动条带和 DAG 表格。

!!! note

    顶栏中的 **+ New DAG** 按钮仅对 `admin` 用户显示。Viewer 看到的是只读仪表盘——可以浏览一切内容，但不能创建、暂停或触发 DAG。

## 统计卡片

四张卡片一目了然地汇总整个调度器的状态：

| 卡片 | 展示内容 |
|---|---|
| **Active DAGs** | 未暂停的 DAG，下方显示定义总数（`N defined`）。 |
| **Running runs** | 当前处于 `running` 状态的 DAG 运行，覆盖所有资源池。 |
| **Recent success** | 最近若干次终态运行的成功率（%）——与迷你趋势图中绘制的运行相同。 |
| **Failed DAGs** | *最近一次*运行以 `failed` 或 `timed out` 结束的 DAG。 |

当 **Failed DAGs** 不为零时，该卡片变为可点击——单击即可对表格应用 *Failed* 过滤器，从数字直接进入排查。

## 最近活动

**RECENT ACTIVITY** 条带把所有 DAG 的最近 24 次运行绘制为共享时间轴上按状态着色的刻度，最左侧是最早的运行，最右侧是*当前时刻*。每个刻度位于该次运行的真实开始时间：

- **悬停**刻度可查看 DAG id、运行状态、时长和开始时间。
- **点击**刻度跳转到该次运行的详情页——参见[运行、日志与恢复](runs-logs.md)。

如果尚未有任何运行，条带会显示"No runs yet"。

## 入门检查清单

在完成三个里程碑之前，表格上方会显示一条检查清单栏：

1. **创建第一个 DAG**
2. **触发一次运行**
3. **获得一次绿色运行**（一次成功的运行）

每一步都根据存储中的真实数据勾选，而非根据点击行为。三项全部完成后该栏会永久隐藏；也可以用 ✕ 按钮提前关闭。

## DAG 表格

每一行对应一个工作流定义。点击行内任意位置可打开其运维页面——参见[操作 DAG](dag.md)。

| 列 | 内容 |
|---|---|
| *(开关)* | 暂停/恢复开关。关闭 = 已暂停：调度器停止为该 DAG 创建运行（手动触发仍然有效）。 |
| **DAG** | DAG id、触发类型标签（`scheduled`、`manual` 或 `dependency`），第二行显示调度表达式——或所有者，无调度的 DAG 则显示"manual trigger"。 |
| **LAST RUN** | 最近一次运行的状态徽标：`success`、`failed`、`running`、`queued`、`timed out`、`cancelled`……或"no runs"。 |
| **LAST 14** | 最近 14 次运行的迷你趋势图。颜色编码运行状态；柱高编码真实运行时长，并以整个仪表盘上最近最慢的一次运行为基准缩放，因此"越高 = 越慢"在所有 DAG 间读法一致。悬停柱形可查看状态和时长。 |
| **POOL** | DAG 的任务所运行的资源池（任务使用多个资源池时以逗号分隔）。参见[依赖图、资源池、变量、审计与 API](admin.md)。 |
| **NEXT** | 下一次触发时间——不足一小时时显示 `in Nm`，不足一分钟时显示 `due`，已暂停的 DAG 显示 `paused`，无调度的 DAG 显示 `—`。 |
| **ACTIONS** | ▶ 立即排队一次手动运行（会看到"Run queued"提示，行数据片刻后刷新）。 |

### 过滤

页面标题旁的过滤药丸用于收窄表格：

| 药丸 | 显示内容 |
|---|---|
| **All** | 全部 DAG。 |
| **Running** | 最近一次运行当前为 `running` 的 DAG。 |
| **Failed** | 最近一次运行以 `failed` 或 `timed out` 结束的 DAG。 |
| **Paused** | 已暂停的 DAG。 |

顶栏中的 **Filter DAGs…** 搜索框可额外按 DAG id 子串过滤；搜索与药丸可组合使用。

## 创建 DAG

点击顶栏中的 **+ New DAG**。弹窗把主流程压缩为两个决定：选模板、命名、创建。

### 1. 选择起步模板

| 模板 | 得到的内容 |
|---|---|
| **Blank** | 一个空的 0 任务外壳——在[任务编辑器](task-editor.md)中自行添加任务。 |
| **Daily ETL** | 一条三步 `extract → transform → load` 的 shell 流水线。 |
| **Scheduled report** | `fetch → render`，预设 `0 8 * * *` cron（每天 08:00）。 |
| **Fan-out / fan-in** | `start` → 两条并行分支 → `join`。 |

模板创建的是真实可编辑的 shell 任务——ETL 和报表模板使用了 `{{ logical_date }}` 和 `{{ run_id }}`，可以直观看到[模板变量](../tutorial/template-variables.md)的效果。选择 *Scheduled report* 会自动展开调度区块，使其预设的 cron 保持可见且可修改。

### 2. 命名

输入 **DAG ID**——由字母、数字、`_`、`-`、`.` 组成，且以字母或数字开头。弹窗会在输入过程中实时校验，并立即标记重复的 id；在 id 合法之前 **Create** 保持禁用状态。按 ++enter++ 提交。

### 3. 设置调度（可选）

点击 **Schedule & more options** 展开调度编辑器。共三种模式：

| 模式 | 行为 |
|---|---|
| **Manual** | 无调度——DAG 仅在手动、通过 API 或由上游 DAG 触发时运行。 |
| **Interval** | `@every N` 秒/分/时——固定间隔调度。 |
| **Cron expression** | 标准 5 字段 cron 表达式。 |

在 cron 模式下，预设药丸一键填入表达式——*every minute*、*hourly*、*daily 0:00*、*daily 2:00*、*Mon 0:00*——**?** 按钮会打开一份 cron 速查表，包含字段布局、运算符以及可点击的示例和快捷写法（`@hourly`、`@daily`、`@every 30s`……）。

输入过程中，实时预览会请求服务端计算**接下来 3 次触发时间**并显示在编辑器下方，对间隔和预设调度还会附上通俗解释（"daily 2:00 — Next: …"）。表达式无效时则显示错误——控制台绝不会在客户端猜测触发时间。

调度模式还提供 **Start date** 字段。*Catchup* 复选框可见但目前无法在控制台中编辑——补跑的工作原理参见[调度与补跑](../tutorial/scheduling.md)。

!!! tip

    这里的一切都不是最终决定。调度、开始日期及其他所有配置随时可以在 DAG 的 **Settings** 页签中修改，且每次编辑都会自动保存——参见[操作 DAG](dag.md)。

### 或导入 YAML

点击 **or paste YAML to import…** 将弹窗切换为 YAML 文本框。粘贴完整的 DAG 规格：

```yaml
dag_id: my_workflow
schedule: "0 2 * * *"
tasks:
  - id: hello
    command: echo hi
```

**Import** 会走 REST API 使用的完全相同的解析器和校验——控制台绝不会重新实现该格式。成功后直接跳转到新 DAG 的页面。规格格式的完整文档见 [DAG 与任务参考](../DAG_REFERENCE.md)。

### 创建之后

你会进入该 DAG 的运维页面，模板任务（或 0 任务外壳）已就绪待编辑。任何地方都没有 Save 按钮——控制台会自动持久化每次编辑，并在页眉显示 **Saved / Saving** 徽标。继续阅读[操作 DAG](dag.md)和[任务编辑器](task-editor.md)，或跟随[首个 DAG 教程](../tutorial/first-dag.md)完整走一遍。

## 常见问题

**Save 按钮在哪里？**
没有 Save 按钮。控制台在短暂防抖后自动保存每次 DAG 编辑，并显示 *Saved* / *Saving* 状态徽标；如果当前状态无法持久化，则会改为显示 *Fix errors* 徽标。

**仪表盘上 DAG 何时算作"failed"？**
其最近一次运行以 `failed` 或 `timed out` 结束。**Failed DAGs** 卡片和 *Failed* 过滤药丸都遵循这一规则。

**可以创建没有调度的 DAG 吗？**
可以——把调度模式留在 *Manual*（默认值）即可。通过 ▶ 按钮、[REST API 或 CLI](../CLI.md)，或经由[跨 DAG 触发](../tutorial/cross-dag.md)由上游 DAG 运行它。

**NEXT 列中的"due"是什么意思？**
下一次触发时间已不足一分钟——调度器将在下一个 tick 时拾取它。
