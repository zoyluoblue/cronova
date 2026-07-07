# 模板变量

到目前为止，流水线里的每条命令都是静态文本。本章介绍如何把每次运行的值——逻辑日期、运行 id、尝试次数——注入到任意任务中：既可以在命令里使用 `{{ ... }}` 模板，也可以在脚本里读取 `CRONOVA_*` 环境变量。

## 六个内置变量

每次任务尝试都会获得六个内置运行变量。每个变量都有**两种使用方式**：作为 `{{ name }}` 占位符，在分发时替换进 `command`；或者作为环境变量，注入到任务进程中：

| 模板 | 环境变量 | 值 |
|---|---|---|
| `{{ logical_date }}` | `CRONOVA_LOGICAL_DATE` | 本次运行的逻辑日期，格式 `YYYY-MM-DD` |
| `{{ logical_datetime }}` | `CRONOVA_LOGICAL_DATETIME` | 逻辑日期时间，RFC 3339 格式 |
| `{{ run_id }}` | `CRONOVA_RUN_ID` | 本次运行的唯一 id |
| `{{ dag_id }}` | `CRONOVA_DAG_ID` | DAG 的 id |
| `{{ task_id }}` | `CRONOVA_TASK_ID` | 本任务的 id |
| `{{ try_number }}` | `CRONOVA_TRY_NUMBER` | 尝试次数（每次重试递增） |

!!! tip
    **逻辑日期**指的是一次运行所*代表*的时间段，而不是墙上时钟的"现在"。正是这一区别让 `catchup` 回填有了意义：为 6 月 3 日补跑的运行看到的 `{{ logical_date }}` 是 `2026-06-03`，即使它是在今天执行的。逻辑日期的分配规则见[调度](scheduling.md)。

## 在命令中使用模板

更新 `dags/daily_etl.yaml`，让流水线知道自己正在处理哪一天：

```yaml
dag_id: daily_etl
schedule: "0 2 * * *"
start_date: 2026-06-01
catchup: false
tasks:
  - id: extract
    type: shell
    command: echo "extracting data for {{ logical_date }}"
  - id: transform
    type: shell
    command: echo "transforming for $CRONOVA_LOGICAL_DATE (run $CRONOVA_RUN_ID, attempt $CRONOVA_TRY_NUMBER)"
    deps: [extract]
```

`extract` 任务使用的是**模板形式**：任务被分发时，工作流调度器把 `{{ logical_date }}` 替换进命令字符串，因此 shell 收到的是类似 `echo "extracting data for 2026-07-07"` 的命令。花括号内的空格可有可无——`{{logical_date}}` 同样有效。

触发一次运行并观察：

```bash
./cronova trigger daily_etl
./cronova runs daily_etl
```

**验证一下：**`cronova runs` 会显示新的运行，`extract` 和随后的 `transform` 依次达到 `success`。接着打开控制台 **http://localhost:8090**，点击 **daily_etl** → 最新一次运行 → **extract** 任务。它的日志内容为：

```
extracting data for 2026-07-07
```

显示的是今天的日期——手动触发的运行，其逻辑日期就是你触发的那一刻（UTC 时间）。

## 或者读取环境变量

上面的 `transform` 任务用的则是**环境变量形式**：`$CRONOVA_LOGICAL_DATE` 是普通的 shell 语法，在运行时由 shell 从注入的环境中展开。它的日志会显示全部三个值：

```
transforming for 2026-07-07 (run daily_etl__manual_..., attempt 1)
```

两种形式携带的值完全相同，按需选用即可：

- **模板**（`{{ logical_date }}`）——当值本身属于命令行的一部分时：`python extract.py --date {{ logical_date }}`。
- **环境变量**（`CRONOVA_LOGICAL_DATE`）——当值在脚本或程序*内部*被使用时。你的 Python 代码只需读取 `os.environ["CRONOVA_LOGICAL_DATE"]`，文件本身完全不需要模板化。

`{{ try_number }}` / `CRONOVA_TRY_NUMBER` 从 1 开始，每次重试递增——当你在后面的[重试、超时与资源池](retries-timeouts-pools.md)一章加上重试后，用它按尝试次数标记日志行或输出文件会非常方便。

## 未知占位符保持原样

替换只处理它能识别的占位符。未知的 `{{ ... }}` 会原封不动地保留，因此普通的 shell 花括号绝不会被破坏：

```yaml
  - id: braces_demo
    type: shell
    command: echo "{{ logical_date }} is replaced, {{ not_a_variable }} is not"
```

**验证一下：**再次触发该 DAG，在控制台打开这个任务的日志：

```
2026-07-07 is replaced, {{ not_a_variable }} is not
```

单层花括号甚至不会被当作候选——`awk '{ print $1 }'`、`${HOME}` 以及 `{a,b}` 这样的花括号展开都会原样通过。只有由单词字符和点组成的 `{{ name }}` 才会被纳入考虑。

## 控制台中的胶囊标签

你不必手动敲 `{{ }}`。在控制台的任务编辑器里，每个变量都会在命令中渲染为一个**彩色胶囊标签（pill）**，旁边还有按分组排列的调色板（内置、变量、连接、参数），**点击或拖拽**即可插入。胶囊是原子化的——拖动它可以移动位置，点它的 **×** 即可删除——因此模板永远不会被删掉一半。

**验证一下：**打开 **http://localhost:8090**，点击 **daily_etl**，编辑 `extract` 任务——你在 YAML 里写的 `{{ logical_date }}` 会显示为一个胶囊，编辑器旁的调色板还提供另外五个变量。

!!! note
    只有内置运行变量（以及每次运行的触发参数）才会成为 `CRONOVA_*` 环境变量。UI 管理的变量和连接在服务端解析，**只能**通过显式引用进入命令——这是下一章的内容。

## 本章小结

- 六个内置运行变量——`logical_date`、`logical_datetime`、`run_id`、`dag_id`、`task_id`、`try_number`——既能以 `{{ ... }}` 模板使用，也能以 `CRONOVA_*` 环境变量使用。
- 模板在分发时渲染进命令；环境变量由脚本在运行时读取——值完全相同，按使用场景选择即可。
- 未知占位符原样通过，shell 花括号是安全的。
- 控制台的胶囊编辑器支持点击或拖拽插入变量——无需手敲 `{{ }}`。

下一步：借助[变量、连接与参数](variables-connections-params.md)，把密钥和配置从 YAML 中移出去。
