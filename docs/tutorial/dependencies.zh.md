# 依赖与触发规则

在本章中，你将使用 `deps` 把任务连成一张真正的依赖图，观察某个任务失败时下游会发生什么，并通过**触发规则（trigger rule）**来掌控这种行为——同时学会两件用于修复失败运行的运维工具：`cronova retry` 和 `cronova mark`。

## 用 `deps` 连接任务

cronova 的 **DAG** 是一张有向无环图：任务是节点，每个任务的 `deps` 列表定义了指向它的入边。一个任务只有在其上游任务（即 `deps` 中列出的任务）达到其触发规则所要求的状态时才会运行——默认情况下，是所有上游都已**成功**。

创建 `dags/daily_etl.yaml`，一条经典的 extract → transform → load 链。先不写 `schedule`，这样它只在你手动触发时运行：

```yaml
dag_id: daily_etl
tasks:
  - id: extract
    type: shell
    command: echo "extracting rows"
  - id: transform
    type: shell
    command: echo "transforming"
    deps: [extract]
  - id: load
    type: shell
    command: echo "loading"
    deps: [transform]
```

`deps` 是一个列表，所以图不必是一条直线：写了 `deps: [a, b]` 的任务会汇聚（fan in）并等待两者完成；而两个都写了 `deps: [extract]` 的任务会分叉（fan out）并行运行。

每个 DAG 文件在加载时都会被校验并做**环检测**。如果你不小心把依赖连成了环（比如让 `extract` 依赖 `load`），该文件会被拒绝，`serve` 日志中会出现 `dependency cycle detected` 错误并跳过此文件——环永远不会悄无声息地让工作流调度器死锁。

**验证一下**——触发这个 DAG，观察整条链按顺序执行：

```bash
cronova trigger daily_etl
cronova runs daily_etl
```

```
RUN_ID                                 LOGICAL_DATE          STATE    TRIGGER  TASKS
daily_etl__manual_1783472901234567000  2026-07-07T09:08:21Z  success  manual   extract=success transform=success load=success
```

在 **http://localhost:8090** 的控制台中打开 `daily_etl`，你会看到同一张图以节点和边的形式呈现——这正是你 `deps` 列表的可视化对应。

## 上游失败时：`upstream_failed`

如果 `transform` 失败了，`load` 会怎样？试一下。把 `transform` 的命令改成故意失败：

```yaml
  - id: transform
    type: shell
    command: exit 1
    deps: [extract]
```

保存，再次触发，然后查看：

```bash
cronova trigger daily_etl
cronova runs daily_etl -n 1
```

```
RUN_ID                                 LOGICAL_DATE          STATE   TRIGGER  TASKS
daily_etl__manual_1783473010987654000  2026-07-07T09:10:10Z  failed  manual   extract=success transform=failed load=upstream_failed
```

`load` 根本没有运行。当上游任务失败时，cronova 会把它下方的任务标记为 **`upstream_failed`**——这是一个终态，含义是"因上游失败而被阻断，从未执行"。传播沿着边进行：只有失败任务的后代会被阻断，同一次运行中互不相关的并行分支仍会继续执行直至完成。甚至一个已经排队的任务也可能被此波及——只要在执行器取走它之前其上游失败了。

任何 `failed` 或 `upstream_failed` 的任务都会使整次运行最终以 `failed` 结束——这正是你在 `STATE` 列看到的结果。

## 触发规则

默认的门槛——"当**所有**上游都成功时运行"——只是六种规则之一。设置任务的 `trigger_rule` 可以改变它相对于 `deps` 何时触发：

| 规则 | 运行条件 |
|---|---|
| `all_success`（默认） | 所有上游任务都成功 |
| `all_done` | 所有上游任务都已结束（任意状态） |
| `all_failed` | 所有上游任务都失败 |
| `one_success` | 至少一个上游任务成功 |
| `one_failed` | 至少一个上游任务失败 |
| `none_failed` | 没有上游任务失败（成功或跳过均可） |

其中两条能解决日常问题。**清理（cleanup）**任务无论流水线成败都应该运行——用 `all_done`。**告警（alert）**任务恰恰应该*因为*有任务失败而运行——用 `one_failed`。把这两个任务加到 `daily_etl` 中（暂时保持 `transform` 是失败的）：

```yaml
  - id: cleanup
    type: shell
    command: echo "removing temp files"
    deps: [extract, transform, load]
    trigger_rule: all_done
  - id: alert
    type: shell
    command: echo "ALERT daily_etl failed"   # curl your pager here
    deps: [transform, load]
    trigger_rule: one_failed
```

**验证一下**——再触发一次：

```bash
cronova trigger daily_etl
cronova runs daily_etl -n 1
```

```
RUN_ID                                 LOGICAL_DATE          STATE   TRIGGER  TASKS
daily_etl__manual_1783473120123456000  2026-07-07T09:12:00Z  failed  manual   extract=success transform=failed load=upstream_failed cleanup=success alert=success
```

`transform` 失败、`load` 被阻断，和之前完全一样——但 `cleanup` 照样运行了（`all_done`），`alert` 也因为有依赖失败而触发了（`one_failed`）。在控制台中打开 `alert` 任务的日志即可看到那条消息。

!!! warning

    如果一个任务的触发规则**永远**不可能再被满足，它会被标记为
    `upstream_failed`——而任何 `upstream_failed` 任务都会让这次运行的记录
    状态变为 `failed`。在一次全绿的运行里，无条件的 `one_failed` 告警任务
    永远不可能触发，于是它以 `upstream_failed` 结束，*成功的*流水线却被
    记录为一次失败的运行。请把 `one_failed` / `all_failed` 分支用于在你
    本来就预期会失败的运行内部*响应*失败；如果只是想"运行失败时通知我"，
    应优先使用 DAG 级的 `notify` webhook（见 [DAG Reference](../DAG_REFERENCE.md)），
    并在把这个 DAG 放上调度之前删掉告警任务。

## 重试失败的运行：`cronova retry`

现在修复这个 bug——把 `transform` 恢复为能正常工作的命令：

```yaml
  - id: transform
    type: shell
    command: echo "transforming"
    deps: [extract]
```

保存文件并不会改写历史：失败的运行仍然是失败的。要只重新运行出问题的部分，用 `cronova retry` 加上从 `cronova runs` 拿到的运行 ID：

```bash
cronova retry daily_etl__manual_1783473120123456000 -server http://localhost:8090
```

```
{
  "retried": true
}
```

!!! note

    `retry`、`mark` 和 `cancel` 是与运行中服务器的 REST API 通信的运维
    命令，因此需要一个目标地址：传入
    `-server http://localhost:8090`（或导出 `CRONOVA_SERVER`）。如果你
    用 `-auth` 启用了登录，还需通过 `-token` / `CRONOVA_TOKEN` 提供
    API token——用 `cronova tokens create` 签发一个。完整细节见
    [CLI Reference](../CLI.md)。

一次 retry 会把所有 `failed`、`upstream_failed` 和 `cancelled` 的任务——以及它们下游的一切——重新入队，并重新激活这次运行。已经成功的任务保留其结果，不会被重跑。重新入队的任务会按照**当前**的 DAG 定义执行，所以"修复—重试"的闭环就是：改 YAML、保存、retry。如果这次运行仍在活跃中，或其中没有任何失败，API 会返回冲突而不是执行重试。

你也可以只针对单个任务；它的下游任务会随之一并被清除重跑：

```bash
cronova retry daily_etl__manual_1783473120123456000 transform -server http://localhost:8090
```

**验证一下：**

```bash
cronova runs daily_etl -n 1
```

这次运行会翻回 `running`，`transform` 和 `load` 重新执行，`STATE` 列最终落在 `success`。在控制台中，同一次运行的历史现在会显示新的执行尝试。

!!! tip

    手动 `retry` 用于事后恢复。对于可以预见的失败——不稳定的网络、繁忙的
    数据库——应改为给任务配置自动的 `retries` 和 `retry_delay`，详见
    [重试、超时与资源池](retries-timeouts-pools.md)。

## 手动覆盖状态：`cronova mark`

有时重跑并不合适——你已经手工修好了数据，或者某个任务卡住了、你想让流水线继续往前走。`cronova mark` 就是运维层面的手动覆盖：

```bash
cronova mark <run_id> <state>              # run:  success | failed
cronova mark <run_id> <task_id> <state>    # task: success | failed | skipped
```

假设 `transform` 失败了，但你已经手动完成了这次转换。把它标记为完成，让运行继续：

```bash
cronova mark daily_etl__manual_1783473120123456000 transform success -server http://localhost:8090
```

```
{
  "marked": true
}
```

把任务标记为 `success` 或 `skipped` 会释放那些因它而处于 `upstream_failed` 的下游任务——调度器在下一个 tick 就会把它们捡起来，运行从被阻断的地方恢复。mark 对活跃中的运行同样有效：仍在运行的任务会先被杀掉进程，然后以你选择的状态为准。

有一个细节：默认的 `all_success` 规则会把**已跳过（skipped）**的上游视为阻断。如果一个任务应当容忍上游被跳过，请给它设置 `trigger_rule: none_failed`——"没有上游失败；成功或跳过都可以"。

运行级的 `mark` 用于修正一次*已结束*运行的记录结果——例如在你已通过其他途径处理完故障后，把这次运行声明为 `success`：

```bash
cronova mark daily_etl__manual_1783473120123456000 success -server http://localhost:8090
```

**验证一下**——`cronova runs daily_etl -n 1` 会立即反映这次覆盖，而且每一次 `trigger`、`cancel`、`retry` 和 `mark` 都会记录在服务器的操作审计日志中，手动覆盖绝不会不留痕迹。

## 本章小结

- `deps` 画出图的边；每个 DAG 文件在加载时都会做环检测，任务在其上游满足 `trigger_rule`（默认 `all_success`）时触发。
- 一次失败只阻断它的后代——它们以 `upstream_failed` 结束——并行分支照常完成；任何失败或被阻断的任务都会让这次运行变为 `failed`。
- 六种触发规则覆盖其余场景：`all_done` 用于清理，`one_failed` 用于运行内的失败处理，`none_failed` 用于容忍被跳过的上游，另外还有 `one_success` 和 `all_failed`。
- `cronova retry <run_id> [task_id]` 按当前 DAG 定义把已结束运行中失败的部分重新入队；`cronova mark <run_id> [task_id] <state>` 是可以解除阻断或修正运行结果的手动覆盖。

**下一步：**在[模板变量](template-variables.md)中，用运行的逻辑日期及相关变量来参数化你的命令。
