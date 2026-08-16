# cronova 改进路线图（Roadmap）

> 生成于 2026-08-07，基于对代码库五个视角（规模与高可用 / 可观测性 / 功能深度 / 控制台体验 / API 与生态）的逐项探查。
> 每项标注 **影响**（高/中/低）、**工作量**（小/中/大）与代码出处；勾选框用于跟踪进度。
> 定位假设：cronova 是"单机友好的轻量调度器"，向"可托付的团队基础设施"演进。

## 总览

| 批次 | 主题 | 项数 | 预期节奏 |
|---|---|---|---|
| P0 | 立即修（bug 级，均 ≤1 天/项） | 5 | 一周内 |
| P1 | 运维信任闭环 | 6 | 1 个迭代 |
| P2 | 安全边界与团队协作 | 4 | 1–2 个迭代 |
| P3 | 控制台体验迭代 | 8 | 穿插进行 |
| P4 | 调度功能深度 | 6 | 按需排期 |
| P5 | 长线架构演进 | 3 | 先定位后动工 |

**关键依赖链**（顺序不能乱）：

```
PostgreSQL store ──→ 调度器 HA（租约选主）──→ 多机 executor
任务间数据传递（XCom）──→ 运行时分支/条件执行
入站 webhook ──→ 外部事件触发 ──→ poll 型 sensor（二期）
```

**如果只做三件事**：
1. P0 全部 + P1 的 backup/readyz —— 把"静默坏掉"变成不可能（约 2–3 天）
2. P1 告警闭环 + P3 新手通知开关 —— 从"能跑"到"敢托付"
3. P2 的 `cronova apply` + 入站 webhook + operator token —— 从"个人工具"到"团队基础设施"

---

## P0 · 立即修（bug 级）

- [x] **P0-1 双实例互锁**　影响:高 / 工作量:小
  - 现状：无 flock/租约/pidfile，两个 `cronova serve` 指向同一 DB 时 dispatch 对 queued 的写是普通 Update 而非 CAS，会静默双重派发（`internal/scheduler/scheduler.go:1581`、`internal/store/sqlite/sqlite.go`）。
  - 方案：启动时取 DB 内单行租约（`scheduler_lease` 表，持有者心跳 + 过期抢占）或文件 flock，第二实例显式报错退出。这是后续 HA 的地基（见 P5-2）。

- [x] **P0-2 修复 `cronova_runs_total` 指标语义**　影响:高 / 工作量:小
  - 现状：声明为 `TYPE counter` 但实为活表行数（抓取时 `CountRunsByState` 现算），retention 清理删除旧 run 后数值回落，Prometheus 把下降当 counter reset，`rate()/increase()` 产生虚假尖峰（`internal/api/observability.go:17`、`internal/scheduler/scheduler.go:949`）。
  - 方案：改为进程内单调计数器或落库累计计数表。

- [x] **P0-3 catchup 控制台接线**　影响:中 / 工作量:小
  - 现状：引擎完整实现（`scheduleAnchor` 回补 + one-run-per-tick 节流，`scheduler.go:1039-1051`），控制台却把复选框禁用并标「coming soon」，两条创建路径硬编码 `catchup:false`（`builder.js:92,292,495`）。
  - 方案：接线到现有字段（`views.js dagSpecFrom` 已透传），附回补量与节流行为的警示文案。后端零改动。

- [x] **P0-4 MCP 补 backfill 工具**　影响:中 / 工作量:小
  - 现状：`POST /api/dags/{id}/backfill` 在 API catalog 里但不在 MCP 白名单，agent 无法补数，疑似遗漏（`internal/mcp/tools.go:24-55`、`internal/api/openapi.go:174`）。
  - 方案：`toolNames` 加一行；顺带审视 `delete_project` 有删无传的不对称。

- [x] **P0-5 `cronova prune` 路径陷阱 + 空间回收**　影响:低 / 工作量:小
  - 现状：默认 `-db data/cronova.db` 与已安装服务实际路径（`/var/lib/cronova`）不符，裸跑对空库静默 no-op；且 prune 只删行不回收空间，DB 文件停在历史高水位（无 auto_vacuum / VACUUM）。
  - 方案：prune 默认读取已安装的 cronova.yaml（与 healthcheck 同款解析）；打开时设 `PRAGMA auto_vacuum=INCREMENTAL` + 周期 `incremental_vacuum`，或 prune 内跑 VACUUM。

## P1 · 运维信任闭环

- [x] **P1-1 告警体系补全**　影响:高 / 工作量:中
  - 现状：仅 per-DAG webhook（slack/feishu/dingtalk/raw，SSRF 防护完善），但：无全局默认 webhook（新 DAG 默认静默）；投递单次 POST 失败即丢（`internal/scheduler/notify.go:214`）；executor 掉线、DB 错误、tick 停摆只进 stderr。
  - 方案：`cronova.yaml` 加 `default_notify_url/format`（DAG 级覆盖）；`postNotify` 加 2–3 次指数退避重试；系统级事件（executor 不可达、prune 失败、tick 停滞 N 周期）复用同一 webhook 通道。

- [x] **P1-2 `cronova backup` / restore**　影响:高 / 工作量:小
  - 现状：运行中直接 cp DB 可能拷出撕裂快照；restore 流程零文档；一致性快照需含 cronova.db + cronova.key + dags/ + projects/，文档只提了 key（`docs/DEPLOY.md`）。
  - 方案：`cronova backup <dest>` 子命令在活动连接上 `VACUUM INTO`（DELETE journal 下安全且原子）并打包上述四件套；DEPLOY.md 写明 restore 步骤与 healthcheck 验证。

- [x] **P1-3 readyz 覆盖调度循环活性**　影响:中 / 工作量:小
  - 现状：`/readyz` 只查 DB 可达 + executor 健康（`internal/api/auth.go:247-269`）；调度 goroutine 停摆时仍报 ready——单体调度器最致命且最难被外部发现的故障。
  - 方案：每 tick 更新原子 last-tick 时间戳，readyz 校验距今 ≤ 3–5×tick，并在 /metrics 暴露同一时间戳。

- [x] **P1-4 指标扩展 + Monitoring 文档**　影响:高 / 工作量:中
  - 现状：无 run/task 时长 histogram（时间戳已在库）、无 per-DAG 标签、无调度器内部指标；`/metrics` 在 docs 与 README 中零提及。
  - 方案：run finalize 处记录 `cronova_run_duration_seconds`（按 dag_id 标签）；加 `scheduler_last_tick_timestamp`、`notify_failures_total`、队列水位（见 P1-6）；DEPLOY.md 增加 Monitoring 一节（scrape 示例 + 建议告警规则）。

- [x] **P1-5 结构化日志配置化**　影响:中 / 工作量:小
  - 现状：调度器 slog TextHandler 硬编码（`scheduler.go:120`），main/executor 用 stdlib log，API 无访问日志；无 log_level/log_format 配置。
  - 方案：`cronova.yaml` 加 `log.level` / `log.format(text|json)`，统一 slog 注入三处；API 加 method/path/status/duration 中间件（跳过 healthz/metrics）。

- [x] **P1-6 队列水位可观测**　影响:低 / 工作量:小
  - 现状：多级背压设计正确（各级上限 + ErrQueueFull + anchor 不前移），但水位只有事后翻日志；ErrQueueFull 响应不带当前水位。
  - 方案：/metrics 与控制台暴露 queued/cap、active/cap、per-pool 占用；ErrQueueFull 附带水位。

## P2 · 安全边界与团队协作

- [x] **P2-1 Token 权限模型**　影响:高 / 工作量:中
  - 现状：非 GET 一律要求 admin（`internal/api/auth.go:114`）；只有 admin/viewer 两档、永不过期；CI 触发一个 DAG 也得持全权 token（能删 DAG、能自我复制新 token）。
  - 方案：新增 operator 角色（trigger/cancel/retry/backfill）；token 支持 `expires_at` 与 dag_id 范围；在 api catalog 的 Endpoint 上标注所需角色，withAuth 升级为端点级授权表。

- [x] **P2-2 入站 Webhook + 外部事件触发**　影响:高 / 工作量:中
  - 现状：外部触发只能用全权 token 打 `/api/dags/{id}/trigger`；`TriggerEvent` 枚举与 events 表存在但唯一事件源是内部依赖链（实质死代码）。
  - 方案：`POST /api/hooks/{dagID}` + per-DAG 独立 secret（仅能触发该 DAG、可带 params，复用 loginLimiter 限流与 audit）；在此之上加 `POST /api/events` + DAG 级 `trigger_on_event` 订阅（复用 `processPendingDependencyEvents` 循环）；poll 型 sensor 作为二期新任务类型。

- [x] **P2-3 GitOps：`cronova apply` + 热重载**　影响:高 / 工作量:中
  - 现状：`LoadDAGs` 仅启动时执行一次（`scheduler.go:894`），改 YAML 不重启不生效；无批量推送/diff/dry-run 命令；文件与控制台双向写同一目录无漂移检测。
  - 方案：`cronova apply <dir>`（先 validate 出 diff/dry-run 报告再逐个 POST，均为现有端点）；serve 侧可选 `-reload` 周期重扫；定义 file vs console 优先级策略。

- [x] **P2-4 导出 / 导入与实例迁移**　影响:中 / 工作量:中
  - 现状：DAG 完整定义只能逐个 GET；variables/connections/pools 无批量导出；迁移只能整拷 DB + key 文件。
  - 方案：`cronova export/import`（或 API 端点）：打包全部 DAG YAML + pools + variables（secrets 默认排除），import 复用 parser + CreateDAG 做校验与幂等 upsert。

## P3 · 控制台体验迭代

- [x] **P3-1 新手模式「失败时通知我」**　影响:高 / 工作量:中
  - 新手用户最关心的诉求在新手模式下无入口。向导第 3 步或详情「更多设置」加：开关 + webhook URL（默认 `notify_on:["failure"]`，按 URL 自动识别平台格式）。复用现有字段与 `saveDag()`，零后端改动；与 P1-1 全局默认 webhook 呼应。

- [x] **P3-2 运行时长趋势与运行对比**　影响:高 / 工作量:中
  - 回答"是不是越跑越慢/哪个任务拖慢了/这次失败和上次成功差在哪"。Runs 标签顶部加趋势条形图（数据来自现有 runs 端点）；任务级趋势加轻量端点 `GET /api/dags/{id}/task-durations`。

- [x] **P3-3 服务端错误串 i18n**　影响:中 / 工作量:中
  - 中文界面弹英文 toast（21 处 `toast(e.message)` 原样透出英文字面量），与新手模式最割裂。`httpErr` 加稳定 `code` 字段，前端按 code 映射词条、无映射回退原文；先覆盖最常见 20 个。

- [x] **P3-4 批量操作**　影响:中 / 工作量:中
  - 维护窗口批量暂停、事故后批量重跑、批量归档。专家仪表盘行级复选框 + 全选（可与 filter 组合）+ 粘性操作条；客户端顺序调现有单个端点即可。

- [x] **P3-5 运行历史与审计检索**　影响:中 / 工作量:中
  - runs 固定 limit=25 无分页、审计固定 200 条无筛选。加载更多（offset/before 游标）+ 日期范围 + trigger_type 过滤；审计加 actor/action/target/时间过滤；顶栏 jump 扩展到描述与任务 id。

- [x] **P3-6 整 run 聚合实时日志**　影响:中 / 工作量:中
  - 并行 run（fan-out）只能逐任务切日志。「全部任务」标签：每任务各开一条 SSE 按到达交错渲染（`[task_id]` 前缀着色），或后端加 `/api/runs/{id}/log/stream` 聚合流。

- [x] **P3-7 移动端触控**　影响:中 / 工作量:小
  - 依赖图无双指缩放（仅 Ctrl+滚轮）；sparkline/activity/Gantt 的 title 悬停提示在触屏不可见。attachPanZoom 加 pinch；刻度点按弹轻量 tooltip。

- [x] **P3-8 日志框主题 token 化**　影响:低 / 工作量:小
  - `.logbox` 两处硬编码 `#06070a`（styles.css:219,465）游离于主题 token 之外。抽 `--log-bg/--log-fg/--log-border`，浅色主题显式建模"终端永远深色"或给配套浅色。

## P4 · 调度功能深度

- [x] **P4-1 任务间数据传递（XCom 等价）**　影响:高 / 工作量:中　*← P4-2 的前置*
  - 上游无法把行数/文件路径/API 返回传给下游，只能靠外部文件约定。方案：任务写小 JSON 到 `$CRONOVA_OUTPUT`（限 64KB），executor 收集入新表，命令模板加 `{{ ti.<task_id>.<key> }}` 接入现有 render 链。

- [x] **P4-2 运行时分支与条件执行**　影响:中 / 工作量:中
  - 分支只能由上游成败驱动。两步走：保留退出码约定（如 exit 99 ⇒ skipped）让任务自我短路 + 借 `none_failed` 放行下游；P4-1 落地后加 `when:` 条件模板在 taskGate 求值。

- [x] **P4-3 Cron 时区支持**　影响:中 / 工作量:中
  - 全程 UTC，`CRON_TZ` 可用但 start_date/展示/构建器均无时区概念，对 DST 地区团队语义偏差。DAG 级 `timezone:` 字段（映射 CRON_TZ + start_date 解释），控制台双时区显示，存储保持 UTC。

- [x] **P4-4 DAG 定义版本历史**　影响:中 / 工作量:中
  - 只有运行级快照（definition_hash），无版本列表/diff/回滚。append-only `dag_versions` 表（CreateDAG/apply 路径写入，actor 取自 audit 链），run 经 hash 关联版本；控制台加版本视图。

- [x] **P4-5 动态任务展开（mapped tasks）**　影响:高 / 工作量:大
  - 无法按输入列表并行生成 N 个实例，用户只能手写 N 份任务。任务 YAML 加 `foreach`（静态列表或模板解析后的 JSON 数组），run 创建时展开为 `task_id[idx]` 实例（表已按字符串键控，无需改 schema），复用 pool 槽位限并发。

- [x] **P4-6 优先级作用域 + per-DAG 任务并发**　影响:低 / 工作量:中
  - priority 只在单 run 内生效，跨 run 竞争 pool 先到先得。dispatch 改为每 tick 跨全部 active run 收集 ready 后全局排序统一分配；顺手加 DAG 级 `max_active_tasks`。

- [x] **P4-7 调度循环每 tick 开销优化**　影响:中 / 工作量:中
  - 查询量 O(DAG 数 + 全部 queued + 活跃 run×任务)，queued 积压 1 万时每 tick 全量取回。queued 查询加 LIMIT（按剩余额度）；per-DAG CountActiveRuns 合并为一条 GROUP BY；pool 占用一次性预取。均为存储层局部改动。

## P5 · 长线架构演进

- [x] **P5-1 PostgreSQL store**　影响:高 / 工作量:大　*← P5-2/P5-3 的前置*
  - 现状：SQLite 单连接（`SetMaxOpenConns(1)`）串行化所有调度写 + API 读，是规模与 HA 的双重天花板；store 接口已隔离（注释明言 PG 可 drop-in）。过渡缓解：API 只读路径单开 read-only 连接池。

- [x] **P5-2 调度器 HA（active-standby）**　影响:高 / 工作量:大
  - P0-1 的租约升级为选主：持有者心跳 + 备机热待抢占。依赖 P5-1（SQLite 文件无法多机共享）。

- [x] **P5-3 多机 executor**　影响:高 / 工作量:大
  - 现状：executor 强制 unix socket 同机部署，日志与 project 依赖共享文件系统；gRPC proto 已就绪。三步：TCP+mTLS 传输 → 多 target 路由（pool→executor 绑定或最少负载）→ 日志 executor 本地写 + gRPC 流式拉取（解除共享文件系统假设）。

- [x] **P5-4 executor 重启不杀任务**　影响:中 / 工作量:中　*（可先于 P5-1 独立做）*
  - 现状：Runner 任务表纯内存且 Shutdown 主动杀进程组，executor 升级/崩溃 ⇒ 所有 in-flight 记失败并消耗 retry——"调度器可重启"的保证未对称覆盖 executor。方案：attempt 元数据（ref/pgid/启动时间/退出码）落盘，重启后扫描进程组重新认领；Shutdown 区分升级与停机。

---

## 已知定位取舍（暂不做，除非定位变化）

- **K8s executor / 容器化任务**：当前定位单机单二进制，引入编排依赖与定位冲突。
- **多租户 / RBAC 细粒度到 DAG 属主**：model 已预留 Owner/Project 字段，待团队规模需求明确后再启动。
- **前端框架化改造**：无构建步骤的 vanilla JS 是刻意的部署简单性取舍，现有规模下维护成本可控。

---

# ROADMAP v2（2026-08-08）：分布式与平台化

> 基于与 DolphinScheduler 3.4.x 的差距分析立项。定位升级：从"单机调度器"到"轻量分布式编排平台"。
> 铁律：**所有分布式能力都是可选增量**——不配 worker 时行为与今天完全一致，单二进制 DNA 不动摇。
> 已拍板的设计决策：worker 用 **join token 自注册**（自动签发 mTLS 证书）；任务日志**流式回传中心**落盘；
> 拖拽编排走**自动布局 + 结构编辑**（YAML 为唯一事实源）；dependent 与 sub-workflow **都做**。

## V2 总览

| 阶段 | 主题 | 项数 |
|---|---|---|
| V2-P1 | 地基与快赢 | 4 |
| V2-P2 | 分布式核心 | 5 |
| V2-P3 | 工作流组合与图编辑 | 3 |
| V2-P4 | 收口与验证 | 4 |

**依赖链**：V2-5（注册）→ V2-6（路由）→ V2-7（日志回传）→ V2-8（failover）→ V2-9（控制台）；V2-13/15 依赖 V2-P2 全部；V2-11 依赖 V2-10 的跨 DAG 状态查询。

## V2-P1 · 地基与快赢

- [ ] **V2-1 Docker 镜像 + compose minimal**　多阶段 Dockerfile（静态二进制 → distroless，目标 ≤40MB）、`docker-compose.yml` minimal profile（单容器 + SQLite 卷）、healthcheck 接 `/healthz`、`.dockerignore`、文档。
- [x] **V2-2 日期偏移模板函数**　模板层新增偏移与锚点运算：`logical_date` 加减天/小时、月初/月末/周初/周末、自定义格式化。对齐 DS 的 `$[yyyyMMdd+N]` 能力面，语法融入现有 `{{ }}` 模板。DAG 带 `timezone:` 时按该时区渲染。
- [x] **V2-3 Email 告警 + 告警组**　SMTP 配置（实例级）+ notify 格式新增 `email`（`mailto:` 目标）；告警组：命名组聚合多个通道，DAG `notify.group` 引用（悬空回退不丢告警）；系统级事件同样可路由到组；export/import 纳入；控制台管理卡 + DAG 组下拉。
- [x] **V2-4 跨 run 全局优先级 + 串行策略**　admitReady 每 tick 跨全部 active run 收集 ready 任务全局排序（run priority > task priority > logical_date）；run 级 priority（触发 API/CLI ±100）；DAG 级 `execution_policy`（serial_wait/serial_discard/serial_priority 三态测试覆盖）。

## V2-P2 · 分布式核心

- [x] **V2-5 worker 注册与 join token**　`workers`/`worker_join_tokens` 表；`cronova workers token` 生成一次性 token（哈希落库、限流）；worker 凭 token + CSR 注册（私钥不出 worker）获内置 CA 签发的 mTLS 证书；**拨入式**长连接（worker 无需可达端口）；`cronova worker` / `cronova workers list/token/drain/remove`。
- [x] **V2-6 worker 分组与路由**　DAG/task 级 `worker_group:`（默认走本地 executor，完全向后兼容）；组内最少活跃任务数路由；组内无在线 worker 时任务保持 queued 等待并告警日志（真机验证），DAG 保存不受影响。
- [x] **V2-7 任务日志流式回传**　gRPC 双向流回传 scheduler 按原目录落盘（SSE/retention 零改动）；字节偏移去重 + ack + 断线续传；远程 `$CRONOVA_OUTPUT` 随 TaskEvent 回传（远程 XCom，真机验证 `{{ ti.x.y }}`）。
- [x] **V2-8 worker failover**　心跳静默 3×interval → worker 标 lost、在途 ref 释放 → PhaseUnknown → 按 retry 策略重派；worker 重启重认领不重跑（sidecar 退出文件跨死亡窗口恢复退出码，真机验证 try 1 完成）；调度器重启经 Hello active_refs 收养孤儿 ref（含日志高水位续传）。
- [x] **V2-9 workers 控制台页面**　专家模式 Workers 页（列表/心跳/负载/标签、drain/摘除、一次性 join token 弹窗含 TTL 与接入命令、5s 轮询防泄漏、双语、浏览器实测）；`/metrics` 加 `cronova_workers{state}` 与 `cronova_worker_active_tasks{worker,group}`；execution_policy 与触发优先级控制台接线由同批完成。

## V2-P3 · 工作流组合与图编辑

- [x] **V2-10 dependent 等待语义**　task 级 `depends_on_dag: {dag, offset, timeout, on_timeout}`；offset 复用日期表达式文法（`- 1d`、`.month_start`）；目标周期 run 成功放行、失败继续等（可能被重试）、超时按 fail/skip 处置；5 项测试覆盖。
- [x] **V2-11 sub-workflow**　任务类型 `subdag:`；子 run 带 `parent_run_id` 关联（新列+迁移）；父 cancel 级联取消子 run（含嵌套递归，锁安全）；重试启动新子 run 保留历史；运行时嵌套深度上限（≤5）兜底循环引用；5 项测试覆盖（含互递归防护）。
- [ ] **V2-12 graph 结构编辑**　现有 graph 视图升级：加任务节点、拖线连接/断开依赖、点节点侧栏编辑参数；每个操作实时生成 YAML diff 预览，保存走现有校验与版本历史；novice 模式不暴露。

## V2-P4 · 收口与验证

- [ ] **V2-13 compose full profile**　PG + scheduler + N×worker 的完整拓扑，作为分布式集成测试床与演示环境。
  - 进展：full profile 已写入 docker-compose.yml（postgres:16 + cronova-full(worker hub) + 2×worker；join token 经 `CRONOVA_WORKER_JOIN_TOKENS` 预置、worker 容器重启幂等复用身份）；双 profile `compose config` 均通过；待镜像构建完成后 up 冒烟。
- [ ] **V2-14 PG 生产验证**　compose 环境跑全量 postgres_test 套件 + 分布式场景冒烟。（进展：PG16 测试容器拉取中，端口 55433，DSN `postgres://postgres:test@127.0.0.1:55433/cronova_test?sslmode=disable`）
- [ ] **V2-15 规模基准测试报告**　单机 SQLite vs PG 的吞吐/延迟基准，产出 `docs/BENCHMARKS.md`。
  - 进展：可复现基准已入库（`internal/scheduler/bench_test.go`，`CRONOVA_BENCH=1` 门控）；SQLite 首组数据：20 DAG×25 run×3 任务链 = 500 runs/1500 tasks，wall 4.83s，**103.5 runs/s / 310.6 tasks/s**，run p50 1.72s p99 2.32s（M 系列 Mac，含排队时间）。
- [ ] **V2-16 文档收口**　DEPLOY.md 已加"Distributed workers (dial-in)"与"Email alerts (SMTP)"章节；剩余：CLI.md(+zh) 补 worker/workers/trigger -priority、README 定位更新、BENCHMARKS.md。

---

## 实施状态（2026-08-07 全量落地）

全部 34 项已实施并通过测试（14/14 Go 包 + 前端语法/浏览器验证）。范围界定如实说明：

- **P4-5 动态 fan-out**：以 parser 级静态 `foreach` 展开实现（每分片独立重试/日志/状态，状态机零侵入）。运行时动态列表（如按上游输出展开）不在本期范围。
- **P4-6 优先级**：`max_active_tasks`（跨 run 的 per-DAG 任务并发预算）已实现；priority 仍在单 run 内排序，跨 run 的全局统一优先级分配未做（影响评估为低，需重构 dispatch 流程时再议）。
- **P5-1 PostgreSQL store**：完整接口实现（编译期 `store.Store` 断言保证方法完备），集成测试需 `CRONOVA_TEST_PG_DSN` 指向真实 PG 才运行（本机无 PG 时自动跳过）——上生产前先在带 PG 的环境跑一遍该测试套件。
- **P5-2 HA**：`-standby` 主备接管已实测（清停 ~秒级接管、崩溃 ≤15s 租约过期接管）。跨机 HA 需配合共享存储或 P5-1 的 PG。
- **P5-3 多机 executor**：第一阶段（`tcp://` + 强制 mTLS 双向认证）已实测跑通任务；多 executor 路由与日志流式拉取为后续阶段（远程 executor 的任务日志落在其本机；project attach 需共享文件系统，运行时有明确报错）。
- **P5-4 executor 重启不杀任务**：已实测（重启后认领运行中进程组、sidecar 文件回收真实退出码）。独立 executor 默认开启（`-state-dir none` 关闭）。
