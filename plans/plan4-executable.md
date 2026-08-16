# Cronova Plan 4 Execution Edition 2.0

> 主题：一键部署、单机迁群、批量扩容与 AI-assisted deployment  
> Suite Contract ID：`CRONOVA-SUITE-EXEC-2.0`  
> 日期：2026-08-12  
> 原始详细 WBS：`plan4.md`，SHA-256 `f6d50b9a49d4fe37bda69ba233a8b13382809464704eefe0de38488138837ae1`  
> 重排依据：`plan-suite-sequencing-audit.md`  
> 性质：执行编排版；不代表代码已经实现、提交或通过生产 Gate

执行层级：本文件是施工顺序、Owner、Profile、Gate 和发布标签的权威；原 `plan4.md` 中逐 Issue 的部署、迁移、安全、批量执行、升级、DR 和 Evidence DoD 仍是对应工作包的强制子任务。无法自动合并的语义冲突必须先经 ADR/变更单解决并重跑依赖图，不得择宽执行。

## 0. 执行结论

Plan4 分成两个互不阻塞的产品出口：

1. `P4-DEPLOYMENT-CORE-GA`：人类通过 YAML/API/CLI/Web 安装、迁群和扩容；不等待最终模型客户端认证。
2. `P4-AI-ASSISTED-DEPLOY-GA`：AI/MCP 可以草拟、解释、诊断和准备 Proposal；它在四计划 Public Surface 冻结后，由最终 `CRONOVA-SUITE-AI-NATIVE-GA` 派生。

因此，Plan4 Core 不再硬依赖旧的 `P3-AI-NATIVE-GA`。它只依赖 Action 合同、通用人类审批安全内核、Plan1 集群权威和 Plan1 资源准入。

### 0.1 已冻结的用户范围

| 项目 | 执行值 |
|---|---|
| OS | Ubuntu Server LTS；首个 RC 候选 24.04 LTS x86_64，精确 minor/kernel/runtime 在 R17 冻结 |
| 网络 | 同一受控局域网；不认证 WAN、跨站、跨 NAT、公网扫描 |
| 主机 | 用户预先准备；不创建云 VM，不扫描 IP 段 |
| SSH | 用户名、自定义端口；密码、私钥、用户证书或 SSH Agent；必须固定 Host key/Host CA |
| 批量 | 显式 YAML/CSV/JSON 清单；单批上限 1,000，真实验证 100 个同时在线 Ubuntu OS 实例 |
| 单机 | 从第一天使用 PostgreSQL、S3-compatible 对象存储和 digest Registry；禁 SQLite 作为新 profile 权威 |
| 迁群 | 保持 `cluster_uid`，提升 authority generation/incarnation；零数据丢失，不承诺零停机 |
| 扩容 | Basic 批量加 Worker；HA 的 Control 只逐个 ADD/Replacement |
| 缩容 | 通用 scale-down、自动缩容、卸载和回收继续延期 |
| AI | 不接触 Secret、不接受 Host key、不直接 Apply、不生成 raw shell、不批准不可逆边界 |

## 1. Authority 和执行边界

| 对象/效果 | 唯一 Authority | Plan4 角色 |
|---|---|---|
| Cluster/Node/Session/Capacity/Grant | Plan1 Store/Receipt | 调用、等待并展示 canonical Receipt |
| Upgrade/Backup/DR/Incarnation | Plan1 Upgrade/Recovery Ledger | 编排，不裁决 effect |
| Resource admission | Plan1 R0-17、R1-03、R1-03A、R1-14 | doctor 后申请管理员 CAS；未批准容量为 0 |
| Domain deployment state | `ClusterManifestRevision` + `DeploymentOperationLedger` | 拥有期望拓扑和部署进度，不拥有业务执行权 |
| Action/Approval/Coverage | Plan3 全局合同和 Compiler | 只提交 `Plan4DomainActionPolicyFragment` |
| Bootstrap pre-PG approval | Plan4 本地 `BootstrapHumanGrant` | 仅 PG 建成前有效，Handoff 后永久 sealed |
| 中文/多语言显示 | Plan2 Presentation 合同 | 稳定 ID 定位；名称只展示与搜索 |

硬不变量：任一时刻同一个 effect 只能有一个 Authority、一个 current generation、一个稳定 operation ID；超时或 COMMIT outcome unknown 只能查询原 Receipt，不能换 ID 重试。

## 2. 入口 Gate

```text
P4-REBASE-PREFLIGHT =
  read-only access to actual repository/deployment baseline
  + provisional accountable owner/reviewer
  + zero mutation

P4-REBASE-ENTRY-GATE =
  R16-E00 signed ResourceLoadedPlan
  + actual repository HEAD
  + current Plan1/2/3 schema and capability inventory
  + Suite WorkPackageLedger
  + owner/reviewer/environment/resource booking
  + no unresolved duplicate client/approval/installer owner
```

`P1-RESOURCE-ADMISSION-GATE` 只引用 Plan1 的权威定义；Plan4 不复制或改写其公式。

R16 的纯 Schema、Golden、模拟器和 Provider Spike 可在 Plan1 后期开始；任何真实 host mutation 必须等待相应 Plan1 Functional Gate。AI-only 工作还必须等待 Plan3 Agent Secure Action Gate。

## 3. R16 — Deployment Kernel

### 3.1 工作包

| ID | 工作包 | 主要内容 | 依赖 | 完成定义 |
|---|---|---|---|---|
| R16-E00 | Rebase、资源装载与 Profile 冻结 | 按实际 HEAD 重建依赖、估算、Ubuntu/LAN/Profile、环境和 Reviewer | `P4-REBASE-PREFLIGHT` | 发布签名 ResourceLoadedPlan 并关闭 `P4-REBASE-ENTRY-GATE`；旧 814/1,389 人日只保留为上限参考 |
| R16-E01 | Public ClusterManifest/API 提前冻结 | ClusterManifest、Inventory、ResolvedSpec、Error Catalog、DomainAction fragment | `P4-REBASE-ENTRY-GATE`、`P3-ACTION-CONTRACT-KERNEL-GATE` | R17 开始前 Schema/Action major/operation envelope 冻结；后续破坏性变更必须新 major |
| R16-E02A | LocalBootstrapPlanJournal | `validate→plan→approve→local apply`、host-bound append-only signed journal | R16-E01 | 不依赖 PG/Plan1 Store；每个 durable boundary 以同 operation ID kill/resume；操作机丢失可从 init host 恢复 |
| R16-E02B | ClusterDeploymentOperationLedger | 将 bootstrap journal 和 Plan1 Receipt 导入 PG；集群级锁、generation、pause/partial | R16-E02A、R17-E02、`P1-ACTION-PRIMITIVES-GATE`、`P1-CLUSTER-INIT-PRIMITIVES-GATE` | PG 建成后才产生；不裁决 Plan1 业务 effect；重复 import=no-op |
| R16-E03 | BootstrapHumanGrant | 本地可信 `cronova-bootstrap` CLI/Web、完整 Target/Plan/Boundary、单次签名批准 | R16-E02A | PG 前 Agent/MCP mutation=0；Grant 不含 Secret；AI不能签 |
| R16-E04-PREPARE | Approval handoff prepare | 冻结 local writer、导入最终 journal tail、固定 approval intent/generation/incarnation/nonce | R16-E02B、R16-E03、`P3-HUMAN-ACTION-SECURITY-GATE` | 进入 `HANDOFF_PAUSED`；mutation authority count=0；新旧 writer 均不产生 effect |
| R16-E04-COMMIT | Approval handoff commit | PG serializable CAS 激活 Action Kernel；本地观察 Receipt 后永久 seal | R16-E04-PREPARE、`P3-PER-ACTION-AUTHORITY-CUTOVER-PRIMITIVE-GATE` | 输出同一 `APPROVAL-AUTHORITY-CUTOVER-RECEIPT`；全程 authority count≤1；unknown 只查原 Receipt |
| R16-E05 | SSH/Credential/Host Executor | password/key/cert/agent、custom port、Host key pin、sudo allowlist、helper typed actions | R16-E01、R16-E02A | Secret 不进 YAML/argv/env/log/DOM/模型；wrong host/TOCTOU/bundle traversal fail closed |
| R16-E06 | Supply chain/PKI/Recovery Kit | threshold trust root、BundleLock、签名/SBOM/撤销、CSR 本地产生、Root ceremony | R16-E05 | rollback/freeze/mix-match/旧证书重放 effect=0 |
| R16-E07A | Local Bootstrap Conformance | fake provider + 外置临时 PG + 两台 lab host；pre-PG crash/security tests | R16-E01、E02A、E03、E05、E06 | `P4-LOCAL-BOOTSTRAP-KERNEL-GATE` PASS；不宣传集群 Capability |
| R16-E07B | Ledger/Handoff Conformance | journal import、PG ledger、pause/commit/unknown/seal 全边界 | R16-E02B、E04-PREPARE、E04-COMMIT、E07A | `P4-DEPLOYMENT-KERNEL-GATE` PASS；无双 writer/遗失 Receipt |

`P4-LOCAL-BOOTSTRAP-KERNEL-GATE = R16-E01+E02A+E03+E05+E06+E07A`；只允许从 Ubuntu 裸机执行签名 typed action 以建起 PG/S3/Registry，不宣称集群 Authority 已建立。

`P4-DEPLOYMENT-KERNEL-GATE = P4-LOCAL-BOOTSTRAP-KERNEL-GATE+R16-E02B+E04-PREPARE+E04-COMMIT+E07B`；R16-E00 的 Rebase manifest 是所有节点的共同底稿，任何 dependency/owner/resource 漂移都使 Gate 失效。

### 3.2 Bootstrap 状态机

```text
LOCAL_PLANNED
→ BOOTSTRAP_HUMAN_APPROVED
→ LOCAL_APPLY
→ PG_BOOTSTRAPPED_SINGLE_WRITER
→ PLAN1_CLUSTER_INIT_RECEIPT
→ CLUSTER_LEDGER_IMPORTED
→ HANDOFF_PAUSED(authority_count=0)
→ ACTION_KERNEL_ACTIVATION_COMMIT/UNKNOWN
→ ACTION_KERNEL_ACTIVE(authority_count=1)
→ BOOTSTRAP_SEALED
```

Init host 失联且无法证明旧 authority 已隔离时进入 `NEEDS_INTERVENTION`，不得由笔记本自动夺权。`HANDOFF_PAUSED` 允许暂时零 writer，禁止任何时刻双 writer；COMMIT unknown 时保持 paused 并查原 Receipt。只有 PG 以同 nonce 证明从未激活且终态 CAS `ACTIVATION_ABORTED` 后，人工 RecoveryDecision 才能恢复 bootstrap writer。

## 4. R17 — Ubuntu 单机、Basic 与 Self-hosted HA

| ID | 工作包 | 依赖 | 完成定义/出口 |
|---|---|---|---|
| R17-E01 | Ubuntu Mutation Journal | `P4-LOCAL-BOOTSTRAP-KERNEL-GATE` | apt/dpkg/config/systemd/reboot 每步可 inspect/adopt/continue；不覆盖 foreign Docker/config |
| R17-E02 | single-portable 数据层 | R17-E01、`P1-ACTION-PRIMITIVES-GATE` | 同一 PG Schema、S3 合同、Registry digest、稳定 cluster UID；断电恢复通过 |
| R17-E03 | one-command single | R17-E02、`P4-DEPLOYMENT-KERNEL-GATE` | 安装→首次管理员→登录→发布并运行 DAG→查询 Artifact/Log/Output；重复 no-op |
| R17-E04 | single backup/restore/new-incarnation | R17-E03、`P1-UPGRADE-DR-FUNCTIONAL-GATE` | 隔离恢复、源 STONITH、提升 incarnation、旧凭据 effect=0；禁止双权威 restore |
| R17-E05 | cluster-basic-lan | `P4-DEPLOYMENT-KERNEL-GATE`、R17-E02、`P1-CLUSTER-INIT-PRIMITIVES-GATE` | 1 serving Control + 至少 2 独立 Worker；跨 Worker DAG；明确 `NOT_HIGHLY_AVAILABLE` |
| R17-E06 | Self-hosted HA Provider Decision | `P4-LOCAL-BOOTSTRAP-KERNEL-GATE`、`P1-CLUSTER-HA-FUNCTIONAL-GATE` | 冻结 PG/DCS、MinIO、Registry、L7/L4 LB、FenceProvider、许可证、磁盘、仲裁；任一关键 NO-GO 则只发布 Core |
| R17-E07 | HA Provider lifecycle | R17-E06、`P1-UPGRADE-DR-FUNCTIONAL-GATE` | OOB fence、old primary拒写、provider升级/换盘/证书轮换/restore；无 Fence 不 promote |
| R17-E08 | R17 Gates | 对应工作包 | `R17-SINGLE-GATE`、`R17-BASIC-GATE`、`R17-SELFHOSTED-HA-GATE` 独立签发 |

`R17-SINGLE-GATE = R17-E01..E04`；`R17-BASIC-GATE = R17-E01+E02+E05`；`R17-SELFHOSTED-HA-GATE = R17-E01+E06+E07`。HA NO-GO 不阻塞 Single/Basic。

Plan1 `R3-04/R3-04A` 只作为逐主机 Compose、init/join、Receipt 和参考安装 primitive。完整 SSH、Inventory、Wave、OS mutation 和产品 UX 只由 Plan4 拥有。

## 5. R18 — Legacy 导入与单机迁群

三条迁移 DAG 独立，不能用 BASIC 的 in-place topology transition 冒充 HA authority relocation。

| ID | 工作包 | 适用路径 | 完成定义 |
|---|---|---|---|
| R18-E01 | MigrationValidation/Merge/Projection Spec | 全部 | 逐表 `COPY_EXACT/TARGET_OWNED/REBUILD/MERGE/MUST_EMPTY`；canonical hash/version；容量和维护时间预算 |
| R18-E02 | Legacy→single-portable | 旧 Cronova | 签名加密 allowlist Bundle、唯一 cluster UID、独立 projection verifier；有排除项不得标 lossless |
| R18-E03 | single→cluster-basic | portable single | drain 后切 endpoint/trust/session；数据层保持权威，不复制后再误标源只读 |
| R18-E04 | single/basic→selfhosted HA | HA relocation | `QUIESCE→SNAPSHOT/ROOT HOLD→COPY→VERIFY→FENCE SOURCE→ACTIVATE TARGET→REENROLL` |
| R18-E05 | TriggerCutoverManifest | 全部 | schedule UTC boundary、event delivery/offset、manual operation、outbox watermark；漏/重 occurrence=0 |
| R18-E06 | DB/Object/Registry migration | HA relocation | 同 snapshot LSN/root digest；batch/multipart resume；GET digest；GC/lifecycle hold |
| R18-E07 | Authority handoff/recovery | HA relocation | activation nonce、global mutation lock、COMMIT unknown定案、`ACTIVATION_ABORTED` 或 roll-forward |
| R18-E08 | Migration Gates | 各路径 | `R18-LEGACY-GATE`、`R18-BASIC-GATE`、`R18-HA-GATE` 分开；cutover 后新 Trigger→Run→Grant→Output 成功，旧源 effect=0 |

`R18-LEGACY-GATE = R18-E01+E02+E05+E08(legacy cell)`；`R18-BASIC-GATE = R18-E01+E03+E05+E08(basic cell)`；`R18-HA-GATE = R18-E01+E04..E08(HA cell)`。

不可中断、非幂等或 `UNCERTAIN` Attempt 阻断 Cutover；本计划不迁移活动 Attempt。

## 6. R19 — 1–1,000 台显式主机批量 ADD

| ID | 工作包 | 依赖 | 完成定义 |
|---|---|---|---|
| R19-E01 | Inventory V2 与身份 | `P4-DEPLOYMENT-KERNEL-GATE`、`P2-IDENTITY-PRESENTATION-CONTRACT-GATE` | 稳定 host UID 与中文/多语言 display name 分离；无 CIDR/glob；重复地址/身份/角色 fail closed |
| R19-E02 | Secure SSH Transport | R19-E01 | 密码/密钥/证书/Agent/Bastion、host pin、credential lockout budget、脱敏 |
| R19-E03 | Batch/Wave Ledger | R19-E02 | parent/child稳定ID、RiskCohort canary、不可变wave、failure budget、pause/resume/stop-remainder |
| R19-E04 | Cluster-wide TransportBudget | R19-E03 | SSH/bastion/apt/Registry/S3/PG/Control join共享预算；业务调度优先，资源有界 |
| R19-E05 | Read-only Preflight + Prefetch | R19-E04 | preflight零副作用；批准后才缓存固定 digest Bundle；cache ownership可恢复 |
| R19-E06 | Worker CANARY_ONLY admission | R19-E05、P1-RESOURCE-ADMISSION-GATE | 普通capacity=0；固定 canary 成功后单事务 CAS ACTIVE+schedulable generation |
| R19-E07 | Control ADD/Replacement | R19-E05、`R17-SELFHOSTED-HA-GATE` | exactly单角色；direct canary→LB generation→ACTIVE；逐个执行；无OOB fence不激活 |
| R19-E08 | Cleanup/Replacement | R19-E06/E07 | cleanup先fence session再复验；Replacement add-first、双目标claim、完整drained predicate |
| R19-E09 | Scale Gate cells | Worker cell 依赖 R19-E01–E06；Control cell另依赖 R19-E07/E08 与 `R17-SELFHOSTED-HA-GATE` | 100个同时在线Ubuntu实例真实扩容；1,000生产状态机模拟；凭据泄漏/重复identity/双Grant/误清理=0 |

`R19-WORKER-ADD-GATE = R19-E01..E06+E09(worker cells)`；`R19-CONTROL-ADD-GATE = R17-SELFHOSTED-HA-GATE+R19-E01..E05+E07+E08+E09(control cells)`，不得借 Worker Gate 签发 Control 效果。

通用 REMOVE、自动缩容和 scale-to-zero 继续返回 `UNSUPPORTED`。

## 7. R20 — Human Deployment Core 与 AI Slice

### 7.1 Human Core

| ID | 工作包 | 依赖 | 完成定义 |
|---|---|---|---|
| R20-H01 | `cronova-bootstrap`、Domain API 与 reference client | `P4-DEPLOYMENT-KERNEL-GATE+R17-SINGLE-GATE+R17-BASIC-GATE+R18-LEGACY-GATE+R18-BASIC-GATE+R19-WORKER-ADD-GATE` | 本地签名 launcher 支持 guided plan/apply/resume/verify；Domain API/JSON/JSONL 稳定；reference client 标记 non-canonical；未知命令/Action fail closed |
| R20-H02 | Bootstrap Web / Operation Center | R20-H01 | 完整Target/Diff/Wave/Receipt/边界；刷新断线不换operation；Secret不可见；PG 后审批只渲染 Plan3 trusted Approval component，不自签 Grant |
| R20-H03 | Human Core Surface Conformance | R20-H01/H02 | YAML/Domain API/`cronova-bootstrap`/Web/reference non-canonical client 的 target、risk、approval、Receipt、error一致；不签发正式 SDK/MCP |
| R20-H04 | Upgrade/DR/Runbook/Evidence | `P1-UPGRADE-DR-FUNCTIONAL-GATE`、R20-H03 | N/N-1、PITR/new-incarnation、非作者安装恢复、off-cluster Evidence |
| R20-H05 | Performance/100-host/Operability | `R19-WORKER-ADD-GATE` | ResourceBudgetProfile、P95/P99样本规则、SLO、support bundle、a11y |
| R20-H06 | Core Window | `P4-DEPLOYMENT-CORE-PRODUCTION-READY-GATE` | 同RC 72h Chaos + 28天Pilot，持续标准DAG；修复/漂移按generation重置 |
| R20-H07 | Deployment Core Final | R20-H06 | Independent Board CAS关闭Blocker并签 `P4-DEPLOYMENT-CORE-GA` |

`P4-HUMAN-SURFACE-CORE-GATE = R20-H01..H03`；`P4-HUMAN-SURFACE-HA-GATE = P4-HUMAN-SURFACE-CORE-GATE+R17-SELFHOSTED-HA-GATE+R18-HA-GATE+R19-CONTROL-ADD-GATE+R20-H03(HA cells)`。`P4-UPGRADE-DR-GATE = R20-H04`；`P4-PERF-OPERABILITY-GATE = R20-H05`；`P4-CORE-WINDOW-GATE = R20-H06`；`P4-CORE-FINAL-EVIDENCE-GATE = R20-H07`。

### 7.2 AI-assisted deployment

| ID | 工作包 | 依赖 | 完成定义 |
|---|---|---|---|
| R20-A01 | Plan4 Domain Action Fragment | R16-E01、`P3-ACTION-CONTRACT-KERNEL-GATE` | Plan4不建第二Coverage Ledger；每个surface有Owner/Gate/六分类 |
| R20-A02 | bootstrap/cluster conformance adapter spec | R16-E04-COMMIT、`P3-MCP-FRAMEWORK-GATE` | handoff 前 MCP mutation=0；Plan4 只用非生产 harness 验证；生产 MCP 只由 Plan3 R14B 生成并调用唯一 Action Kernel |
| R20-A03 | Intent/Diff/Explain/Recovery Advisor | R20-A01/A02 | 只生成Draft/typed remediation；不生成shell、不换operation ID |
| R20-A04 | Infra injection/redaction/Egress | `P3-AGENT-SECURE-ACTION-GATE` | SSH/banner/log/CMDB不可信；外部模型默认仅见alias；Secret canary=0 |
| R20-A05 | AI Deployment Functional Gate | R20-A01–A04 | 百台blast radius、wrong target、拆单降级、迁群/恢复Human-only负向测试通过 |

`P4-DOMAIN-ACTION-FRAGMENT-GATE(profile_uid, capability_head_manifest_digest, rc_digest) = R20-A01 + zero(duplicate,orphan,missing_owner,missing_gate,second_authority)`。每个 mutation 记录 `ACTION-AUTHORITY-CUTOVER-GATE(action_id)`；Core/HA/installed Optional 按 Profile 分开签发。  
`P4-AI-DEPLOY-FUNCTIONAL-GATE = R20-A01..A05`。  
`P4-PUBLIC-SURFACE-FREEZE-GATE(SUITE_CANDIDATE, capability_head_manifest_digest, rc_digest) = P4-HUMAN-SURFACE-CORE-GATE + applicable HA/Optional surface cells + P4-DOMAIN-ACTION-FRAGMENT-GATE(SUITE_CANDIDATE, capability_head_manifest_digest, rc_digest) + P4-AI-DEPLOY-FUNCTIONAL-GATE + signed PublicSurfaceSnapshot`。

`P4-AI-DEPLOY-FUNCTIONAL-GATE` 进入最终 Suite AI Gate，但不进入 Human Deployment Core Gate。Plan4 本地 bootstrap launcher 由 Plan4 拥有；集群建立后的最终跨领域 CLI/SDK/MCP 仍由 Plan3 从 ActionIR 生成。

## 8. 最终 Gate 公式

```text
P4-BOOTSTRAP-APPROVAL-HANDOFF-GATE =
  R16-E03
  + P1-CLUSTER-INIT-PRIMITIVES-GATE
  + P3-HUMAN-ACTION-SECURITY-GATE
  + R16-E04-PREPARE + R16-E04-COMMIT
  + APPROVAL-AUTHORITY-CUTOVER-RECEIPT
  + Bootstrap writer sealed
  + mutation authority count <= 1
```

```text
P4-DEPLOYMENT-CORE-PRODUCTION-READY-GATE =
  P1-CLUSTER-CORE-GA
  + P1-RESOURCE-ADMISSION-GATE
  + P3-ACTION-KERNEL-PRODUCTION-GATE
  + P4-BOOTSTRAP-APPROVAL-HANDOFF-GATE
  + P4-DEPLOYMENT-KERNEL-GATE
  + R17-SINGLE-GATE + R17-BASIC-GATE
  + R18-LEGACY-GATE + R18-BASIC-GATE
  + R19-WORKER-ADD-GATE
  + P4-DOMAIN-ACTION-FRAGMENT-GATE(CORE, core_capability_head_manifest_digest, rc_digest)
  + P4-HUMAN-SURFACE-CORE-GATE + P4-UPGRADE-DR-GATE + P4-PERF-OPERABILITY-GATE
  + non-AI Launch Blockers closed
```

```text
P4-DEPLOYMENT-CORE-GA =
  P4-DEPLOYMENT-CORE-PRODUCTION-READY-GATE
  + P4-CORE-WINDOW-GATE
  + P4-CORE-FINAL-EVIDENCE-GATE
```

```text
P4-SELFHOSTED-HA-PRODUCTION-READY-GATE =
  P4-DEPLOYMENT-CORE-PRODUCTION-READY-GATE
  + R17-SELFHOSTED-HA-GATE
  + R18-HA-GATE
  + R19-CONTROL-ADD-GATE
  + P4-HUMAN-SURFACE-HA-GATE
  + HA-specific Upgrade/DR/Perf/Blockers closed

P4-SELFHOSTED-HA-GA =
  P4-SELFHOSTED-HA-PRODUCTION-READY-GATE
  + independent HA 72h Chaos + 28d Pilot
  + HA Final Evidence
```

```text
P4-AI-ASSISTED-DEPLOY-GA =
  CRONOVA-SUITE-AI-NATIVE-GA
  + P4-AI-DEPLOY-FUNCTIONAL-GATE
  + current ACTIVE P4 Deployment CapabilityHead
```

Core Window 与 Suite AI Window 可在同一 RC、不同 Admission Receipt、不同 Cell 并行；AI Production Ready 只要求 P4 Core Production Ready，最终 AI GA 才要求 P4 Core Capability 已 ACTIVE。

## 9. 估算与资源规则

原始 Plan4 总 envelope 为 P50 814/P80 1,389 人日；Core 闭包约 553/939，HA 闭包约 798/1,363。由于本版移除了 Core 对完整 AI GA 的等待，并把重复 CLI/MCP/Approval 工作迁回 Plan3，这些数字只能作为上限，不可直接承诺。

R16-E00 必须在 10 个工作日内交付去重后的新估算、关键路径和资源装载日历；未签字前，只允许 Spike、Schema、Golden 和模拟器工作。至少冻结以下稀缺资源：DB/迁移、Security/PKI、SRE/Provider、QA/Chaos、Release/DR、独立 Reviewer、100-host lab、FenceProvider 和真实 AI 客户端环境。

## 10. 立即执行顺序

1. `R16-E00`：按实际实现 HEAD rebase，而不是继续使用旧代码基线。
2. `R16-E01`：把 API/Action fragment 从原 R20 前移，先冻结再写 CLI。
3. 与 Plan3 协作完成 Action Contract 和 Human Approval Kernel；同时实现本地 BootstrapHumanGrant。
4. `R16-E02–E07`：Kernel crash/security Gate。
5. R17 single/basic；HA Spike 可提前，NO-GO 不阻塞 Core。
6. R18 和 R19 在共同 primitive 稳定后按资源并行。
7. R20 Human Core 先签；AI Slice 在 Suite Surface Freeze 后汇入最终 AI GA。

不得在完成 R16-E00 之前沿用原日历承诺，也不得在 Plan4 Core 中重新引入完整 P3 AI GA 祖先。
