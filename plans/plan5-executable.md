# Plan5 Executable — Plan1–Plan4 逐步核查、对抗性验证与 0-错误验收

> **定位**：Plan5 不是新增产品功能，而是 Cronova Plan1–Plan4 的独立质量闸门（Quality Gate）、证据账本（Evidence Ledger）和发布签发流程。
>
> **核心原则**：任何一步只要存在 1 个未执行、失败、超时、Flaky、未处置告警、开放缺陷、证据缺失、越权、未解释副作用或不可重放证据，均不得标记 PASS，也不得进入下一步。
>
> **注意**：这里的“0 错误”是冻结范围、冻结候选版本、冻结测试矩阵和规定环境内的验收合同，不是宣称软件在宇宙所有状态下不存在 Bug。

---

## 0. 执行规则

### 0.1 验收最小单位

所有验收必须落到：

`Requirement × Case × Checker × Oracle × EnvironmentCell × Candidate`

每一项都有唯一 `CaseUID` 和 `AttemptUID`。

不得使用“通过率 100%”替代逐 Case 验收。

### 0.2 PASS 禁止条件

以下任一项非 0，Gate = `BLOCKED`：

- Failed Cases
- Timed-out Cases
- Flaky Cases
- Skipped Applicable Cases
- Open Blockers
- Untriaged Alerts
- Missing Evidence
- Invalid/expired Evidence
- Unauthorized Actions
- Unexpected Side Effects
- Undeclared Environment Drift
- Evidence Replay Failure
- Coverage Closure Gap
- Dependency Closure Gap

**空 Case 集合永远不能 PASS。**

### 0.3 失败后的 Reset

任意已 PASS Gate 在其覆盖范围内发生：

- 代码改变
- 数据迁移改变
- Schema/API/Protocol 改变
- 权限模型改变
- 镜像或运行时改变
- 基础设施拓扑改变
- 测试矩阵改变
- Blocker 新增或重新打开

则该 Gate 自动进入 `STALE`，必须重新验收。

不得用“上一次 PASS”覆盖新候选版本。

### 0.4 证据要求

每个 PASS 至少具备：

```text
CandidateID
GitSHA
ImageDigest
EnvironmentCellID
CaseUID集合摘要
AttemptUID集合摘要
CheckerDigest
OracleDigest
EvidenceDigest
StartedAt
FinishedAt
Operator/AutomationIdentity
Signature
```

Evidence 必须可独立重放。

---

# 1. Plan5 总执行顺序

```text
P5-R00  Genesis / 信任根
   ↓
P5-R01  源计划冻结与完整性校验
   ↓
P5-R02  WBS/Requirement/Case 建账
   ↓
P5-R03  Plan1–4 各阶段施工前 Gate 定义校验
   ↓
P5-R04  Plan1 逐步验收
   ↓
P5-R05  Plan2 逐步验收
   ↓
P5-R06  Plan3 前置 Action/Approval/SDK/MCP 内核验收
   ↓
P5-R07  Plan4 非AI Core 验收
   ↓
P5-R08  跨计划集成与 Authority 验收
   ↓
P5-R09  Plan3 最终 AI Coverage 验收
   ↓
P5-R10  迁移/升级/灾备/Chaos
   ↓
P5-R11  31天稳定性与持续哨兵
   ↓
P5-R12  Finalization Snapshot
   ↓
P5-R13  Release Authorization
   ↓
P5-R14  原 Release Controller 激活
```

**禁止**：P5 变成调度数据面的同步依赖。P5 只控制发布授权，不参与正常 Workflow Run 的实时路径。

---

# 2. Plan1 核查记录表

| ID | 核查对象 | 必须检查 | 对抗性检查 | 0错误验收 |
|---|---|---|---|---|
| P5-P1-01 | R0 基线 | 源SHA、数据库、API、配置快照一致 | 修改一个基线字段验证检测器拒绝 | 0漏检 |
| P5-P1-02 | 集群身份 | cluster_id/incarnation 唯一且不可回退 | 旧 incarnation 写入、重放旧 token | 0旧写成功 |
| P5-P1-03 | Control HA | leader/failover/recovery | 双 leader、网络分区、旧 leader 恢复 | 0双写 |
| P5-P1-04 | Worker 注册 | identity、lease、资源清单 | 重复注册、伪造资源、过期 lease | 0非法节点 |
| P5-P1-05 | Scheduler Authority | 单一调度权威 | 并发调度、leader切换、重复消息 | 0重复执行 |
| P5-P1-06 | Queue/Claim | claim fencing、lease | lease过期后旧worker继续执行 | 0越权 |
| P5-P1-07 | Run 状态机 | 状态转移合法 | 乱序事件、重复事件、迟到事件 | 0非法状态 |
| P5-P1-08 | Retry | retry policy 一致 | 并发 retry、崩溃恢复 | 0超额执行 |
| P5-P1-09 | Artifact | digest、引用、生命周期 | 删除/替换 artifact 后重放 | 0错误引用 |
| P5-P1-10 | 日志 | trace/run/node 关联 | 日志乱序、重复、丢失 | 0无法定位 |
| P5-P1-11 | Resource Authority | CPU/mem/queue/resource pool | 超额资源、伪造 capacity | 0越界调度 |
| P5-P1-12 | Authoring | workflow/version immutable | 并发编辑/发布/回滚 | 0版本污染 |
| P5-P1-13 | ID/Name | stable ID 与 display name 分离 | 中文/日文/emoji/重名/改名 | 0 ID漂移 |
| P5-P1-14 | Canvas | DAG 与版本一致 | 并发拖拽、非法边、循环边 | 0非法DAG |
| P5-P1-15 | API/DB | transaction/idempotency | 重复请求、断网重试 | 0重复副作用 |
| P5-P1-16 | Security | authn/authz/audit | 越权、旧token、重放 | 0越权 |
| P5-P1-17 | Plan1 Gate | 全量 Evidence Closure | 删除/篡改证据、候选切换 | 0缺证 |

**Plan1 Gate 规则**：上述每个 Applicable Case 都必须有成功 Attempt；任何 Blocker 非 0，Gate = BLOCKED。

---

# 3. Plan2 核查记录表

| ID | 核查对象 | 必须检查 | 对抗性检查 | 0错误验收 |
|---|---|---|---|---|
| P5-P2-01 | 调度策略 | FIFO/priority/resource policy | 同优先级竞争、资源瞬变 | 0错误排序 |
| P5-P2-02 | Resource Pool | quota/concurrency | 超 quota、瞬时扩容 | 0越界 |
| P5-P2-03 | Dependency | upstream/downstream语义 | 迟到/重复/失败事件 | 0错误触发 |
| P5-P2-04 | Calendar | timezone/DST | DST切换、跨时区 | 0错调度 |
| P5-P2-05 | Backfill | 范围、并发、幂等 | 大批量 backfill 中断恢复 | 0重复副作用 |
| P5-P2-06 | Priority | 优先级公平性 | starvation/priority inversion | 0饥饿 |
| P5-P2-07 | Retry/Timeout | 策略可解释 | 边界秒数、重启 | 0策略漂移 |
| P5-P2-08 | Queue | queue isolation | noisy neighbor | 0跨池污染 |
| P5-P2-09 | SLA | SLA状态、告警 | 时间漂移/延迟事件 | 0错误告警 |
| P5-P2-10 | History | run lineage | 删除/归档后追溯 | 0断链 |
| P5-P2-11 | Name/ID | 多语言 display_name | 改名、重名、Unicode | 0 ID改变 |
| P5-P2-12 | DAG UI | Canvas与运行语义一致 | UI与API并发修改 | 0状态错位 |
| P5-P2-13 | Scheduling Authority | 与Plan1单一Authority一致 | 双调度器、旧leader | 0双写 |
| P5-P2-14 | Production Gate | Core能力全部闭合 | Optional能力启用后遗漏 | 0覆盖缺口 |

**特别规则**：Plan2 不得复制 Plan1 的调度 Authority；Plan1 是执行/资源 Authority，Plan2 是策略/产品语义 Owner。

---

# 4. Plan3 核查记录表

Plan3 分为两层。

## 4.1 前置层

| ID | 核查对象 | 对抗性检查 | 0错误验收 |
|---|---|---|---|
| P5-P3-01 | ActionIR | 非法参数、未知Action | 0非法执行 |
| P5-P3-02 | Capability Registry | capability漂移 | 0虚假能力 |
| P5-P3-03 | Approval | 未授权Action | 0越权 |
| P5-P3-04 | Idempotency | 重复调用 | 0重复副作用 |
| P5-P3-05 | CLI SDK | schema兼容 | 0合同破坏 |
| P5-P3-06 | MCP | tool schema/权限 | 0越权工具 |
| P5-P3-07 | Error Contract | 错误码稳定 | 0未知关键错误 |
| P5-P3-08 | Audit | actor/action/result链 | 0不可追溯 |
| P5-P3-09 | Secret Boundary | secret不可进入模型上下文 | 0泄露 |
| P5-P3-10 | Prompt Injection | 恶意日志/banner/tool output | 0越权执行 |

## 4.2 最终 AI Coverage

只有 Plan1、Plan2、Plan4 的功能 Surface 全部冻结后才能执行。

建立：

```text
EnabledCapabilitySet
        =
Plan1 Enabled
∪ Plan2 Enabled
∪ Plan3 Enabled
∪ Plan4 Enabled
```

每个 Capability 必须存在：

```text
Human/API Path
AI Action
Authorization Rule
Approval Rule
Audit Rule
Failure Contract
Adversarial Case
Evidence
```

**Coverage Closure 必须：**

```text
EnabledCapabilities = CoveredCapabilities
Uncovered = 0
Unauthorized = 0
AI-only hidden capability = 0
```

特别攻击：

- AI 调用未公开 API。
- AI 绕过人工审批。
- AI 修改不可变 Workflow。
- AI 伪造节点资源。
- AI 读取 Secret。
- 恶意日志诱导模型执行 Shell。
- MCP Tool 越权。
- Codex/Claude/GPT 不同客户端产生不同语义。

---

# 5. Plan4 核查记录表

| ID | 核查对象 | 对抗性检查 | 0错误验收 |
|---|---|---|---|
| P5-P4-01 | Ubuntu Preflight | 不兼容内核/Docker | 0误安装 |
| P5-P4-02 | SSH Password | 错密/锁定/超时 | 0泄露 |
| P5-P4-03 | SSH Certificate | 错证书/过期/HostKey | 0越权 |
| P5-P4-04 | Batch Inventory | 重复IP/重复machine-id | 0重复节点 |
| P5-P4-05 | Install Resume | 中断后续跑 | 0重复安装 |
| P5-P4-06 | Docker/Compose | 已有环境冲突 | 0破坏外部环境 |
| P5-P4-07 | Single Deploy | 全链路安装 | 0健康检查漏项 |
| P5-P4-08 | Cluster Bootstrap | Seed/Bootstrap Ledger | 0自举死锁 |
| P5-P4-09 | HA Control | VIP/failover | 0双主 |
| P5-P4-10 | PostgreSQL | backup/PITR/failover | 0数据丢失 |
| P5-P4-11 | MinIO/Artifact | integrity/recovery | 0引用丢失 |
| P5-P4-12 | Single→Cluster | maintenance window migration | 0数据丢失 |
| P5-P4-13 | Batch Add | 100+ nodes、wave/resume | 0重复/漏节点 |
| P5-P4-14 | Node Replace | add-first/revoke-old | 0双身份 |
| P5-P4-15 | Drift | 手工改配置 | 0未发现 |
| P5-P4-16 | Rollback | pre-authority rollback | 0双写 |
| P5-P4-17 | Failure Cleanup | 半安装节点 | 0残留身份 |
| P5-P4-18 | Final Deployment Gate | 全部证据闭合 | 0开放问题 |

---

# 6. Plan1–4 跨计划对抗矩阵

以下不是普通回归，而是专门寻找“单计划都 PASS，合起来却出错”的问题。

| Cross ID | 场景 | 预期 |
|---|---|---|
| X-01 | Plan2策略 + Plan1资源不足 | 不超配 |
| X-02 | Plan1 leader切换 + Plan2调度 | 0双调度 |
| X-03 | Plan1资源扩容 + Plan2 queue | 新资源正确进入策略 |
| X-04 | Plan2中文名称 + Plan1 Canvas | ID不变 |
| X-05 | Plan3 AI调度 + Plan1 Authority | AI不能成为第二Authority |
| X-06 | AI + Plan2 priority | AI不能绕过策略 |
| X-07 | AI + Plan4 node add | 必须走同一审批Action |
| X-08 | AI + SSH credential | secret不进模型 |
| X-09 | Plan4扩容 + Plan1 scheduler | 新节点必须先注册再调度 |
| X-10 | Plan4迁群 + Plan3 AI | migration期间禁止旧cluster写 |
| X-11 | Plan4 replace + AI | AI不能绕过身份吊销 |
| X-12 | HA failover + MCP | 旧tool invocation不可继续写 |
| X-13 | DAG中文改名 + AI | AI仍使用stable ID |
| X-14 | Optional capability启用 | 必须进入AI Coverage |
| X-15 | Upgrade后旧CLI | 合同兼容或明确拒绝 |
| X-16 | Upgrade后旧MCP | 不得产生隐藏副作用 |
| X-17 | Backup restore后AI | capability/audit不漂移 |
| X-18 | 31天哨兵期间发生升级 | 窗口重置 |

---

# 7. 每一步的标准核查记录表

每一个 Plan1–4 原子步骤都必须创建一行记录：

| 字段 | 内容 |
|---|---|
| Plan | P1/P2/P3/P4 |
| SourceID | 原计划 Issue/Step ID |
| RequirementID | 对应需求 |
| CaseUID | 唯一测试用例 |
| CandidateID | 当前候选 |
| GitSHA | 代码版本 |
| ImageDigest | 镜像摘要 |
| EnvironmentCell | 测试环境 |
| Checker | 检查器 |
| Oracle | 独立预期结果 |
| AttemptID | 本次执行 |
| Result | PASS/FAIL/BLOCKED |
| Evidence | 证据引用 |
| EvidenceDigest | 证据摘要 |
| DefectID | 失败对应缺陷 |
| ResetRequired | YES/NO |
| Operator | 自动化身份/人工身份 |
| Signature | 签名 |
| Timestamp | 时间 |

### PASS 条件

```text
ApplicableCases > 0
AND
PassedCases = ApplicableCases
AND
FailedCases = 0
AND
TimeoutCases = 0
AND
FlakyCases = 0
AND
SkippedApplicableCases = 0
AND
OpenBlockers = 0
AND
MissingEvidence = 0
AND
InvalidEvidence = 0
AND
UnauthorizedActions = 0
AND
UnexpectedSideEffects = 0
AND
DependencyClosureGap = 0
```

---

# 8. Blocker 关闭流程

禁止：

```text
FAIL → 手动改成 CLOSED
```

必须：

```text
FAIL
 ↓
Defect Created
 ↓
Root Cause
 ↓
Fix Commit
 ↓
New Candidate
 ↓
Affected Case Reset
 ↓
Original Case Re-run
 ↓
Regression Matrix
 ↓
Evidence
 ↓
Independent Oracle
 ↓
Reviewer Signature
 ↓
CLOSED
```

如果修复影响了另一个 Gate：

```text
Gate A PASS
     ↓
Fix
     ↓
Gate B affected
     ↓
A + B 同时 RESET
```

---

# 9. 迁移专用 0 错误验收

Plan4 的 Single → Cluster 必须额外生成：

`R18-DependencyClosureRecord`

包含：

- source DB schema
- target DB schema
- workflow count
- version count
- run count
- artifact count
- audit count
- credential reference count
- checksum
- orphan reference count
- duplicate ID count

迁移前：

```text
Orphan = 0
Duplicate = 0
ChecksumMismatch = 0
UnresolvedReference = 0
```

迁移后必须再次验证。

活动 Run 不允许热迁移。

允许维护窗口。

权威切换：

```text
freeze writes
→ snapshot
→ migrate
→ verify
→ promote new incarnation
→ fence old incarnation
→ resume
```

任何旧 incarnation 写成功：

**Migration Gate = BLOCKED**

---

# 10. Plan5 自身也必须被审查

Plan5 不能享受“我是检查器所以我没问题”的人类特权。

必须对：

- Checker
- Oracle
- Evidence Ledger
- Genesis Trust Root
- Reset Engine
- Gate Evaluator
- Coverage Calculator

建立独立测试。

特别要求：

```text
故意注入一个已知失败
→ Checker 必须 FAIL
故意删除一份 Evidence
→ Gate 必须 BLOCK
故意制造空 Case 集合
→ Gate 必须 BLOCK
故意篡改 Evidence
→ Signature/Digest 必须失败
故意使用旧 Candidate
→ Gate 必须 STALE/BLOCK
故意打开一个 Blocker
→ Release 必须 BLOCK
```

如果这些测试失败，Plan5 自己不能签发任何 PASS。

---

# 11. Finalization

最终生成：

`FinalizationSnapshot`

必须冻结：

```text
Plan1 SHA
Plan2 SHA
Plan3 SHA
Plan4 SHA
Plan5 SHA

Git SHAs
Image Digests
DB Schema
EnabledCapabilitySet
TestMatrix
CaseSetDigest
EvidenceSetDigest
EnvironmentCells
OpenBlockers
CoverageReport
SecurityReport
ChaosReport
DRReport
31DayReport
```

然后：

```text
SnapshotBody
   ↓
Independent Board Signatures
   ↓
ApprovalManifest
   ↓
Attestation
   ↓
Original Release Controller
   ↓
Activation
```

Plan5 本身**不直接改变 Cronova 调度状态**。

---

# 12. 最终发布 Gate

只有同时满足：

```text
Plan1 Closure = PASS
Plan2 Closure = PASS
Plan3 Foundation Closure = PASS
Plan4 Core Closure = PASS

Cross-Plan Closure = PASS
Security = PASS
Migration = PASS
DR = PASS
Chaos = PASS

AI Coverage:
Enabled = Covered
Uncovered = 0
Unauthorized = 0

31-Day Sentinel:
Failures = 0
Alerts = 0
Unexplained Events = 0

Open Blockers = 0
Missing Evidence = 0
Invalid Evidence = 0
ResetRequired = 0
```

才能：

```text
CRONOVA-SUITE-GA
```

如果 AI 最终 Coverage 未闭合：

```text
CRONOVA-SUITE-GA
```

不得签发。

只能：

```text
CRONOVA-SUITE-CORE-GA
```

---

# 13. 总核查记录表

| Phase | Source | Cases | PASS | FAIL | BLOCKED | Evidence | Reset | Gate |
|---|---|---:|---:|---:|---:|---:|---:|---|
| Plan1 | P1 |  |  |  |  |  |  |  |
| Plan2 | P2 |  |  |  |  |  |  |  |
| Plan3 Foundation | P3-F |  |  |  |  |  |  |  |
| Plan4 Core | P4-C |  |  |  |  |  |  |  |
| Cross-Plan | X |  |  |  |  |  |  |  |
| AI Final Coverage | P3-AI |  |  |  |  |  |  |  |
| Security | SEC |  |  |  |  |  |  |  |
| Migration | MIG |  |  |  |  |  |  |  |
| DR | DR |  |  |  |  |  |  |  |
| Chaos | CHAOS |  |  |  |  |  |  |  |
| 31-Day Sentinel | SENT |  |  |  |  |  |  |  |
| Finalization | FIN |  |  |  |  |  |  |  |

---

## 14. 施工纪律

1. **先验收，后进入下一步。**
2. **失败不能被重跑覆盖。**
3. **修复必须产生新 Candidate。**
4. **代码/镜像/配置/环境变化自动触发 Reset。**
5. **不能用人工解释替代 Evidence。**
6. **不能用汇总百分比替代逐 Case 结果。**
7. **不能把 Optional 当成天然“不适用”。**
8. **不能把 AI Coverage 提前签成最终完成。**
9. **不能让 Plan5 进入调度数据面。**
10. **最终 GA 必须由冻结 Snapshot 签发。**

---

## 15. 交付物

Plan5 执行完成后必须产生：

```text
/plan5/
├── source-manifest.json
├── requirement-registry.json
├── case-registry.json
├── dependency-closure.json
├── candidate-manifest.json
├── execution-attempts/
├── evidence/
├── blockers/
├── reset-events/
├── coverage/
├── security/
├── migration/
├── disaster-recovery/
├── chaos/
├── sentinel-31d/
├── finalization-snapshot.json
├── approval-manifest.json
└── release-attestation.json
```

**最终原则：**

> Plan1–4 负责把 Cronova 做出来。  
> Plan5 负责证明它真的做出来了，而且没有因为人类急着上线而把“绿色”刷出来。

