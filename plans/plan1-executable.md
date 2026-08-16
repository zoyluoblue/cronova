# Cronova Plan1 Execution Edition 2.0

> 文档 ID：`P1-EXEC-2.0`  
> Suite Contract ID：`CRONOVA-SUITE-EXEC-2.0`  
> 范围：R0–R4——分布式执行、HA、生产安全与 Authoring 地基  
> 基线来源：冻结版 `plan1.md`（SHA-256 `f70d7cec2bbea6476f6f12149639e2bf9557cfe308d2d2f856e63211381d262a`）  
> 编排修订：消费四计划五轮时序审计；不修改、也不替代冻结原文  
> 状态：`READY_FOR_OWNER/DATE/HEAD BASELINE`；完成第 3 节 Entry 后才能开工

执行层级：本文件是施工顺序、Owner、Gate 和发布标签的权威；原冻结方案中的逐 Issue 验收项仍是必须关闭的子任务。两者若出现无法自动合并的语义冲突，必须先提交 ADR/变更单并重新生成依赖图，禁止实现者自行选择较宽松版本。

## 1. 交付目标与非目标

Plan1 交付一个真正可横向部署的 Cronova 基座：PostgreSQL 权威状态、不可变 Workflow Version、资源感知 Pull 调度、跨机 Worker、3-Control HA、升级/DR、安全基线，以及按中文/多语言 Presentation 合同建设的 Raw/Canvas Authoring。

Plan1 不签“全套 AI Native GA”，也不拥有最终生产 CLI/SDK/MCP 或完整多主机安装产品。它向后续计划交付稳定 Domain API、Schema、Action fragment、Conformance corpus 和底层 init/join primitive。

必须保持的不变量：

1. PostgreSQL 是调度状态、资源账本、Receipt 和 Authority generation 的唯一权威；不得以内存、心跳或客户端状态回写权威事实。
2. Source text 是业务定义真相；Run 固定不可变 Version/IR/Compilation Lock，不读取 latest Catalog 或当前 parser 重编历史运行。
3. Worker 只主动出站连接 Gateway/S3；无共享卷，无 Control→Worker 入站要求，无 rootful Docker socket。
4. Assignment、Reservation、Grant、Command、Completion 均有唯一 owner、CAS、fence、receipt 和可审计状态机。
5. display name 可为中文或任意语言；稳定 UID、技术 ID、显示名、别名、Layout key 永不混用。
6. 任何生产 mutation 只能有一个 Authority；旧入口必须登记，切换后映射 canonical Action 或被 fence。

## 2. Suite Contract 与唯一 Owner

### 2.1 Plan1 必须消费的 Suite Contract

| Contract ID | 必需内容 | 关闭条件 |
|---|---|---|
| `SC-SUITE-00-RESOURCE-LOADED-PROGRAM-MODEL@v1` | 四计划团队、环境、Provider、100 台实验、AI 客户端和 Reviewer 的真实资源装载 | Program Owner、SRE、QA、Security 签字 |
| `SC-REBASE-ENTRY@v1` | 当前 HEAD、Schema、route、feature generation、迁移状态和重复工作清单 | 第 3 节全部 PASS |
| `P2-IDENTITY-PRESENTATION-CONTRACT-GATE` | UID、technical ID、`display_name[locale]`、alias、locale fallback、`presentation_revision`、Layout keyed-by-UID | Plan2 的提前 ADR `R5-00A` 与 Plan1 WF/FE 共同签字 |
| `P3-ACTION-CONTRACT-KERNEL-GATE` | Action ID、target、Result/Error/Page/Cursor/ContentRef、operation ID、Policy fragment、canonicalization；底层 Contract ID=`SC-ACTION-CONTRACT-KERNEL@v1` | Plan3 Contract Owner 发布 schema、codegen 和 fuzz corpus |
| `SC-MUTATION-INGRESS@v1` | REST/UI/CLI/MCP/bootstrap/break-glass 登记与 generation CAS cutover | 每个入口有 owner、classification、fence 行为 |
| `SC-SUITE-EVIDENCE@v1` | `SuiteEvidenceKey`、affects-set、复用判定、签名、WORM 与 reset matrix | Release/QA/Security 独立签字 |

`P2-IDENTITY-PRESENTATION-CONTRACT-GATE` 至少冻结：稳定实体 UID 不可变；技术 ID 仅用于 API/引用且有唯一性规则；中文/其他语言显示名允许修改；alias 不改变引用；locale fallback 可确定重放；Presentation 变更不改变 semantic/compiled hash；Canvas 节点和边只按 UID 关联。

`SUITE-BASELINE-ACCESS-GATE = SC-SUITE-00-RESOURCE-LOADED-PROGRAM-MODEL@v1 + SC-REBASE-ENTRY@v1 + Owner Registry + SC-SUITE-EVIDENCE@v1 schema`；只证明可读基线、人员/环境和证据骨架已就绪。

`SUITE-CONTRACT-INTEGRATION-GATE = SUITE-BASELINE-ACCESS-GATE + P2-IDENTITY-PRESENTATION-CONTRACT-GATE + P3-ACTION-CONTRACT-KERNEL-GATE + SC-MUTATION-INGRESS@v1`；只阻塞需要 Identity/Action 结构的 Source/Authoring/fragment 工作，不阻塞 R0 止血、PG CI 和无副作用 Spike。任一合同变更必须提升 Suite Contract revision，不得覆写旧证据。

### 2.2 唯一 Owner 决议

| 能力 | 唯一生产 Owner | Plan1 允许交付 | Plan1 禁止交付 |
|---|---|---|---|
| CLI/SDK/MCP transport adapter | Plan3 | 临时 bootstrap/reference/read-only client、Domain API、Schema、Golden、Conformance | 第二套长期 mutation Authority、手写最终 MCP/CLI 参数语义 |
| Raw/Canvas Authoring 产品 | Plan1 R4 | React+TypeScript UI、Authoring Domain API、浏览器验证 | 绕过 Domain API 的客户端权威 AST |
| Workflow/Task UID、technical ID、Source/Layout | Plan1 Domain Store | 唯一生成/唯一性/迁移；Run 事务固定 Plan2 `presentation_revision_uid` | Plan2 或 UI 重发 UID/technical ID |
| SSH/Inventory/Wave/批量安装器 | Plan4 | `cluster init/join`、Receipt、角色 Compose、host-local signed bundle primitive | 完整 SSH 编排、批量 ADD、迁群和“一键部署”产品体验 |
| 全局 Action namespace/Compiler/Coverage Ledger | Plan3 | `Plan1DomainActionPolicyFragment` | 第二个 Coverage Ledger 或全局 ID 分配器 |
| 人类/Agent Approval Kernel | Plan3 | Plan1 AuthZ、影响数据和 receipt，可供 Action Kernel 消费 | 自建第二种 ApprovalGrant |

冻结版 `R0-12`、`R4-01`、`R4-09` 的 CLI/MCP 内容按上述 reference/conformance 范围执行；`R3-04A` 只保留底层 primitive。原 Issue ID 不删除，产品 Owner 边界以本表为准。

## 3. REBASE Entry：开工前的硬门

### `P1-REBASE-ENTRY-GATE`

入口：能只读访问目标 Cronova 仓库、部署配置和匿名化数据库/DAG 样本；关键 Owner 与独立 Reviewer 已实名分配。

执行：

1. 记录 `source_commit`、dirty-state、Go/Node/DB/runtime 版本及全部构建 digest；旧 `90cec6d` 只作 compatibility fixture，不再作施工 HEAD。
2. 导出当前 routes、UI mutation、CLI/MCP、Schema/migration、tables/indexes/constraints、feature flags、worker protocol 与 Compose 清单。
3. 用至少三份匿名生产快照重跑 legacy analyzer；记录数据量、unknown 字段、历史 Version/Run、外部依赖和不可自动迁移比例。
4. 建立 `MutationIngressRegistry` 初始清单，禁止未登记 mutation；为每个入口指定 Authority generation 与退役方式。
5. 逐项对照 71 个原 Issue，标记 `UNCHANGED/ALREADY_DONE/REWORK/SPLIT/MOVED_OWNER`，生成可机读 Work Package Ledger。
6. 依据当前 HEAD 重画依赖 DAG、重新估算、装载 DB/Security/SRE/QA/FE 容量，并预约真实 PG/S3/LB/Registry、两主机、3+3 HA 和独立故障注入环境。

出口/DoD：`evidence/p1/rebase-manifest.yaml` 含上述清单、SHA-256、Owner、资源日历和差异决议；悬空依赖、双 Owner、未登记 mutation、关键环境未预约均为 0。

回滚：本阶段只读；若无法形成可信基线，状态置 `BLOCKED_BASELINE`，不得开始 Schema 或生产 mutation。

估算 envelope：P50 4–7 人日，P80 7–12 人日；确认已由 Suite Wave0 完成的部分不得重复计费。

## 4. 执行拓扑与发布 Gate

```text
P1-REBASE-ENTRY-GATE
  → SUITE-BASELINE-ACCESS-GATE → R0 stop-loss/PG CI/spikes
  P2 Identity + P3 Action Contract → SUITE-CONTRACT-INTEGRATION-GATE
  R0 stop-loss + SUITE-CONTRACT-INTEGRATION-GATE → P1-SAFETY-FOUNDATION-GATE
        └→ P1-ACTION-PRIMITIVES-GATE → Plan3 Human Action foundation
  → P1-CLUSTER-BASIC-FUNCTIONAL-GATE ─────────────→ R2 implementation
        └→ P1-CLUSTER-BASIC-SOAK-GATE ────────────┐
  R2 implementation → P1-CLUSTER-HA-FUNCTIONAL-GATE
        └─────────────────────────────────────────→ P1-CLUSTER-HA-BETA-GATE
  → P1-CLUSTER-CORE-FUNCTIONAL-GATE → 72h Chaos → 28d Pilot → P1-CLUSTER-CORE-GA

P1-SOURCE-CAS-CONTRACT-GATE
  + P2-IDENTITY-PRESENTATION-CONTRACT-GATE
  + P3-ACTION-CONTRACT-KERNEL-GATE
  → P1-AUTHORING-CONTRACT-GATE
  → R4 UI implementation → P1-AUTHORING-FUNCTIONAL-GATE
  + P1-CLUSTER-CORE-GA → P1-AUTHORING-GA

P1-CLUSTER-CORE-FUNCTIONAL-GATE + P1-AUTHORING-FUNCTIONAL-GATE
  → P1-DOMAIN-ACTION-FRAGMENT-GATE
  → P1-PUBLIC-SURFACE-FREEZE-GATE
```

R2 可以在 R1 Functional 通过后开发，不等待 7 天 Soak；但 `P1-CLUSTER-HA-BETA-GATE` 必须消费同一兼容候选上的 `P1-CLUSTER-BASIC-SOAK-GATE`。`P1-CLUSTER-CORE-GA` 与 `P1-AUTHORING-GA` 分别签字；headless 集群稳定版不等待浏览器/WCAG，Authoring GA 必须等待 R3 稳定的 RBAC/API/升级合同。

### 4.1 四计划总施工波次

| Suite Wave | 可并行工作 | 汇合点 |
|---|---|---|
| S0 Contract/Rebase | 四计划 Rebase 先关闭 `SUITE-BASELINE-ACCESS-GATE`；Plan2 `R5-00A` 与 Plan3 R11A 并行产出 Integration Contract | `SUITE-CONTRACT-INTEGRATION-GATE` |
| S1 Safety/Framework | Plan1 R0 止血/PG CI/Spike 可与 S0 后半并行；Contract 就绪后完成 Source/Version/状态地基；Plan3 R13A/R14A 只做 framework | `P1-SAFETY-FOUNDATION-GATE` + `P1-ACTION-PRIMITIVES-GATE` |
| S2 Basic/Human Kernel | Plan1 R1 Functional；Plan3 R12A Human Action Kernel；Plan4 R16 Schema/Golden | `P1-CLUSTER-BASIC-FUNCTIONAL-GATE` + `P3-ACTION-KERNEL-FUNCTIONAL-GATE` |
| S3 HA/Domain Build | Plan1 R2/R3 与 R1 Soak；Plan1 R4；Plan2 R5A 及 Runtime Design；Plan4 R16/R17 不可变 primitive；在 RPO=0 Provider 和 `P1-CLUSTER-CORE-FUNCTIONAL-GATE` 后关闭 Action Kernel Production | 各计划 Functional Gate + `P3-ACTION-KERNEL-PRODUCTION-GATE` |
| S4 Product Authority | Plan1 Core/Authoring GA；Plan2 Runtime Authority 及 R6–R10 Core；Plan4 R17–R20 Human Core | P1/P2/P4 Production Ready |
| S5 Surface Freeze | P1/P2/P4 分别冻结 Public Surface/fragment；Plan3 R11B 聚合唯一 Coverage；R13B/R14B 生成正式 adapter | `SUITE-ACTION-COVERAGE-GATE` |
| S6 Certification | P1 先完成 Core GA；P2/P4 达到 Production Ready 且全 Public Surface 冻结后，P2/P4 Core 窗口与 Plan3 Suite AI 窗口可在不同环境、同一 SuiteCandidate 上并行 | P1/P2/P4 GA + `CRONOVA-SUITE-AI-NATIVE-GA` |

S5 之前可做 AI 合同、审批内核和 adapter 框架，但不宣称全产品 AI Native；真实客户端最终认证必须放在所有已选 Public Surface 冻结之后。

## 5. 可执行工作包

### WP0：Suite 合同冻结（P50 6–10d，跨计划共享）

| Entry | 原 Issue 映射 | 执行与 DoD | Rollback/Stop |
|---|---|---|---|
| `P1-REBASE-ENTRY-GATE` | 新编排项；关联 R0-01、R0-17、R4-01/04 | 先关闭 `SUITE-BASELINE-ACCESS-GATE`；建立 Owner Registry/Evidence schema；Presentation/Action fixture 归后续 `SUITE-CONTRACT-INTEGRATION-GATE` | Access 未关不开工；Integration 未关仅允许止血/PG CI/无副作用 Spike |

### WP1：R0 Safety & Foundation（原 104d/P80 141d）

| WP | 原 Issue 映射 | Entry | Exit/DoD | Rollback |
|---|---|---|---|---|
| `P1-W10` 基线止血与实验 | R0-01～R0-06 | `SUITE-BASELINE-ACCESS-GATE`；匿名 DAG/DB/环境可用 | 旧 structured writer 对高风险 DAG 返回 409/412/428；PG CI 零 skip；六项 spike 有 Go/No-Go；真实两主机 lab 可重放 | Guard 只能退为只读；不得恢复无保护写 |
| `P1-W11` Source→Version | R0-07～R0-12 | W10 + `SUITE-CONTRACT-INTEGRATION-GATE` | strict Source、LegacyAdapter、Catalog Snapshot、三 hash、Compilation Lock、Draft/Preview/Atomic Publish、version-pinned shadow Run 全通过；R0-12 仅 reference client | Expand-only；关闭 shadow；Version/Lock 不原地改 |
| `P1-W12` 执行正确性地基 | R0-13～R0-18 | W10；资源/状态 ADR 签字 | 状态转移、固定锁序、Attempt shadow、receipt、invariant、rollout generation、资源模型跨进程确定；R0 Evidence 可离线重放 | 关闭 authority flag；只 quarantine，不自动修事实 |

`P1-SOURCE-CAS-CONTRACT-GATE`=`R0-07+R0-09+R0-10+R0-11A`；允许 R4 Contract 线开始。  
`P1-SAFETY-FOUNDATION-GATE`=`P1-W10+W11+W12`，并要求 10 次 PG CI、100 次确定性编译、10,000 次状态/receipt 测试、真实远程调度副作用为 0。

`P1-ACTION-PRIMITIVES-GATE`=`P1-SAFETY-FOUNDATION-GATE+P2-IDENTITY-PRESENTATION-CONTRACT-GATE+R0-10/R0-14 Conformance`；输出 UID/ID、deny-by-default AuthZ、operation/receipt/audit、PostgreSQL Authority 和幂等 primitive，供 Plan3 直接消费。它只证明 primitive 可用；生产安全认证仍等待 `P1-CLUSTER-CORE-GA`。

### WP2：R1 cluster-basic Functional 与独立 Soak（原 107d/P80 145d）

| WP | 原 Issue 映射 | Entry | Exit/DoD | Rollback |
|---|---|---|---|---|
| `P1-W20` 协议、身份、资源准入 | R1-01～R1-04（含 R1-03A） | `P1-SAFETY-FOUNDATION-GATE`；D0a/PKI 可用 | V2 protocol、one-shot alpha enrollment、current Session、approved inventory generations、单一 Eligibility evaluator、资源+quota 原子 Reservation | 停止 occurrence→drain→CAS authority 回旧 generation |
| `P1-W21` Grant 与 crash-safe runtime | R1-05～R1-07（含 R1-05A） | W20；Grant/journal ADR | Offer→Prepare→Grant 永久唯一；journal/fsync/runtime identity/Cancel/Reconcile crash matrix 无双执行权；rootless fail-closed | Fence 新 Grant；保留 journal/tombstone；只 roll-forward V2-only 状态 |
| `P1-W22` Artifact/Log/Secret | R1-08～R1-10 | W21；S3/Secret Provider ready | 跨 Worker Artifact、不可变日志/Output、最小 capability 与 canary-secret=0，无共享路径 | 撤销 capability；保留 GC roots，不删除运行证据 |
| `P1-W23` Occurrence、Planner、init primitive、E2E | R1-11～R1-16 | W20–W22；V1/V2 bridge 先行 | 迁移 ledger、唯一 Occurrence、immutable IR→Ready、preflight、host-local init/join primitive、两真实 Worker E2E 完成 | 未 cutover 可停 shadow；cutover 后 V2-only 只 roll-forward |

`P1-CLUSTER-BASIC-FUNCTIONAL-GATE`：除连续 7 天窗口外，冻结版 R1 Gate 全部通过；至少 1 Control、独立 PG/S3、2 台异构 Worker，10k Ready microbench、1,000 次 RequestWork replay、全 durable-boundary failpoint、资源/Grant/Runtime 不变量违规为 0。

`P1-CLUSTER-BASIC-SOAK-GATE`：绑定同一 `SuiteEvidenceKey`，连续 7 天每小时≥1,000 Attempt、100 跨 Worker Artifact、10 条≥10MiB 日志、每日≥4 次重连；执行/数据面修复后从 0 重启。通过后才可发布 `cluster-basic/alpha`。

`P1-RESOURCE-ADMISSION-GATE`=`R0-17+R1-03+R1-03A+R1-14`；同时输出版本化 Contract `SC-P1-RESOURCE-ADMISSION@v1`。Plan4 新 Worker 未消费此 Gate 时可安装/doctor，但 `schedulable_capacity=0`。

`P1-CLUSTER-INIT-PRIMITIVES-GATE`=`R1-15+R3-04+R3-04A+R3-10 Conformance`；只签发逐主机 bundle、init/join、Receipt、resume/verify 和 doctor primitive，不包含 SSH Inventory、Wave 或一键部署产品。

### WP3：R2 cluster-ha Beta（原 56d/P80 76d）

| WP | 原 Issue 映射 | Entry | Exit/DoD | Rollback |
|---|---|---|---|---|
| `P1-W30` HA 角色、Signer、Fence | R2-01～R2-04（含 R2-01A） | `P1-CLUSTER-BASIC-FUNCTIONAL-GATE` | API/Gateway/Dispatcher AA；Planner/Reconciler DB-time lease+epoch；Signer 隔离；Stream rebind/Command recovery/Reconcile 全 fenced | 切回单 active role；旧 epoch/session/incarnation 写入继续拒绝 |
| `P1-W31` HA API、quota、Provider、Chaos | R2-05～R2-10 | W30；3 Control 与真实 Provider | 无状态 Publish/Cutover、quota 并发、LB/mTLS、PG/S3/LB/Registry failover、N/N-1 matrix 与组合 Chaos 通过 | Capability 标 `HA_UNVERIFIED`；不得用缓存或同主机冒充 HA |

`P1-CLUSTER-HA-FUNCTIONAL-GATE`：R2-01～R2-10 的功能、token 负向矩阵、100 次 owner failover 与不变量收敛通过。  
`P1-CLUSTER-HA-BETA-GATE`=`P1-CLUSTER-HA-FUNCTIONAL-GATE+P1-CLUSTER-BASIC-SOAK-GATE+Provider Evidence`；只有真实故障域证据才可标 `cluster-ha/beta`，否则必须标 `HA_UNVERIFIED`。

### WP4：R3 Production RC→Stable（原 86d/P80 121d）

| WP | 原 Issue 映射 | Entry | Exit/DoD | Rollback |
|---|---|---|---|---|
| `P1-W40` 安全与供应链 | R3-01～R3-04 | `P1-CLUSTER-HA-BETA-GATE` | RBAC/IDOR、Signer 生命周期、OCI/egress/SSRF、角色 Compose、digest/signature/provenance 独立认证 | 撤销 capability/证书，fence mutation；不回退安全 Guard |
| `P1-W41` init/join primitive 与运维 | R3-04A～R3-06 | W40 | R3-04A 仅交付 host-local bundle、init/join/receipt/resume/verify primitive；Admin 处置、metrics/alert/安全 GC 可非作者执行 | 未决 receipt resume/abort；禁止越界实现 SSH/Wave 产品 |
| `P1-W42` Upgrade/DR/Retention/doctor | R3-07～R3-10 | W40；真实 N-1 build/备份环境 | Expand→upgrade→Contract ledger、半程恢复、三次 PITR/new-incarnation、GC roots、Provider/runtime doctor 通过 | 优先 resume/roll-forward；存在 V2-only row 时禁止 down migration |
| `P1-W43` 非作者演练与发布窗口 | R3-11～R3-12 | W40–W42；RC digests 冻结 | 非作者用 primitive+runbook 完成 3+3 部署/升级/DR；72h Chaos 与连续 28 天 Pilot 在同一 EvidenceKey 下通过 | 受影响修复按 reset matrix 重跑完整窗口 |

`P1-CLUSTER-CORE-FUNCTIONAL-GATE`：R3-01～R3-11、LB-01～LB-12 的非窗口证明均关闭；可生成 RC，不得称 Stable。  
`P1-UPGRADE-DR-FUNCTIONAL-GATE = P1-W42 + R3-11 中 N/N-1/PITR/new-incarnation 非窗口 Evidence`；供 Plan4 安装器、备份恢复和迁群实现消费，不代表 31 天 Stable 标签。  
`P1-CLUSTER-CORE-GA`=`P1-CLUSTER-CORE-FUNCTIONAL-GATE+72h Chaos+28d Pilot+三次 DR+真实 N/N-1+全部 LB CLOSED`；签 `cluster-ha/stable`。Plan1 只要求可重放的底层安装 primitive，不把 Plan4 的一键 SSH/Inventory 产品倒灌为前置。

### WP5：R4 Authoring Contract→GA（原 97d/P80 131d）

| WP | 原 Issue 映射 | Entry | Exit/DoD | Rollback |
|---|---|---|---|---|
| `P1-W50` Authoring Contract | R4-01、R4-04；关联 R0-10/11A/11B | `P1-SOURCE-CAS-CONTRACT-GATE`+Presentation+Action Contract | OpenAPI/Schema/Catalog 同源；Source/Layout revision、稳定 UID、localized name/alias、Domain Operation、ETag/409/412/428/receipt 冻结；R4-01 不生成最终 CLI/MCP | Contract 未签只允许 Raw prototype；不允许 Canvas 数据模型定型 |
| `P1-W51` Raw/Catalog/Canvas/冲突 | R4-02、R4-03、R4-05、R4-06 | `P1-AUTHORING-CONTRACT-GATE` | invalid exact Draft、opaque higher-version、拖拽/连线、离线 autosave、三方冲突、Undo/inverse 均不丢稿且不 LWW | 禁 mutation，保留 exact Source 与本地队列；不得重写历史 |
| `P1-W52` Diff/Test/Plan/Migration | R4-07～R4-09 | W51；R1-16、R3-07 | version diff/restore/test-run pin、统一 Eligibility Resource Plan、迁移/cutover/rollback UX；R4-09 只提交 MCP conformance，不拥有最终 adapter | 关闭 test-run/cutover flag；旧 writer 不得在 V2 Authority 后复活 |
| `P1-W53` Browser/性能/可访问性 | R4-10 | W51–W52 | 500/1,000-node p95、1,000/3,000 stress、双标签/离线/乱序 E2E、WCAG 2.2 AA 人工+自动矩阵通过 | 保持 headless stable；Authoring 只降级 beta/read-only |

`P1-AUTHORING-CONTRACT-GATE`=`P1-W50`；这是 Plan2 Presentation 实现、Plan3 Action projection 的稳定输入，不代表 UI GA。  
`P1-AUTHORING-FUNCTIONAL-GATE`=`P1-W50+W51+W52+W53`，生成 Authoring candidate 和 Public Surface snapshot。  
`P1-AUTHORING-GA`=`P1-AUTHORING-FUNCTIONAL-GATE+P1-CLUSTER-CORE-GA+Browser/WCAG independent review`。

### 5.1 原 71 个 Issue 的完整映射

| Work Package | 冻结版 Issue ID |
|---|---|
| P1-W10 | R0-01、R0-02、R0-03、R0-04、R0-05、R0-06 |
| P1-W11 | R0-07、R0-08、R0-09、R0-10、R0-11A、R0-11B、R0-12 |
| P1-W12 | R0-13、R0-14、R0-15、R0-16、R0-17、R0-18 |
| P1-W20 | R1-01、R1-02、R1-03、R1-03A、R1-04 |
| P1-W21 | R1-05、R1-05A、R1-06、R1-07 |
| P1-W22 | R1-08、R1-09、R1-10 |
| P1-W23 | R1-11、R1-12、R1-13、R1-14、R1-15、R1-16 |
| P1-W30 | R2-01、R2-01A、R2-02、R2-03、R2-04 |
| P1-W31 | R2-05、R2-06、R2-07、R2-08、R2-09、R2-10 |
| P1-W40 | R3-01、R3-02、R3-03、R3-04 |
| P1-W41 | R3-04A、R3-05、R3-06 |
| P1-W42 | R3-07、R3-08、R3-09、R3-10 |
| P1-W43 | R3-11、R3-12 |
| P1-W50 | R4-01、R4-04 |
| P1-W51 | R4-02、R4-03、R4-05、R4-06 |
| P1-W52 | R4-07、R4-08、R4-09 |
| P1-W53 | R4-10 |

总数复核：R0 19 + R1 18 + R2 11 + R3 13 + R4 10 = 71；无删除、无悬空。Owner carve-out 仅改变 R0-12、R3-04A、R4-01、R4-09 的最终产品责任，不丢弃其 Domain primitive/conformance 工作。

## 6. Plan1 DomainActionPolicyFragment

产物：`contracts/actions/fragments/plan1/v1/fragment.yaml`，Contract ID `SC-P1-DOMAIN-ACTION-FRAGMENT@v1`。Plan1 只拥有 fragment；Plan3 分配/校验全局 namespace、编译并生成唯一只读 `SuiteCoverageLedger`。每个新增或修改 Public Surface 的 PR 必须同步更新 fragment 与 fixture；本节 Gate 是最终闭包，不是等到最后才补登记。

每条记录必须包含：`action_id=cronova.p1.<domain>.<verb>.v1`、surface refs、stable target resolver、input/result/error/cursor schema digest、AuthZ、risk、approval class、idempotency/operation ID、handler owner、Domain Gate、rollback/compensation、evidence refs、classification。

首批 Action family：

| Domain | Action family | 默认 classification |
|---|---|---|
| Workflow Authoring | create/get/update source、validate、preview、publish、restore-to-draft、rollback-version | read/draft 可开放；publish/rollback=`APPROVAL_REQUIRED` |
| Execution | trigger、get/list run、cancel、logs/output/artifact read | query 可开放；trigger/cancel=`APPROVAL_REQUIRED` |
| Cluster Resource | worker/list/get/doctor、drain、node/capacity admission proposal/approve、resource plan | read/plan 可开放；容量增加/drain=`APPROVAL_REQUIRED`；SSH Host Inventory/Wave 永久归 Plan4 |
| Recovery/Security | source fence、incarnation activate、secret capture、break-glass、不可逆 DR 裁决 | `HUMAN_ONLY`，Agent execute 永远为 0 |
| Internal state | Grant、Reservation release、epoch/lease、Reconcile repair primitives | `INTERNAL_ONLY`，不得暴露为用户 Action |

`P1-DOMAIN-ACTION-FRAGMENT-GATE`：Public Surface extractor 与 fragment 双向覆盖；`unclassified/duplicate/orphan/missing_owner/missing_gate=0`；Golden 验证 REST/UI/reference client 对同一 domain mutation 得到同一 Result/Error/Receipt。每个 mutation 记录 `ACTION-AUTHORITY-CUTOVER-GATE(action_id)` 的 domain gate、surface generation、old ingress set 和 Receipt；未切换时可标记 pending，但不得出现两个 writer。新增 Surface 必须提升 generation 并使旧聚合 coverage `SUPERSEDED`。

## 7. Public Surface、Evidence 与回滚规则

`P1-PUBLIC-SURFACE-FREEZE-GATE` 固定：source commit、API/OpenAPI/Proto、UI routes/mutations、Schema/migration、Task/Resource Catalog、Action fragment、CapabilityHead、security/authority generation 和全部 digest。它是 Plan3 最终 CLI/SDK/MCP 生成与 Suite AI Coverage 的输入，不是 AI GA。

每个 Gate 的 Evidence Bundle 必须：可离线重放、签名、WORM 保存；绑定同一 `SuiteEvidenceKey`；包含原始结果、seed、环境/Provider、组件 digest、invariant snapshot、Accountable 与非作者 Reviewer。不同 RC 的“latest PASS”不得拼接；只有机器生成且独立签字的 `EvidenceCompatibilityDecision` 能复用不相交 affects-set 的证据。

统一回滚原则：

- DB 默认 Expand-only，Contract/drop 至少延后两个稳定 minor；不可逆状态只 roll-forward。
- 先停止新 occurrence/Grant，再 drain 或隔离，最后 CAS Authority generation；禁止直接改 DB 状态列。
- Version/IR/Lock、Receipt、Audit、Grant、Command、Completion 不原地修改或删除。
- Provider Gate 失败降级 capability 标签，不伪造 HA；Authoring Gate 失败不撤销已签 headless cluster stable。
- 执行面、数据面、安全、Schema、Catalog/Policy 或 Provider 的受影响变更使对应连续窗口从 0 重启。

## 8. Launch Blocker Ledger

| Blocker | 必须为 0 的风险 | 原 Issue 归属 |
|---|---|---|
| LB-01 | Source 被 UI/API/reference client 静默改写 | R0-02/03/07/08、R4-01/02/04/10 |
| LB-02 | PG 测试 skip、dirty migration 仍启动 | R0-04/14/18、R3-07 |
| LB-03 | 单机冒充多机、init/join 无法恢复 | R0-06、R1-15/16、R3-04/04A/11 |
| LB-04 | 共享路径、rootful socket、任务触达 agent/runtime | R1-07/08/09、R3-03/04/10 |
| LB-05 | 双 Grant、换 operation ID 重试、第二 runtime identity | R0-13/14、R1-04/05/05A/06 |
| LB-06 | 旧 epoch/session/fence/incarnation 写权威状态 | R2-02/03/04/10 |
| LB-07 | 资源/device/quota 超配、双扣或双释放 | R0-17、R1-03/03A/04/06/14/16、R2-06/10 |
| LB-08 | 未验证 Provider/故障域却标 HA | R0-01、R2-07/08/10、R3-10 |
| LB-09 | 无真实 N-1、DR、72h、28d 却标 Stable | R2-09、R3-07/08/11/12 |
| LB-10 | Run 重编 latest、V2 后恢复 V1 writer | R0-09/10/11B、R1-11/12/13、R2-05 |
| LB-11 | AuthZ、Secret、OCI/egress、供应链边界可绕过 | R0-10/15、R1-08/09/10、R3-01/02/03/04 |
| LB-12 | 跨未认证 WAN/恢复域，或可选依赖带走核心调度 | R0-01/16、R2-08/10、R3-06/10/12 |

任一 Blocker 未 `CLOSED`，绑定的 capability 不得签字；每项必须记录 commit、自动测试、Evidence digest、Accountable、独立 Reviewer 和时间。

## 9. 估算 Envelope 与资源装载

冻结版 Core 基数仍为 P50 450/P80 614 工程日：R0 104/141、R1 107/145、R2 56/76、R3 86/121、R4 97/131。Execution 2.0 新增 rebase、Presentation conformance、Action fragment、Ingress/Evidence 对齐，同时移出重复的最终 CLI/MCP 与完整安装器产品责任；在真实 HEAD 重估前只使用 **P50 450–475、P80 614–660 工程日** 的 envelope，不承诺单点数字。

不可压缩窗口：R1 连续 7 天 Soak、R3 连续 72h Chaos、连续 28 天 Pilot、R4 独立 Browser/WCAG 人工复核。建议 6–7 人覆盖 Architecture、WF/API、CP/DB、Runtime/SRE、FE、QA/Chaos、Security 帽子，平均约 3.6 个持续实施 FTE；若与 Plan2/3/4 共用约 10 人团队，同一时段最多一条重实施主线加一条轻量合同/前端支线。

条件认证不并入 Core：`airgap-certified` 15/22d、`worker-edge-network-certified` 8/12d、`arm64-certified` 6/9d、`registry-preloaded-cache-certified` 2/3d。目标生产环境强制 air-gap 时，15/22d 前移并成为 R3 阻断 Gate。

## 10. 最终交付与下一计划入口

Plan1 完成必须同时交付：

1. `P1-CLUSTER-CORE-GA` 对应的 `cluster-ha/stable` CapabilityHead。
2. `P1-AUTHORING-GA` 对应的 Authoring CapabilityHead；或明确记录 headless 已稳定、Authoring 仍 beta。
3. `SC-P1-RESOURCE-ADMISSION@v1`、`SC-P1-AUTHORING@v1`、`SC-P1-CLUSTER-INIT-PRIMITIVES@v1`。
4. `P1-ACTION-PRIMITIVES-GATE`、`SC-P1-DOMAIN-ACTION-FRAGMENT@v1` 与 `SC-P1-PUBLIC-SURFACE@v1`，供 Plan3 全局编译与最终 AI Coverage 使用。
5. Signed Evidence Bundle、Compatibility tuple、升级/DR runbook、Owner/Blocker Ledger，且所有标记都绑定未过期的同一 EvidenceKey。

Plan2 Runtime 的 Schema/Golden 可在 `P1-CLUSTER-BASIC-FUNCTIONAL-GATE` 后受控并行，生产 Gate 必须消费 `P1-CLUSTER-CORE-GA`；Plan2 Authoring 必须消费 `P1-AUTHORING-CONTRACT-GATE`。Plan3 可以前置建设 Action/Approval/Adapter framework，但最终 Suite Coverage 必须等待 Plan1/2/4 Public Surface Freeze。Plan4 消费 init/join primitive 和 `P1-RESOURCE-ADMISSION-GATE`，独占一键部署、SSH Inventory、迁群与批量扩容产品。

在 `P1-REBASE-ENTRY-GATE` 未关闭前，不创建大规模实现 Milestone；关闭后按 WP0→WP1→WP2 Functional→WP3/WP2 Soak 并行→WP4，同时沿 Authoring 合同线执行 WP5。Issue 顺序不是权威，本文 Gate 与 Work Package Ledger 才是执行拓扑。
