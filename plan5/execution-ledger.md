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
| W12 执行正确性（状态机/锁序/receipt/invariant） | 待执行 | |
| W2x cluster-basic（协议/Grant/Artifact/E2E） | 待执行 | 既有：拨入 worker E2E；按 plan1 语义补强 |
| W3x HA | 待执行 | 既有：lease 主备；3-Control AA 为 BLOCKED_ENVIRONMENT 候选 |
| W4x 生产安全/升级/DR | 待执行 | |
| W5x Authoring（Raw/Canvas/冲突/Diff） | 待执行 | 既有：graph 结构编辑（保存链路已验） |
| 7 天 Soak / 72h Chaos / 28d Pilot | BLOCKED_ENVIRONMENT | 需连续多周真实窗口 |

## Plan2（调度产品能力）

待 Plan1 阶段推进后逐项展开（hold/release/timeline、DST/Backfill、Inbox/Outbox/DLQ、WFQ、Bin-pack…）。

## Plan3（AI Native 操作层）

待执行（ActionIR/审批内核/MCP 分类；基于既有 MCP 34 工具与 openapi 单源目录）。

## Plan4（部署/迁群/批量扩容）

待执行（single→cluster 存储迁移工具、fleet 批量安装、bootstrap 收口；基于既有 join/compose）。

## Plan5（质量闸门与证据）

- R00/R01 源冻结：**PASS**（source-manifest.txt，5 份 SHA-256）
- R02+ 逐步验收：随各计划推进滚动记录于 `plan5/evidence/`。

## Commit 日志（阶段性提交索引）

| # | commit | 内容 |
|---|---|---|
| 1 | (见 git log) | V2 平台化基线 + 4 项对抗性修复 |
| 2 | (见 git log) | 计划冻结 + 执行账本骨架 |
