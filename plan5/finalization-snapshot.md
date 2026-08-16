# FinalizationSnapshot（plan5 §11）

生成于 2026-08-16。冻结本轮 Plan1–5 执行的最终状态。

## 版本与摘要

- 施工 HEAD: `094b28b5e03c9ec1b28eeea2d7e1fcfcb95ca08f`（main，已推送）
- 计划源哈希: 见 `plan5/source-manifest.txt`（5 份 SHA-256）
- 本轮提交链: 43132b7(V2基线+4修复) → f04b49c(计划冻结) → e15e39a(P1 CAS+PG-CI) → 850faa2(队列) → ed40804(P1 状态守卫) → 3da4edb(P1 图编辑验收) → 532b5bc(P1 收口) → 5a8a8a2(P2 hold/timeline) → d960fbd(P3 六分类) → bf59364(P4 migrate-store) → 094b28b(5 项对抗性修复+compose)

## 测试矩阵终态

- `go test ./...`：17/17 包 PASS
- `go test -race ./...`：17/17 包 PASS
- PG 真库套件（PG 17.5 容器）：6/6 PASS
- 定向行为测试新增本轮：CAS(1)、状态转移守卫(1)、hold/release(2)、timeline(1)、覆盖闭合(1)、subdag 饥饿回归(1)
- 浏览器实测：graph 编辑全环（加点/连线/删边/环拒/保存/CAS 冲突）、双语
- 真机/容器 E2E：migrate-store（迁移后 PG 直接服务并跑通运行）、compose full（PG+hub+双 worker 自动接入、远程任务 success）

## 对抗性验证终态

- 第一轮（V2 后）：4 确认 → 4 修复（终态事件可靠投递、rewind 协议、active 计数、refs GC）
- 第二轮（收口重验 7 条未决）：5 确认 → **5 全修复**（subdag 预算饥饿 HIGH、通知抑制泄漏 MED、sweep 锁内 IO MED、组清空 TOCTOU LOW、PG 准入奇偶 LOW）；2 反驳（runTask guarded 降级为有意设计、admit 重取无陈旧字段泄漏）
- 当前未决确认缺陷：**0**

## 已知开放缺口（非缺陷，如实记录）

- Plan2：durable 通知 Outbox、事件 DLQ、WFQ/Bin-pack（独立 capability，下轮）
- 双 worker 同组竞争 E2E 已由 compose full 冒烟覆盖（单任务路径）；多任务负载均衡分布未做定量断言
- BLOCKED_ENVIRONMENT 清单见 execution-ledger（连续窗口/多人签字/百台实验室/真实 Provider 类）
