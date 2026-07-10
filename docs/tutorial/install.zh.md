# 安装 cronova

cronova 是一个自托管的**工作流调度器**，以单个静态 Go 二进制文件发布——内置调度器、Web 控制台、REST API 和 CLI，并使用内嵌的 SQLite 数据库。本章将带你完成安装、启动，并首次打开控制台。

获取 `cronova` 二进制文件有三种方式。跟随本教程时，建议使用普通二进制文件并在一个工作目录中运行——无需 root，无需安装服务，随时可以清理丢弃。

## 方式一：预编译发布版（教程推荐）

从 [Releases 页面](https://github.com/zoyluoblue/cronova/releases)获取最新版本（当前为 **v0.2.1**）。二进制文件覆盖 Linux 和 macOS 的 `amd64` 与 `arm64` 架构，命名为 `cronova_<os>_<arch>.tar.gz`：

```bash
mkdir cronova-tutorial && cd cronova-tutorial
curl -fsSLO https://github.com/zoyluoblue/cronova/releases/latest/download/cronova_darwin_arm64.tar.gz
tar -xzf cronova_darwin_arm64.tar.gz
chmod +x cronova
```

请根据你的平台替换文件名：`cronova_linux_amd64.tar.gz`、`cronova_linux_arm64.tar.gz` 或 `cronova_darwin_amd64.tar.gz`。每个发布版都附带 `SHA256SUMS`，请在解压前完成校验；一行安装脚本和 `cronova update` 会强制校验，缺失校验信息时直接终止。

!!! tip
    压缩包里不只有二进制文件：它还会解出一个包含可直接运行的[示例 DAG](https://github.com/zoyluoblue/cronova/tree/main/dags) 的 `dags/` 目录、一份 `cronova.yaml.example` 配置模板，以及独立的 `cronova-executor`。从压缩包开始，首次启动时控制台就不会是空的。

## 方式二：从源码构建

安装 **Go 1.26.5+** 后：

```bash
git clone https://github.com/zoyluoblue/cronova
cd cronova
go build -o cronova ./cmd/cronova
```

这一步会把调度器、Web 控制台和 CLI 构建为一个静态二进制文件。整个构建不依赖 CGO（使用纯 Go 实现的 SQLite），因此无需 C 工具链。

!!! note
    直接 `go build` 出来的版本号显示为 `dev`——这是预期行为。发布版二进制文件才携带真实的版本标签。

## 方式三：一行安装脚本（原生服务）

若要在 Linux 或 macOS 机器上做正式部署，一条命令即可完成全部工作：

```bash
curl -fsSL https://raw.githubusercontent.com/zoyluoblue/cronova/main/deploy/bootstrap.sh | sudo bash
```

它会检测你的操作系统和 CPU 架构，下载匹配的预编译发布版，校验其 SHA256，将 cronova 安装为原生服务（Linux 上为 systemd，macOS 上为 launchd），并运行交互式设置向导，配置端口、监听范围、管理员账号和认证。

服务方式安装后可自行管理生命周期：`cronova update` 支持原地升级（若新二进制无法保持运行会自动回滚），`cronova uninstall` 则负责卸载。完整的生产部署指南见[部署](../DEPLOY.md)。

教程的后续内容，请继续使用方式一或方式二得到的普通二进制文件。

## 验证安装：`cronova version`

在存放二进制文件的目录中执行：

```bash
./cronova version
```

你会看到构建版本和平台信息，格式为 `cronova <version> <os>/<arch>`：

```
cronova v0.2.1 darwin/arm64
```

只要能打印出来，安装就完成了。

## 启动调度器并打开控制台

`cronova serve` 在同一个进程中运行调度循环**以及** Web 控制台 + REST API：

```bash
./cronova serve
```

默认情况下，它以当前目录为基准工作：DAG YAML 文件从 `./dags` 加载（不存在时自动创建），SQLite 数据库位于 `data/cronova.db`，任务日志写入 `logs/`。这正是建议在专用工作目录中运行、跟随教程操作的原因。

现在在浏览器中打开 **<http://localhost:8090>**。你会看到 cronova 控制台——DAG 列表、运行历史、任务状态，以及一键手动触发。如果你是从发布版压缩包安装的，随附的示例 DAG（如 `example_etl` 和 `ticker`）已经出现在列表中。

你也可以在第二个终端里用 CLI 确认同样的信息：

```bash
./cronova dags
```

每个已加载的 DAG 都会连同其调度一起列出——这证明调度器已经启动并在读取你的 DAG 目录。

!!! warning
    本教程中的普通开发模式 `serve` 默认**关闭认证**，但只监听 `127.0.0.1:8090`，因此仅本机可访问。未认证的非回环监听会被拒绝，除非显式打开危险豁免。对外提供访问前请先启用登录——见[启用登录](../GETTING_STARTED.md#enabling-login)。

随时可以用 ++ctrl+c++ 停止服务——你的 DAG 和数据库仍保留在磁盘上，等待下一次 `./cronova serve`。

## 本章小结

- 安装 cronova 的三种方式：预编译发布版二进制、从源码 `go build`，或用一行 `curl | sudo bash` 安装脚本设置原生服务。
- `./cronova version` 用于确认二进制可用，输出格式为 `cronova <version> <os>/<arch>`。
- `./cronova serve` 在一个进程中运行调度器、Web 控制台和 REST API，所有内容（DAG、数据库、日志）都相对于工作目录存放——控制台地址为 <http://localhost:8090>。

下一步：编写并触发你的第一个 DAG——[第一个 DAG](first-dag.md)。
