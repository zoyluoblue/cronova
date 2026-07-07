# 运行你自己的脚本与工程

一行 `echo` 命令能做的事有限——真实的流水线往往是一个脚本，或者一整个目录的代码和数据文件。本章介绍如何把这些代码作为**工程**上传到 cronova，并在任务中运行它，每次尝试都会使用一份干净的隔离副本。

## 为什么需要工程

`shell` 任务的 `command` 在调度器的工作目录下执行，所以除非 `main.py` 恰好在那里，`python3 main.py` 就会失败。你*可以*硬编码绝对路径、手动把代码部署到调度器旁边——也可以只上传一次工程，让工作流调度器为每次运行自动准备好代码。

## 创建一个小工程

在本机建一个小应用——一个脚本加上一个通过相对路径读取的数据文件：

```text
my_app/
├── main.py
└── data/
    └── greeting.txt
```

```python
# my_app/main.py
import os

print("cwd:", os.getcwd())
print("project dir:", os.environ["CRONOVA_PROJECT_DIR"])

with open("data/greeting.txt") as f:
    print(f.read().strip(), "on", os.environ["CRONOVA_LOGICAL_DATE"])
```

```text
hello from my_app
```

相对路径 `data/greeting.txt` 正是关键所在：脚本假定自己*从工程根目录*运行，就像在你的笔记本电脑上一样。

## 在控制台上传

打开控制台 **http://localhost:8090**，编辑一个 DAG，在任务编辑器中打开一个任务——上传功能就在 **Project** 区域。可以上传单个脚本、整个文件夹，或一个 `.zip`（自动解压），并给工程起一个名字，比如 `my_app`。工程名允许字母、数字和 `. _ -`；上传有大小限制（单文件和整个工程各有上限），并防范路径穿越（path traversal）/ zip-slip 攻击。

**验证一下：** 通过 REST API 列出已上传的工程：

```bash
./cronova api GET /api/projects -server http://localhost:8090
```

```json
[{"name": "my_app", "files": 2, "size": 253}]
```

## 在任务中引用它

用 `project` 字段让 `shell` 任务指向该工程。创建 `dags/my_app_report.yaml`：

```yaml
dag_id: my_app_report
tasks:
  - id: run_main
    type: shell
    command: python3 main.py     # cwd is a clean copy of my_app, so this resolves
    project: my_app
```

触发并观察：

```bash
./cronova trigger my_app_report
./cronova runs my_app_report
```

**验证一下：** 在控制台中打开 **my_app_report** → 最新一次运行 → **run_main**。日志显示脚本是从工程的一份全新副本中运行的——位于系统临时目录下的每次尝试独立目录（具体路径因操作系统而异）：

```
cwd: /tmp/cronova-ws-9f8a3c21d4e5/my_app_report__manual_...-run_main
project dir: /tmp/cronova-ws-9f8a3c21d4e5/my_app_report__manual_...-run_main
hello from my_app on 2026-07-07
```

## 工程暂存(staging)的工作原理

当 shell 任务设置了 `project`，调度器会在每次尝试前准备好代码：

- 已上传工程的**一份全新隔离副本**成为本次尝试的工作目录（`cwd`）。各次尝试互不干扰，重试永远从干净副本开始——绝不会从失败尝试留下的半成品状态继续。
- 副本的绝对路径通过 **`CRONOVA_PROJECT_DIR`** 环境变量导出，因此脚本即使 `cd` 到别处，也能定位自己捆绑的数据文件。
- 文件权限位会被保留，可执行脚本仍然可执行——`./run.sh` 可以直接运行。
- 副本位于系统临时目录下，并在**尝试结束后被删除**。

!!! warning
    工作目录是**临时的**——它是每次尝试的一次性副本，尝试结束后即被删除。不要把需要持久保存的结果留在 `cwd`。要么打印到 stdout（会被任务日志捕获），要么写到外部存储：数据库、对象存储，或工作区之外的绝对路径。

## 更新你的代码

改了 `main.py`？重新上传这个改动的文件即可——上传是增量/覆盖式（upsert）的，无需重传整个文件夹。因为每次尝试都会复制*当前*工程，改动在**下一次运行**时生效；已在运行中的尝试继续使用它开始时的那份副本。

!!! tip
    带工程的 `shell` 任务可以运行**主机上的任何语言**——Python、Node、Go 或 Rust 二进制、`psql`、JAR 包。调度器与任务语言完全解耦：上传代码，用对应的解释器调用它，就这么简单。

## 仅限 shell 任务

`project` 字段只对 **`shell`** 任务生效。`python`、`sql` 和 `http` 类型要么在进程内运行，要么有自己的执行模型，暂存工作目录对它们没有意义——[下一章](task-types.md)会介绍每种类型的用途。

## 工程存放在哪里

上传的工程就是服务器工程目录下的普通目录——默认是 `~/.cronova/projects`，可通过 `-projects` 标志或 `CRONOVA_PROJECTS` 环境变量覆盖。如果没有配置工程目录，上传功能会被禁用，并且任何引用工程的任务都会在运行时失败。

## 在运行前发现缺失的工程

一个 DAG 引用了从未上传的工程，解析时不会报错——然后在第一次运行时失败。先做校验；dry-run 端点恰好能标记这种问题：

```bash
./cronova api POST /api/dags/validate \
  '{"dag_id":"my_app_report","tasks":[{"id":"run_main","type":"shell","command":"python3 main.py","project":"ghost"}]}' \
  -server http://localhost:8090
```

**验证一下：** 响应中 `"valid": true`，但带有一条警告：

```json
"warnings": ["task \"run_main\" references project \"ghost\" which is not uploaded yet"]
```

!!! note
    “为什么我的工程任务立刻就失败了？”几乎总是这两个原因之一：工程没有上传，或者服务器没有配置工程目录。`POST /api/dags/validate` 会在任何东西运行之前把这两种情况都以警告的形式暴露出来。

## 本章小结

- 在控制台的任务编辑器中把脚本、文件夹或 `.zip` 作为命名**工程**上传，并通过 `project: my_app` 挂到 shell 任务上。
- 每次尝试都在工程的一份**全新隔离副本**中运行（即它的 `cwd`，同时也在 `CRONOVA_PROJECT_DIR` 中）；副本是临时的，所以持久化输出要写到 stdout 或外部存储。
- 重新上传在下一次运行时生效；工程目录默认是 `~/.cronova/projects`（`-projects` / `CRONOVA_PROJECTS`）。
- `POST /api/dags/validate` 会对引用了未上传工程的情况发出警告——在首次运行前先检查。

下一步：`shell` 只是五种任务类型之一——看看 [`python`、`sql`、`jar` 和 `http` 任务](task-types.md)能做什么。
