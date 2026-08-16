# Cronova Plan3：AI Native 操作层执行方案

> 版本：Execution Edition 2.0  
> 文档 ID：`P3-EXEC-2.0`  
> Suite Contract ID：`CRONOVA-SUITE-EXEC-2.0`  
> 日期：2026-08-12  
> 状态：待负责人批准后执行  
> 来源：`plan3.md`、`plan3-review.md`、`plan-suite-sequencing-audit.md`  
> 原冻结方案 SHA-256：`738ea68492f7220fe108490b9546dec379a625b9a042b0decc36d7ebb0ec1591`  
> 说明：本文件不修改原方案；它把 Plan3 重排为“框架前置、全套件覆盖认证最后执行”的无环施工版。

执行层级：本文件是施工顺序、Owner、Gate 和最终 AI 认证范围的权威；原 `plan3.md` 中逐 Issue 的安全、恢复、客户端、Eval、容量和 Evidence DoD 仍是对应工作包的强制子任务。发生语义冲突时必须先提交 ADR/变更单并重跑 Coverage/依赖图，禁止择宽执行。

## 0. 执行结论

Plan3 不再作为 Plan2 与 Plan4 之间的一整块串行工程。它分成两段：

1. 前段先交付 Action 合同、人类审批内核、CLI/SDK/MCP 生成框架，供 Plan1、Plan2、Plan4 持续登记能力。
2. 后段等待 Plan1、Plan2、Plan4 的 Public Surface 全部冻结，再生成正式适配器，接入真实 Codex、Claude、OpenAI 与 Generic MCP，完成安全、升级、灾备、72 小时 Chaos 和连续 28 天 Pilot。

最终唯一全产品 AI 认证名称为 `CRONOVA-SUITE-AI-NATIVE-GA`。此前的 `P3-AI-NATIVE-GA` 不再作为最终产品标签；历史结果只作范围受限的 Evidence，不能证明覆盖 Plan4。

## 1. 产品目标与非目标

目标：GPT、Codex、Claude Code、自研 Agent 和 CI 通过同一 ActionIR、CLI、SDK、MCP 与审批体系发现和使用 Cronova 全部公开能力。

“全部覆盖”指每个公开 Surface 都有机器可判定的处理方式，不等于 Agent 能直接执行全部操作：

| 分类 | 允许行为 |
|---|---|
| `DIRECT_READ` | 在对象、字段、数据分级和预算约束下查询 |
| `DIRECT_DRAFT` | 只修改不可执行 Draft/Staging |
| `PREPARE_APPROVE_EXECUTE` | Agent 准备 Proposal；可信人类独立批准后执行 |
| `HUMAN_ONLY` | Agent 只能解释、生成 Runbook 和检查结果 |
| `INTERNAL_ONLY` | 仅状态 Owner、Reconciler、Migrator 调用 |
| `UNSUPPORTED` | 当前 Profile 未安装、未开启或未通过所需 Gate |

非目标：

- AI 不成为 Scheduler、Planner、Placement、扩缩容、终态裁决或 DR 主站 Authority。
- 不提供 raw shell、raw SQL、通用 `METHOD PATH BODY` 或任意 URL 工具。
- Agent 不管理 RBAC、Trust Root、长期 Token、Signer、Cluster Incarnation 或 Secret 明文。
- 不把任何模型 SDK 放进 Control、Dispatcher、Worker 或 Scheduler 核心进程。
- 不承诺模型永远正确、外部副作用可撤销或跨系统 exactly-once。

## 2. 权威边界

| 对象 | 唯一权威 | Plan3 的职责 |
|---|---|---|
| Workflow、Run、Queue、Pool、部署与扩容状态 | Plan1/Plan2/Plan4 Domain Service | 调用正式 Domain Command，不复制状态机 |
| Domain Operation Receipt | 对应 Domain Receipt Store | 提供统一只读视图，不创建第二结果账本 |
| RBAC、对象与字段 AuthZ | Plan1/Plan2 身份与权限服务 | 叠加 client、delegation、approval 的收紧交集 |
| Action 合同 Schema、全局 ID namespace、Compiler | Plan3 | 唯一规范和编译器 Owner |
| Domain Action 内容 | Plan1/Plan2/Plan4 各自的 fragment | 各领域只维护自己的 fragment |
| 全套件 Coverage | 生成的 `SuiteCoverageLedger` | Plan3 唯一发布、查询和 CI 权威；禁止手改 |
| Proposal、HumanApprovalGrant、Agent Delegation | Plan3 Action Service | 管理附加安全状态，不扩大底层权限 |
| BootstrapHumanGrant | Plan4 pre-PG 本地可信通道 | 只在集群 Authority 尚不存在时使用，Agent 不可签发 |

单向生成链固定为：

```text
独立提取的 P1/P2/P4 PublicSurfaceSnapshot
        + P1/P2/P4 DomainActionPolicyFragment
        + Plan3 DomainCommandSpec/ActionPolicy schema
                         ↓
                Plan3 Global Compiler
                         ↓
 ActionIR → Agent OpenAPI/CLI/SDK/MCP/Docs
                         ↓
              GENERATED SuiteCoverageLedger
```

任何计划不得建立第二份 Coverage Ledger；从生成适配器反向推导 Surface Inventory 也被禁止。

## 3. 全局硬约束

1. Mutation 只能以稳定 UID 或精确技术 ID 为目标；中文、多语言名称和 alias 只用于搜索与展示，重名必须返回候选。
2. 每个写请求固定 `operation_id + canonical_request_hash + expected_revision/generation`；同 ID 异 hash 零效果并拒绝。
3. Proposal 固定不可变目标快照、Diff、影响、成本、Policy/Catalog digest 和过期时间；执行时重新校验全部 Authority。
4. Agent 不能批准自己的 Proposal；OAuth consent、模型确认和客户端 `confirmed=true` 都不是 Cronova ApprovalGrant。
5. 单项 Grant 只绑定一个逻辑 execute operation；consume、Domain decision/intent、Receipt 与 AuditFact 在同一 Authority 事务提交。
6. 日志、Source、Output、Artifact、Event、Presentation 和模型结果都是 `UNTRUSTED_DATA`，不得成为授权或系统指令。
7. Secret、私钥、长期 Token、raw presigned URL 不进入模型上下文、argv、stdout、trace、Evidence 或支持包。
8. Gateway 无状态且可 active-active；断线和 cancel 只终止等待，不取消 Domain Operation。
9. Agent 使用独立 DB role、连接池、队列、CPU/IO/WAL/Content 预算；不得挤占 Completion、Cancel、Release 和 Receipt resolution。
10. 未通过底层 Production Gate 的 Action 必须返回 `UNSUPPORTED`，不能因代码或 Tool 已存在而宣称可用。

## 4. 外部入口 Gate

| Gate | 必须提供的证据 |
|---|---|
| `P3-REBASE-ENTRY-GATE` | 当前真实 HEAD、Schema/route/Capability 清单、Suite 资源合同、团队与环境已装载、四计划 Owner 无冲突 |
| `P1-ACTION-PRIMITIVES-GATE` | UID/ID、RBAC/AuthZ、Operation/Receipt、Audit、PostgreSQL Authority 和幂等原语可用 |
| `P1-PUBLIC-SURFACE-FREEZE-GATE` | Plan1 独立 Surface Snapshot、fragment、CapabilityHead |
| `P2-PUBLIC-SURFACE-FREEZE-GATE(SUITE_CANDIDATE, capability_head_manifest_digest, rc_digest)` | Plan2 Core + 实际 installed/enabled/exposed Optional 的 Surface Snapshot、fragment、CapabilityHead |
| `P4-PUBLIC-SURFACE-FREEZE-GATE(SUITE_CANDIDATE, capability_head_manifest_digest, rc_digest)` | Plan4 Core + 实际 installed/enabled/exposed HA/Optional 的 Surface Snapshot、fragment、CapabilityHead |
| `P4-DEPLOYMENT-CORE-PRODUCTION-READY-GATE` | 非 AI 单机/集群/迁群/Worker ADD 已完成生产预检 |
| `P4-DEPLOYMENT-CORE-GA` | Plan4 Core 连续窗口和最终 Evidence 已完成 |
| `P4-AI-DEPLOY-FUNCTIONAL-GATE` | Plan4 AI Intent/Explain/Redaction 与 Domain fragment 已通过安全功能验收 |

外部 Gate 必须绑定 commit、Schema、CapabilityHead、Evidence digest、签字时间和有效期；自由文本 PASS 无效。

## 5. 阶段、工作量与施工顺序

以下沿用原 Plan3 的 P50/P80 总量级，仅作为初始资源包；每个 Wave 的 Rebase Gate 必须按实际 HEAD 重估。

| 阶段 | 工作包 | P50/P80 人日 | 最早入口 |
|---|---|---:|---|
| R11A | Action Contract Framework | 85/140 | Suite Rebase |
| R12A | Human Action Kernel | 280/470 | R11A + P1 primitives |
| R13A | CLI/SDK Framework | 55/90 | R11A |
| R14A | MCP Framework | 75/125 | R11A |
| R12B | Agent/Model Security | 190/340 | R12A |
| R11B | Final Suite Coverage | 80/130 | P1/P2/P4 Surface Freeze |
| R13B | Suite CLI/SDK Conformance | 75/125 | R11B + R13A |
| R14B | Suite MCP Conformance | 100/170 | R11B + R14A |
| R15 | 真实客户端、Suite 安全与 31 天窗口 | 299/507 | R11B/R12B/R13B/R14B + `P2-CORE-PRODUCTION-READY-GATE` + `P4-DEPLOYMENT-CORE-PRODUCTION-READY-GATE` |
| **合计** |  | **1,239/2,097** | 不含上游等待 |

允许并行：R13A 与 R14A；R12B 的非 Egress 部分可与两个 Framework 并行；R13B 与 R14B 可并行。

禁止并行错序：R11B 不能在任一领域 Surface Freeze 前签字；R15 不能使用历史 Coverage 或不同 RC 拼接 Evidence。

## 6. R11A：Action Contract Framework

| Issue | 交付物 | 依赖 | Done 条件 |
|---|---|---|---|
| `P3-R11A-01` | Rebase 与 Legacy Surface inventory | `P3-REBASE-ENTRY-GATE` | route/UI/CLI/MCP/raw mutation 均有 Owner |
| `P3-R11A-02` | Authority/Trust Boundary ADR | 01 | 进程、网络、事务、禁止路径和 actor chain 签字 |
| `P3-R11A-03` | DomainCommandSpec 与 fragment schema | 02 | command、handler、receipt、required gate 无悬空 |
| `P3-R11A-04` | ActionPolicy schema 和六分类 | 02 | risk、approval floor、scope、sensitivity、budget 可验证 |
| `P3-R11A-05` | Wire/Scalar/Canonicalization 合同 | 03,04 | JSON Schema 2020-12、OpenAPI 3.1、跨语言 hash Golden 一致 |
| `P3-R11A-06` | ActionIR Compiler 与全局 ID namespace | 05 | 同输入 bit-for-bit；重复 ID/handler fail closed |
| `P3-R11A-07` | Result/Error/Page/ContentRef 合同 | 06 | 无静默截断；cursor/handle/provenance/certainty 有界 |
| `P3-R11A-08` | Contract codegen、Golden、Fuzz、drift CI | 06,07 | N/N-1、Unicode、unknown field、higher major 全矩阵通过 |

`P3-ACTION-CONTRACT-KERNEL-GATE = 01..08`。该 Gate 只证明框架正确，不宣称全产品 Coverage。

## 7. R12A：Human Action Kernel

| Issue | 交付物 | 依赖 | Done 条件 |
|---|---|---|---|
| `P3-R12A-01` | server-side AuthZ 交集与稳定 Target Resolver | `P3-ACTION-CONTRACT-KERNEL-GATE` + `P1-ACTION-PRIMITIVES-GATE` | 权限只减不增；名称不能作为 mutation selector |
| `P3-R12A-02` | Immutable Proposal/Diff/Impact | 01 | 目标、revision、默认值、成本和 digest 均冻结 |
| `P3-R12A-03` | 可信 Human Approval UI/TTY | 02 | 重新认证、可信渲染、SoD、禁止 approve-all |
| `P3-R12A-04` | Grant/Execute schema 与线性化 DB function | 03 | 单次 consume；固定锁序；网络/模型不进入事务 |
| `P3-R12A-05` | OperationReservation、幂等、unknown outcome | 04 | takeover 有 fence；同 ID 单结果；未知结果不用新 ID |
| `P3-R12A-06` | SecurityAuditFact、hash chain、WORM anchor | 04 | Agent/DBA/Release 不能无检测篡改或自签 |
| `P3-R12A-07` | `MutationIngressRegistry` 与 legacy inventory | 01 | 所有 REST/UI/CLI/MCP/bootstrap/break-glass writer 登记 |
| `P3-R12A-08` | Legacy Mutation Fence 与 shadow/cutover 演练 | 05,06,07 | 测试环境旧 writer 只读/410；双 Authority 窗口为 0 |
| `P3-R12A-09` | N/N-1、rolling、Schema expand/contract | 08 | mixed version 不双写；未知 major fail closed |
| `P3-R12A-10` | PITR/new-incarnation RecoveryOverlay | 05,06,08 | 旧授权、旧 writer、旧 operation effect=0 |
| `P3-R12A-11` | HA、RPO=0、旧 primary fencing 与 failpoints | 09,10 | acknowledged mutation 不丢失、不重复；无法证明则关闭写 Authority |
| `P3-R12A-12` | Kernel 性能隔离、Runbook 与独立安全验收 | 11 | 核心 Scheduler SLO 无越界，所有 P0 failpoint 通过 |
| `P3-R12A-13` | Per-Action Authority Cutover primitive 与 reference pilot | 08..12 | 每次绑定 `action_id/domain_gate/surface_generation/old_ingress_set/operation_id`；DB writer 同事务 CAS；无全局提前关旧入口 |

`MutationIngressRegistry` 每项固定 `ingress_id/owner/audience/action_id/mode/generation/incarnation`，mode 仅允许：

`DISABLED | READ_ONLY | ACTION_KERNEL | BOOTSTRAP_HUMAN_ONLY | ISOLATED_BREAK_GLASS`。

Plan4 首装时只能激活 `BOOTSTRAP_HUMAN_ONLY`。pre-PG writer 无法与 PG 做跨存储原子 CAS，因此必须使用两阶段安全停顿：本地 writer 先进入 `HANDOFF_PAUSED` 并拒绝新 effect，PG 再以同 nonce 激活 `ACTION_KERNEL`并产生 `APPROVAL-AUTHORITY-CUTOVER-RECEIPT`，本地观察 Receipt 后永久 sealed。允许暂时零 writer，禁止双 writer；AI 在 handoff 前 mutation 数必须为 0。

`P3-HUMAN-ACTION-SECURITY-GATE = 01..07`。  
`P3-LEGACY-MUTATION-FENCE-GATE = 08`。  
`P3-ACTION-KERNEL-NN1-GATE = 09`。  
`P3-ACTION-KERNEL-RECOVERY-GATE = 10,11`。  
`P3-PER-ACTION-AUTHORITY-CUTOVER-PRIMITIVE-GATE = R12A-13(reference pilot)`。  
`P3-ACTION-KERNEL-FUNCTIONAL-GATE = P3-ACTION-CONTRACT-KERNEL-GATE + R12A-01..13`；只证明机制，不表示全 Suite writer 已切换。  
`P3-ACTION-KERNEL-PRODUCTION-GATE = P3-ACTION-KERNEL-FUNCTIONAL-GATE + P1-CLUSTER-CORE-FUNCTIONAL-GATE + certified RPO=0 Provider + per-action pilot cutover Evidence`。

## 8. R13A/R14A：适配器框架

| Issue | 交付物 | 依赖 | Done 条件 |
|---|---|---|---|
| `P3-R13A-01` | machine-first CLI JSON/JSONL engine | `P3-ACTION-CONTRACT-KERNEL-GATE` | stdout 纯协议、stderr 人类提示、稳定 exit code |
| `P3-R13A-02` | Go/Python/TypeScript SDK runtime | `P3-ACTION-CONTRACT-KERNEL-GATE` | 同 canonical hash、错误、cursor、operation resume |
| `P3-R13A-03` | 签名产物、SBOM、版本协商与 anti-rollback | 01,02 | 安装和运行时验证；降级包拒绝 |
| `P3-R14A-01` | MCP stdio 与 Streamable HTTP transport | `P3-ACTION-CONTRACT-KERNEL-GATE` | transport 无业务语义、断线不改变 operation |
| `P3-R14A-02` | Schema/Tool/Resource 生成框架 | R14A-01 | Tool 只能来自 ActionIR，禁 raw wrapper |
| `P3-R14A-03` | session、限流、backpressure、drain | R14A-01 | 慢客户端/重连风暴不耗尽 Gateway/PG |
| `P3-R14A-04` | transport 安全与 SSRF 负向框架 | R14A-02,03 | origin、redirect、DNS、metadata 绕过为 0 |

`P3-CLI-SDK-FRAMEWORK-GATE = R13A-01..03`。  
`P3-MCP-FRAMEWORK-GATE = R14A-01..04`。  
这两个 Gate 只交付生成器和协议 runtime，不允许宣称最终 Tool 覆盖。

## 9. R12B：Agent 与模型安全

| Issue | 交付物 | 依赖 | Done 条件 |
|---|---|---|---|
| `P3-R12B-01` | OAuth/OIDC audience、sender constraint、Delegation graph | `P3-HUMAN-ACTION-SECURITY-GATE` | ancestor revoke 级联；refresh/token exchange 不扩权 |
| `P3-R12B-02` | Credential Broker 与 client/workspace/device 绑定 | 01 | 长期 secret 不进 Agent；跨 host/client replay=0 |
| `P3-R12B-03` | Model EgressDecision/EgressGrant/EgressIntent | 01 | Action Grant 不能替代 EgressGrant；use 线性化 |
| `P3-R12B-04` | Content Security Gateway 与 lane isolation | 03 | raw content 不与 Author/Operator authority context 组合 |
| `P3-R12B-05` | Outbound Broker、EndpointPolicy、SSRF fence | 03,04 | 内网/metadata/重绑定/跳转绕过=0 |
| `P3-R12B-06` | Prompt/tool/content injection 与 cache 隔离 | 04,05 | 跨 workspace/provider/tenant 污染和泄漏=0 |
| `P3-R12B-07` | Egress unknown、预算、load-shed、kill generation | 03,05 | unknown 不自动重投；Agent 故障不拖累调度 |
| `P3-R12B-08` | Agent RecoveryOverlay 与旧 context 撤销 | 01..07 | PITR 后旧 token/grant/ref/cursor/provider thread effect=0 |

`P3-AGENT-SECURE-ACTION-GATE = R12B-01..08 + P3-ACTION-KERNEL-PRODUCTION-GATE`。

## 10. R11B：最终 Suite Coverage

| Issue | 交付物 | 依赖 | Done 条件 |
|---|---|---|---|
| `P3-R11B-01` | 三领域独立 Surface Snapshot 验证 | `P1-PUBLIC-SURFACE-FREEZE-GATE` + `P2-PUBLIC-SURFACE-FREEZE-GATE(SUITE_CANDIDATE, capability_head_manifest_digest, rc_digest)` + `P4-PUBLIC-SURFACE-FREEZE-GATE(SUITE_CANDIDATE, capability_head_manifest_digest, rc_digest)` | Domain API/UI/bootstrap/reference/legacy ingress/runbook/capability 均来自真实 commit；正式 CLI/SDK/MCP 不作为输入 Surface |
| `P3-R11B-02` | Fragment lint 与签名 `SuiteSelectionManifest` | 01 + `P3-ACTION-CONTRACT-KERNEL-GATE` | 固定 CapabilityHead/Action/Surface 精确集合；全部 Core 纳入；installed/enabled/exposed/ACTIVE Optional 全纳入；重复、悬空、无 owner、无 production gate 为 0 |
| `P3-R11B-03` | 全局编译和 `SuiteCoverageLedger` | 02 | 所有 Surface 恰好一项分类；生成 diff 为 0 |
| `P3-R11B-04` | Coverage 负向与新增 Surface CI | 03 | 任一未分类 Surface 或 generation 漂移立即失败 |

`SUITE-PUBLIC-SURFACE-FREEZE-GATE = P1-PUBLIC-SURFACE-FREEZE-GATE + P2-PUBLIC-SURFACE-FREEZE-GATE(SUITE_CANDIDATE, capability_head_manifest_digest, rc_digest) + P4-PUBLIC-SURFACE-FREEZE-GATE(SUITE_CANDIDATE, capability_head_manifest_digest, rc_digest) + R11B-01`。  
`SUITE-ACTION-COVERAGE-GATE = P3-ACTION-CONTRACT-KERNEL-GATE + R11B-01..04 + zero(unclassified,duplicate,orphan,missing_owner,missing_gate)`。

`SUITE-CAPABILITY-SET-CLOSURE-GATE = R11B-01..04 + signed SuiteSelectionManifest + zero(runtime_discovered_set != selected_set)`：所有 Core 必选；所有 installed/enabled/exposed/ACTIVE Optional 必选；未选能力必须同时 Surface 不暴露、mutation ingress disabled 且返回 `UNSUPPORTED`；Experimental 也必须显式分类。

历史 Ledger 不会被改写；新增 Public Surface 提升 generation，并把“全当前 Surface”证书标记为 `SUPERSEDED`。

## 11. R13B/R14B：Suite 适配器与跨 Surface 一致性

| Issue | 交付物 | 依赖 | Done 条件 |
|---|---|---|---|
| `P3-R13B-01` | 从冻结 ActionIR 生成最终 CLI 与三 SDK | `P3-CLI-SDK-FRAMEWORK-GATE` + `SUITE-ACTION-COVERAGE-GATE` | 无手写业务分支；签名 digest 绑定 SuiteCandidate；输出 `SuiteGeneratedSurfaceManifest.cli_sdk` |
| `P3-R13B-02` | CLI/SDK N/N-1 和 FRESH/upgrade conformance | 01 | N/N-1 允许矩阵通过；higher major 写 fail closed |
| `P3-R14B-01` | 从冻结 ActionIR 生成最终 MCP stdio/HTTP | `P3-MCP-FRAMEWORK-GATE` + `SUITE-ACTION-COVERAGE-GATE` | Tool、Schema、annotation、错误与 Ledger 一致；输出 `SuiteGeneratedSurfaceManifest.mcp` |
| `P3-R14B-02` | MCP N/N-1、断线恢复与 transport conformance | 01 | 重连查询原 operation；无重复业务 effect |
| `P3-R14B-03` | Legacy MCP tombstone 与 raw mutation 负向测试 | 02 + `P3-LEGACY-MUTATION-FENCE-GATE` | legacy writer effect=0；read-only 期限明确 |
| `P3-R14B-04` | Cross-surface corpus | R13B-02,R14B-02 | 相同 intent 得到同 Action、hash、AuthZ、审批、Receipt |

`P3-SUITE-CLI-SDK-CONFORMANCE-GATE = R13B-01,02`。  
`P3-SUITE-MCP-CONFORMANCE-GATE = R14B-01..03`。  
`P3-SUITE-CROSS-SURFACE-CONFORMANCE-GATE = R14B-04`。

`SUITE-GENERATED-SURFACE-MANIFEST-GATE = P3-SUITE-CLI-SDK-CONFORMANCE-GATE + P3-SUITE-MCP-CONFORMANCE-GATE + signed SuiteGeneratedSurfaceManifest`；生成产物只能来自冻结 ActionIR，禁止从 CLI/SDK/MCP 反推或补写领域 Surface。

`SUITE-ACTION-AUTHORITY-CUTOVER-GATE = SUITE-ACTION-COVERAGE-GATE + SUITE-GENERATED-SURFACE-MANIFEST-GATE + signed SuiteSelectionManifest + all selected ACTION-AUTHORITY-CUTOVER-GATE(action_id) + zero(unmapped_mutation,two_writer,stale_generation)`；它按 `SuiteSelectionManifest` 的精确 Action 集逐项关闭旧 writer，不允许使用一个全局开关提前关闭尚未冻结的领域入口。

## 12. R15：真实客户端、Suite 安全与最终认证

R15 只能在 Plan1/Plan2/Plan4 Public Surface Freeze、Plan2 与 Plan4 Core Production Ready、Suite Capability Set Closure、Suite Action Authority Cutover 和 AI Deploy Functional Gate 后开始正式认证；Plan2/Plan4 Core GA 可与 R15 窗口并行，但必须在 Suite 最终签字前关闭。此前只允许准备 fixture，不允许累计最终 Evidence。所有并行窗口必须绑定同一个 `SuiteCandidateManifest`/`SuiteEvidenceKey`，但使用各自独立的 Admission Receipt；任一共享 Surface、RC、Authority 或安全 generation 漂移按 affects-set 重置受影响窗口。

客户端路径必须分开签证：Codex CLI 认证不自动等于 IDE/Desktop；OpenAI Responses 的 remote MCP 是独立 API surface；ChatGPT web 使用 hosted plugin/remote tool，不读取本地 Codex 配置，若需必须单独建 Capability cell。执行时以 [OpenAI MCP 客户端文档](https://learn.chatgpt.com/docs/extend/mcp?surface=cli) 和 [Responses MCP 文档](https://developers.openai.com/api/docs/guides/tools-connectors-mcp) 的当前版本为准，并将文档 digest/日期绑定到 `SuiteCandidateManifest`。

| Issue | 交付物 | 依赖 | Done 条件 |
|---|---|---|---|
| `P3-R15-01` | `SuiteCandidateManifest` 与 `SuiteEvidenceKey` | `SUITE-CAPABILITY-SET-CLOSURE-GATE` + `SUITE-ACTION-AUTHORITY-CUTOVER-GATE` + `P2-CORE-PRODUCTION-READY-GATE` + `P4-DEPLOYMENT-CORE-PRODUCTION-READY-GATE` + `P4-AI-DEPLOY-FUNCTIONAL-GATE` | 消费不可变 `SuiteSelectionManifest`；固定 commit/RC/schema/catalog/policy/generation/incarnation/provider/client/evaluator digest；三个领域与生成 Surface 集完全一致 |
| `P3-R15-02` | Codex CLI 真实接入 | 01 + `P3-AGENT-SECURE-ACTION-GATE` | Draft/diagnose/prepare/approve boundary/execute/resume E2E |
| `P3-R15-03` | Claude Code 真实接入 | 01 + `P3-AGENT-SECURE-ACTION-GATE` | exact build/model/transport/vendor boundary 冻结并通过 E2E |
| `P3-R15-04` | OpenAI Responses remote MCP 真实接入 | 01 + `P3-AGENT-SECURE-ACTION-GATE` | OAuth/egress/retention/data class 和断线恢复通过 |
| `P3-R15-05` | Generic MCP stdio/HTTP 认证 | 01 + `P3-SUITE-MCP-CONFORMANCE-GATE` | 非模型 conformance 与确定性 E2E 通过 |
| `P3-R15-06` | CLI + Go/Python/TypeScript SDK 认证 | 01 + `P3-SUITE-CLI-SDK-CONFORMANCE-GATE` | 同 corpus、错误、审批、Receipt 完全一致 |
| `P3-R15-07` | Eval protocol/样本/阈值预冻结 | 02..06 | 运行结果前签名；每模型 surface ≥300 episode |
| `P3-R15-08` | Prompt injection、IDOR、Secret、SSRF、approval 红队 | 07 | 未授权读/出站/目标改变/自批/重复 effect=0 |
| `P3-R15-09` | Scheduler isolation、容量、延迟和成本基准 | 07 | 绝对 SLO 通过；Agent-on 回归不越冻结预算 |
| `P3-R15-10` | N/N-1 rolling/canary/fallback/roll-forward | 08,09 | mixed version 无双 writer；不可逆点后只 roll-forward |
| `P3-R15-11` | 三次 full-size PITR/new-incarnation/旧站复活演练 | 10 | RPO=0；旧站/旧授权/旧 context/旧 operation effect=0 |
| `P3-R15-12` | Suite blocker preflight 与窗口 Manifest | 11 | 所有 P0 关闭；只允许“连续窗口未完成”保持 OPEN |
| `P3-R15-13` | 同一候选连续 72h Chaos | 12 | 每小时预注册故障；修复或影响性漂移从 0 重启 |
| `P3-R15-14` | 紧接同一候选连续 28 天 Pilot | 13 | 每模型 surface ≥100 真实 session；五类旅程各≥20 |
| `P3-R15-15` | FinalStateQuery、Evidence bundle、独立签字 | 14 | 未定案 Authority 状态=0；全部 EvidenceKey 完全一致 |

真实客户端只证明适配和安全边界，不拥有客户端专属业务语义。客户端版本、模型版本、租户、传输或数据合同变化时，按 affects-set 重跑相应 Cell。

## 13. N/N-1、PITR 与 Evidence 安全合同

- Server N 可以服务 N/N-1 的兼容读取；写入仅在 Action major、canonicalization、Schema 和 Authority generation 均兼容时开放。
- Schema 使用 `EXPAND → BACKFILL → SHADOW → AUTHORITY → CONTRACT`；CONTRACT 至少延后两个稳定 minor，混合版本期禁止两个 writer。
- Authority cutover 前可退回旧只读入口；一旦产生 Plan3-only state 或越过 writer boundary，禁止启动旧 writer，只能 kill-switch 后 roll-forward。
- 所有已向调用方返回 accepted 的 Plan3 写要求认证 Provider RPO=0；Promotion 必须验证 acknowledged WAL frontier 并物理 fence 旧 primary。
- PITR 必须生成新 `cluster_incarnation_uid`。旧 session、Delegation、Grant、EgressGrant、ContentRef、cursor、provider context 和 mutation operation 永久不可执行。
- 旧 Proposal 仅保留为 Evidence；继续操作必须重新解析目标、重新 prepare、重新批准，不能换 operation ID 自动重放。
- 若 acknowledged frontier、审计 anchor 或外部副作用无法定案，对应 Authority 保持 `RECOVERY_RECONCILIATION_REQUIRED/DISABLED`，由人工 Runbook 收口。
- Evidence 复用必须由机器生成 `EvidenceCompatibilityDecision`，证明变更不命中原 Evidence 的 affects-set，并由独立 Reviewer 签字。

## 14. 最终 Gate 与无环 Meta-DAG

```text
CRONOVA-SUITE-AI-PRODUCTION-READY-GATE = SUITE-CAPABILITY-SET-CLOSURE-GATE + SUITE-ACTION-AUTHORITY-CUTOVER-GATE + P3-AGENT-SECURE-ACTION-GATE + P3-SUITE-CLI-SDK-CONFORMANCE-GATE + P3-SUITE-MCP-CONFORMANCE-GATE + P3-SUITE-CROSS-SURFACE-CONFORMANCE-GATE + P2-CORE-PRODUCTION-READY-GATE + P4-DEPLOYMENT-CORE-PRODUCTION-READY-GATE + P4-AI-DEPLOY-FUNCTIONAL-GATE + R15-01..12
P3-SUITE-AI-31D-WINDOW-GATE = R15-13 + R15-14
P3-SUITE-FINAL-EVIDENCE-GATE = R15-15
CRONOVA-SUITE-AI-NATIVE-GA = CRONOVA-SUITE-AI-PRODUCTION-READY-GATE + P3-SUITE-AI-31D-WINDOW-GATE + P3-SUITE-FINAL-EVIDENCE-GATE + P1-CLUSTER-CORE-GA + P1-AUTHORING-GA + P2-CORE-GA + P4-DEPLOYMENT-CORE-GA + all current CapabilityCertification revisions selected by SUITE-CAPABILITY-SET-CLOSURE-GATE
```

无环主链：

```text
Suite Rebase → R11A → R12A → P3 Action Kernel Production
R11A → R13A/R14A；R12A → R12B；P3 Action Kernel Production → Plan4 Deployment Core/Surface Freeze
P1/P2/P4 Surface Freeze → R11B → R13B/R14B → Suite per-action cutover → R15
R15 → 72h Chaos → 28d Pilot → CRONOVA-SUITE-AI-NATIVE-GA
```

Plan4 的 `AI-assisted-deploy` 标签只能由最终 Suite GA 派生，不能反向成为 Suite GA 的前置条件。

## 15. Launch Blocker

以下任一项非零，禁止最终签字：未分类/重复/无 Owner Surface；runtime Capability set 与 SuiteCandidate 不一致；选定 Action 未完成逐项 Authority cutover；raw mutation 可达；Agent 自批准；跨 workspace IDOR；Secret 泄漏；Prompt Injection 改变目标或权限；Grant 重放；重复业务 effect；N/N-1 双 writer；PITR 后旧授权复活；Agent 负载突破核心调度预算；任一 Core 客户端缺 exact-version Evidence；72h 或 28 天窗口不连续。

## 16. 立即可执行项

1. 关闭 `P3-REBASE-ENTRY-GATE`，按当前 HEAD 重新生成真实 Surface 和 mutation inventory。
2. 冻结 Action ID、DomainCommandSpec、fragment、六分类及跨语言 Canonicalization Golden，并建立 Compiler/drift CI。
3. 建 `MutationIngressRegistry`，登记 REST/UI/CLI/MCP/bootstrap/break-glass writer。
4. 等 P1 primitives 后实现 Proposal、ApprovalGrant、OperationReservation 和同事务 Receipt/Audit；先 shadow，再经 CAS 切 Authority。
5. 并行建立 CLI/SDK 与 MCP framework；每个新增 Surface 同时提交 fragment 和 conformance fixture。
6. 预订真实客户端账号、独立红队、三次 PITR 环境和连续 31 天不漂移的候选窗口。

## 17. 负责人批准项

- [ ] 接受“AI 框架前置、全功能认证最后执行”。
- [ ] 接受 `SuiteCoverageLedger` 为唯一全局 Coverage 权威。
- [ ] 接受 Plan1/Plan2/Plan4 只提交 Domain fragment，不再各建正式 CLI/MCP/审批层。
- [ ] 接受所有 mutation 进入 `MutationIngressRegistry`，旧 writer 经过 CAS cutover 后不得恢复。
- [ ] 接受 AI 不能执行 `HUMAN_ONLY`，但最终 Coverage 仍要求对其明确分类和解释。
- [ ] 接受最终 Core 客户端集合：Codex CLI、Claude Code、OpenAI Responses remote MCP、Generic MCP、CLI 与三 SDK。
- [ ] 接受同一 SuiteCandidate 的 72h Chaos + 连续 28 天 Pilot，影响性变更从 0 重启。
- [ ] 接受唯一最终标签 `CRONOVA-SUITE-AI-NATIVE-GA`。

批准格式：`APPROVE / APPROVE_WITH_REQUIRED_CHANGES / BLOCK`。
