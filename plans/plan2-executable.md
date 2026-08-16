# Cronova Plan2：调度产品能力执行方案

> 版本：Execution Edition 2.0  
> 文档 ID：`P2-EXEC-2.0`  
> Suite Contract ID：`CRONOVA-SUITE-EXEC-2.0`  
> 日期：2026-08-12  
> 状态：待负责人批准后执行；不表示代码已实现  
> 依据：`plan2.md` Five-Round Reviewed 1.0、`plan2-review.md`、`plan-suite-sequencing-audit.md`  
> 原冻结方案 SHA-256：`1294afdad3be5b074d8d3bc492d50ee3989b2b873745a1186994656b60e0a7dd`  
> 原则：保留原 Plan2 的领域能力，重写跨计划入口、客户端所有权、发布顺序和最终证据链。

执行层级：本文件是施工顺序、Owner、Gate 和发布标签的权威；原 `plan2.md` 中逐 Issue 的 Schema、状态机、安全、迁移、性能和 Evidence DoD 仍是对应工作包必须关闭的子任务。若两者存在无法自动合并的语义冲突，先提交 ADR/变更单并重跑依赖图，不得择宽执行。

## 1. 交付目标

Plan2 把 Plan1 的分布式执行与 Authoring 地基升级为可日常使用的调度产品能力：

- DAG 中可直接看到中文或其他语言名称，同时稳定 UID、技术 ID、显示名称彻底分离。
- 提供可审计的 hold、release、cancel、bulk、timeline、retry、timeout、deadline 和并发控制。
- 提供 DST 安全的时间调度、Backfill/Reprocess、Webhook、事件 Inbox、DLQ 和通知 Outbox。
- 提供 Approved OCI Task Extension、加权公平队列和 Bin-pack Placement 的独立生产能力。
- WASM Hook、Connector Sidecar、多资源公平、Spread/Affinity 保持独立 Beta；Cache Locality 仅 Experimental。
- 每项公开能力同时交付领域 API、reference client、Conformance corpus 和 `DomainActionPolicyFragment`，供 Plan3 最终生成 CLI/SDK/MCP 并完成 AI Native 全套件认证。

Plan2 不拥有最终 CLI、SDK、MCP、Action Catalog、全局 Coverage Ledger 或 Agent Approval Authority。

## 2. 执行主线

```text
Wave 0  REBASE + R5-00A 身份/展示合同
              ├─> Plan1 Canvas 数据模型消费合同
              └─> Plan2 两个独立入口

Runtime Design Entry ─> R5B Design ─> R6 Design（ADR/Schema/Golden/Simulator/DISABLED/OBSERVE）
Runtime Candidate Authority ─> 隔离环境 SHADOW/CANARY
Runtime Production Authority ─> R5B Functional ─> R6 Functional/AUTHORITY
                                             ├─> R7 Temporal/Backfill
                                             ├─> R8 Event/Notification ─> R9C Sidecar Beta
                                             ├─> R9A Approved OCI GA
                                             ├─> R9B WASM Hook Beta
                                             ├─> R10A WFQ GA ─> R10B Multi-resource Beta
                                             └─> R10P Bin-pack GA ─> Spread/Affinity Beta

Authoring Entry ─> R5A Presentation/Localization + Plan1 Canvas integration

各 Capability Functional Gate
        ─> 独立 ProductionSlice
        ─> P2 Public Surface Freeze
        ─> Core 72h Chaos + 连续 28 天 Pilot
        ─> P2-CORE-GA
```

R5A 可以与 R5B/R6 并行；R6 不等待完整 Canvas GA。R7、R8、R9、R10 不按阶段号串行，而按下文的 Capability 依赖并行交付。

## 3. 唯一 Authority 与产品边界

| 对象或能力 | 唯一 Owner | Plan2 交付 |
|---|---|---|
| Cluster、Run/Grant/Receipt、Resource、Upgrade、DR | Plan1 | 调用并扩展，不另建权威表或总开关 |
| UID/技术 ID/Presentation 设计合同 | Plan2 Design Authority，Suite 共用 | ADR、Schema、迁移、Conformance；不因此拥有 Workflow/Task UID 运行时写权 |
| Workflow/Task UID 与 technical ID | Plan1 Domain Store | 唯一生成、唯一性、迁移和引用 Authority；Plan2 只按 UID 引用 |
| PresentationRevision/Head/Alias/Search | Plan2 | 展示内容和搜索投影；不建第二 Source/Layout/Canvas 状态 |
| RunOps intent/DecisionFact/Timeline、Schedule、Event、Extension、QoS、Placement | Plan2 | 领域 Schema、状态机、Domain API、Evidence；Run/Attempt/Grant 终态仍由 Plan1 transition function 裁决 |
| ActionIR、全局 Action ID、Approval、CLI/SDK/MCP | Plan3 | Plan2 只提交 fragment 与 reference client |
| 主机 SSH 变更、批量扩容、单机迁群 | Plan4 | Plan2 只消费最终容量和拓扑结果 |

约束：

1. 一个写操作只能存在一个生产 mutation Authority。
2. Plan2 reference client 仅用于测试、bootstrap 或兼容验证，必须标注 `reference/non-canonical`。
3. Plan3 Adapter 切换后，Plan2 旧写包装器必须删除、只读或返回 `410/UPGRADE_REQUIRED`。
4. Plan2 只生成 `P2DomainActionPolicyFragment`；Suite Coverage 只能由 Plan3 编译。

## 4. 全局不变量

1. 终态不可逆；retry 新建 Attempt，rerun/reprocess 新建 Run。
2. cancel/timeout 只先写持久意图；没有 STOPPED、absent 或机器隔离证据不得释放资源。
3. PostgreSQL 内的权威 effect 与 receipt 同事务；外部副作用使用稳定 operation/delivery ID 和 durable intent。
4. preview/dry-run 不得创建 Run、Grant、Reservation、Occurrence 或业务 mutation。
5. 新权威状态统一走 `DISABLED → OBSERVE → SHADOW → CANARY → AUTHORITY`。
6. 名称、翻译和别名不得参与外键、权限、幂等键、DAG 边、调度、路径或指标 label。
7. 所有执行语义固定 Version、Revision、Snapshot 或 digest；禁止运行时读取 latest。
8. 硬性 AuthZ、capability、Pool、quota、资源和数据位置过滤永远先于公平性和 placement 软评分。
9. Event、Notification、Connector 只承诺 at-least-once；重复、乱序、重放必须安全。
10. 每个新增实体必须具有复合 workspace 归属、唯一 state owner、generation、锁序和 invariant auditor。

## 5. Wave 0：重新基线与提前合同

### P2-REBASE-ENTRY

在任何 Authority 实现前完成：

- 固定实际代码 HEAD、Plan1 当前 Gate 状态（允许 `NONE/PARTIAL/PASS`，此处只记录不形成等待边）、Schema、route、迁移状态、组件 digest 和环境清单。
- 对 Domain API、客户端、审批、证据、迁移工作建立跨计划 Work Package Ledger，删除与 Plan1/3/4 重复计费。
- 为 DB、Security、SRE、QA/Chaos、Release/DR 和独立 Reviewer 绑定真实人员与可用时段。
- 冻结 FRESH 与 UPGRADE 两套脱敏基线 fixture；记录对象数、hash、active/queued/UNCERTAIN Run 和可恢复 backup。
- 重新生成依赖图并验证：重复节点、悬空依赖、自环、循环均为 0。
- 重新计算 P50/P80；超出第 14 节 envelope 必须提交范围变更，不得暗中扩张。

Gate：`P2-REBASE-ENTRY-GATE`。实际 HEAD、资源、依赖或迁移假设缺一项即 STOP。

### R5-00A：UID-ID-Presentation Contract ADR

本项提前到 Plan1 Canvas 数据模型定型前，只做合同和无副作用验证，不开启生产 mutation。

- `uid`：系统生成、永久稳定、不可复用，是外键、DAG 边、权限和幂等身份。
- `technical_id`：用户或工具可读的稳定机器标识；字符集、长度、唯一域和保留字固定。
- `PresentationRevision`：保存 `default_name`、BCP 47 多语言名称、aliases、description、locale 和 revision UID。
- 中文、日文及其他语言名称允许直接显示在 DAG；未命中语言时按固定回退链返回名称，最后回退到 technical ID。
- Source、Layout、Presentation 为三份独立文档和 CAS；改名不得改变 Source hash、编译结果或运行语义。
- Run 创建事务固定当时的 PresentationRevision；历史 Run 不使用 current 名称冒充历史名称。
- 冻结 Unicode normalization/confusable、长度、alias 数、搜索、XSS、RTL、IME、离线字体和可访问性 corpus。
- Legacy 只生成不可执行 Projection；用户显式 `MigrateAndPublish` 后才能创建新的 V2 WorkflowVersion。

Gate：`P2-IDENTITY-PRESENTATION-CONTRACT-GATE`。这是 Design Authority Gate，不授予 Plan2 重发 UID/technical ID 的权力。`P1-AUTHORING-CONTRACT-GATE`、Plan2 R5A/R5B 和 Plan3 target resolver 都必须消费同一 digest。

## 6. 两个独立入口

### P2-RUNTIME-DESIGN-ENTRY-GATE

必须同时满足：

- `P1-CLUSTER-BASIC-FUNCTIONAL-GATE` 已通过，Run/Task/Attempt UID、Receipt、PostgreSQL Authority 和基本 Upgrade Contract 可用。
- `P2-REBASE-ENTRY-GATE`、`P2-IDENTITY-PRESENTATION-CONTRACT-GATE` 已通过。
- `P3-ACTION-CONTRACT-KERNEL-GATE` 的 fragment schema 可用；不要求 Agent、MCP 或最终 AI GA。

本 Gate 只允许 ADR、Schema、Golden、模拟器、migration dry-run 和 `DISABLED/OBSERVE`；不得发放 V2 Grant、切换 Authority 或累计生产 Evidence。

### P2-RUNTIME-CANDIDATE-AUTHORITY-GATE

必须同时满足：

- `P1-CLUSTER-CORE-FUNCTIONAL-GATE` 已通过，RPO=0 Provider、Grant/Reservation/Completion fence 和 N/N-1 候选环境可用。
- `R5B-DESIGN-GATE`、`R6-DESIGN-GATE` 及当前 migration/rollback Golden 已通过。

本 Gate 只允许隔离环境的 `SHADOW/CANARY`；不承接生产流量、不切换生产 Authority、不累计 Final Evidence。

### P2-RUNTIME-PRODUCTION-AUTHORITY-GATE

必须同时满足：

- `P1-CLUSTER-CORE-GA` 当前有效；3 Control、外部 HA PostgreSQL/S3/LB/Registry 与至少 3 Worker 通过 fresh/upgrade 复验。
- Run/Task/Attempt UID、Grant、Reservation、Cancel、Completion、Receipt、Feature Generation、N/N-1 与 DR Evidence 有效。
- `P2-RUNTIME-CANDIDATE-AUTHORITY-GATE` 已通过，且当前 Schema/Golden 与目标 RC 无漂移。
- `P3-ACTION-KERNEL-PRODUCTION-GATE` 已通过；每个候选 Action 仍需独立 `ACTION-AUTHORITY-CUTOVER-GATE(action_id)` 才能切生产 writer。

只有此 Gate 通过后才允许 `SHADOW/CANARY/AUTHORITY`、生产数据回填和生产 Evidence。

### P2-AUTHORING-ENTRY-GATE

必须同时满足：

- `P2-IDENTITY-PRESENTATION-CONTRACT-GATE` 已通过。
- `P1-AUTHORING-CONTRACT-GATE` 已固定 Source/Layout/Publication、Task UID 和 CAS，不要求完整 Browser/WCAG 生产标签。
- R5A Browser fixture、迁移 Golden、locale/RTL/IME/AT 测试环境已预约。

`P1-AUTHORING-GA` 是 R5A ProductionSlice 的前置，但不是 R5B/R6 后端开发入口。

## 7. R5A：Presentation、本地化与 Plan1 Canvas 集成

入口：`P2-AUTHORING-ENTRY-GATE`。

工作包：

- 消费 Plan1 已生成的 Workflow/Task UID 和 technical ID；只实现 PresentationRevision、Head、Alias、Search projection 与 EntityReference，不重发 ID。
- 在 Plan1 拥有的 Source/Layout/Canvas 上集成 Presentation CAS、离线冲突和 publication manifest；Plan2 不新建第二 Canvas route 或 Layout graph Authority。
- Plan1 Canvas、列表、搜索、Timeline 和历史 Run 同时展示本地化名称与可复制的稳定 ID。
- 完成中文、英文、日文、阿拉伯文、希伯来文、Emoji、RTL、IME、键盘和读屏 fixture。
- 实现 V1 Projection、显式 MigrateAndPublish、批量预览、receipt、kill/resume 和失败回滚。
- 提交 `P2-NAMING-PRESENTATION.action.fragment.yaml` 与领域 Conformance。

Gate：`R5A-FUNCTIONAL-GATE`。DoD 为 Source hash 不因改名变化、Plan1 UID/technical ID 无重发/改写、历史名称可重现、跨 workspace IDOR 为 0、最大 DAG/名称/alias 下浏览器与数据库不越预算。Run 创建时由 Plan1 在同一 PostgreSQL 事务写入 `presentation_revision_uid`，Presentation 正文由 Plan2 拥有。

## 8. R5B：RunOps、Explain 与 Timeline

设计入口：`P2-RUNTIME-DESIGN-ENTRY-GATE`；隔离候选环境需 `P2-RUNTIME-CANDIDATE-AUTHORITY-GATE`；生产 mutation 与 Gate Evidence 还必须满足 `P2-RUNTIME-PRODUCTION-AUTHORITY-GATE`。

工作包：

- 冻结 operation parent/child receipt、target snapshot、部分成功、权限变化和重复请求语义。
- 实现 hold/release/cancel/bulk intent 与 DecisionFact；Run/Attempt/Grant 终态只能由 Plan1 transition function 裁决；明确 hold 不等于 suspend，cancel 不等于 runtime 已停止。
- 建立 immutable DecisionFact、Timeline、Explain、Lineage 和 bounded pagination/cursor。
- 固定 Plan2 state owner、generation、全局锁序、OUTCOME_UNKNOWN 与 reconciler。
- 完成 RBAC/IDOR、tamper-evident audit、retention、删除传播和 WORM checkpoint。
- 提交 `P2-RUNOPS.action.fragment.yaml` 与 reference client Conformance。

`R5B-DESIGN-GATE` 关闭 Schema、状态矩阵、Golden、模拟器和无副作用 Conformance。`R5B-FUNCTIONAL-GATE = R5B-DESIGN-GATE + P2-RUNTIME-PRODUCTION-AUTHORITY-GATE + 真实 intent/receipt/reconcile 验证`。R6 设计不等待 R5A 完整关闭。

## 9. R6：运行语义 V2

设计入口：`R5B-DESIGN-GATE`；生产实现入口：`R5B-FUNCTIONAL-GATE + P2-RUNTIME-PRODUCTION-AUTHORITY-GATE`。

交付：immutable ExecutionPolicy/RunInput/Display pin；retry/timeout/deadline；trigger rule；Workflow/Task/Pool 并发；强类型输入输出；Derived Run；Subworkflow；foreach/条件的有界语义；统一 admission 和资源释放。

必须验证：pre-Grant abandon、post-Grant uncertain、Control/Worker crash、重复 command/completion、超时与 cancel 竞态、Pool debit 恰好一次、N/N-1 opaque behavior、连续 7 天 mixed-runtime。

提交 `P2-RUNTIME-SEMANTICS.action.fragment.yaml`。

`R6-DESIGN-GATE` 关闭 ExecutionPolicy、语义状态机、Golden、模拟器和 migration dry-run。`R6-FUNCTIONAL-GATE = R6-DESIGN-GATE + R5B-FUNCTIONAL-GATE + P2-RUNTIME-PRODUCTION-AUTHORITY-GATE + fault/mixed-runtime Evidence`。R7/R8/R9/R10 可提前做 ADR、Schema、Golden、模拟器和 `DISABLED/OBSERVE`，但生产 SHADOW/CANARY/AUTHORITY 必须等待此 Gate。

## 10. R6 后的 Capability 并行发布

| Capability | 直接前置 | 交付与级别 | 独立 Gate |
|---|---|---|---|
| R7 Temporal/Backfill | R6 | DST/TZDB、ScheduleRevision、misfire/catchup、Backfill/Reprocess；Core GA | `R7-PRODUCTION-GATE` |
| R8 Webhook Event | R6 | durable Inbox、HMAC/mTLS、DLQ/replay、ACK-after-commit；Core GA | `R8-EVENT-PRODUCTION-GATE` |
| R8 Notification | R6 | durable Outbox、SSRF/Secret/egress、与 core 隔离；Core GA | `R8-NOTIFICATION-PRODUCTION-GATE` |
| R9A Approved OCI | R6 + Plan1 OCI/Artifact | digest/signature/trust/deny、专用 Pool；Core GA | `R9A-APPROVED-OCI-PRODUCTION-GATE` |
| R9B WASM Hook | R6 + compiler contract | 确定性、低权限编译 Hook；Optional Beta | `R9B-WASM-BETA-GATE` |
| R9C Sidecar Framework | R8 connector contract | 进程外 lease/fence/protocol；Optional Beta | `R9C-SIDECAR-BETA-GATE` |
| R10A WFQ | R6 | 加权公平、aging、FIFO fallback；Core GA | `R10A-WFQ-PRODUCTION-GATE` |
| R10B Multi-resource | R10A | dominant-share 与独立成本模型；Optional Beta | `R10B-MRF-BETA-GATE` |
| R10P Bin-pack | R6 | 资源硬过滤后的软评分；Core GA | `R10P-BINPACK-PRODUCTION-GATE` |
| R10X Spread/Affinity | R10P | topology/label 有界评分；Optional Beta | `R10X-SPREAD-AFFINITY-BETA-GATE` |
| Cache Locality | R10P | 低权重 TTL/generation hint；Experimental | `R10-CACHE-EXPERIMENTAL-GATE` |

每条 Capability 必须独立提交：Schema/状态机、Domain API、reference client、`P2-<CAP>.action.fragment.yaml`、Conformance、Threat Model、Migration、Rollback、SLI/Alert/Runbook、ProductionSlice 和独立 Reviewer 签字。

R9A 不等待 R9B/R9C；R10A 不等待 R10B 或 Placement；Bin-pack 不等待 WFQ；Optional/Experimental 不得成为 Core Gate 的祖先。

## 11. DomainActionPolicyFragment 合同

每个 fragment 至少包含：

- 稳定 `action_id`、domain owner、target UID 类型和 stable target resolver。
- read/draft/prepare/mutate 分类，以及 `HUMAN_ONLY/INTERNAL_ONLY/UNSUPPORTED` 状态。
- 输入/输出 Schema、错误码、分页/游标、幂等键、operation receipt 和内容引用。
- AuthZ scope、审批等级、风险/影响、Secret/PII redaction、预算和速率限制。
- handler、feature generation、Capability Gate、minimum version、rollback class 和 Evidence key。
- Domain API 与 reference client 的正反向 Conformance 用例。

CI 必须拒绝 duplicate action ID、duplicate handler、missing owner、missing gate、unclassified public surface 和两个 mutation Authority。每个 mutation Action 还必须绑定 `ACTION-AUTHORITY-CUTOVER-GATE(action_id)`：精确记录 `action_id/domain_gate/surface_generation/old_ingress_set/cutover_receipt`，不允许以一个全局开关代替逐 Action 切换。

`P2-DOMAIN-ACTION-FRAGMENT-GATE(profile_uid, capability_head_manifest_digest, rc_digest)` 按 Profile 签发：`CORE` 只包含 Core，R9B/R9C/R10B/Spread/Cache 各自独立；`SUITE_CANDIDATE` 是 Core 与实际 installed/enabled/exposed Optional 的并集。Plan2 不直接生成正式 CLI/SDK/MCP，也不维护 Suite Coverage 百分比。

## 12. 跨阶段生产闭包

以下工作从 R5 启动并持续到 Final：

- `P2-OPS-01`：在看结果前冻结 Control/Worker/PG/S3 规格、实体/速率/payload/retention 与 steady/burst/overload/recovery 容量包络。
- `P2-DB-01/02`：冻结并实测索引、unique、partition、max-scan、autovacuum、WAL、bloat、replica、连接池和 P80 增长。
- `P2-OPS-02/03`：保护 Completion/Cancel/Release/Reconcile/Heartbeat 的独享预算；建立降级矩阵、SLI/SLO、告警、容量预测和非作者 Runbook 演练。
- `P2-UPGRADE-01`：使用 Plan1 UpgradeLedger 完成 Expand、滚动升级、Authority cutover、N/N-1 和 kill/resume。
- `P2-DR-01`：完成 full-size backup、PITR、新 incarnation、SecurityRecoveryOverlay、旧 lease/token/cert 拒写和对象完整性复验。

每个 Capability 可先获得自己的 ProductionSlice；Plan2 Core Final 才要求上述全量闭包。

## 13. 迁移、回滚与兼容

迁移顺序固定为：`EXPAND → SHADOW_WRITE → BACKFILL → VERIFY → CANARY → AUTHORITY → OBSERVE → CONTRACT`。

- 所有游标、generation、operation ID、receipt、from/to digest 和 stop condition 持久化；kill 后以同一 operation resume。
- Authority 前可 binary rollback；产生旧 build 无法理解的 V2 权威行后只允许 roll-forward 或 feature fallback。
- Rollback class 只能为 `BINARY_ROLLBACK_SAFE`、`FEATURE_FALLBACK_ONLY`、`DATA_ROLL_FORWARD_ONLY`、`SCHEMA_CONTRACT_IRREVERSIBLE`、`SECURITY_DENY_MONOTONIC`。
- Presentation/UID mapping、Inbox、Outbox、Pool/Fairness debit、Extension digest/deny、crypto-erasure tombstone 不因 rollback 删除。
- QoS fallback 只停止新策略选择并返回 Plan1 FIFO/priority，不退款或重解释历史 debit。
- N-1 对未知 Schema 只读或拒绝，永远不得按默认值写；不支持 Presentation 的客户端只能显示 technical ID，不能覆盖名称。
- Contract 至少等待两个 stable minor、最大 backup/PITR 窗口和 retained reader 清零。

## 14. 预算、团队和并行上限

冻结版工作量作为本版重新估算前的上限 envelope：

| 范围 | P50 | P80 | 说明 |
|---|---:|---:|---|
| Core Production | 764 人日 | 1,192 人日 | R5A/R5B、R6、R7、R8、R9A、R10A、Bin-pack、Ops/DB/Upgrade/DR/Final |
| Optional 增量 | 140 人日 | 222 人日 | R9B/R9C/R10B/Spread-Affinity/Cache |
| 完整 Portfolio | 904 人日 | 1,414 人日 | 迁出正式 CLI/SDK/MCP 后应在 Rebase 中减少重复工作 |

至少覆盖 Architecture、Workflow/API、CP/DB、Runtime/Security、Frontend、QA/Chaos/Release、SRE/Security、Localization/A11y 九类责任帽子；实现者不能审批自己的安全、迁移、公平性或 Final Evidence。

资源装载规则：

- 约 10 人团队同一时间最多一条重后端主线，加一条轻量合同/前端支线。
- R5B→R6 是首先保障的重主线；R5A 作为轻支线并行。
- R6 后最多同时开启两条重 Capability；DB/SRE/Security/QA 共享角色冲突时按 R7/R8 → R9A → R10A/Bin-pack 的 Core 顺序排队。
- Optional 不得占用 Core Final 环境，也不得在 Core 31 天窗口中修改共享执行路径。
- 原冻结日历 P50 W48–W53、P80 W69–W75 仅作 envelope；每个 Wave 按实际 HEAD 和真实 FTE 重新预测。

## 15. Evidence、最终 Gate 与重置规则

每项 ProductionSlice 必须绑定同一个 `SuiteEvidenceKey`：source commit、组件/RC/Schema/迁移 digest、Action fragment/Surface digest、feature/security/approval generation、Provider/environment、恢复 overlay 和 verifier 版本。

`P2-PUBLIC-SURFACE-FREEZE-GATE(profile_uid, capability_head_manifest_digest, rc_digest)` 要求该 Profile 的 Plan2 route、UI read/mutation、Domain API、Capability 和正式 Runbook 已盘点，并由对应 fragment 覆盖或明确分类；Plan3 Compiler 聚合时未分类、重复、无 Owner、缺 Gate 均为 0。

`P2-CORE-FUNCTIONAL-GATE = R5A-FUNCTIONAL-GATE + R5B-FUNCTIONAL-GATE + R6-FUNCTIONAL-GATE + R7-PRODUCTION-GATE + R8-EVENT-PRODUCTION-GATE + R8-NOTIFICATION-PRODUCTION-GATE + R9A-APPROVED-OCI-PRODUCTION-GATE + R10A-WFQ-PRODUCTION-GATE + R10P-BINPACK-PRODUCTION-GATE`。

`P2-CORE-PRODUCTION-READY-GATE` 还要求：

- 上述 Core ProductionSlice 当前有效，且 `P2-DOMAIN-ACTION-FRAGMENT-GATE(CORE, core_capability_head_manifest_digest, rc_digest)` 通过。
- `P2-PUBLIC-SURFACE-FREEZE-GATE(CORE, core_capability_head_manifest_digest, rc_digest)` 已绑定同一 RC、Schema、fragment generation 和 CapabilityHead manifest。
- Ops、DB、backpressure、Upgrade、DR、Security、Performance、Runbook 和 scoped blocker 全部关闭。
- Optional/Experimental 祖先为 0；最终 Suite CLI/SDK/MCP 和 AI GA 不是本 Gate 前置。

Final 在同一 RC 与同一 EvidenceKey 上连续执行 72h Chaos 和 28 天 Pilot。影响 Schema、状态机、Action fragment、核心路径、安全、Provider、恢复或 RC digest 的变化从 0 重启；仅文档且 `affects_set` 不相交的变化需机器兼容裁决和独立签字。

`P2-CORE-31D-WINDOW-GATE = P2-CORE-PRODUCTION-READY-GATE + same-candidate 72h Chaos + consecutive 28d Pilot`；`P2-FINAL-EVIDENCE-GATE = signed FinalStateQuery + current blocker generations + immutable Evidence root`；`P2-CORE-GA = P2-CORE-PRODUCTION-READY-GATE + P2-CORE-31D-WINDOW-GATE + P2-FINAL-EVIDENCE-GATE`。

最终向 Suite 提交 ACTIVE、未过期的 CapabilityHead、Public Surface Snapshot 和 fragment digest；这不是 `CRONOVA-SUITE-AI-NATIVE-GA`，后者由 Plan3 在 Plan4 全部 Surface 冻结后签发。

## 16. 统一完成定义（DoD）

任何工作包只有同时满足以下条件才能关闭：

1. ADR、Schema、状态机、Owner、锁序、唯一键和预算已冻结。
2. 代码、迁移、反向迁移或 roll-forward 工具、测试和文档已合并到同一 RC。
3. Domain API、reference client、Conformance 与 Action fragment 同步；不存在第二 mutation Authority。
4. 单元、属性、并发、故障注入、N/N-1、RBAC/IDOR、Unicode/SSRF/供应链或算法对抗测试按适用范围通过。
5. 真实 PostgreSQL/S3/HA 环境完成性能、增长、kill/resume、滚动升级和恢复验证。
6. 指标低基数、告警可触发、Runbook 由非作者演练，Evidence 可离线复核。
7. Blocker 为 0，独立 Reviewer 签字；只有代码或演示截图不得关闭。

## 17. 前四周可直接执行的工作

1. 任命责任人、备份和独立 Reviewer，建立跨计划 Work Package Ledger。
2. 执行 P2 Rebase，冻结 FRESH/UPGRADE fixture、容量环境和当前依赖图。
3. 召开 R5-00A ADR，冻结 UID、technical ID、PresentationRevision 和三文档 CAS。
4. 将合同 digest 提供给 Plan1 Canvas 与 Plan3 target resolver，并运行双向 Golden。
5. 建立 Plan2 fragment 仓库、Schema 校验器和 duplicate/orphan/missing-gate CI。
6. 完成 Source/Presentation 迁移、name-only Run pin、hold-vs-Grant、DecisionFact 四个 time-boxed spike。
7. 分别关闭 Runtime Design Entry 与 Authoring Entry；任何缺失只阻塞对应支线。Candidate Authority 在 Plan1 Core Functional 后关闭，Production Authority 在 Plan1 Core GA 后独立关闭。
8. R5B/R6 先做 ADR、Schema、Golden、Simulator 与 OBSERVE；R5A 并行开始 Presentation/Canvas；P2-OPS-01 与 P2-DB-01 同步启动。
9. 冻结 R6 retry/timeout/deadline/Pool/Derived Run 状态矩阵和 fault corpus。
10. 第四周重新计算吞吐、人员装载、关键路径和 P50/P80，形成首个可执行 Release Forecast。

## 18. 明确不做

- 抢占、自动扩缩容、scale-to-zero、运行中热迁移和跨 Cluster 联合调度。
- 敌对租户代码强隔离；R9A 只认证 operator-approved OCI。
- 通用 CEP、任意 Connector 全家桶、全局最优 Placement 或 AI 权威调度。
- 在 Plan2 重复建设正式 CLI/SDK/MCP、全局 Action Catalog、Approval Authority 或 Suite Coverage Ledger。
- 用单机 Compose、历史 PASS、不同 RC 或不同 Evidence generation 拼接成生产证书。

批准本文件后，执行起点是 `P2-REBASE-ENTRY` 与 `R5-00A`，不是直接编码 R7–R10。
