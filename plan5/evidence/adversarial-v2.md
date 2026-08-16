# V2 对抗性审查记录（2026-08-08）

5 路专项猎手（hub 并发 / 安全 / dispatch 重构 / 组合状态机 / 存储奇偶性）+ 逐条对抗性反驳验证。

## 确认并已修复（4）

| # | 严重度 | 发现 | 修复 |
|---|---|---|---|
| 1 | high | worker 终态 TaskEvent 经非阻塞 send 丢弃：日志积压时任务在 hub 侧永远停留 Running | `internal/worker/worker.go` `sendReliable`（阻塞、会话切换自适应、~1min 上限后交给重连 Probe 兜底）；launch 失败事件同路径 |
| 2 | high | 日志 ack 回退协议单边：hub 拒 gap 并期待回退，worker 忽略回退型 ack → 无限拒绝、日志整体丢失、spool 泄漏 | proto `LogAck.rewind` 标志；hub gap 分支发 rewind；worker `handleAck` 处理回退 + pump 每轮检查 rewind 游标 |
| 3 | medium | 重连重认领不恢复 `session.active`：least-loaded 路由反向倾斜且计数持续错乱 | Session Hello 重认领循环中对本 worker 运行中 ref 逐个 `s.active++` |
| 4 | medium | `Hub.Forget` 全仓库零调用：`h.refs` 按进程生命周期无限增长 | sweep 中对终态 ref（`doneAt` > 30min）GC |

验证：`go build ./...` + `go test ./...` 17/17 包绿（含 workerhub e2e 三用例）。

## 已反驳（审查中排除的误报）

见 workflow 输出存档（refuted 列表）：包括"dispatch 使用陈旧 readyTask 指针"（admit 前重读 TI）、
"serial reorder 破坏公平"（稳定排序保持交错）等——均有具体守卫代码定位。

## 因模型限额中断、未完成验证的发现（7）——待 Plan5-R04 重验

1. sweep 持有 h.mu 期间逐 ref 调用 store.GetWorker（锁内 IO，随 refs 规模放大）
2. runTask 标记 running 的 guarded 写失败路径（keepWorkspace 分支的进程遗留语义）
3. notifySuppress 条目在 MarkTask 重激活后的清理时机
4. worker group 可用性检查与 Launch 之间的 TOCTOU 窗口
5. Postgres 版 CreateDagRunBounded 计数子查询与 sqlite 语义差异
6. subdag 占位任务是否应占用 pool/全局/每 DAG 任务预算（当前不占用——设计如此，需确认无越额）
7. harvest 与 admission 之间 MarkTask 竞态（admit 前重读 TI 是否覆盖全部字段）
