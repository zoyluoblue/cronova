"use strict";

const $ = (id) => document.getElementById(id);
const main = $("main");

let view = "dags";       // dags | dag | task | run | pools | graph | workers | …
let activeDag = null;
let currentRun = null;
let filter = "all";
let query = "";
let overviewCache = null;
let authUser = null; // {username, role, auth} when signed in; null before auth resolves
let logES = null;
// language: a ?lang=en|zh query param (deep-linkable / shareable) wins, then the
// saved preference, then Chinese.
const _urlLang = new URLSearchParams(location.search).get("lang");
let lang = (_urlLang === "en" || _urlLang === "zh") ? _urlLang : (localStorage.getItem("cnv_lang") || "zh");
let theme = localStorage.getItem("cnv_theme") || "dark";
// uiMode: "novice" | "expert". A saved preference wins; otherwise resolveMode()
// infers a default from the instance (empty => novice onboarding, has DAGs =>
// expert, so existing users never get their console flipped under them). Only an
// explicit toggle click persists — the inferred default stays adaptive.
let uiMode = null;
const nvMode = () => uiMode === "novice";
const DOCS_URL = "https://zoyluoblue.github.io/cronova/";

// D: in-memory editable spec for the active DAG operation page (immediate-save).
let D = null;
// ND: transient state for the minimal new-DAG modal.
let ND = null;
// SCHED: binding for the shared schedule UI {state, idp, host, onChange}.
let SCHED = null;
// coachDag: dag_id just created from a starter template -> show a one-time
// "template ready, hit ▶" ribbon on its operation page (session-only).
let coachDag = null;

// ---- i18n ----
const DICT = {
  zh: {
    workspace: "工作区", nav_dags: "工作流", newdag: "+ 新建工作流",
    f_all: "全部", f_running: "运行中", f_failed: "失败", f_paused: "已暂停",
    dags_sub: "点击名称查看运行记录和结构；用左侧开关暂停 / 恢复自动调度。",
    ov_fail_title: (id) => `${id} 最近一次运行失败`, ov_fail_more: (n) => `另有 ${n} 个工作流也在失败`,
    ov_rest_ok: (n) => `其余 ${n} 个工作流一切正常`, ov_all_ok: "所有工作流运行正常", ov_last_run: (w) => `最近一次运行 ${w}`,
    ov_running: "运行中", ov_running_tip: "正在执行的运行数", ov_rate: "近期成功率", ov_go: "去处理 →",
    hlp_dag: '<b>工作流（DAG）</b>= 一组按依赖顺序自动执行的任务，加上「什么时候跑」的调度规则。<span class="eg">例如：每天 2 点，先取数 → 再加工 → 最后写入。</span>',
    hlp_toggle: "<b>调度开关。</b>关掉后不再自动运行（手动触发仍可用），历史全部保留，随时可恢复。",
    hlp_spark: '每一格是一次运行：<b class="ok">绿</b>=成功、<b class="bad">红</b>=失败、<b class="run">蓝</b>=进行中，越高耗时越长。悬停看详情。',
    hlp_next: "按调度规则算出的下一次自动运行时间。「就绪」= 正在等一个空闲槽位。",
    hlp_rate: "最近运行中成功的比例（每个工作流最多统计 14 次，跳过和取消不计入）。",
    toggle_tip_on: "已启用 —— 点击暂停自动调度", toggle_tip_off: "已暂停 —— 点击恢复自动调度",
    btn_run_word: "运行", run_now_tip: "立即手动运行一次，不影响原有调度",
    day_today: "今天", day_yesterday: "昨天",
    h_dag: "工作流", h_spark: "最近 14 次", h_pool: "POOL", h_next: "下次运行",
    no_match: "没有匹配的工作流", no_match_filter: "这个筛选下没有工作流", no_dags_title: "还没有工作流", no_dags_sub: "创建第一个工作流，开始调度任务。", trigger: "触发", manual_trigger: "手动触发",
    back_dags: "← 工作流", run_word: "run", sub_manual: "仅手动触发", max_active: "最大并发",
    run_progress: "进度",
    sec_graph: "依赖图", sec_structure: "结构", sec_runs: "运行历史", sec_instances: "任务实例",
    g_timeline: "时间线", g_never_ran: "未运行", run_no_tasks: "该运行暂无任务实例", run_done_ok: "运行成功完成", run_done_fail: "运行失败", run_done_timeout: "运行超时",
    run_cancel: "取消运行", run_retry: "重跑失败", task_retry: "重跑", run_cancelled_toast: "运行已取消", run_retried_toast: "已重新排队",
    task_mark: "标记状态", run_mark: "标记运行", mark_skip: "跳过", mark_done_toast: "已标记",
    mark_task_title: (id) => `标记任务“${id}”为?`, mark_task_body: "手动覆盖任务状态。运行中的任务会先被终止;标记成功/跳过会放行被它阻塞的下游。",
    mark_run_title: (id) => `标记运行“${id}”为?`, mark_run_body: "覆盖已结束运行的最终状态(不改动任务)。标记成功会触发下游工作流。",
    confirm_cancel_title: (id) => `取消运行“${id}”?`, confirm_cancel_body: "正在运行的任务会被终止。", th_act: "操作",
    confirm_retry_title: (id) => `重跑“${id}”?`, confirm_retry_body: "该任务及其所有下游任务会被重置并重新运行。",
    copied: "已复制", copy_fail: "复制失败，请手动选择文本", copy_hint: "点击复制", search_ph: "搜索工作流…", jump_open: "打开", jump_none: "无匹配工作流",
    gz_in: "放大", gz_out: "缩小", gz_fit: "适应视图", gz_hint: "拖拽平移 · Ctrl/⌘+滚轮缩放",
    login_title: "登录 cronova", login_sub: "请输入你的账户凭据", login_user: "用户名", login_pass: "密码", login_btn: "登录", login_bad: "用户名或密码错误", logout: "登出", sess_expired: "会话已过期，请重新登录", role_admin: "管理员", role_viewer: "只读",
    tab_runs: "运行", tab_structure: "结构", tab_settings: "设置",
    dh_last: "上次运行", dh_next: "调度", dh_rate: "近期成功率", dh_never: "还没有运行", dh_norate: "—",
    set_done: "完成", set_edit: "编辑", set_none: "无", set_sched: "调度", set_max: "最大并发", set_retries: "默认重试", set_deps: "上游依赖",
    set_deps_hint: "上游工作流成功后自动触发本工作流", set_no_deps_avail: "暂无其他工作流可选",
    set_notify: "通知", set_notify_hint: "运行结束后向 Webhook 发送 JSON（兼容 Slack/飞书/Discord），或引用告警组一次通知多个渠道", notify_failure: "失败", notify_success: "成功", notify_off: "未选择事件", notify_need_url: "先填写 Webhook URL 或选择告警组，再选择触发事件", err_notify_url: "通知 URL 必须以 http://、https:// 或 mailto: 开头",
    nf_label: "消息格式", nf_hint: "raw = 完整 JSON 载荷;其余按平台的入群机器人格式包装摘要文本;email 供 mailto: 地址使用", nf_feishu: "飞书", nf_dingtalk: "钉钉", nf_email: "邮件",
    set_group: "告警组", ag_group_none: "不使用告警组", ag_opt_missing: (n) => `${n}（不存在）`,
    ag_url_overridden: "已选择告警组，单独 URL 将被忽略",
    notify_mailto_hint: "URL 也支持 mailto:地址1,地址2（需在服务端配置 smtp: 后才能发信）",
    btn_backfill: "回填", bf_hint: "为区间内每个调度周期补建一次运行(含首尾;已存在的周期自动跳过,执行受最大并发限制)", bf_from: "开始日期", bf_to: "结束日期", bf_go: "开始回填", bf_need_dates: "请选择起止日期",
    bf_done: (c, s) => `回填已入队:新建 ${c} 个运行,跳过 ${s} 个已存在`,
    rf_all: "全部", rf_running: "进行中", rf_failed: "失败", rf_success: "成功",
    t_backoff: "重试退避", bo_fixed: "固定间隔", bo_exponential: "指数退避", t_backoff_hint: "指数退避:第 n 次重试等待 重试间隔×2ⁿ⁻¹",
    t_backoffmax: "退避上限(秒)", t_backoffmax_hint: "指数退避的最长等待;0 = 不封顶",
    set_sla: "SLA（软）", set_sla_hint: "从 run 开始算，超时未完成即告警（继续运行）。0=关闭。需配置通知 Webhook。", set_timeout: "运行超时（硬）", set_timeout_hint: "从 run 开始算，超时则强制失败并杀掉运行中任务 → timed_out。0=关闭。", secs: "秒", set_off: "关闭",
    t_sla: "任务 SLA（秒）", t_sla_hint: "从 run 开始算，此任务超时未完成即告警。0=关闭。", t_timeout_hint: "单次执行超时即杀（秒）。0=不限。",
    danger_title: "危险操作", danger_del_hint: "归档此工作流：不再调度，历史保留。",
    nd_more: "调度与更多选项", nd_less: "收起",
    nav_resources: "变量 & 连接", nav_audit: "审计", nav_api: "API",
    audit_sub: "运维操作记录:谁在何时对哪个工作流/运行做了什么。", audit_empty: "暂无操作记录", au_time: "时间", au_actor: "操作人", au_action: "操作", au_target: "对象",
    act_trigger: "触发", act_cancel: "取消", act_retry_run: "重跑运行", act_retry_task: "重跑任务", act_mark_task: "标记任务", act_mark_run: "标记运行", act_update_dag: "保存工作流", act_create_dag: "创建工作流", act_delete_dag: "归档工作流", act_pause: "暂停", act_unpause: "恢复", act_create_token: "创建 Token", act_delete_token: "撤销 Token", act_set_alert_group: "保存告警组", act_delete_alert_group: "删除告警组",
    api_title: "API 与集成", api_sub: "把 cronova 的全部能力对接到你的平台。查看交互式 API 文档,并管理机器访问用的 API Token。",
    api_docs_h: "API 文档", api_docs_hint: "完整的 OpenAPI 参考,内置 curl / Go / Python / Java 示例,可在页面内切换语言。", api_open_docs: "打开 API 文档 →", api_spec_link: "OpenAPI 规范",
    tok_title: "API Tokens", tok_sub: "机器访问凭据。以 Authorization: Bearer <token> 调用 API。明文仅在创建时显示一次。",
    tok_name: "名称", tok_role: "角色", tok_prefix: "前缀", tok_created: "创建时间", tok_lastused: "最近使用", tok_never: "从未使用",
    tok_create: "创建 Token", tok_none: "还没有 Token", tok_name_ph: "如 ci-bot", tok_revoke: "撤销", tok_need_name: "请填写名称",
    tok_revoke_title: (n) => `撤销 Token“${n}”?`, tok_revoke_body: "撤销后使用该 Token 的调用会立即失败,且不可恢复。",
    tok_created_ok: "Token 已创建", tok_revoked: "Token 已撤销",
    tok_reveal_h: "你的新 API Token", tok_reveal_warn: "请立即复制并妥善保存 —— 关闭后将无法再次查看明文。", tok_copy: "复制", tok_done: "我已保存",
    role_admin_full: "管理员(读写)", role_operator: "运维(触发/取消/重跑)", role_viewer_ro: "只读(仅 GET)",
    res_vars: "变量", res_conns: "连接", res_groups: "告警组",
    res_sub: "跨任务共享的配置。命令里用 {{ var.KEY }} / {{ conn.ID.字段 }} 引用，触发时用 {{ params.KEY }}。",
    ag_hint: "把多个通知渠道打包成一个具名分组，工作流通过 notify.group 引用；值班渠道调整时只需改这里。",
    ag_name: "组名", ag_channels: "渠道", ag_updated: "更新时间", ag_channel_n: (n) => `${n} 个渠道`,
    ag_none: "还没有告警组", ag_add: "新建告警组", ag_edit: "编辑告警组",
    ag_add_channel: "+ 添加渠道", ag_remove_channel: "移除渠道", ag_fmt: "格式",
    ag_url_ph: "https://hooks.slack.com/… 或 mailto:oncall@example.com",
    ag_max: (n) => `最多 ${n} 个渠道`,
    ag_err_channels: "告警组需要 1-16 个通知渠道",
    ag_err_url: "渠道 URL 必须以 http://、https:// 或 mailto: 开头",
    ag_mailto_hint: "mailto: 渠道通过服务端 SMTP 发信，需先在配置文件的 smtp: 段配置邮件服务器",
    ag_del_title: (n) => `删除告警组“${n}”？`,
    ag_del_body: "引用它的工作流将退回使用各自的通知 URL 或实例默认通知，不会丢失告警。",
    v_key: "变量名", v_value: "值", v_add: "添加变量", v_none: "还没有变量", v_save: "保存",
    c_id: "连接 ID", c_type: "类型", c_host: "主机", c_port: "端口", c_login: "用户名", c_password: "密码", c_extra: "额外(JSON)",
    c_add: "新建连接", c_edit: "编辑连接", c_none: "还没有连接", c_pw_set: "已设置", c_pw_none: "未设置", c_pw_keep: "留空则不修改",
    c_del_title: (id) => `删除连接“${id}”?`, v_del_title: (k) => `删除变量“${k}”?`, del_body: "此操作不可撤销。",
    trig_params: "带参数触发", p_params: "参数", p_add: "加一行", p_key: "键", p_val: "值", p_trigger: "触发", p_hint: "参数注入为 CRONOVA_PARAM_* 环境变量，命令里用 {{ params.键 }} 引用。",
    run_params: "参数", res_saved: "已保存", res_deleted: "已删除", err_key: "无效名称（仅限字母、数字、_ . -）",
    btn_trigger: "▶ 触发运行", btn_pause: "暂停", btn_resume: "恢复", btn_delete: "删除",
    confirm_del_dag_title: (id) => `归档工作流“${id}”？`,
    confirm_del_dag_body: "它将被归档(从列表隐藏),运行历史保留,之后可恢复。",
    dag_archived: "该工作流已归档(已删除)。",
    confirm_word: "确定", cancel_word: "取消", aria_theme: "切换主题", aria_lang: "切换语言",
    toast_run_queued: "已触发，run 已排队", toast_pool_saved: "池已保存", toast_dag_deleted: "工作流已归档",
    th_id: "id", th_type: "类型", th_command: "命令", th_deps: "依赖",
    th_logical: "逻辑时间", th_state: "状态", th_trig: "触发", th_started: "开始", th_dur: "耗时",
    th_task: "任务", th_try: "尝试", th_logs: "日志",
    no_runs: "还没有运行记录 — 触发一次。",
    k_logical: "逻辑时间", k_trig: "触发", k_dur: "耗时", k_started: "开始",
    log_word: "日志", live: "实时",
    pools_sub: "全局并发槽位，跨所有工作流与 run 共享。", p_name: "名称", p_slots: "槽位", p_save: "保存",
    p_newname: "新池名称", p_create: "创建池", p_need: "需要名称和正整数槽位",
    trig_fail: "触发失败", api_err: "API 错误",
    // 服务端错误码 → 本地化文案（api() 按 code 映射；未知码回退原始英文）
    err_code_not_found: "对象不存在（可能已被删除）",
    err_code_no_tasks: "该工作流还没有任何步骤，先添加一个再运行",
    err_code_bad_mark_state: "不允许标记为这个状态",
    err_code_queue_full: "运行队列已满，请稍后再试",
    err_code_active_runs: "该工作流还有正在进行的运行，先取消或等它们结束",
    err_code_run_not_active: "这次运行已经结束，无法取消",
    err_code_nothing_to_retry: "这次运行没有失败的步骤，无需重跑",
    err_code_run_still_active: "这次运行还在进行中，结束后才能重跑",
    err_code_bad_group_name: "告警组名称不合法（仅限字母、数字、_ . -，不超过 128 字符）",
    err_code_dag_conflict: "该工作流已被其他人修改——请刷新页面合并你的改动，直接重试会再次冲突",
    err_code_group_channels: "告警组需要 1-16 个通知渠道",
    err_code_group_channel_url: "渠道 URL 必须以 http://、https:// 或 mailto: 开头",
    err_code_group_channel_format: "渠道格式不合法（raw / slack / feishu / dingtalk / email）",
    dt_hint: "运行时长趋势：越高越慢，颜色为结果；点击柱子打开该次运行",
    runs_more: "加载更多 ↓", audit_more: "加载更多 ↓", log_all: "全部任务",
    bulk_all: "全选（当前筛选）", bulk_pick: (id) => `选择 ${id}`,
    bulk_selected: (n) => `已选 ${n} 个工作流`, bulk_done: (ok, n) => `批量操作完成：${ok}/${n} 成功`,
    bulk_del_title: (n) => `归档选中的 ${n} 个工作流？`,
    au_f_actor: "按操作人筛选", au_f_action: "按操作筛选", au_f_all: "全部",
    nx_paused: "已暂停", nx_due: "就绪", nx_in: (m) => `${m} 分钟后`,
    b_dag_info: "DAG 信息",
    f_dag_id: "DAG ID", f_start: "开始日期",
    f_catchup: "补跑 catchup", f_maxactive: "最大并发", f_defretries: "默认重试",
    f_catchup_hint: "开启后，从 start_date 起每个错过的调度周期都会补建一次运行（每个 tick 最多补一个，受最大并发限制，不会形成风暴）",
    f_trigger_after: "上游依赖 (成功后触发)",
    b_addtask: "+ 添加任务", b_remove: "移除",
    t_id: "任务 ID", t_type: "类型", t_command: "命令", t_pool: "Pool", t_priority: "优先级",
    t_http: "HTTP 请求", http_method: "方法", http_url: "URL", http_headers: "请求头", http_headers_hint: "每行一个,格式 Key: Value,可用 {{ var. }} / {{ conn. }}", http_body: "请求体", http_status: "期望状态码", http_status_hint: "逗号分隔,如 200,201;留空=任意 2xx", err_httpurl: "HTTP 任务必须填 URL",
    t_python: "Python 代码", python_hint: "内联 Python,用 python3 -c 执行;CRONOVA_* 变量在环境里可读;可用 {{ var. }} 模板。退出码非 0=失败。",
    t_sql: "SQL 查询", sql_conn: "连接", sql_conn_hint: "已配置的连接 id(其类型定驱动:postgres/mysql/sqlite)。见「变量 & 连接」。", err_sqlconn: "SQL 任务必须选连接",
    t_retries: "重试 (空=默认)", t_retrydelay: "重试间隔(秒)", t_timeout: "超时(秒)", t_deps: "依赖",
    t_nodeps: "暂无其他任务",
    t_project: "工程", t_optional: "(可选)", proj_none: "无(不附加工程)", proj_upload: "上传 / 新建工程",
    proj_hint: "选中后,命令会在该工程的干净副本目录里运行(如 python3 main.py)。仅 shell 任务可用。",
    proj_name: "工程名", proj_name_bad: "工程名只能含字母/数字/. _ -", proj_mode_files: "上传文件 / 文件夹", proj_mode_inline: "写脚本",
    proj_drop: "把文件或文件夹拖到这里", proj_pick_files: "选择文件", proj_pick_folder: "选择文件夹", proj_ziphint: "拖入 .zip 会自动解压",
    proj_filename: "文件名", proj_content: "脚本内容", proj_do_upload: "上传", proj_selected: (n) => `已选 ${n} 个文件`,
    proj_uploaded: "工程已上传", proj_upload_fail: "上传失败", proj_need_name: "请先填写工程名", proj_need_files: "请先选择文件或写入脚本内容", proj_manage: "管理工程",
    err_dagid: "请填写合法 DAG ID（字母/数字/_-.）", err_taskid: "任务 ID 不合法（字母/数字/_-.）",
    err_dup: "任务 ID 重复", err_emptyid: "存在空任务 ID", err_emptycmd: "存在空命令", err_cycle: "依赖存在环",
    sched: "调度", sm_manual: "手动", sm_every: "固定间隔", sm_cron: "Cron 表达式",
    sched_manual_hint: "仅手动触发或被上游工作流触发", sched_every_pre: "每隔",
    unit_s: "秒", unit_m: "分钟", unit_h: "小时", disabled_note: "(暂不可用)",
    cp_min: "每分钟", cp_hour: "每小时", cp_day: "每天 0:00", cp_2am: "每天 2:00", cp_mon: "每周一 0:00",
    cron_help: "用法", ch_title: "Cron 写法", ch_format: "格式：分 时 日 月 周（5 段，空格分隔）",
    ch_fields: "字段", ch_ops: "符号", ch_examples: "常用示例（点击填入）", ch_shortcuts: "快捷写法",
    t_rule: "触发规则", tr_all_success: "全部成功", tr_all_done: "全部完成", tr_one_success: "任一成功", tr_one_failed: "任一失败", tr_all_failed: "全部失败", tr_none_failed: "无失败",
    trd_all_success: "全部上游成功才运行(默认)", trd_all_done: "全部上游完成即运行(无论成败)——适合清理/汇总", trd_one_success: "任一上游成功即运行", trd_one_failed: "任一上游失败即运行——适合告警", trd_all_failed: "全部上游都失败才运行", trd_none_failed: "没有上游失败(成功或跳过)时运行",
    pool_hint: "并发槽位,跨所有工作流共享;同名 pool 的任务竞争同一批槽位",
    cb_interp: "解释器", cb_runas: "运行方式", cb_target: "模块 / 脚本", cb_args: "参数", cb_jar: "Jar 路径", cb_mainclass: "主类", cb_client: "SQL 客户端", cb_query: "SQL 查询",
    cmdopt_module: "模块 (-m)", cmdopt_script: "脚本文件",
    cmd_will_run: "将执行:", cmd_edit_raw: "编辑原始命令", cmd_use_form: "用表单填写", cmd_cant_parse: "当前命令无法解析成表单,已保留原始编辑",
    var_insert: "点击或拖拽插入变量", var_editor_aria: "命令编辑器,可插入变量药丸",
    var_pill_aria: (n) => `变量 ${n}`, var_pill_remove: (n) => `移除变量 ${n}`,
    var_empty: "无", var_add_key: "自定义…", var_conn_field: "选字段", var_goto_settings: "去设置",
    vd_logical_date: "本次运行的逻辑日期(到天)", vd_logical_datetime: "逻辑日期时间(RFC3339)",
    vd_date_expr: "日期表达式：±N d/h/w/mo 偏移、.month_start/.month_end/.week_start/.week_end 锚点、| %Y%m%d 自定义格式，可组合",
    vd_run_id: "本次运行的唯一 ID", vd_dag_id: "所属 DAG 的 ID", vd_task_id: "当前任务 ID", vd_try_number: "第几次尝试(重试递增)",
    vd_var: "共享变量", vd_conn: "连接字段", vd_params: "手动触发参数",
    vg_builtin: "内置", vg_var: "变量", vg_conn: "连接", vg_params: "参数",
    graph_connect_hint: "拖动节点右侧圆点到另一任务即可连线；点击节点打开任务编辑；Shift+点击两个节点（或开启连线模式后依次点击）同样可连接/断开；点击连线可移除依赖",
    ge_addtask: "+ 添加任务", ge_connect: "连线模式",
    ge_connect_tip: "开启后：点上游任务、再点下游任务即可连接/断开依赖（支持键盘操作）",
    ge_new_id_title: "新任务 ID", ge_edge_remove: "移除依赖", ge_dup_dep: "依赖已存在",
    ge_edge_aria: (a, b) => `依赖 ${a} → ${b}，回车移除`,
    ge_node_aria: (id) => `任务 ${id}`,
    diff_unsaved: (n) => `${n} 处修改未保存`, diff_save: "保存", diff_discard: "放弃",
    diff_show: "查看差异", diff_hide: "收起差异", diff_loading: "正在生成差异预览…",
    diff_invalid: "当前修改未通过校验", diff_same: "与已保存版本一致", diff_discarded: "已放弃未保存的修改",
    t_subdag: "目标工作流", subdag_hint: "作为子流程运行另一个工作流：本任务跟随子运行的最终状态，取消会级联到子运行", subdag_none: "选择工作流…", err_subdag: "子流程任务必须选择目标工作流",
    dod_title: "跨工作流依赖", dod_hint: "等待另一个工作流对应周期的运行成功后，此任务才就绪",
    dod_dag: "依赖的工作流", dod_offset: "周期偏移", dod_offset_hint: "- 1d / .month_start / 留空=同周期",
    dod_timeout: "等待超时（秒）", dod_timeout_hint: "0 = 一直等到运行超时", dod_on_timeout: "超时后",
    dod_fail: "失败", dod_skip: "跳过",
    parent_run: "父运行", child_run: "子运行",
    nav_graph: "关系图", graph_title: "工作流关系图", graph_sub: "按 trigger_after 展示工作流之间的触发依赖",
    graph_none: "暂无跨工作流依赖（没有工作流配置 trigger_after）", graph_view_hint: "提示：箭头表示「触发后」方向；点击节点查看该工作流；虚线节点为未找到的工作流",
    ss_saved: "已保存", ss_saving: "保存中…", ss_invalid: "待修复后保存", ss_error: "保存失败",
    dag_no_tasks_title: "暂无任务", dag_no_tasks_sub: "添加一个任务以启用此工作流", dag_disabled_hint: "添加任务后可触发",
    nd_title: "新建工作流", nd_create: "创建", nd_cancel: "取消", nd_dagid_dup: "该 DAG ID 已存在",
    tpl_start: "从模板开始", tpl_tasks: "个任务",
    tpl_blank: "空白", tpl_blank_d: "从零开始,稍后自己加任务",
    tpl_etl: "每日 ETL", tpl_etl_d: "抽取 → 转换 → 加载 的三步流水线",
    tpl_report: "定时报表", tpl_report_d: "取数 → 生成报表,预设每天 08:00",
    tpl_fanout: "扇出-扇入", tpl_fanout_d: "start → 两个并行分支 → 汇合",
    coach_tpl_ready: "模板已就绪 — 点「触发运行」看它跑一遍,再按需修改任务",
    sp_every: (n, u) => `每 ${n} ${u}运行一次`, sp_next: "接下来", sp_invalid: "表达式无效,无法计算触发时间",
    tz_note: "调度按 UTC 计算;页面时间按你的本地时区显示",
    btn_duplicate: "⧉ 复制", dup_dag_title: "复制为新工作流(输入新 ID)", dup_done: "已复制",
    y_copy: "复制", y_download: "下载", y_close: "关闭", y_copied: "YAML 已复制到剪贴板", y_copy_fail: "复制失败,请手动选择文本",
    nd_import_yaml: "或粘贴 YAML 导入…", nd_back_form: "← 返回表单创建", nd_import: "导入", nd_yaml_empty: "请先粘贴 YAML 内容", nd_imported: "YAML 已导入",
    gs_title: "快速上手", gs_create: "创建第一个工作流", gs_trigger: "触发一次运行", gs_green: "拿到一次成功运行",
    adv_options: "高级选项", log_find_ph: "在日志中查找…", log_download: "下载完整日志", log_matches: (n) => `${n} 行匹配`, log_capped: (n) => `仅显示最近 ${n} 行`,
    back_dag: (d) => `← 返回 ${d}`, confirm_del_task_title: (id) => `删除任务 “${id}”？`,
    // ---- workers fleet ----
    nav_workers: "工作节点",
    wk_sub: "接入本调度器的远程工作节点：实时状态、负载与排空/移除操作。任务通过 worker_group 路由到对应分组。",
    wk_name: "名称", wk_group: "分组", wk_state: "状态", wk_active: "运行任务", wk_version: "版本", wk_heartbeat: "最近心跳", wk_created: "加入时间",
    wk_online: "在线", wk_offline: "离线", wk_lost: "失联", wk_draining: "排空中", wk_unnamed: "（未命名）",
    wk_drain: "排空", wk_undrain: "恢复分配",
    wk_drain_title: (n) => `排空工作节点“${n}”？`,
    wk_drain_body: "排空后不再分配新任务，正在运行的任务会继续跑完。可随时恢复。",
    wk_undrain_title: (n) => `恢复工作节点“${n}”？`,
    wk_undrain_body: "恢复后该节点重新参与任务分配。",
    wk_drained_toast: "已开始排空", wk_undrained_toast: "已恢复分配",
    wk_remove: "移除",
    wk_remove_title: (n) => `移除工作节点“${n}”？`,
    wk_remove_body: "移除立即生效且不可撤销：该节点的证书随即失效，无法重新连接；再次接入必须用新令牌重新加入。",
    wk_removed_toast: "工作节点已移除",
    wk_none_title: "还没有工作节点接入", wk_none_sub: "两步接入一个远程工作节点：",
    wk_join_step1: "在本页生成一次性加入令牌", wk_join_step2: "在工作节点主机上运行：",
    wk_token_btn: "生成加入令牌", wk_token_title: "生成一次性加入令牌",
    wk_token_ttl: "有效期", wk_ttl_1h: "1 小时", wk_ttl_24h: "24 小时", wk_ttl_7d: "7 天",
    wk_token_create: "生成",
    wk_token_reveal_h: "你的加入令牌",
    wk_token_warn: "令牌仅显示这一次，且只能使用一次——关闭后无法再次查看。",
    wk_join_cmd_h: "在工作节点主机上运行：",
    wk_expires: "过期时间",
    err_code_workers_disabled: "工作节点接入未启用——需在服务端配置 worker_listen",
    wk_disabled_hint: "服务端未启用工作节点接入。在 cronova.yaml 配置 worker_listen（或设置 CRONOVA_WORKER_LISTEN），重启后再试。",
    rel_now: "刚刚", rel_ago: (s) => `${s}前`,
    // ---- execution policy + trigger priority ----
    set_policy: "运行策略",
    set_policy_hint: "同一工作流多个运行同时就绪时如何准入。串行策略强制同一时间最多一个活跃运行（无视最大并发）。",
    po_parallel: "并行", po_serial_wait: "串行等待", po_serial_discard: "串行丢弃", po_serial_priority: "串行优先",
    pod_parallel: "并行：按「最大并发」允许多个运行同时执行（默认）",
    pod_serial_wait: "串行等待：同一时间只跑一个，后到的运行排队，按逻辑时间依次执行",
    pod_serial_discard: "串行丢弃：同一时间只跑一个，忙碌期间到来的运行会被取消（可见，不静默）",
    pod_serial_priority: "串行优先：同一时间只跑一个，队列按运行优先级从高到低出队",
    p_priority: "优先级",
    p_priority_hint: "-100 到 100，默认 0；数值高者优先获得调度槽位（serial_priority 队列也按它出队）",
    run_priority_tip: "运行优先级：高者优先获得调度槽位",
    // ---- novice mode ----
    mode_novice: "新手", mode_expert: "专家",
    mode_toggle_title: "界面模式：新手默认只保留必需信息，专家显示全部指标与操作",
    nv_workbench: "工作台", nv_myflows: "我的工作流", nv_shared: "共享配置", nv_help: "帮助中心",
    nv_sys_ok: "系统运行正常", nv_newflow: "＋ 新建工作流",
    s0_title: "把重复的脚本，交给 cronova 定时跑",
    s0_sub: "按时执行、失败自动重试、随时查看日志。",
    s0_sub2: "三步建好第一条工作流，大约需要 1 分钟。",
    s0_step1: "选一个起点", s0_step2: "确认要执行的命令", s0_step3: "试跑一次，看它变绿",
    s0_cta: "创建我的第一个工作流 →",
    s0_expert_link: "我用过 Airflow / Azkaban，直接进专家模式 →",
    s0_term_hint: "「工作流」= 按顺序执行的一组命令，专业术语叫 DAG。切到专家模式会看到完整术语。",
    wz_crumb: "新建工作流", wz_back: "← 返回", wz_stepof: (n) => `第 ${n} 步 / 共 3 步`,
    wz1_title: "选一个和你最像的场景", wz1_sub: "之后每一步都可以改，这只是一个起点。",
    wz_tpl_etl: "每日数据处理", wz_tpl_etl_d: "先取数、再加工、最后写入 —— 三步依次执行", wz_tpl_etl_m: "3 个步骤 · 上一步成功才执行下一步",
    wz_tpl_report: "定时报表", wz_tpl_report_d: "取数、生成报表并发送 —— 适合每天早上自动发日报", wz_tpl_report_m: "2 个步骤 · 预设每天 08:00",
    wz_tpl_blank: "从零开始", wz_tpl_blank_d: "只有一个步骤，跑你自己的任意命令或脚本", wz_tpl_blank_m: "1 个步骤 · 最简单", wz_tpl_blank_chip: "你的命令",
    wz1_next: "下一步：确认命令 →",
    wz2_title: "这些步骤会依次执行",
    wz2_sub: "命令就是你平时在终端里敲的那一行。上一步成功，才会开始下一步；失败会自动重试。",
    wz2_name_label: "给工作流起个名字", wz2_name_hint: "用英文、数字或下划线，例如 daily_report",
    wz2_adv: "进阶（可跳过）：命令里可以插入运行时变量，跑的时候自动替换成真实值。点一下试试——",
    wz2_datevar: "＋ 今天的日期",
    wz2_datevar_note: "会在第 1 步命令末尾加上",
    wz2_inserted: (d) => `已插入 ✓ 运行时会替换成实际日期，例如 --date ${d}（可在上面的输入框里直接改）`,
    wz2_next: "下一步：什么时候运行 →", wz_prev: "← 上一步",
    wz3_title: "它应该什么时候运行？", wz3_sub: "现在选个大概就行，之后随时能改。",
    wz3_daily: "每天固定时间", wz3_daily_d: "最常见：夜里跑数据、早上发报表",
    wz3_interval: "每隔一段时间", wz3_interval_d: "适合轮询、同步类任务", wz3_every: "每", wz3_minutes: "分钟",
    wz3_manual: "只在我手动点击时运行", wz3_manual_d: "先手动跑熟了，再设成自动也不迟",
    wz3_note: "创建后会立刻带你手动试跑一次，确认一切正常。",
    wz3_cron_hint: "专家模式里这里是 cron 表达式（如 0 2 * * *），支持更复杂的规则。",
    wz3_create: "▶ 创建并试跑一次",
    nv_sched_daily: (h) => `将于每天 ${h} 自动运行。`, nv_sched_interval: (n) => `将每隔 ${n} 分钟自动运行一次。`,
    nv_sched_manual: "只在你手动点击「运行」时执行，不会自动触发。",
    nv_gloss_daily: (h) => `每天 ${h} 自动运行`, nv_gloss_every: (n, u) => `每 ${n} ${u}自动运行`, nv_gloss_manual: "仅手动运行",
    nvr_running_title: (d) => `正在运行 ${d} …`, nvr_sub: "每个步骤跑完会自动进入下一步，不用刷新。",
    nvr_done_title: (d) => `运行成功，用时 ${d}`, nvr_done_sub: "失败时它会自动重试，并把日志留在这里。",
    nvr_done_home: "完成，回到首页", nvr_detail: "查看详情",
    nvr_failed_title: (n, id) => `第 ${n} 步 ${id} 失败了`,
    nvr_failed_generic: "运行失败了",
    nvr_failed_sub: "前面的步骤已成功，后面的没有执行。错误原因见日志最后几行。",
    nvr_retried: (n) => `已自动重试 ${n} 次仍失败。`,
    nvr_cancelled_title: "运行已取消", nvr_timeout_title: "运行超时，已强制停止",
    nvr_cancelled_sub: "你手动停止了这次运行。命令没问题的话，随时可以再次运行。",
    nvr_timeout_sub: "运行超过了设定的时长限制，已被强制停止，后面的步骤没有执行。",
    nvr_rerun: "修复后重跑 ▶", nvr_rerun2: "再次运行 ▶", nvr_edit_steps: "编辑步骤", nvr_home: "回首页",
    nvr_waiting: "等待中", nvr_notrun: "未执行", nvr_running_dur: "运行中…",
    nvr_retry_n: (n) => `重试 ${n} 次`,
    nv_health_ok: "一切正常",
    nv_health_line: (n) => `${n} 个工作流 · 最近运行没有失败`,
    nv_health_bad_title: (n) => `有 ${n} 个工作流需要处理`,
    nv_health_bad_line: (id) => `${id} 的最近一次运行失败 · 修复后可一键重跑`,
    nv_fix: "去处理 →", nv_metrics_link: "查看完整指标 →",
    nv_run_btn: "▶ 运行", nv_view_btn: "查看 →",
    nv_next_hints: "下一步可以：", nv_hint_another: "再建一个工作流", nv_hint_expert: "切到专家模式看全部功能",
    nv_never_ran: "还没有运行过", nv_last_run: (s) => `上次运行${s}`,
    nv_enabled: "已启用",
    nv_run_now: "▶ 立即运行", nv_more: "更多 ⋯", nv_more_title: "暂停 / 复制 / 删除等更多操作",
    nv_more_q: (id) => `对 ${id} 做什么？`, nv_more_expert: "切到专家模式编辑",
    nv_steps_h: "步骤", nv_steps_hint: "（专家模式里叫「任务 / task」）",
    nv_edit: "编辑", nv_add_step: "+ 添加步骤",
    nv_edit_step_title: (id) => `编辑步骤 ${id}`, nv_del_step: "删除此步骤",
    nv_recent_h: "最近运行", nv_view_log: "查看日志 →",
    nv_notify_h: "失败通知", nv_notify_toggle: "失败时通知我",
    nv_notify_hint: "粘贴群机器人的 Webhook 地址（支持 Slack / 飞书 / 钉钉，自动识别格式）。运行失败时会发一条消息。",
    nv_adv_summary: "更多设置（并发、重试、超时……）",
    nv_adv_body: (r, m) => `这些默认值已经够用：${r > 0 ? `失败自动重试 ${r} 次、` : ""}同一时间只跑 ${m} 份。`,
    nv_adv_body2: "需要精细控制时再展开，或", nv_adv_body3: "查看全部设置项。", nv_to_expert: "切到专家模式",
    nv_fail_ribbon: (n, id) => `第 ${n} 步 ${id} 失败`, nv_fail_ribbon_generic: "最近一次运行失败",
    nv_see_log: "看日志",
    nv_step_extract: "抽取数据", nv_step_transform: "加工处理", nv_step_load: "写入结果",
    nv_step_fetch: "取数", nv_step_render: "生成并发送报表", nv_step_1: "第一步",
  },
  en: {
    workspace: "Workspace", nav_dags: "Workflows", newdag: "+ New workflow",
    f_all: "All", f_running: "Running", f_failed: "Failed", f_paused: "Paused",
    dags_sub: "Click a name for runs and structure; the left toggle pauses / resumes scheduling.",
    ov_fail_title: (id) => `${id}: latest run failed`, ov_fail_more: (n) => `${n} more failing`,
    ov_rest_ok: (n) => `the other ${n} are healthy`, ov_all_ok: "All workflows healthy", ov_last_run: (w) => `last run ${w}`,
    ov_running: "Running", ov_running_tip: "Runs currently executing", ov_rate: "Recent success", ov_go: "Investigate →",
    hlp_dag: '<b>A workflow (DAG)</b> = tasks that run in dependency order, plus a rule for when to run. <span class="eg">e.g. daily at 2:00 — extract → transform → load.</span>',
    hlp_toggle: "<b>Schedule switch.</b> Off = no more automatic runs (manual trigger still works); history is kept and it can be re-enabled any time.",
    hlp_spark: 'Each bar is one run: <b class="ok">green</b>=success, <b class="bad">red</b>=failed, <b class="run">blue</b>=running; taller = slower. Hover for details.',
    hlp_next: "Next automatic run per the schedule. “due” = waiting for a free slot.",
    hlp_rate: "Share of recent runs that succeeded (up to 14 per workflow; skips and cancellations don't count).",
    toggle_tip_on: "Enabled — click to pause scheduling", toggle_tip_off: "Paused — click to resume scheduling",
    btn_run_word: "Run", run_now_tip: "Run once now, manually — doesn't affect the schedule",
    day_today: "today", day_yesterday: "yesterday",
    h_dag: "WORKFLOW", h_spark: "LAST 14", h_pool: "POOL", h_next: "NEXT RUN",
    no_match: "No matching workflows", no_match_filter: "No workflows under this filter", no_dags_title: "No workflows yet", no_dags_sub: "Create your first workflow to start scheduling tasks.", trigger: "Trigger", manual_trigger: "manual trigger",
    back_dags: "← Workflows", run_word: "run", sub_manual: "manual trigger only", max_active: "max active",
    run_progress: "Progress",
    sec_graph: "Dependency graph", sec_structure: "Structure", sec_runs: "Run history", sec_instances: "Task instances",
    g_timeline: "Timeline", g_never_ran: "did not run", run_no_tasks: "No task instances yet for this run", run_done_ok: "Run finished — success", run_done_fail: "Run failed", run_done_timeout: "Run timed out",
    run_cancel: "Cancel run", run_retry: "Retry failed", task_retry: "Retry", run_cancelled_toast: "Run cancelled", run_retried_toast: "Re-queued",
    task_mark: "Mark state", run_mark: "Mark run", mark_skip: "Skip", mark_done_toast: "Marked",
    mark_task_title: (id) => `Mark task “${id}” as?`, mark_task_body: "Manually override the task state. A running task is stopped first; marking success/skip releases downstream tasks it was blocking.",
    mark_run_title: (id) => `Mark run “${id}” as?`, mark_run_body: "Override a finished run's recorded outcome (tasks untouched). Marking success fires downstream-workflow triggers.",
    confirm_cancel_title: (id) => `Cancel run “${id}”?`, confirm_cancel_body: "Running tasks will be killed.", th_act: "Actions",
    confirm_retry_title: (id) => `Retry “${id}”?`, confirm_retry_body: "This task and all of its downstream tasks will be reset and re-run.",
    copied: "Copied", copy_fail: "Copy failed — select the text manually", copy_hint: "Click to copy", search_ph: "Search workflows…", jump_open: "Open", jump_none: "No matching workflow",
    gz_in: "Zoom in", gz_out: "Zoom out", gz_fit: "Fit to view", gz_hint: "Drag to pan · Ctrl/⌘ + wheel to zoom",
    login_title: "Sign in to cronova", login_sub: "Enter your account credentials", login_user: "Username", login_pass: "Password", login_btn: "Sign in", login_bad: "Invalid username or password", logout: "Sign out", sess_expired: "Session expired — please sign in again", role_admin: "Admin", role_viewer: "Viewer",
    tab_runs: "Runs", tab_structure: "Structure", tab_settings: "Settings",
    dh_last: "Last run", dh_next: "Schedule", dh_rate: "Success rate", dh_never: "No runs yet", dh_norate: "—",
    set_done: "Done", set_edit: "Edit", set_none: "None", set_sched: "Schedule", set_max: "Max active runs", set_retries: "Default retries", set_deps: "Upstream workflows",
    set_deps_hint: "Triggered automatically after these workflows succeed", set_no_deps_avail: "No other workflows available",
    set_notify: "Notifications", set_notify_hint: "POST a JSON webhook when a run finishes (Slack/Feishu/Discord compatible), or reference an alert group to notify several channels at once", notify_failure: "Failure", notify_success: "Success", notify_off: "No events selected", notify_need_url: "Enter a webhook URL or pick an alert group first, then pick events", err_notify_url: "Notify URL must start with http://, https:// or mailto:",
    nf_label: "Message format", nf_hint: "raw = full JSON payload; the others wrap the summary text in that platform's incoming-webhook envelope; email is for mailto: targets", nf_feishu: "Feishu", nf_dingtalk: "DingTalk", nf_email: "Email",
    set_group: "Alert group", ag_group_none: "No alert group", ag_opt_missing: (n) => `${n} (missing)`,
    ag_url_overridden: "An alert group is selected — this standalone URL will be ignored",
    notify_mailto_hint: "The URL also accepts mailto:addr1,addr2 (requires smtp: configured on the server)",
    btn_backfill: "Backfill", bf_hint: "Enqueue one run per schedule period in the window (inclusive; existing periods are skipped, execution obeys max active runs)", bf_from: "From", bf_to: "To", bf_go: "Backfill", bf_need_dates: "Pick both dates",
    bf_done: (c, s) => `Backfill enqueued: ${c} run(s) created, ${s} skipped`,
    rf_all: "All", rf_running: "Active", rf_failed: "Failed", rf_success: "Success",
    t_backoff: "Retry backoff", bo_fixed: "Fixed", bo_exponential: "Exponential", t_backoff_hint: "Exponential: the n-th retry waits retry delay × 2ⁿ⁻¹",
    t_backoffmax: "Backoff cap (s)", t_backoffmax_hint: "Longest exponential wait; 0 = uncapped",
    set_sla: "SLA (soft)", set_sla_hint: "From run start; alert if not finished in time (run keeps going). 0 = off. Needs a notify webhook.", set_timeout: "Run timeout (hard)", set_timeout_hint: "From run start; on breach the run is force-failed and running tasks killed → timed_out. 0 = off.", secs: "sec", set_off: "off",
    t_sla: "Task SLA (sec)", t_sla_hint: "From run start; alert if this task hasn't finished in time. 0 = off.", t_timeout_hint: "Kill a single execution after this many seconds. 0 = none.",
    danger_title: "Danger zone", danger_del_hint: "Archive this workflow: no more scheduling; history is kept.",
    nd_more: "Schedule & more options", nd_less: "Hide",
    nav_resources: "Variables & Connections", nav_audit: "Audit", nav_api: "API",
    audit_sub: "Operations log: who did what to which DAG/run, and when.", audit_empty: "No operations logged yet", au_time: "Time", au_actor: "Actor", au_action: "Action", au_target: "Target",
    act_trigger: "trigger", act_cancel: "cancel", act_retry_run: "retry run", act_retry_task: "retry task", act_mark_task: "mark task", act_mark_run: "mark run", act_update_dag: "save workflow", act_create_dag: "create DAG", act_delete_dag: "delete DAG", act_pause: "pause", act_unpause: "unpause", act_create_token: "create token", act_delete_token: "revoke token", act_set_alert_group: "save alert group", act_delete_alert_group: "delete alert group",
    api_title: "API & Integration", api_sub: "Drive cronova from your own platform. Browse the interactive API reference and manage API tokens for machine access.",
    api_docs_h: "API reference", api_docs_hint: "Full OpenAPI reference with built-in curl / Go / Python / Java samples and an in-page language switcher.", api_open_docs: "Open API reference →", api_spec_link: "OpenAPI spec",
    tok_title: "API Tokens", tok_sub: "Machine credentials. Call the API with Authorization: Bearer <token>. The plaintext is shown only once, at creation.",
    tok_name: "Name", tok_role: "Role", tok_prefix: "Prefix", tok_created: "Created", tok_lastused: "Last used", tok_never: "Never used",
    tok_create: "Create token", tok_none: "No tokens yet", tok_name_ph: "e.g. ci-bot", tok_revoke: "Revoke", tok_need_name: "Name is required",
    tok_revoke_title: (n) => `Revoke token “${n}”?`, tok_revoke_body: "Calls using this token will fail immediately. This cannot be undone.",
    tok_created_ok: "Token created", tok_revoked: "Token revoked",
    tok_reveal_h: "Your new API token", tok_reveal_warn: "Copy it now and store it securely — you won't be able to see the plaintext again.", tok_copy: "Copy", tok_done: "I've saved it",
    role_admin_full: "Admin (read-write)", role_operator: "Operator (trigger/cancel/retry)", role_viewer_ro: "Viewer (GET only)",
    res_vars: "Variables", res_conns: "Connections", res_groups: "Alert groups",
    res_sub: "Config shared across tasks. Reference in commands as {{ var.KEY }} / {{ conn.ID.field }}, or {{ params.KEY }} at trigger time.",
    ag_hint: "Bundle several notify channels under one name and reference it from workflows via notify.group; when the on-call destinations change, edit them here once.",
    ag_name: "Group name", ag_channels: "Channels", ag_updated: "Updated", ag_channel_n: (n) => `${n} channel${n > 1 ? "s" : ""}`,
    ag_none: "No alert groups yet", ag_add: "New alert group", ag_edit: "Edit alert group",
    ag_add_channel: "+ Add channel", ag_remove_channel: "Remove channel", ag_fmt: "Format",
    ag_url_ph: "https://hooks.slack.com/… or mailto:oncall@example.com",
    ag_max: (n) => `up to ${n} channels`,
    ag_err_channels: "An alert group needs 1-16 channels",
    ag_err_url: "Channel URL must start with http://, https:// or mailto:",
    ag_mailto_hint: "mailto: channels are sent through the server's SMTP relay — configure the smtp: section of the config file first",
    ag_del_title: (n) => `Delete alert group “${n}”?`,
    ag_del_body: "Workflows referencing it fall back to their own notify URL or the instance default — no alert is lost.",
    v_key: "Key", v_value: "Value", v_add: "Add variable", v_none: "No variables yet", v_save: "Save",
    c_id: "Connection ID", c_type: "Type", c_host: "Host", c_port: "Port", c_login: "Login", c_password: "Password", c_extra: "Extra (JSON)",
    c_add: "New connection", c_edit: "Edit connection", c_none: "No connections yet", c_pw_set: "set", c_pw_none: "not set", c_pw_keep: "leave blank to keep",
    c_del_title: (id) => `Delete connection “${id}”?`, v_del_title: (k) => `Delete variable “${k}”?`, del_body: "This cannot be undone.",
    trig_params: "Trigger with params", p_params: "Params", p_add: "Add row", p_key: "Key", p_val: "Value", p_trigger: "Trigger", p_hint: "Params are injected as CRONOVA_PARAM_* env vars; reference as {{ params.key }} in commands.",
    run_params: "Params", res_saved: "Saved", res_deleted: "Deleted", err_key: "Invalid name (letters, digits, _ . - only)",
    btn_trigger: "▶ Trigger run", btn_pause: "Pause", btn_resume: "Resume", btn_delete: "Delete",
    confirm_del_dag_title: (id) => `Archive workflow “${id}”?`,
    confirm_del_dag_body: "It will be archived (hidden from lists); run history is kept and it can be restored.",
    dag_archived: "This workflow is archived (deleted).",
    confirm_word: "Confirm", cancel_word: "Cancel", aria_theme: "Toggle theme", aria_lang: "Switch language",
    toast_run_queued: "Triggered — run queued", toast_pool_saved: "Pool saved", toast_dag_deleted: "Workflow archived",
    th_id: "id", th_type: "type", th_command: "command", th_deps: "deps",
    th_logical: "logical date", th_state: "state", th_trig: "trigger", th_started: "started", th_dur: "duration",
    th_task: "task", th_try: "try", th_logs: "logs",
    no_runs: "No runs yet — trigger one.",
    k_logical: "logical date", k_trig: "trigger", k_dur: "duration", k_started: "started",
    log_word: "Log", live: "live",
    pools_sub: "Global concurrency slots, shared across all workflows and runs.", p_name: "name", p_slots: "slots", p_save: "Save",
    p_newname: "new pool name", p_create: "Create pool", p_need: "name + positive slots required",
    trig_fail: "trigger failed", api_err: "API error",
    err_code_not_found: "Not found (it may have been deleted)",
    err_code_no_tasks: "This workflow has no steps yet — add one before running it",
    err_code_bad_mark_state: "That state is not a valid mark target",
    err_code_queue_full: "The run queue is full — try again shortly",
    err_code_active_runs: "This workflow still has active runs — cancel or wait for them first",
    err_code_run_not_active: "This run has already finished — nothing to cancel",
    err_code_nothing_to_retry: "This run has no failed steps to retry",
    err_code_run_still_active: "This run is still active — retry it after it finishes",
    err_code_bad_group_name: "Invalid alert group name (letters, digits, _ . - only; max 128 chars)",
    err_code_dag_conflict: "This workflow changed since you loaded it — reload to merge your edits; retrying will conflict again",
    err_code_group_channels: "An alert group needs 1-16 channels",
    err_code_group_channel_url: "Channel URL must start with http://, https:// or mailto:",
    err_code_group_channel_format: "Invalid channel format (raw / slack / feishu / dingtalk / email)",
    dt_hint: "Duration trend: taller = slower, color = outcome; click a bar to open that run",
    runs_more: "Load more ↓", audit_more: "Load more ↓", log_all: "All tasks",
    bulk_all: "Select all (current filter)", bulk_pick: (id) => `Select ${id}`,
    bulk_selected: (n) => `${n} workflow${n > 1 ? "s" : ""} selected`, bulk_done: (ok, n) => `Bulk action done: ${ok}/${n} succeeded`,
    bulk_del_title: (n) => `Archive the ${n} selected workflow${n > 1 ? "s" : ""}?`,
    au_f_actor: "Filter by actor", au_f_action: "Filter by action", au_f_all: "All",
    nx_paused: "paused", nx_due: "due", nx_in: (m) => `in ${m}m`,
    b_dag_info: "DAG info",
    f_dag_id: "DAG ID", f_start: "Start date",
    f_catchup: "Catchup", f_maxactive: "Max active", f_defretries: "Default retries",
    f_catchup_hint: "When on, every schedule period missed since start_date gets a backfilled run (at most one per tick, bounded by max active runs — no thundering herd)",
    f_trigger_after: "Trigger after (upstream success)",
    b_addtask: "+ Add task", b_remove: "Remove",
    t_id: "Task ID", t_type: "Type", t_command: "Command", t_pool: "Pool", t_priority: "Priority",
    t_http: "HTTP request", http_method: "Method", http_url: "URL", http_headers: "Headers", http_headers_hint: "One per line, Key: Value — supports {{ var. }} / {{ conn. }}", http_body: "Body", http_status: "Expected status", http_status_hint: "Comma-separated, e.g. 200,201; empty = any 2xx", err_httpurl: "HTTP task requires a URL",
    t_python: "Python code", python_hint: "Inline Python run with python3 -c; CRONOVA_* vars are in the environment; supports {{ var. }} templates. Non-zero exit = failure.",
    t_sql: "SQL query", sql_conn: "Connection", sql_conn_hint: "A configured connection id (its type picks the driver: postgres/mysql/sqlite). See Variables & Connections.", err_sqlconn: "SQL task requires a connection",
    t_retries: "Retries (empty=default)", t_retrydelay: "Retry delay (s)", t_timeout: "Timeout (s)", t_deps: "Depends on",
    t_nodeps: "no other tasks",
    t_project: "Project", t_optional: "(optional)", proj_none: "None (no project)", proj_upload: "Upload / new project",
    proj_hint: "When set, the command runs inside a clean copy of this project (e.g. python3 main.py). Shell tasks only.",
    proj_name: "Project name", proj_name_bad: "Name may contain only letters/digits/. _ -", proj_mode_files: "Upload files / folder", proj_mode_inline: "Write a script",
    proj_drop: "Drag files or a folder here", proj_pick_files: "Choose files", proj_pick_folder: "Choose folder", proj_ziphint: "Drop a .zip to auto-extract",
    proj_filename: "Filename", proj_content: "Script content", proj_do_upload: "Upload", proj_selected: (n) => `${n} file(s) selected`,
    proj_uploaded: "Project uploaded", proj_upload_fail: "Upload failed", proj_need_name: "Enter a project name first", proj_need_files: "Choose files or write script content first", proj_manage: "Manage projects",
    err_dagid: "Valid DAG ID required (letters/digits/_-.)", err_taskid: "Invalid task ID (letters/digits/_-.)",
    err_dup: "Duplicate task ID", err_emptyid: "Empty task ID", err_emptycmd: "Empty command", err_cycle: "Dependency cycle detected",
    sched: "Schedule", sm_manual: "Manual", sm_every: "Interval", sm_cron: "Cron expression",
    sched_manual_hint: "Manual trigger or triggered by an upstream workflow only", sched_every_pre: "Every",
    unit_s: "sec", unit_m: "min", unit_h: "hr", disabled_note: "(coming soon)",
    cp_min: "every minute", cp_hour: "hourly", cp_day: "daily 0:00", cp_2am: "daily 2:00", cp_mon: "Mon 0:00",
    cron_help: "help", ch_title: "Cron format", ch_format: "Format: min hour day month weekday (5 space-separated fields)",
    ch_fields: "Fields", ch_ops: "Operators", ch_examples: "Examples (click to fill)", ch_shortcuts: "Shortcuts",
    t_rule: "Trigger rule", tr_all_success: "all success", tr_all_done: "all done", tr_one_success: "one success", tr_one_failed: "one failed", tr_all_failed: "all failed", tr_none_failed: "none failed",
    trd_all_success: "Runs only if all upstreams succeeded (default)", trd_all_done: "Runs once all upstreams finish, success or not — good for cleanup/summary", trd_one_success: "Runs as soon as any upstream succeeds", trd_one_failed: "Runs as soon as any upstream fails — good for alerts", trd_all_failed: "Runs only if all upstreams failed", trd_none_failed: "Runs if no upstream failed (succeeded or skipped)",
    pool_hint: "Concurrency slots shared across all DAGs; tasks in the same pool compete for its slots",
    cb_interp: "Interpreter", cb_runas: "Run as", cb_target: "Module / script", cb_args: "Arguments", cb_jar: "Jar path", cb_mainclass: "Main class", cb_client: "SQL client", cb_query: "SQL query",
    cmdopt_module: "module (-m)", cmdopt_script: "script file",
    cmd_will_run: "Will run:", cmd_edit_raw: "edit raw command", cmd_use_form: "use form", cmd_cant_parse: "This command can't be parsed into the form; keeping the raw editor",
    var_insert: "click or drag to insert a variable", var_editor_aria: "command editor — insert variable pills",
    var_pill_aria: (n) => `variable ${n}`, var_pill_remove: (n) => `remove variable ${n}`,
    var_empty: "none", var_add_key: "custom…", var_conn_field: "field", var_goto_settings: "set up",
    vd_logical_date: "this run's logical date (day)", vd_logical_datetime: "logical date-time (RFC3339)",
    vd_date_expr: "date expression: ±N d/h/w/mo offsets, .month_start/.month_end/.week_start/.week_end anchors, | %Y%m%d custom format; composable",
    vd_run_id: "this run's unique id", vd_dag_id: "the DAG id", vd_task_id: "this task's id", vd_try_number: "attempt number (increments on retry)",
    vd_var: "shared variable", vd_conn: "connection field", vd_params: "manual-trigger param",
    vg_builtin: "built-in", vg_var: "variables", vg_conn: "connections", vg_params: "params",
    graph_connect_hint: "Drag the dot on a node's right edge onto another task to connect; click a node to edit the task; Shift+click two nodes (or toggle connect mode and click them in turn) also connects/disconnects; click an edge to remove the dependency",
    ge_addtask: "+ Add task", ge_connect: "Connect mode",
    ge_connect_tip: "When on: click the upstream task, then the downstream task to connect/disconnect (keyboard friendly)",
    ge_new_id_title: "New task ID", ge_edge_remove: "Remove dependency", ge_dup_dep: "Dependency already exists",
    ge_edge_aria: (a, b) => `Dependency ${a} → ${b}, press Enter to remove`,
    ge_node_aria: (id) => `Task ${id}`,
    diff_unsaved: (n) => `${n} unsaved change${n > 1 ? "s" : ""}`, diff_save: "Save", diff_discard: "Discard",
    diff_show: "View diff", diff_hide: "Hide diff", diff_loading: "Building diff preview…",
    diff_invalid: "Pending changes fail validation", diff_same: "Identical to the saved version", diff_discarded: "Unsaved changes discarded",
    t_subdag: "Target workflow", subdag_hint: "Runs another workflow as a child run: this task mirrors the child run's terminal state, and cancel cascades into it", subdag_none: "Pick a workflow…", err_subdag: "A sub-workflow task needs a target workflow",
    dod_title: "Cross-workflow dependency", dod_hint: "The task becomes ready only after the target workflow's matching period run succeeds",
    dod_dag: "Depends on workflow", dod_offset: "Period offset", dod_offset_hint: "- 1d / .month_start / blank = same period",
    dod_timeout: "Wait timeout (sec)", dod_timeout_hint: "0 = wait until the run times out", dod_on_timeout: "On timeout",
    dod_fail: "fail", dod_skip: "skip",
    parent_run: "Parent run", child_run: "Child run",
    nav_graph: "Graph", graph_title: "Workflow Graph", graph_sub: "Trigger dependencies between workflows via trigger_after",
    graph_none: "No cross-workflow dependencies yet (none declares trigger_after)", graph_view_hint: "Tip: arrows point in the trigger-after direction; click a node to open that workflow; dashed nodes are unknown workflows",
    ss_saved: "Saved", ss_saving: "Saving…", ss_invalid: "Fix errors to save", ss_error: "Save failed",
    dag_no_tasks_title: "No tasks yet", dag_no_tasks_sub: "Add a task to enable this workflow", dag_disabled_hint: "Add a task to enable triggering",
    nd_title: "New workflow", nd_create: "Create", nd_cancel: "Cancel", nd_dagid_dup: "A DAG with this id already exists",
    tpl_start: "Start from a template", tpl_tasks: "tasks",
    tpl_blank: "Blank", tpl_blank_d: "Start empty and add tasks yourself",
    tpl_etl: "Daily ETL", tpl_etl_d: "Three-step extract → transform → load pipeline",
    tpl_report: "Scheduled report", tpl_report_d: "Fetch → render, preset to run daily at 08:00",
    tpl_fanout: "Fan-out / fan-in", tpl_fanout_d: "start → two parallel branches → join",
    coach_tpl_ready: "Template ready — hit “Trigger run” to watch it execute, then tweak the tasks",
    sp_every: (n, u) => `Runs every ${n} ${u}`, sp_next: "Next", sp_invalid: "Invalid expression — cannot compute fire times",
    tz_note: "Schedules evaluate in UTC; times shown in your local timezone",
    btn_duplicate: "⧉ Duplicate", dup_dag_title: "Duplicate as a new workflow (enter a new id)", dup_done: "Duplicated",
    y_copy: "Copy", y_download: "Download", y_close: "Close", y_copied: "YAML copied to clipboard", y_copy_fail: "Copy failed — select the text manually",
    nd_import_yaml: "or paste YAML to import…", nd_back_form: "← back to the form", nd_import: "Import", nd_yaml_empty: "Paste some YAML first", nd_imported: "YAML imported",
    gs_title: "Getting started", gs_create: "Create your first workflow", gs_trigger: "Trigger a run", gs_green: "Get a green run",
    adv_options: "Advanced options", log_find_ph: "Find in log…", log_download: "Download full log", log_matches: (n) => `${n} matching lines`, log_capped: (n) => `showing last ${n} lines`,
    back_dag: (d) => `← Back to ${d}`, confirm_del_task_title: (id) => `Delete task “${id}”?`,
    // ---- workers fleet ----
    nav_workers: "Workers",
    wk_sub: "Remote workers dialed into this scheduler: live state, load, and drain/remove operations. Tasks route to a group via worker_group.",
    wk_name: "Name", wk_group: "Group", wk_state: "State", wk_active: "Active tasks", wk_version: "Version", wk_heartbeat: "Last heartbeat", wk_created: "Joined",
    wk_online: "online", wk_offline: "offline", wk_lost: "lost", wk_draining: "draining", wk_unnamed: "(unnamed)",
    wk_drain: "Drain", wk_undrain: "Undrain",
    wk_drain_title: (n) => `Drain worker “${n}”?`,
    wk_drain_body: "A draining worker gets no new assignments; its running tasks finish normally. You can undrain it any time.",
    wk_undrain_title: (n) => `Undrain worker “${n}”?`,
    wk_undrain_body: "The worker resumes receiving task assignments.",
    wk_drained_toast: "Draining started", wk_undrained_toast: "Assignments resumed",
    wk_remove: "Remove",
    wk_remove_title: (n) => `Remove worker “${n}”?`,
    wk_remove_body: "Removal is immediate and cannot be undone: the worker's certificate stops being accepted, so it cannot reconnect — rejoining requires a fresh join token.",
    wk_removed_toast: "Worker removed",
    wk_none_title: "No workers have joined yet", wk_none_sub: "Attach a remote worker in two steps:",
    wk_join_step1: "Mint a one-time join token on this page", wk_join_step2: "Run on the worker host:",
    wk_token_btn: "New join token", wk_token_title: "Mint a one-time join token",
    wk_token_ttl: "Expires in", wk_ttl_1h: "1 hour", wk_ttl_24h: "24 hours", wk_ttl_7d: "7 days",
    wk_token_create: "Mint token",
    wk_token_reveal_h: "Your join token",
    wk_token_warn: "This token is shown only once and can be used only once — you won't see it again after closing.",
    wk_join_cmd_h: "Run on the worker host:",
    wk_expires: "Expires",
    err_code_workers_disabled: "Worker hub is not enabled — set worker_listen in the server config",
    wk_disabled_hint: "The server has no worker hub enabled. Configure worker_listen in cronova.yaml (or set CRONOVA_WORKER_LISTEN), restart, and try again.",
    rel_now: "just now", rel_ago: (s) => `${s} ago`,
    // ---- execution policy + trigger priority ----
    set_policy: "Execution policy",
    set_policy_hint: "How runs of this workflow are admitted when several are ready at once. Serial policies force at most one active run, whatever Max active runs says.",
    po_parallel: "Parallel", po_serial_wait: "Serial (wait)", po_serial_discard: "Serial (discard)", po_serial_priority: "Serial (priority)",
    pod_parallel: "Parallel: up to Max active runs execute concurrently (default)",
    pod_serial_wait: "Serial wait: one at a time; later runs queue and execute in logical-date order",
    pod_serial_discard: "Serial discard: one at a time; runs arriving while busy are cancelled (visibly, never silently)",
    pod_serial_priority: "Serial priority: one at a time; the queue drains highest run priority first",
    p_priority: "Priority",
    p_priority_hint: "-100 to 100, default 0; higher wins the competition for dispatch slots (and drains first from a serial_priority queue)",
    run_priority_tip: "Run priority: higher wins dispatch-slot competition",
    // ---- novice mode ----
    mode_novice: "Simple", mode_expert: "Expert",
    mode_toggle_title: "Interface mode: Simple keeps only the essentials; Expert shows every metric and action",
    nv_workbench: "Workbench", nv_myflows: "My workflows", nv_shared: "Shared config", nv_help: "Help center",
    nv_sys_ok: "System healthy", nv_newflow: "+ New workflow",
    s0_title: "Hand your repetitive scripts to cronova",
    s0_sub: "Runs on schedule, retries on failure, logs always at hand.",
    s0_sub2: "Three steps to your first workflow — about a minute.",
    s0_step1: "Pick a starting point", s0_step2: "Confirm the commands", s0_step3: "Run once, watch it go green",
    s0_cta: "Create my first workflow →",
    s0_expert_link: "I've used Airflow / Azkaban — take me to expert mode →",
    s0_term_hint: "A “workflow” is a set of commands run in order — the technical term is DAG. Expert mode uses the full terminology.",
    wz_crumb: "New workflow", wz_back: "← Back", wz_stepof: (n) => `Step ${n} of 3`,
    wz1_title: "Pick the scenario closest to yours", wz1_sub: "Everything can be changed later — this is just a starting point.",
    wz_tpl_etl: "Daily data pipeline", wz_tpl_etl_d: "Fetch, transform, then load — three steps in order", wz_tpl_etl_m: "3 steps · each waits for the previous to succeed",
    wz_tpl_report: "Scheduled report", wz_tpl_report_d: "Fetch data and send a report — great for a daily 8am digest", wz_tpl_report_m: "2 steps · preset daily 08:00",
    wz_tpl_blank: "From scratch", wz_tpl_blank_d: "A single step running any command or script of yours", wz_tpl_blank_m: "1 step · simplest", wz_tpl_blank_chip: "your command",
    wz1_next: "Next: confirm commands →",
    wz2_title: "These steps run one after another",
    wz2_sub: "A command is the line you'd type in a terminal. Each step starts only after the previous succeeds; failures retry automatically.",
    wz2_name_label: "Name your workflow", wz2_name_hint: "Letters, digits or underscores — e.g. daily_report",
    wz2_adv: "Optional: commands can embed runtime variables, replaced with real values at run time. Try it —",
    wz2_datevar: "+ Today's date",
    wz2_datevar_note: "appends to the end of step 1's command:",
    wz2_inserted: (d) => `Inserted ✓ replaced with the actual date at run time, e.g. --date ${d} (editable in the input above)`,
    wz2_next: "Next: when should it run →", wz_prev: "← Previous",
    wz3_title: "When should it run?", wz3_sub: "A rough choice is fine — change it any time later.",
    wz3_daily: "Every day at a fixed time", wz3_daily_d: "Most common: crunch data at night, send reports in the morning",
    wz3_interval: "At a regular interval", wz3_interval_d: "Good for polling and sync jobs", wz3_every: "every", wz3_minutes: "minutes",
    wz3_manual: "Only when I trigger it", wz3_manual_d: "Run it by hand first — automate once you trust it",
    wz3_note: "After creating, we'll take you straight to a trial run to confirm everything works.",
    wz3_cron_hint: "In expert mode this is a cron expression (e.g. 0 2 * * *) with more powerful rules.",
    wz3_create: "▶ Create & run once",
    nv_sched_daily: (h) => `It will run automatically every day at ${h}.`, nv_sched_interval: (n) => `It will run automatically every ${n} minutes.`,
    nv_sched_manual: "It only runs when you click “Run” — never automatically.",
    nv_gloss_daily: (h) => `daily at ${h}`, nv_gloss_every: (n, u) => `every ${n} ${u}`, nv_gloss_manual: "manual only",
    nvr_running_title: (d) => `Running ${d} …`, nvr_sub: "Steps advance automatically — no need to refresh.",
    nvr_done_title: (d) => `Run succeeded in ${d}`, nvr_done_sub: "On failure it retries automatically and keeps the logs here.",
    nvr_done_home: "Done — back home", nvr_detail: "View details",
    nvr_failed_title: (n, id) => `Step ${n} ${id} failed`,
    nvr_failed_generic: "The run failed",
    nvr_failed_sub: "Earlier steps succeeded; later ones didn't run. See the last lines of the log for the cause.",
    nvr_retried: (n) => `Retried ${n} time(s) automatically, still failing.`,
    nvr_cancelled_title: "Run cancelled", nvr_timeout_title: "Run timed out and was stopped",
    nvr_cancelled_sub: "You stopped this run. If the commands look right, run it again any time.",
    nvr_timeout_sub: "The run exceeded its time limit and was force-stopped; later steps did not run.",
    nvr_rerun: "Fixed — run again ▶", nvr_rerun2: "Run again ▶", nvr_edit_steps: "Edit steps", nvr_home: "Home",
    nvr_waiting: "waiting", nvr_notrun: "did not run", nvr_running_dur: "running…",
    nvr_retry_n: (n) => `${n} retr${n > 1 ? "ies" : "y"}`,
    nv_health_ok: "All good",
    nv_health_line: (n) => `${n} workflow${n > 1 ? "s" : ""} · no recent failures`,
    nv_health_bad_title: (n) => `${n} workflow${n > 1 ? "s" : ""} need${n > 1 ? "" : "s"} attention`,
    nv_health_bad_line: (id) => `${id}'s latest run failed · rerun with one click once fixed`,
    nv_fix: "Fix it →", nv_metrics_link: "Full metrics →",
    nv_run_btn: "▶ Run", nv_view_btn: "View →",
    nv_next_hints: "Next you could:", nv_hint_another: "create another workflow", nv_hint_expert: "switch to expert mode for everything else",
    nv_never_ran: "hasn't run yet", nv_last_run: (s) => `last run ${s}`,
    nv_enabled: "enabled",
    nv_run_now: "▶ Run now", nv_more: "More ⋯", nv_more_title: "Pause / duplicate / delete and more",
    nv_more_q: (id) => `What to do with ${id}?`, nv_more_expert: "Edit in expert mode",
    nv_steps_h: "Steps", nv_steps_hint: "(called “tasks” in expert mode)",
    nv_edit: "Edit", nv_add_step: "+ Add step",
    nv_edit_step_title: (id) => `Edit step ${id}`, nv_del_step: "Delete this step",
    nv_recent_h: "Recent runs", nv_view_log: "View log →",
    nv_notify_h: "Failure alerts", nv_notify_toggle: "Notify me on failure",
    nv_notify_hint: "Paste an incoming-webhook URL (Slack / Feishu / DingTalk auto-detected). A message is sent when a run fails.",
    nv_adv_summary: "More settings (concurrency, retries, timeouts…)",
    nv_adv_body: (r, m) => `The defaults are enough to start: ${r > 0 ? `${r} automatic retr${r > 1 ? "ies" : "y"} on failure, ` : ""}at most ${m} run${m > 1 ? "s" : ""} at a time.`,
    nv_adv_body2: "Expand when you need fine control, or", nv_adv_body3: "for every setting.", nv_to_expert: "switch to expert mode",
    nv_fail_ribbon: (n, id) => `Step ${n} ${id} failed`, nv_fail_ribbon_generic: "The latest run failed",
    nv_see_log: "view log",
    nv_step_extract: "Extract data", nv_step_transform: "Transform", nv_step_load: "Load results",
    nv_step_fetch: "Fetch data", nv_step_render: "Render & send report", nv_step_1: "First step",
  },
};
const STATE = {
  zh: { success: "成功", failed: "失败", running: "运行中", queued: "排队", scheduled: "待运行", up_for_retry: "重试中", upstream_failed: "上游失败", skipped: "跳过", cancelled: "已取消", timed_out: "超时", "": "未运行", none: "未运行" },
  en: { success: "success", failed: "failed", running: "running", queued: "queued", scheduled: "scheduled", up_for_retry: "retrying", upstream_failed: "upstream failed", skipped: "skipped", cancelled: "cancelled", timed_out: "timed out", "": "no runs", none: "no runs" },
};
const TYPEL = {
  zh: { schedule: "定时", manual: "手动", dependency: "依赖", event: "事件", backfill: "回填", subdag: "子流程" },
  en: { schedule: "scheduled", manual: "manual", dependency: "dependency", event: "event", backfill: "backfill", subdag: "sub-workflow" },
};
function t(k, ...a) { const v = (DICT[lang][k] ?? DICT.zh[k] ?? k); return typeof v === "function" ? v(...a) : v; }
const stateLabel = (s) => STATE[lang][s] ?? STATE.zh[s] ?? s;
const typeLabel = (s) => TYPEL[lang][s] ?? s;
// next_schedule label from backend ("paused"/"due"/"in Nm"/"—"/date) -> localized
function nextLabel(s) {
  if (s === "paused") return t("nx_paused");
  if (s === "due") return t("nx_due");
  const m = /^in (\d+)m$/.exec(s);
  if (m) return t("nx_in", m[1]);
  return s; // "—" or absolute date
}
function descLabel(d) { return d === "manual trigger" ? t("manual_trigger") : d; }
const ID_RE = /^[A-Za-z0-9][A-Za-z0-9_.-]*$/;

// ---- helpers ----
async function api(path, opts) {
  const r = await fetch(path, opts);
  if (!r.ok) {
    // Prefer the machine code (localizable) over the raw English error string;
    // unknown codes fall back to the server's message so nothing is lost.
    let m = r.statusText, code = "";
    try { const b = await r.json(); m = b.error || m; code = b.code || ""; } catch (_) {}
    if (code) { const loc = t("err_code_" + code); if (loc !== "err_code_" + code) m = loc; }
    const err = new Error(m); err.status = r.status; err.code = code;
    // session expired mid-use → bounce to login (not during the auth calls themselves)
    if (r.status === 401 && authUser && !path.startsWith("/api/login") && !path.startsWith("/api/me")) {
      authUser = null; showLogin(true);
    }
    throw err;
  }
  const ct = r.headers.get("content-type") || "";
  return ct.includes("json") ? r.json() : r.text();
}
const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
// copySpan: a click-to-copy value (handled by the delegated [data-copy] listener
// in boot.js). Keyboard-activatable via the global Enter/Space delegation. An
// aria-label conveys the copy action to screen readers (role=button otherwise
// announces only the value); title (defaults to the copy hint) can carry the full
// value for a truncated cell.
function copySpan(text, cls, titleText) {
  const title = titleText || t("copy_hint");
  return `<span class="copyable ${cls || ""}" data-copy="${esc(text)}" role="button" tabindex="0" title="${esc(title)}" aria-label="${t("copy_hint")}: ${esc(text)}">${esc(text)}</span>`;
}
// copyText: clipboard write that works in INSECURE contexts too. navigator.clipboard
// is undefined on any non-localhost http:// origin (the console's real topology),
// so fall back to a hidden textarea + execCommand. Resolves to whether it copied.
function copyText(text) {
  if (navigator.clipboard && window.isSecureContext) {
    return navigator.clipboard.writeText(text).then(() => true, () => legacyCopy(text));
  }
  return Promise.resolve(legacyCopy(text));
}
function legacyCopy(text) {
  try {
    const ta = document.createElement("textarea");
    ta.value = text; ta.setAttribute("readonly", "");
    ta.style.cssText = "position:fixed;left:-9999px;opacity:0";
    document.body.appendChild(ta); ta.select();
    const ok = document.execCommand("copy");
    document.body.removeChild(ta);
    return ok;
  } catch (_) { return false; }
}
const fmt = (x) => (x ? new Date(x).toLocaleString() : "—");
// friendly short time for banner copy: 今天 08:12 / 昨天 08:12 / 8月7日 08:12 (en: today / yesterday / Aug 7)
function fmtDay(x) {
  if (!x) return "—";
  const d = new Date(x);
  if (isNaN(d)) return "—";
  const hm = d.toLocaleTimeString(lang === "zh" ? "zh-CN" : "en-US", { hour: "2-digit", minute: "2-digit", hour12: false });
  const key = (a) => a.getFullYear() * 10000 + a.getMonth() * 100 + a.getDate();
  const now = new Date(), yd = new Date(now); yd.setDate(yd.getDate() - 1);
  if (key(d) === key(now)) return `${t("day_today")} ${hm}`;
  if (key(d) === key(yd)) return `${t("day_yesterday")} ${hm}`;
  const md = lang === "zh" ? `${d.getMonth() + 1}月${d.getDate()}日` : d.toLocaleDateString("en-US", { month: "short", day: "numeric" });
  return `${md} ${hm}`;
}
// compact "how long ago" label (e.g. worker heartbeats): 刚刚 / 5s前 / 3m前 / 2h前;
// anything older than a day falls back to the friendly day format above.
function relTime(x) {
  if (!x) return "—";
  const d = new Date(x);
  if (isNaN(d)) return "—";
  const s = Math.max(0, Math.round((Date.now() - d) / 1000));
  if (s < 5) return t("rel_now");
  if (s < 60) return t("rel_ago", s + "s");
  if (s < 3600) return t("rel_ago", Math.floor(s / 60) + "m");
  if (s < 86400) return t("rel_ago", Math.floor(s / 3600) + "h");
  return fmtDay(x);
}

// ---- toast + in-app confirm (themed + bilingual; replaces native alert/confirm) ----
// kind: ok | fail | warn | info. Success/info auto-dismiss; errors persist until clicked.
function toast(msg, kind = "ok") {
  const host = $("toast-root"); if (!host) return;
  const el = document.createElement("div");
  el.className = "toast t-" + kind;
  el.setAttribute("role", kind === "fail" ? "alert" : "status");
  el.textContent = msg;
  const dismiss = () => { el.classList.remove("in"); setTimeout(() => el.remove(), 220); };
  el.onclick = dismiss;
  host.appendChild(el);
  requestAnimationFrame(() => el.classList.add("in"));
  if (kind !== "fail") setTimeout(dismiss, 3200);
}
// Promise<bool> confirm dialog reusing the .overlay/.modal markup. Escape=cancel,
// Enter=confirm, click-outside=cancel. opts: {danger, okLabel}.
function confirmDialog(title, body, opts = {}) {
  return new Promise((resolve) => {
    const root = $("modal-root");
    root.innerHTML = `<div class="overlay" id="cfm-ovl"><div class="modal confirm" role="dialog" aria-modal="true" aria-label="${esc(title)}">
      <h2>${esc(title)}</h2>
      <div class="body">${body ? `<p class="cfm-body">${esc(body)}</p>` : ""}</div>
      <div class="foot"><button id="cfm-cancel">${esc(t("cancel_word"))}</button><button class="${opts.danger ? "danger" : "primary"}" id="cfm-ok">${esc(opts.okLabel || t("confirm_word"))}</button></div>
    </div></div>`;
    const close = (v) => { document.removeEventListener("keydown", onKey); root.innerHTML = ""; resolve(v); };
    const onKey = (e) => { if (e.key === "Escape") close(false); else if (e.key === "Enter") close(true); };
    document.addEventListener("keydown", onKey);
    $("cfm-cancel").onclick = () => close(false);
    $("cfm-ok").onclick = () => close(true);
    $("cfm-ovl").onclick = (e) => { if (e.target.id === "cfm-ovl") close(false); };
    $("cfm-ok").focus();
  });
}
// Promise<value|null> single-choice picker reusing the .overlay/.modal markup.
// options: [{value, label, danger}]. Escape / click-outside / Cancel resolve null.
function pickDialog(title, body, options) {
  return new Promise((resolve) => {
    const root = $("modal-root");
    const btns = options.map((o, i) => `<button class="${o.danger ? "danger" : (i === 0 ? "primary" : "")}" data-pick="${esc(String(o.value))}">${esc(o.label)}</button>`).join("");
    root.innerHTML = `<div class="overlay" id="pick-ovl"><div class="modal confirm" role="dialog" aria-modal="true" aria-label="${esc(title)}">
      <h2>${esc(title)}</h2>
      <div class="body">${body ? `<p class="cfm-body">${esc(body)}</p>` : ""}</div>
      <div class="foot pick-foot"><button id="pick-cancel">${esc(t("cancel_word"))}</button>${btns}</div>
    </div></div>`;
    const close = (v) => { document.removeEventListener("keydown", onKey); root.innerHTML = ""; resolve(v); };
    const onKey = (e) => { if (e.key === "Escape") close(null); };
    document.addEventListener("keydown", onKey);
    $("pick-cancel").onclick = () => close(null);
    root.querySelectorAll("[data-pick]").forEach((b) => b.onclick = () => close(b.dataset.pick));
    $("pick-ovl").onclick = (e) => { if (e.target.id === "pick-ovl") close(null); };
    const first = root.querySelector("[data-pick]"); if (first) first.focus();
  });
}
function dur(a, b) { if (!a) return "—"; const ms = (b ? new Date(b) : new Date()) - new Date(a); if (ms < 0) return "—"; const s = Math.round(ms / 1000); return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m${s % 60}s`; }
function badge(s, sm) { const k = s || "none"; return `<span class="badge${sm ? " sm" : ""} s-${k}"><span class="d"></span>${stateLabel(s)}</span>`; }
// ?-bubble with a rich HTML tip from the dict. pos: "" above-left, "r" above-right, "b" below
function hlp(key, pos) {
  const html = t(key);
  return `<span class="hlp${pos ? " hlp-" + pos : ""}" tabindex="0" aria-label="${esc(html.replace(/<[^>]+>/g, ""))}">?<span class="tip" aria-hidden="true">${html}</span></span>`;
}
let logESAll = []; // extra streams opened by the all-tasks combined log view
function closeLog() {
  if (logES) { logES.close(); logES = null; }
  logESAll.forEach((es) => { try { es.close(); } catch (_) {} });
  logESAll = [];
}
// human label for a seconds threshold (SLA / timeout); 0 => "off".
function secsLabel(sec) {
  sec = +sec || 0;
  if (sec <= 0) return t("set_off");
  if (sec < 60) return sec + "s";
  if (sec < 3600) return Math.floor(sec / 60) + "m" + (sec % 60 ? (sec % 60) + "s" : "");
  const h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60);
  return h + "h" + (m ? m + "m" : "");
}
// compact duration label from a millisecond count (dashboard sparkline + activity)
function fmtMs(ms) { if (!ms || ms < 0) return "—"; if (ms < 1000) return `${ms}ms`; const s = Math.round(ms / 1000); return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m${s % 60}s`; }
function sparkline(points, scaleMs) {
  // points: [{state, ms}] oldest→newest (tolerates legacy [string]). Height now
  // HONESTLY encodes real run duration for finished runs (taller = longer). We
  // scale against a DASHBOARD-WIDE max (scaleMs, passed by renderDags) so "taller
  // = slower" reads consistently across DAGs; no-run/skipped stay short stubs and
  // running/queued (no duration yet) get a neutral mid bar — never a fabricated one.
  const arr = (points || []).slice(-14).map((p) => (typeof p === "string" ? { state: p, ms: 0 } : p));
  while (arr.length < 14) arr.unshift({ state: "noruns", ms: 0 });
  const maxMs = Math.max(1, scaleMs || 0, ...arr.map((p) => p.ms || 0));
  const LO = 6, HI = 22;
  return `<div class="spark">${arr.map((p) => {
    const k = p.state || "noruns", stub = k === "noruns" || k === "skipped";
    let h;
    if (stub) h = LO;
    else if (p.ms > 0) h = Math.round(LO + (p.ms / maxMs) * (HI - LO)); // duration-scaled
    else h = 15; // running/queued: active, duration unknown — neutral, not fabricated
    const label = k === "noruns" ? stateLabel("none") : (p.ms > 0 ? `${stateLabel(k)} · ${fmtMs(p.ms)}` : stateLabel(k));
    return `<i class="${esc(k)}" style="height:${h}px" title="${esc(label)}"></i>`;
  }).join("")}</div>`;
}

// ---- graph ----
// [fill, stroke] for a task/run state, single-sourced from the theme vars (via
// color-mix) so the graph re-themes live. Injected into the node rect's inline
// `style` — only literal token strings here, never user data.
function colorForState(s) {
  const tint = (v, p) => [`color-mix(in srgb, var(${v}) ${p}%, transparent)`, `var(${v})`];
  const m = {
    success: tint("--ok", 15), failed: tint("--fail", 16), running: tint("--run", 16),
    up_for_retry: tint("--warn", 16), queued: tint("--warn", 12), scheduled: tint("--warn", 10),
    upstream_failed: tint("--upstream", 12), skipped: tint("--skip", 18), cancelled: tint("--skip", 22),
    timed_out: tint("--fail", 20),
  };
  return m[s] || ["var(--panel-2)", "var(--line-2)"]; // neutral: follows theme
}
function renderGraph(tasks, stateByTask, opts) {
  opts = opts || {};
  if (!tasks || !tasks.length) return `<div class="empty">—</div>`;
  // Decorations for cross-DAG waits: a task carrying depends_on_dag gets a small
  // dashed "external DAG" stub node feeding it (read-only, layout-only — the
  // stub id never leaks into the real model). Stubs are shared per target dag.
  const EXT = "__ext__";
  const list = tasks.map((t2) => ({ ...t2, deps: (t2.deps || []).slice() }));
  const stubs = {};
  const extEdges = new Set(); // "from|to" pairs drawn dashed (external waits)
  list.forEach((t2) => {
    const ext = t2.dod || (t2.depends_on_dag && t2.depends_on_dag.dag) || "";
    if (!ext) return;
    const sid = EXT + ext;
    stubs[sid] ||= { id: sid, deps: [], extDag: ext };
    t2.deps.push(sid);
    extEdges.add(sid + "|" + t2.id);
  });
  const all = [...Object.values(stubs), ...list];
  const byId = {}; all.forEach((t2) => byId[t2.id] = t2);
  const level = {};
  const lvl = (id, seen) => { if (level[id] != null) return level[id]; if (seen.has(id)) return 0; seen.add(id); const deps = (byId[id]?.deps || []).filter((d) => byId[d]); return level[id] = deps.length ? 1 + Math.max(...deps.map((d) => lvl(d, seen))) : 0; };
  all.forEach((t2) => lvl(t2.id, new Set()));
  const cols = {}; all.forEach((t2) => (cols[level[t2.id]] ||= []).push(t2.id));
  const NW = 150, NH = 36, CG = 200, RG = 52, PAD = 16, pos = {};
  Object.keys(cols).forEach((L) => cols[L].forEach((id, i) => pos[id] = { x: PAD + L * CG, y: PAD + i * RG }));
  const maxL = Math.max(...Object.keys(cols).map(Number)), maxR = Math.max(...Object.values(cols).map((c) => c.length));
  const W = PAD * 2 + maxL * CG + NW, H = PAD * 2 + (maxR - 1) * RG + NH;
  let edges = "", nodes = "";
  all.forEach((t2) => (t2.deps || []).forEach((d) => {
    if (!pos[d]) return;
    const x1 = pos[d].x + NW, y1 = pos[d].y + NH / 2, x2 = pos[t2.id].x, y2 = pos[t2.id].y + NH / 2, mx = (x1 + x2) / 2;
    const dPath = `M${x1} ${y1} C ${mx} ${y1}, ${mx} ${y2}, ${x2} ${y2}`;
    const isExt = extEdges.has(d + "|" + t2.id);
    // editable graphs get a per-edge group with a wide invisible hit path so a
    // dependency can be selected/removed by pointer or keyboard.
    const hit = opts.editable && !isExt
      ? `<path class="edge-hit" d="${dPath}" tabindex="0" role="button" aria-label="${esc(t("ge_edge_aria", d, t2.id))}"/>` : "";
    edges += `<g class="edge-g${isExt ? " ext" : ""}" data-efrom="${esc(d)}" data-eto="${esc(t2.id)}"><path class="graph-edge" d="${dPath}"/>${hit}</g>`;
  }));
  all.forEach((t2) => {
    const p = pos[t2.id];
    if (t2.extDag) { // external-DAG stub: dashed, decorative, never interactive
      nodes += `<g class="graph-node ext" aria-hidden="true"><rect x="${p.x}" y="${p.y + 4}" width="${NW}" height="${NH - 8}" rx="8" style="fill:var(--panel-2);stroke:var(--line-2)" stroke-width="1.2" stroke-dasharray="4 4"/><text x="${p.x + NW / 2}" y="${p.y + NH / 2 + 4}" text-anchor="middle">⌁ ${esc(t2.extDag.length > 16 ? t2.extDag.slice(0, 15) + "…" : t2.extDag)}</text></g>`;
      return;
    }
    let [f, st] = colorForState(stateByTask ? stateByTask[t2.id] : null);
    let sw = 1.2;
    if (opts.pending === t2.id) { st = "var(--accent)"; sw = 2.6; }
    const dash = opts.dashed && opts.dashed.has(t2.id) ? ` stroke-dasharray="5 4"` : "";
    // tag: expose data-node for live patching without making the node clickable
    const clickable = opts.editable || opts.clickable;
    const attrs = clickable
      ? ` data-node="${esc(t2.id)}" style="cursor:pointer"${opts.editable ? ` tabindex="0" role="button" aria-label="${esc(t("ge_node_aria", t2.id))}"` : ""}`
      : (opts.tag ? ` data-node="${esc(t2.id)}"` : "");
    const cls = "graph-node" + (opts.tag && stateByTask && stateByTask[t2.id] === "running" ? " g-running" : "") + (t2.subdag ? " subdag" : "");
    // subdag task: double border + ⧉ marker + the target dag id as a subtitle
    const inner = t2.subdag ? `<rect class="g-inner" x="${p.x + 3}" y="${p.y + 3}" width="${NW - 6}" height="${NH - 6}" rx="5.5" style="stroke:${st}" stroke-width="1"/>` : "";
    const label = t2.subdag
      ? `<text x="${p.x + NW / 2}" y="${p.y + NH / 2 - 1}" text-anchor="middle">⧉ ${esc(t2.id)}</text><text class="g-sub" x="${p.x + NW / 2}" y="${p.y + NH / 2 + 11}" text-anchor="middle">${esc(t2.subdag.length > 20 ? t2.subdag.slice(0, 19) + "…" : t2.subdag)}</text>`
      : `<text x="${p.x + NW / 2}" y="${p.y + NH / 2 + 4}" text-anchor="middle">${esc(t2.id)}</text>`;
    // editable nodes carry a connector handle on the right edge: edge-drag starts
    // ONLY here, so a plain node-body drag still pans (and touch pinch survives)
    const handle = opts.editable
      ? `<circle class="gh-hit" cx="${p.x + NW}" cy="${p.y + NH / 2}" r="11" data-ghandle="${esc(t2.id)}"/><circle class="gh" cx="${p.x + NW}" cy="${p.y + NH / 2}" r="5" data-ghandle="${esc(t2.id)}" aria-hidden="true"/>` : "";
    // fill/stroke via inline style (SVG presentation attributes don't resolve color-mix reliably)
    nodes += `<g class="${cls}"${attrs}><rect x="${p.x}" y="${p.y}" width="${NW}" height="${NH}" rx="8" style="fill:${f};stroke:${st}" stroke-width="${sw}"${dash}/>${inner}${label}${handle}</g>`;
  });
  // compact height for small graphs; capped so a big graph pans/zooms instead of
  // dominating the page. attachPanZoom() wires drag-pan + ctrl/⌘-wheel zoom.
  const wrapH = Math.min(H + 24, 460);
  const zoom = `<div class="graph-zoom" aria-hidden="false">
    <button class="gz" data-z="in" aria-label="${t("gz_in")}" title="${t("gz_in")}">+</button>
    <button class="gz" data-z="fit" aria-label="${t("gz_fit")}" title="${esc(t("gz_hint"))}">⤢</button>
    <button class="gz" data-z="out" aria-label="${t("gz_out")}" title="${t("gz_out")}">−</button></div>`;
  return `<div class="graph-wrap" style="height:${wrapH}px" title="${esc(t("gz_hint"))}"><svg class="graph-svg" width="${W}" height="${H}" viewBox="0 0 ${W} ${H}">${edges}${nodes}</svg>${zoom}</div>`;
}
// Pan/zoom controller for a .graph-wrap. Operates via a CSS transform on the
// inner <svg> (vectors stay crisp; node click handlers + live rect-fill patching
// are untouched). Idempotent per element. Never traps plain page scroll — wheel
// zoom requires Ctrl/⌘. A drag past a small threshold suppresses the trailing
// click, so panning over a node doesn't activate it.
//   store: optional {s,tx,ty} holder that survives the element (e.g. the editable
//   graph is destroyed + rebuilt on every dependency edit). When it carries a
//   saved view we reseed from it; otherwise a graph larger than its box auto-fits
//   on attach (the old overflow:auto used to expose oversized graphs via scroll).
function attachPanZoom(wrap, store) {
  if (!wrap || wrap.dataset.pz) return; wrap.dataset.pz = "1";
  const svg = wrap.querySelector("svg"); if (!svg) return;
  const cw = svg.viewBox.baseVal.width, ch = svg.viewBox.baseVal.height; // content bounds
  const MIN = 0.25, MAX = 4;
  const seeded = store && store.s != null;
  let s = seeded ? store.s : 1, tx = seeded ? store.tx : 0, ty = seeded ? store.ty : 0;
  // viewport size, robust to a transient 0 (pre-layout / offscreen reflow)
  const vpW = () => wrap.clientWidth || wrap.getBoundingClientRect().width || 0;
  const vpH = () => wrap.clientHeight || wrap.getBoundingClientRect().height || 0;
  const apply = () => {
    svg.style.transform = `translate(${tx.toFixed(2)}px,${ty.toFixed(2)}px) scale(${s.toFixed(4)})`;
    if (store) { store.s = s; store.tx = tx; store.ty = ty; } // persist across re-render
  };
  const clamp = () => { // keep a margin of content on-screen so it can't be lost
    const vw = vpW(), vh = vpH(), m = 44;
    if (vw < 10 || vh < 10) return; // not laid out — don't clamp against garbage
    tx = Math.min(vw - m, Math.max(m - cw * s, tx));
    ty = Math.min(vh - m, Math.max(m - ch * s, ty));
  };
  const zoomAt = (px, py, ns) => {
    ns = Math.min(MAX, Math.max(MIN, ns));
    tx = px - (px - tx) * (ns / s); ty = py - (py - ty) * (ns / s);
    s = ns; clamp(); apply();
  };
  const fit = () => {
    const vw = vpW(), vh = vpH(), pad = 18;
    if (vw < 10 || vh < 10) return; // keep current view rather than compute a garbage scale
    s = Math.min(1, Math.max(MIN, Math.min((vw - pad * 2) / cw, (vh - pad * 2) / ch))); // never enlarge past natural
    tx = (vw - cw * s) / 2; ty = (vh - ch * s) / 2; apply();
  };
  wrap.addEventListener("wheel", (e) => {
    if (!(e.ctrlKey || e.metaKey)) return; // plain wheel → page scrolls normally
    e.preventDefault();
    const r = wrap.getBoundingClientRect();
    zoomAt(e.clientX - r.left, e.clientY - r.top, s * (e.deltaY < 0 ? 1.12 : 1 / 1.12));
  }, { passive: false });
  let drag = false, moved = false, sx = 0, sy = 0, otx = 0, oty = 0;
  // touch: track active pointers so two fingers pinch-zoom (mobile has no
  // Ctrl+wheel); a second finger cancels the pan and hands over to the pinch.
  const touches = new Map(); // pointerId -> {x, y}
  let pinchDist = 0;
  wrap.addEventListener("pointerdown", (e) => {
    if (e.pointerType !== "touch" && e.button !== 0) return;
    wrap._sup = 0; // any fresh press clears a stale suppress-latch (e.g. drag released off-element)
    if (e.target.closest(".graph-zoom")) return; // let the zoom buttons handle themselves
    if (e.pointerType === "touch") {
      touches.set(e.pointerId, { x: e.clientX, y: e.clientY });
      if (touches.size === 2) {
        drag = false; wrap.classList.remove("panning");
        const [a, b] = [...touches.values()];
        pinchDist = Math.hypot(a.x - b.x, a.y - b.y);
        wrap._sup = 1; // the pinch must not synthesize a node click
        return;
      }
    }
    drag = true; moved = false; sx = e.clientX; sy = e.clientY; otx = tx; oty = ty;
    try { wrap.setPointerCapture(e.pointerId); } catch (_) {}
  });
  wrap.addEventListener("pointermove", (e) => {
    if (e.pointerType === "touch" && touches.has(e.pointerId)) {
      touches.set(e.pointerId, { x: e.clientX, y: e.clientY });
      if (touches.size === 2) {
        const [a, b] = [...touches.values()];
        const d = Math.hypot(a.x - b.x, a.y - b.y);
        if (pinchDist > 0 && d > 0) {
          const r = wrap.getBoundingClientRect();
          zoomAt((a.x + b.x) / 2 - r.left, (a.y + b.y) / 2 - r.top, s * (d / pinchDist));
        }
        pinchDist = d;
        return;
      }
    }
    if (!drag) return;
    const dx = e.clientX - sx, dy = e.clientY - sy;
    if (!moved && Math.hypot(dx, dy) < 4) return; // below threshold: still a click
    moved = true; wrap.classList.add("panning");
    tx = otx + dx; ty = oty + dy; clamp(); apply();
  });
  const end = (e) => {
    if (e && e.pointerType === "touch") { touches.delete(e.pointerId); if (touches.size < 2) pinchDist = 0; }
    if (!drag) return;
    drag = false; wrap.classList.remove("panning"); if (moved) wrap._sup = 1;
  };
  wrap.addEventListener("pointerup", end);
  wrap.addEventListener("pointercancel", end);
  // capture-phase: swallow the click synthesized after a pan so node handlers don't
  // fire — but never suppress the zoom controls (a keyboard/mouse zoom-button click
  // has no preceding wrap pointerdown, so it must not be eaten by a stale latch).
  wrap.addEventListener("click", (e) => {
    if (e.target.closest(".graph-zoom")) return;
    if (wrap._sup) { e.stopPropagation(); e.preventDefault(); wrap._sup = 0; }
  }, true);
  wrap.querySelector(".graph-zoom").addEventListener("click", (e) => {
    const b = e.target.closest("[data-z]"); if (!b) return;
    e.stopPropagation();
    const vw = vpW(), vh = vpH();
    if (b.dataset.z === "in") zoomAt(vw / 2, vh / 2, s * 1.25);
    else if (b.dataset.z === "out") zoomAt(vw / 2, vh / 2, s / 1.25);
    else fit();
  });
  apply(); // identity (or reseeded view) — avoids a first-frame flash
  if (!seeded) {
    // frame an oversized graph once layout is known (clientWidth is 0 synchronously)
    requestAnimationFrame(() => {
      const vw = vpW(), vh = vpH();
      if (vw >= 10 && vh >= 10 && (cw > vw + 1 || ch > vh + 1)) fit();
    });
  }
}

// ---- sidebar/topbar ----
// ---- hash routing: every drill-down is linkable and refresh-safe ----
let suppressHash = false;
function resetMainScroll() {
  if (main) main.scrollTop = 0;
}
function finishRouteRender() {
  resetMainScroll();
  requestAnimationFrame(resetMainScroll);
}
// replace=true rewrites the current entry (no new history entry, no hashchange) —
// use it for in-page normalization (e.g. tab canonicalization) so Back isn't trapped.
function setHash(h, replace) {
  if (location.hash === h) return;
  resetMainScroll();
  if (replace) { history.replaceState(null, "", h); return; }
  suppressHash = true; location.hash = h;
}
function applyRoute() {
  if (typeof closeConnMenu === "function") closeConnMenu(); // dismiss any open popover on navigation
  const seg = location.hash.replace(/^#\/?/, "").split("/").map(decodeURIComponent).filter(Boolean);
  if (!seg.length || seg[0] === "dags") return loadDags();
  if (seg[0] === "new") return showWizard();
  if (seg[0] === "pools") return showPools();
  if (seg[0] === "resources") return showResources();
  if (seg[0] === "graph") return showGraph();
  if (seg[0] === "audit") return showAudit();
  if (seg[0] === "workers") return showWorkers();
  if (seg[0] === "api") return showApi();
  if (seg[0] === "run" && seg[1]) return showRun(seg[1]);
  if (seg[0] === "dag" && seg[1] && seg[2] === "task" && seg[3]) {
    return showDag(seg[1]).then(() => { if (D && D.dag.dag_id === seg[1] && D.tasks.some((x) => x.id === seg[3])) showTask(seg[1], seg[3]); });
  }
  if (seg[0] === "dag" && seg[1]) return showDag(seg[1], seg[2]); // seg[2]: runs|structure|settings (optional)
  return loadDags();
}
window.addEventListener("hashchange", () => {
  if (suppressHash) { suppressHash = false; return; }
  resetMainScroll();
  Promise.resolve(applyRoute()).then(finishRouteRender).catch(() => {});
});

// ---- global quick-jump: the topbar search doubles as a "jump to any DAG" box
// (an autocomplete dropdown), available on every page — not just a dashboard filter.
let jumpDags = [], jumpSel = -1;
async function ensureJumpDags() {
  if (overviewCache && overviewCache.dags) { jumpDags = overviewCache.dags.map((d) => d.dag_id); return; }
  if (jumpDags.length) return;
  try { jumpDags = (await api("/api/dags")).map((d) => d.dag_id); } catch (_) {}
}
function hlMatch(id, q) {
  const i = id.toLowerCase().indexOf(q);
  if (i < 0) return esc(id);
  return esc(id.slice(0, i)) + `<b>${esc(id.slice(i, i + q.length))}</b>` + esc(id.slice(i + q.length));
}
function updateJump(raw) {
  const menu = $("jump-menu"); if (!menu) return;
  const q = raw.trim().toLowerCase();
  if (overviewCache && overviewCache.dags) jumpDags = overviewCache.dags.map((d) => d.dag_id); // freshest
  const setExpanded = (v) => $("search").setAttribute("aria-expanded", v);
  const clearAD = () => $("search").removeAttribute("aria-activedescendant");
  if (!q) { menu.hidden = true; menu.innerHTML = ""; jumpSel = -1; setExpanded("false"); clearAD(); return; }
  const matches = jumpDags.filter((id) => id.toLowerCase().includes(q)).slice(0, 8);
  menu.hidden = false; setExpanded("true");
  if (!matches.length) { menu.innerHTML = `<div class="jump-empty">${t("jump_none")}</div>`; jumpSel = -1; clearAD(); return; }
  jumpSel = 0;
  menu.innerHTML = matches.map((id, i) => `<div class="jump-item ${i === 0 ? "sel" : ""}" id="jump-opt-${i}" data-jump="${esc(id)}" role="option" aria-selected="${i === 0}"><span class="mono">${hlMatch(id, q)}</span><span class="jump-open">${t("jump_open")} →</span></div>`).join("");
  menu.querySelectorAll("[data-jump]").forEach((it) => it.onmousedown = (e) => { e.preventDefault(); jumpTo(it.dataset.jump); }); // mousedown beats blur
  $("search").setAttribute("aria-activedescendant", "jump-opt-0"); // SR announces the active option
}
function jumpMove(delta) {
  const menu = $("jump-menu"); if (!menu || menu.hidden) return;
  const items = [...menu.querySelectorAll(".jump-item")]; if (!items.length) return;
  jumpSel = (jumpSel + delta + items.length) % items.length;
  items.forEach((it, i) => { it.classList.toggle("sel", i === jumpSel); it.setAttribute("aria-selected", i === jumpSel); });
  $("search").setAttribute("aria-activedescendant", "jump-opt-" + jumpSel);
  items[jumpSel].scrollIntoView({ block: "nearest" });
}
function jumpEnter() {
  const menu = $("jump-menu"); if (!menu || menu.hidden) return false;
  const sel = menu.querySelectorAll(".jump-item")[jumpSel];
  if (sel) { jumpTo(sel.dataset.jump); return true; }
  return false;
}
function jumpTo(dagID) { closeJump(); const s = $("search"); s.value = ""; query = ""; if (view === "dags") renderDags(); showDag(dagID); }
function closeJump() { const m = $("jump-menu"); if (m) { m.hidden = true; m.innerHTML = ""; jumpSel = -1; } $("search").setAttribute("aria-expanded", "false"); $("search").removeAttribute("aria-activedescendant"); }

let serverTZ = "";
async function loadInfo() { try { const i = await api("/api/info"); serverTZ = i.tz || ""; $("f-exec").textContent = i.executor || "—"; $("f-tick").textContent = "tick " + (i.tick || "—"); $("tick").textContent = "tick " + (i.tick || "—"); const v = $("side-version"); if (v) v.textContent = "scheduler " + (i.version || "dev"); const z = $("tzlab"); if (z) { z.textContent = serverTZ; z.title = t("tz_note"); } } catch (_) {} }
// ---- auth: login gate + user chip ----
// Resolve who we are. /api/me is 200 (authed, or auth-disabled → implicit admin)
// or 401 (login required). Returns whether the app may start.
async function initAuth() {
  try { authUser = await api("/api/me"); }
  catch (e) {
    if (e.status === 401) { showLogin(false); return false; }
    authUser = { role: "admin", auth: false }; // transient error: don't hard-block
    return true;
  }
  document.body.dataset.role = authUser.role || "admin";
  renderUserChip();
  return true;
}
function renderUserChip() {
  const el = $("user-chip"); if (!el) return;
  if (!authUser || authUser.auth === false || !authUser.username) { el.hidden = true; el.innerHTML = ""; return; }
  const roleLbl = authUser.role === "viewer" ? t("role_viewer") : t("role_admin");
  el.hidden = false;
  el.innerHTML = `<div class="uc-id"><span class="uc-name">${esc(authUser.username)}</span><span class="uc-role">${roleLbl}</span></div><button class="uc-logout" id="uc-logout">${t("logout")}</button>`;
  $("uc-logout").onclick = doLogout;
  const nd = $("newdag"); if (nd) nd.style.display = authUser.role === "admin" ? "" : "none"; // hide write CTA for viewers
}
async function doLogout() {
  try { await api("/api/logout", { method: "POST" }); } catch (_) {}
  authUser = null; showLogin(false);
}
function showLogin(expired) {
  const root = $("login-root"); if (!root) return;
  root.innerHTML = `
    <div class="login-overlay">
      <form class="login-card" id="login-form" novalidate>
        <div class="login-logo"><span class="logo" style="width:22px;height:22px;font-size:13px">c</span> cronova</div>
        <div class="login-h">${t("login_title")}</div>
        <div class="login-sub">${expired ? t("sess_expired") : t("login_sub")}</div>
        <label class="login-lbl">${t("login_user")}<input id="login-user" autocomplete="username"></label>
        <label class="login-lbl">${t("login_pass")}<input id="login-pass" type="password" autocomplete="current-password"></label>
        <div class="login-err" id="login-err" hidden></div>
        <button class="primary login-submit" type="submit">${t("login_btn")}</button>
      </form>
    </div>`;
  const form = $("login-form"), errEl = $("login-err"), btn = form.querySelector("button");
  form.onsubmit = async (e) => {
    e.preventDefault(); btn.disabled = true; errEl.hidden = true;
    try {
      await api("/api/login", { method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ username: $("login-user").value, password: $("login-pass").value }) });
      root.innerHTML = "";
      if (await initAuth()) startApp();
    } catch (err) {
      errEl.textContent = err.status === 401 ? t("login_bad") : (err.message || t("api_err"));
      errEl.hidden = false; btn.disabled = false;
      $("login-pass").value = ""; $("login-pass").focus();
    }
  };
  $("login-user").focus();
}
// startApp runs the normal boot once we know the user is authorized.
async function startApp() {
  await resolveMode();
  loadInfo();
  Promise.resolve(applyRoute()).catch((e) => { main.innerHTML = `<div class="empty err">${t("api_err")}: ${esc(e.message)}</div>`; });
}

// ---- novice/expert mode ----
// Effective mode: saved preference, else inferred once per boot from the store
// (empty instance => novice onboarding; existing DAGs => expert, so upgrading
// never flips a working console). Inference isn't persisted — only a toggle click is.
async function resolveMode() {
  const saved = localStorage.getItem("cnv_mode");
  if (saved === "novice" || saved === "expert") uiMode = saved;
  else {
    try { overviewCache = await api("/api/overview"); uiMode = overviewCache.stats.total_dags > 0 ? "expert" : "novice"; }
    catch (_) { uiMode = "expert"; }
  }
  document.documentElement.dataset.mode = uiMode;
  syncModeChrome();
}
function setMode(m) {
  if (uiMode === m) return;
  uiMode = m; localStorage.setItem("cnv_mode", m);
  document.documentElement.dataset.mode = m;
  syncModeChrome();
  Promise.resolve(applyRoute()).then(finishRouteRender).catch(() => {});
}
// sync the topbar chrome that depends on mode: segmented toggle + primary CTA label
function syncModeChrome() {
  if (!uiMode) return;
  const tg = $("mode-toggle");
  if (tg) {
    tg.hidden = false;
    tg.title = t("mode_toggle_title");
    const nv = $("mode-nov"), ex = $("mode-exp");
    if (nv) { nv.classList.toggle("on", nvMode()); nv.setAttribute("aria-pressed", nvMode()); }
    if (ex) { ex.classList.toggle("on", !nvMode()); ex.setAttribute("aria-pressed", !nvMode()); }
  }
  const nd = $("newdag"); if (nd) nd.textContent = t(nvMode() ? "nv_newflow" : "newdag");
}
// display label for a step/task id — glosses the ids our own templates create,
// falls back to the raw id for anything user-authored (honest, no guessing)
const TASK_LABELS = { extract: "nv_step_extract", transform: "nv_step_transform", load: "nv_step_load", fetch: "nv_step_fetch", render: "nv_step_render", step_1: "nv_step_1" };
function nvTaskLabel(id) { return TASK_LABELS[id] ? t(TASK_LABELS[id]) : id; }
// stable topological order (Kahn) so novice screens can number steps 1..N;
// cycles can't occur in saved DAGs (validated), but tolerate them anyway.
function topoOrder(tasks) {
  const byId = {}; (tasks || []).forEach((tk) => byId[tk.id] = tk);
  const out = [], done = new Set();
  let progress = true;
  while (out.length < (tasks || []).length && progress) {
    progress = false;
    for (const tk of tasks) {
      if (done.has(tk.id)) continue;
      if ((tk.deps || []).every((d) => done.has(d) || !byId[d])) { out.push(tk); done.add(tk.id); progress = true; }
    }
  }
  for (const tk of tasks || []) if (!done.has(tk.id)) out.push(tk); // cycle fallback
  return out;
}
// plain-language schedule gloss for novice screens. Only shapes we're sure of
// ("M H * * *" daily cron, @every, empty); anything else shows the raw expression.
function nvSchedGloss(schedule) {
  const s = (schedule || "").trim();
  if (!s) return t("nv_gloss_manual");
  let m = /^(\d{1,2})\s+(\d{1,2})\s+\*\s+\*\s+\*$/.exec(s);
  if (m) return t("nv_gloss_daily", `${String(m[2]).padStart(2, "0")}:${String(m[1]).padStart(2, "0")}`);
  m = /^@every\s+(\d+)(s|m|h)$/.exec(s);
  if (m) return t("nv_gloss_every", m[1], t("unit_" + m[2]));
  return s;
}

// navKey highlights a sidebar item; crumb (optional) overrides the topbar breadcrumb text.
let lastNavLabel = null;
function setNav(navKey, crumb) {
  document.body.dataset.screen = view; // lets CSS hide chrome per screen (e.g. mode toggle on the wizard)
  document.querySelectorAll(".nav-item[data-nav]").forEach((n) => n.classList.toggle("active", n.dataset.nav === navKey));
  // novice mode names the same pages in its own words (共享配置 / 我的工作流)
  const label = crumb != null ? crumb : (navKey === "pools" ? "Pools" : navKey === "graph" ? t("graph_title") : navKey === "resources" ? t(nvMode() ? "nv_shared" : "nav_resources") : navKey === "audit" ? t("nav_audit") : navKey === "workers" ? t("nav_workers") : navKey === "api" ? t("nav_api") : t(nvMode() ? "nv_myflows" : "nav_dags"));
  $("crumb").textContent = label;
  // the topbar search only filters the dashboard list — hide it elsewhere.
  // search stays visible everywhere now (global jump-to-DAG), not just the dashboard
  // 120ms crossfade — only when actually navigating, never on a data refresh
  if (label !== lastNavLabel) {
    lastNavLabel = label;
    main.classList.remove("enter"); void main.offsetWidth; main.classList.add("enter");
  }
}

// fill static [data-i18n] / [data-i18n-ph] + lang button
function applyStaticI18n() {
  document.documentElement.lang = lang;
  document.querySelectorAll("[data-i18n]").forEach((e) => e.textContent = t(e.dataset.i18n));
  $("search").placeholder = t("search_ph");
  $("lang").textContent = lang === "zh" ? "EN" : "中";
  $("lang").setAttribute("aria-label", t("aria_lang"));
  $("theme").setAttribute("aria-label", t("aria_theme"));
  syncModeChrome(); // mode-dependent labels (CTA text, toggle title) re-localize too
}
function setLang(l) {
  lang = l; localStorage.setItem("cnv_lang", l); applyStaticI18n();
  renderUserChip(); // role label + logout button are built with t(), not data-i18n
  // dag/task re-render from in-memory D (no refetch) so unsaved edits survive.
  if (view === "dags") { setNav("dags"); renderDags(); } // crumb is localized now (工作流/Workflows) — refresh it too
  else if (view === "dag") renderDagPage();
  else if (view === "task") renderTaskPage();
  else if (view === "run") showRun(currentRun);
  else if (view === "wizard") renderWizard();
  else if (view === "pools") showPools();
  else if (view === "resources") renderResources(); // from in-memory RES, no refetch
  else if (view === "graph") showGraph();
  else if (view === "audit") renderAudit(); // from in-memory AUD — keeps filters + loaded pages
  else if (view === "workers") { setNav("workers"); renderWorkers(); } // crumb + table from in-memory WK, no refetch
  else if (view === "api") renderApi(); // from in-memory TOKENS, no refetch
}
