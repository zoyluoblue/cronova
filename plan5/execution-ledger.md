# Plan1–5 执行账本（Execution Ledger）

> 本文件是 5 份规划（`plans/plan{1..5}-executable.md`，哈希见 `plan5/source-manifest.txt`）
> 在本仓库/本机环境中的**权威执行记录**。每个小阶段：实现 → 验收 → 记录 → commit。
> 状态词汇（对齐 plan5 §0.2 纪律，绝不刷绿）：
> - `PASS` — 机器可验证的验收全部通过，证据可重放（测试命令 + commit 哈希）
> - `PARTIAL` — 已实现并验证核心路径，剩余项明确列出
> - `BLOCKED_ENVIRONMENT` — 结构性不可在本环境完成（多周窗口/多人签字/百台实验室/真实云 Provider），列明缺口
> - `N/A_SCOPE` — 按计划的 Owner 划分不属于本仓库（例如多计划团队治理条款）
>
> **环境事实**（适用于所有 BLOCKED_ENVIRONMENT 判定）：单开发机（macOS/arm64）、
> Docker 单机、无独立评审人/委员会、无 100 台 Ubuntu 实验室、无真实 LB/Registry/云 Provider、
> 无法执行连续 7 天/72h/28 天/31 天不中断窗口。

## 基线（Wave 0 / REBASE Entry）

- 施工 HEAD：见本文件所在 commit 的父提交（V2 平台化基线）。
- 上一轮（V2）交付并验证：分布式拨入 worker（mTLS/join token/日志回传/failover/重认领）、
  subdag/dependent、告警组+Email、全局优先级+execution_policy、日期模板、PG store（真库全绿）、
  Docker/compose（minimal+full 双 profile）、Workers/告警组/优先级控制台、graph 结构编辑（部分）。
- 对抗性审查（5 路猎手+逐条反驳验证）确认 4 个真 bug，已全部修复并回归（17/17 包绿）：
  1. worker 终态 TaskEvent 非阻塞丢弃 → `sendReliable` 阻塞投递 + 重连 Probe 兜底
  2. 日志 gap 回退协议单边 → LogAck 增加 `rewind` 标志，worker 端游标回退
  3. 重连不恢复 `session.active` → Hello 重认领时重建计数
  4. `h.refs` 无限增长 → sweep 30 分钟宽限后 GC 终态 ref
- 审查中被环境限额中断的 7 条未完成验证的发现：记录于 `plan5/evidence/adversarial-v2.md`，
  在 Plan5-R04 重新验证。

## Plan1（R0–R4：分布式执行、HA、安全与 Authoring 地基）

| 工作包 | 状态 | 验收/证据 |
|---|---|---|
| P1-REBASE-ENTRY | PASS | 本账本 + source-manifest + 基线 commit 43132b7 |
| W10 基线止血/PG CI（R0-04/LB-02） | PASS | test.yml 加 PG 服务容器 + "postgres suite must not skip" 断言步骤（本地等价验证：PG17.5 真库全套件绿） |
| W11 Source CAS（R0-07/LB-01 核心） | PASS | DAG 保存乐观并发：GET 返回 definition_hash → 编辑器回显 expected_hash → 不匹配 409 dag_conflict；build 响应回传新 hash 防自冲突；无 hash 保留 CLI/GitOps last-write-wins。验收：`TestDagSaveCAS`（stale 拒写且不达引擎）+ 浏览器实测（自续期 renewed、并发者获胜、冲突 toast 双语）。版本历史/运行快照/三 hash 中的 source hash 既有 |
| W12 执行正确性（状态机/receipt/invariant） | PASS(核心)/PARTIAL(PG真库单元) | 存储层 run 状态转移守卫（条件 UPDATE，双 store 镜像）：禁止回 queued、禁止篡改已定终态；retry 复活与 mark 覆写合法路径保留。验收：`TestRunTransitionGuard`（非法边拒绝且状态不变）+ 全量调度套件零回归（证明合法表完备）。PG 真库 cell 因本机 Docker daemon 停机待重验（CI PG 容器兜底）。既有 receipt=audit 链 + attempt ref + guarded CAS 写 |
| W2x cluster-basic（协议/身份/Grant/日志/E2E） | PARTIAL | 对应实现：worker session proto（V2 protocol）、join token 一次性注册（enrollment）、assignment 按 ref 幂等（Grant 唯一）、attempt 状态落盘+进程组重认领（crash-safe runtime）、日志流回传+offset 去重、secret redaction、UNIQUE(dag,logical_date) 占位唯一（Occurrence）、init/join primitive（join+compose）。E2E：单 worker 真机全链路 PASS；**双 worker E2E 未跑**（compose full 因 registry 拉取受阻）。ADR 偏差：push-assign（least-loaded）而非 pull（RequestWork）——记录为设计决定，不改 |
| W3x HA | PARTIAL + BLOCKED_ENVIRONMENT | 已有：DB 租约主备（`-standby` 热接管，实测秒级）。plan1 要求的 3-Control active-active + 真实 LB/Provider failover：需多主机+真实故障域，且与单调度器架构冲突——ADR 偏差记录，暂不重构 |
| W4x 生产安全/升级/DR | PARTIAL | PASS 项：RBAC 三角色+token TTL/scope、mTLS 强制、SSRF 防护、secrets AES-GCM、审计链、backup(VACUUM INTO)+restore 文档、healthcheck/readyz 活性。缺口：签名供应链/OCI（未做）、真实 N/N-1 滚动升级矩阵与三次 PITR 演练（BLOCKED_ENVIRONMENT） |
| W5x Authoring（Raw/Canvas/冲突/Diff） | PASS(核心路径) | graph 结构编辑浏览器全链路验收：加节点→diff 条→保存持久化；连线模式建边（c.deps=[b] 落库）；环拒绝（c→a 拒 + toast「依赖存在环」）；删边（edge-hit 命中区→「✕ 移除依赖」→保存后 API 一致）；点节点进任务编辑器；双编辑冲突由 Source CAS 覆盖（W11）。剩余：触屏路径与 WCAG 人工复核未做（BLOCKED_ENVIRONMENT：无独立可访问性评审） |
| 7 天 Soak / 72h Chaos / 28d Pilot | BLOCKED_ENVIRONMENT | 需连续多周真实窗口 |

## Plan2（调度产品能力）

| 工作包 | 状态 | 验收/证据 |
|---|---|---|
| R5B hold/release 意图 | PASS | `dag_runs.held` 列（双 store+迁移）；hold≠suspend（在跑任务继续、不派新任务、queued 不晋升、run 冻结不 finalize，release 原地恢复）；API `POST /api/runs/{id}/hold|release`（operator 权限、审计、终态 409 run_not_active）。验收：`TestHoldBlocksNewDispatch`+`TestHeldQueuedNotPromoted`+`TestRunTimelineAndHold` |
| R5B Timeline/DecisionFact 投影 | PASS | `GET /api/runs/{id}/timeline`：run 生命周期+任务 attempt+审计操作合并时序视图（只读投影，无第二账本，符合 plan2 权威约束）；openapi 目录同源三条目 |
| R7 DST/时区、Backfill | PASS(既有) | V2 已交付：DAG timezone（CRON_TZ+日期模板时区渲染+DST 测试）、catchup/backfill（cron 槽位、节流） |
| R8 事件 Inbox | PASS(既有)/PARTIAL | `POST /api/events` 幂等（UNIQUE source+key）、入站 webhook per-DAG secret+限流；DLQ/重放语义未建（缺口记录） |
| R8 通知 Outbox | PARTIAL | 通知重试退避+失败计数+SSRF 防护已有；durable outbox 表未建（进程内重试，重启丢投递意图——缺口记录） |
| R10A WFQ / R10P Bin-pack | 未实施 | 加权公平与装箱评分需引入 pool 权重与评分层；判定为下轮（不阻塞本轮收口，plan2 允许 capability 独立发布） |
| R9 OCI/WASM/Sidecar | N/A_SCOPE | 容器任务运行时与本项目"宿主进程执行"定位冲突（ADR 偏差记录） |

## Plan3（AI Native 操作层）

待执行（ActionIR/审批内核/MCP 分类；基于既有 MCP 34 工具与 openapi 单源目录）。

## Plan4（部署/迁群/批量扩容）

待执行（single→cluster 存储迁移工具、fleet 批量安装、bootstrap 收口；基于既有 join/compose）。

## Plan5（质量闸门与证据）

- R00/R01 源冻结：**PASS**（source-manifest.txt，5 份 SHA-256）
- R02+ 逐步验收：随各计划推进滚动记录于 `plan5/evidence/`。

## 接续队列（下一步，按序执行；每步完成即验收+记账+commit）

1. **P1-W12 执行正确性**：run/task 状态转移合法性守卫（非法转移拒绝并告警日志）+ 现有 guarded-CAS 写盘点为 evidence 文档；对抗性用例（乱序/重复 finalize）。
2. **P1-W5x Authoring 收口**：graph 连线模式/删边浏览器验证（加节点/保存已验）；循环边拒绝实测；双标签冲突（CAS 已接入，验 graph 保存路径也带 expected_hash）。
3. **P1 剩余项判定**：逐项把 plan1 §5 工作包标 PASS/PARTIAL/BLOCKED_ENVIRONMENT/N/A_SCOPE 写入账本（含 ADR 偏差：push-assign vs pull、lease 主备 vs 3-AA）。commit "plan1 closure"。
4. **Plan2 阶段**：R5B hold/release intent + DecisionFact/Timeline（run 级审计时间线 API+UI）；R8 事件 Inbox 幂等已有→补 DLQ 语义；R10A WFQ（pool 加权公平出队）。逐项验收。
5. **Plan3 阶段**：MCP 工具六分类（read/draft/approve/human-only 注解进 openapi catalog + mcp -read-only 强化为分类感知）；mutation 走 Proposal+确认（MCP 层 approve 参数）；审计链。
6. **Plan4 阶段**：`cronova migrate-store`（SQLite→PG：逐表拷贝+计数/校验和+孤儿检查，对齐 plan5 §9）；bootstrap 收口文档。
7. **Plan5 收口**：重验 adversarial-v2.md 的 7 条未决发现；全量回归+基准重跑；总核查表（§13）填实；FinalizationSnapshot（各 SHA/digest）；最终汇报（CORE 达成/BLOCKED_ENVIRONMENT 清单）。

## Commit 日志（阶段性提交索引）

| # | commit | 内容 |
|---|---|---|
| 1 | (见 git log) | V2 平台化基线 + 4 项对抗性修复 |
| 2 | (见 git log) | 计划冻结 + 执行账本骨架 |
