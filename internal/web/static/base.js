"use strict";

const $ = (id) => document.getElementById(id);
const main = $("main");

let view = "dags";       // dags | dag | task | run | pools | graph
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
    workspace: "工作区", newdag: "+ 新建 DAG", search_ph: "筛选 DAG…",
    f_all: "全部", f_running: "运行中", f_failed: "失败", f_paused: "已暂停",
    dags_sub: "所有工作流定义 · 开关 · 调度 · 查看最近运行",
    c_active: "活跃 DAG", c_running: "运行中 run", c_rate: "近期成功率", c_failed: "失败 DAG",
    c_active_s: (n) => `共 ${n} 个定义`, c_running_s: "across all pools", c_rate_s: "最近运行", c_failed_s: "最近一次失败",
    h_dag: "DAG", h_last: "最近运行", h_spark: "最近 14 次", h_pool: "POOL", h_next: "下次调度", h_act: "操作",
    no_match: "没有匹配的 DAG", no_dags_title: "还没有 DAG", no_dags_sub: "创建第一个工作流，开始调度任务。", trigger: "触发", manual_trigger: "手动触发",
    back_dags: "← DAGs", run_word: "run", sub_manual: "仅手动触发", max_active: "最大并发",
    run_progress: "进度",
    sec_graph: "依赖图", sec_structure: "结构", sec_runs: "运行历史", sec_instances: "任务实例",
    g_timeline: "时间线", g_never_ran: "未运行", run_no_tasks: "该运行暂无任务实例", run_done_ok: "运行成功完成", run_done_fail: "运行失败", run_done_timeout: "运行超时",
    run_cancel: "取消运行", run_retry: "重跑失败", task_retry: "重跑", run_cancelled_toast: "运行已取消", run_retried_toast: "已重新排队",
    task_mark: "标记状态", run_mark: "标记运行", mark_skip: "跳过", mark_done_toast: "已标记",
    mark_task_title: (id) => `标记任务“${id}”为?`, mark_task_body: "手动覆盖任务状态。运行中的任务会先被终止;标记成功/跳过会放行被它阻塞的下游。",
    mark_run_title: (id) => `标记运行“${id}”为?`, mark_run_body: "覆盖已结束运行的最终状态(不改动任务)。标记成功会触发下游 DAG。",
    confirm_cancel_title: (id) => `取消运行“${id}”?`, confirm_cancel_body: "正在运行的任务会被终止。", th_act: "操作",
    confirm_retry_title: (id) => `重跑“${id}”?`, confirm_retry_body: "该任务及其所有下游任务会被重置并重新运行。",
    copied: "已复制", copy_fail: "复制失败，请手动选择文本", copy_hint: "点击复制", search_ph: "跳转 / 筛选 DAG…", jump_open: "打开", jump_none: "无匹配 DAG",
    gz_in: "放大", gz_out: "缩小", gz_fit: "适应视图", gz_hint: "拖拽平移 · Ctrl/⌘+滚轮缩放",
    act_recent: "近期活动", act_now: "现在", act_none: "还没有运行记录",
    login_title: "登录 cronova", login_sub: "请输入你的账户凭据", login_user: "用户名", login_pass: "密码", login_btn: "登录", login_bad: "用户名或密码错误", logout: "登出", sess_expired: "会话已过期，请重新登录", role_admin: "管理员", role_viewer: "只读",
    tab_runs: "运行", tab_structure: "结构", tab_settings: "设置",
    dh_last: "上次运行", dh_next: "调度", dh_rate: "近期成功率", dh_never: "还没有运行", dh_norate: "—",
    set_done: "完成", set_edit: "编辑", set_none: "无", set_sched: "调度", set_max: "最大并发", set_retries: "默认重试", set_deps: "上游依赖",
    set_deps_hint: "上游 DAG 成功后自动触发本 DAG", set_no_deps_avail: "暂无其他 DAG 可选",
    set_notify: "通知", set_notify_hint: "运行结束后向 Webhook 发送 JSON（兼容 Slack/飞书/Discord）", notify_failure: "失败", notify_success: "成功", notify_off: "未选择事件", notify_need_url: "先填写 Webhook URL，再选择触发事件", err_notify_url: "通知 URL 必须以 http:// 或 https:// 开头",
    set_sla: "SLA（软）", set_sla_hint: "从 run 开始算，超时未完成即告警（继续运行）。0=关闭。需配置通知 Webhook。", set_timeout: "运行超时（硬）", set_timeout_hint: "从 run 开始算，超时则强制失败并杀掉运行中任务 → timed_out。0=关闭。", secs: "秒", set_off: "关闭",
    t_sla: "任务 SLA（秒）", t_sla_hint: "从 run 开始算，此任务超时未完成即告警。0=关闭。", t_timeout_hint: "单次执行超时即杀（秒）。0=不限。",
    danger_title: "危险操作", danger_del_hint: "归档此 DAG：不再调度，历史保留。",
    nd_more: "调度与更多选项", nd_less: "收起",
    nav_resources: "变量 & 连接", nav_audit: "审计", nav_api: "API",
    audit_sub: "运维操作记录:谁在何时对哪个 DAG/运行做了什么。", audit_empty: "暂无操作记录", au_time: "时间", au_actor: "操作人", au_action: "操作", au_target: "对象",
    act_trigger: "触发", act_cancel: "取消", act_retry_run: "重跑运行", act_retry_task: "重跑任务", act_mark_task: "标记任务", act_mark_run: "标记运行", act_save_dag: "保存 DAG", act_create_dag: "创建 DAG", act_delete_dag: "删除 DAG", act_pause: "暂停", act_unpause: "恢复", act_create_token: "创建 Token", act_delete_token: "撤销 Token",
    api_title: "API 与集成", api_sub: "把 cronova 的全部能力对接到你的平台。查看交互式 API 文档,并管理机器访问用的 API Token。",
    api_docs_h: "API 文档", api_docs_hint: "完整的 OpenAPI 参考,内置 curl / Go / Python / Java 示例,可在页面内切换语言。", api_open_docs: "打开 API 文档 →", api_spec_link: "OpenAPI 规范",
    tok_title: "API Tokens", tok_sub: "机器访问凭据。以 Authorization: Bearer <token> 调用 API。明文仅在创建时显示一次。",
    tok_name: "名称", tok_role: "角色", tok_prefix: "前缀", tok_created: "创建时间", tok_lastused: "最近使用", tok_never: "从未使用",
    tok_create: "创建 Token", tok_none: "还没有 Token", tok_name_ph: "如 ci-bot", tok_revoke: "撤销", tok_need_name: "请填写名称",
    tok_revoke_title: (n) => `撤销 Token“${n}”?`, tok_revoke_body: "撤销后使用该 Token 的调用会立即失败,且不可恢复。",
    tok_created_ok: "Token 已创建", tok_revoked: "Token 已撤销",
    tok_reveal_h: "你的新 API Token", tok_reveal_warn: "请立即复制并妥善保存 —— 关闭后将无法再次查看明文。", tok_copy: "复制", tok_done: "我已保存",
    role_admin_full: "管理员(读写)", role_viewer_ro: "只读(仅 GET)",
    res_vars: "变量", res_conns: "连接",
    res_sub: "跨任务共享的配置。命令里用 {{ var.KEY }} / {{ conn.ID.字段 }} 引用，触发时用 {{ params.KEY }}。",
    v_key: "变量名", v_value: "值", v_add: "添加变量", v_none: "还没有变量", v_save: "保存",
    c_id: "连接 ID", c_type: "类型", c_host: "主机", c_port: "端口", c_login: "用户名", c_password: "密码", c_extra: "额外(JSON)",
    c_add: "新建连接", c_edit: "编辑连接", c_none: "还没有连接", c_pw_set: "已设置", c_pw_none: "未设置", c_pw_keep: "留空则不修改",
    c_del_title: (id) => `删除连接“${id}”?`, v_del_title: (k) => `删除变量“${k}”?`, del_body: "此操作不可撤销。",
    trig_params: "带参数触发", p_params: "参数", p_add: "加一行", p_key: "键", p_val: "值", p_trigger: "触发", p_hint: "参数注入为 CRONOVA_PARAM_* 环境变量，命令里用 {{ params.键 }} 引用。",
    run_params: "参数", res_saved: "已保存", res_deleted: "已删除", err_key: "无效名称（仅限字母、数字、_ . -）",
    btn_trigger: "▶ 触发运行", btn_pause: "暂停", btn_resume: "恢复", btn_delete: "删除",
    confirm_del_dag_title: (id) => `删除 DAG “${id}”？`,
    confirm_del_dag_body: "它将被归档(从列表隐藏),运行历史保留,之后可恢复。",
    dag_archived: "该 DAG 已归档(已删除)。",
    confirm_word: "确定", cancel_word: "取消", aria_theme: "切换主题", aria_lang: "切换语言",
    toast_run_queued: "已触发，run 已排队", toast_pool_saved: "池已保存", toast_dag_deleted: "DAG 已归档",
    th_id: "id", th_type: "类型", th_command: "命令", th_deps: "依赖",
    th_logical: "逻辑时间", th_state: "状态", th_trig: "触发", th_started: "开始", th_dur: "耗时",
    th_task: "任务", th_try: "尝试", th_logs: "日志",
    no_runs: "还没有运行记录 — 触发一次。",
    k_logical: "逻辑时间", k_trig: "触发", k_dur: "耗时", k_started: "开始",
    log_word: "日志", live: "实时",
    pools_sub: "全局并发槽位，跨所有 DAG 与 run 共享。", p_name: "名称", p_slots: "槽位", p_save: "保存",
    p_newname: "新池名称", p_create: "创建池", p_need: "需要名称和正整数槽位",
    trig_fail: "触发失败", api_err: "API 错误",
    nx_paused: "已暂停", nx_due: "就绪", nx_in: (m) => `${m} 分钟后`,
    b_dag_info: "DAG 信息",
    f_dag_id: "DAG ID", f_start: "开始日期",
    f_catchup: "补跑 catchup", f_maxactive: "最大并发", f_defretries: "默认重试",
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
    sched_manual_hint: "仅手动触发或被上游 DAG 触发", sched_every_pre: "每隔",
    unit_s: "秒", unit_m: "分钟", unit_h: "小时", disabled_note: "(暂不可用)",
    cp_min: "每分钟", cp_hour: "每小时", cp_day: "每天 0:00", cp_2am: "每天 2:00", cp_mon: "每周一 0:00",
    cron_help: "用法", ch_title: "Cron 写法", ch_format: "格式：分 时 日 月 周（5 段，空格分隔）",
    ch_fields: "字段", ch_ops: "符号", ch_examples: "常用示例（点击填入）", ch_shortcuts: "快捷写法",
    t_rule: "触发规则", tr_all_success: "全部成功", tr_all_done: "全部完成", tr_one_success: "任一成功", tr_one_failed: "任一失败", tr_all_failed: "全部失败", tr_none_failed: "无失败",
    trd_all_success: "全部上游成功才运行(默认)", trd_all_done: "全部上游完成即运行(无论成败)——适合清理/汇总", trd_one_success: "任一上游成功即运行", trd_one_failed: "任一上游失败即运行——适合告警", trd_all_failed: "全部上游都失败才运行", trd_none_failed: "没有上游失败(成功或跳过)时运行",
    pool_hint: "并发槽位,跨所有 DAG 共享;同名 pool 的任务竞争同一批槽位",
    cb_interp: "解释器", cb_runas: "运行方式", cb_target: "模块 / 脚本", cb_args: "参数", cb_jar: "Jar 路径", cb_mainclass: "主类", cb_client: "SQL 客户端", cb_query: "SQL 查询",
    cmdopt_module: "模块 (-m)", cmdopt_script: "脚本文件",
    cmd_will_run: "将执行:", cmd_edit_raw: "编辑原始命令", cmd_use_form: "用表单填写", cmd_cant_parse: "当前命令无法解析成表单,已保留原始编辑",
    var_insert: "点击或拖拽插入变量", var_editor_aria: "命令编辑器,可插入变量药丸",
    var_pill_aria: (n) => `变量 ${n}`, var_pill_remove: (n) => `移除变量 ${n}`,
    var_empty: "无", var_add_key: "自定义…", var_conn_field: "选字段", var_goto_settings: "去设置",
    vd_logical_date: "本次运行的逻辑日期(到天)", vd_logical_datetime: "逻辑日期时间(RFC3339)",
    vd_run_id: "本次运行的唯一 ID", vd_dag_id: "所属 DAG 的 ID", vd_task_id: "当前任务 ID", vd_try_number: "第几次尝试(重试递增)",
    vd_var: "共享变量", vd_conn: "连接字段", vd_params: "手动触发参数",
    vg_builtin: "内置", vg_var: "变量", vg_conn: "连接", vg_params: "参数",
    graph_connect_hint: "提示：点上游任务、再点下游任务，即可连接/断开依赖",
    nav_graph: "关系图", graph_title: "DAG 关系图", graph_sub: "按 trigger_after 展示各 DAG 之间的触发依赖",
    graph_none: "暂无跨 DAG 依赖（没有 DAG 配置 trigger_after）", graph_view_hint: "提示：箭头表示「触发后」方向；点击节点查看该 DAG；虚线节点为未找到的 DAG",
    ss_saved: "已保存", ss_saving: "保存中…", ss_invalid: "待修复后保存", ss_error: "保存失败",
    dag_no_tasks_title: "暂无任务", dag_no_tasks_sub: "添加一个任务以启用此 DAG", dag_disabled_hint: "添加任务后可触发",
    nd_title: "新建 DAG", nd_create: "创建", nd_cancel: "取消", nd_dagid_dup: "该 DAG ID 已存在",
    tpl_start: "从模板开始", tpl_tasks: "个任务",
    tpl_blank: "空白", tpl_blank_d: "从零开始,稍后自己加任务",
    tpl_etl: "每日 ETL", tpl_etl_d: "抽取 → 转换 → 加载 的三步流水线",
    tpl_report: "定时报表", tpl_report_d: "取数 → 生成报表,预设每天 08:00",
    tpl_fanout: "扇出-扇入", tpl_fanout_d: "start → 两个并行分支 → 汇合",
    coach_tpl_ready: "模板已就绪 — 点「触发运行」看它跑一遍,再按需修改任务",
    sp_every: (n, u) => `每 ${n} ${u}运行一次`, sp_next: "接下来", sp_invalid: "表达式无效,无法计算触发时间",
    tz_note: "调度按 UTC 计算;页面时间按你的本地时区显示",
    btn_duplicate: "⧉ 复制", dup_dag_title: "复制为新 DAG(输入新 ID)", dup_done: "已复制",
    y_copy: "复制", y_download: "下载", y_close: "关闭", y_copied: "YAML 已复制到剪贴板", y_copy_fail: "复制失败,请手动选择文本",
    nd_import_yaml: "或粘贴 YAML 导入…", nd_back_form: "← 返回表单创建", nd_import: "导入", nd_yaml_empty: "请先粘贴 YAML 内容", nd_imported: "YAML 已导入",
    gs_title: "快速上手", gs_create: "创建第一个 DAG", gs_trigger: "触发一次运行", gs_green: "拿到一次成功运行",
    adv_options: "高级选项", log_find_ph: "在日志中查找…", log_download: "下载完整日志", log_matches: (n) => `${n} 行匹配`, log_capped: (n) => `仅显示最近 ${n} 行`,
    back_dag: (d) => `← 返回 ${d}`, confirm_del_task_title: (id) => `删除任务 “${id}”？`,
  },
  en: {
    workspace: "Workspace", newdag: "+ New DAG", search_ph: "Filter DAGs…",
    f_all: "All", f_running: "Running", f_failed: "Failed", f_paused: "Paused",
    dags_sub: "All workflow definitions · toggle · schedule · recent runs",
    c_active: "Active DAGs", c_running: "Running runs", c_rate: "Recent success", c_failed: "Failed DAGs",
    c_active_s: (n) => `${n} defined`, c_running_s: "across all pools", c_rate_s: "recent runs", c_failed_s: "last run failed",
    h_dag: "DAG", h_last: "LAST RUN", h_spark: "LAST 14", h_pool: "POOL", h_next: "NEXT", h_act: "ACTIONS",
    no_match: "No matching DAGs", no_dags_title: "No DAGs yet", no_dags_sub: "Create your first workflow to start scheduling tasks.", trigger: "Trigger", manual_trigger: "manual trigger",
    back_dags: "← DAGs", run_word: "run", sub_manual: "manual trigger only", max_active: "max active",
    run_progress: "Progress",
    sec_graph: "Dependency graph", sec_structure: "Structure", sec_runs: "Run history", sec_instances: "Task instances",
    g_timeline: "Timeline", g_never_ran: "did not run", run_no_tasks: "No task instances yet for this run", run_done_ok: "Run finished — success", run_done_fail: "Run failed", run_done_timeout: "Run timed out",
    run_cancel: "Cancel run", run_retry: "Retry failed", task_retry: "Retry", run_cancelled_toast: "Run cancelled", run_retried_toast: "Re-queued",
    task_mark: "Mark state", run_mark: "Mark run", mark_skip: "Skip", mark_done_toast: "Marked",
    mark_task_title: (id) => `Mark task “${id}” as?`, mark_task_body: "Manually override the task state. A running task is stopped first; marking success/skip releases downstream tasks it was blocking.",
    mark_run_title: (id) => `Mark run “${id}” as?`, mark_run_body: "Override a finished run's recorded outcome (tasks untouched). Marking success fires downstream-DAG triggers.",
    confirm_cancel_title: (id) => `Cancel run “${id}”?`, confirm_cancel_body: "Running tasks will be killed.", th_act: "Actions",
    confirm_retry_title: (id) => `Retry “${id}”?`, confirm_retry_body: "This task and all of its downstream tasks will be reset and re-run.",
    copied: "Copied", copy_fail: "Copy failed — select the text manually", copy_hint: "Click to copy", search_ph: "Jump / filter DAGs…", jump_open: "Open", jump_none: "No matching DAG",
    gz_in: "Zoom in", gz_out: "Zoom out", gz_fit: "Fit to view", gz_hint: "Drag to pan · Ctrl/⌘ + wheel to zoom",
    act_recent: "Recent activity", act_now: "now", act_none: "No runs yet",
    login_title: "Sign in to cronova", login_sub: "Enter your account credentials", login_user: "Username", login_pass: "Password", login_btn: "Sign in", login_bad: "Invalid username or password", logout: "Sign out", sess_expired: "Session expired — please sign in again", role_admin: "Admin", role_viewer: "Viewer",
    tab_runs: "Runs", tab_structure: "Structure", tab_settings: "Settings",
    dh_last: "Last run", dh_next: "Schedule", dh_rate: "Success rate", dh_never: "No runs yet", dh_norate: "—",
    set_done: "Done", set_edit: "Edit", set_none: "None", set_sched: "Schedule", set_max: "Max active runs", set_retries: "Default retries", set_deps: "Upstream DAGs",
    set_deps_hint: "Triggered automatically after these DAGs succeed", set_no_deps_avail: "No other DAGs available",
    set_notify: "Notifications", set_notify_hint: "POST a JSON webhook when a run finishes (Slack/Feishu/Discord compatible)", notify_failure: "Failure", notify_success: "Success", notify_off: "No events selected", notify_need_url: "Enter a webhook URL first, then pick events", err_notify_url: "Notify URL must start with http:// or https://",
    set_sla: "SLA (soft)", set_sla_hint: "From run start; alert if not finished in time (run keeps going). 0 = off. Needs a notify webhook.", set_timeout: "Run timeout (hard)", set_timeout_hint: "From run start; on breach the run is force-failed and running tasks killed → timed_out. 0 = off.", secs: "sec", set_off: "off",
    t_sla: "Task SLA (sec)", t_sla_hint: "From run start; alert if this task hasn't finished in time. 0 = off.", t_timeout_hint: "Kill a single execution after this many seconds. 0 = none.",
    danger_title: "Danger zone", danger_del_hint: "Archive this DAG: no more scheduling; history is kept.",
    nd_more: "Schedule & more options", nd_less: "Hide",
    nav_resources: "Variables & Connections", nav_audit: "Audit", nav_api: "API",
    audit_sub: "Operations log: who did what to which DAG/run, and when.", audit_empty: "No operations logged yet", au_time: "Time", au_actor: "Actor", au_action: "Action", au_target: "Target",
    act_trigger: "trigger", act_cancel: "cancel", act_retry_run: "retry run", act_retry_task: "retry task", act_mark_task: "mark task", act_mark_run: "mark run", act_save_dag: "save DAG", act_create_dag: "create DAG", act_delete_dag: "delete DAG", act_pause: "pause", act_unpause: "unpause", act_create_token: "create token", act_delete_token: "revoke token",
    api_title: "API & Integration", api_sub: "Drive cronova from your own platform. Browse the interactive API reference and manage API tokens for machine access.",
    api_docs_h: "API reference", api_docs_hint: "Full OpenAPI reference with built-in curl / Go / Python / Java samples and an in-page language switcher.", api_open_docs: "Open API reference →", api_spec_link: "OpenAPI spec",
    tok_title: "API Tokens", tok_sub: "Machine credentials. Call the API with Authorization: Bearer <token>. The plaintext is shown only once, at creation.",
    tok_name: "Name", tok_role: "Role", tok_prefix: "Prefix", tok_created: "Created", tok_lastused: "Last used", tok_never: "Never used",
    tok_create: "Create token", tok_none: "No tokens yet", tok_name_ph: "e.g. ci-bot", tok_revoke: "Revoke", tok_need_name: "Name is required",
    tok_revoke_title: (n) => `Revoke token “${n}”?`, tok_revoke_body: "Calls using this token will fail immediately. This cannot be undone.",
    tok_created_ok: "Token created", tok_revoked: "Token revoked",
    tok_reveal_h: "Your new API token", tok_reveal_warn: "Copy it now and store it securely — you won't be able to see the plaintext again.", tok_copy: "Copy", tok_done: "I've saved it",
    role_admin_full: "Admin (read-write)", role_viewer_ro: "Viewer (GET only)",
    res_vars: "Variables", res_conns: "Connections",
    res_sub: "Config shared across tasks. Reference in commands as {{ var.KEY }} / {{ conn.ID.field }}, or {{ params.KEY }} at trigger time.",
    v_key: "Key", v_value: "Value", v_add: "Add variable", v_none: "No variables yet", v_save: "Save",
    c_id: "Connection ID", c_type: "Type", c_host: "Host", c_port: "Port", c_login: "Login", c_password: "Password", c_extra: "Extra (JSON)",
    c_add: "New connection", c_edit: "Edit connection", c_none: "No connections yet", c_pw_set: "set", c_pw_none: "not set", c_pw_keep: "leave blank to keep",
    c_del_title: (id) => `Delete connection “${id}”?`, v_del_title: (k) => `Delete variable “${k}”?`, del_body: "This cannot be undone.",
    trig_params: "Trigger with params", p_params: "Params", p_add: "Add row", p_key: "Key", p_val: "Value", p_trigger: "Trigger", p_hint: "Params are injected as CRONOVA_PARAM_* env vars; reference as {{ params.key }} in commands.",
    run_params: "Params", res_saved: "Saved", res_deleted: "Deleted", err_key: "Invalid name (letters, digits, _ . - only)",
    btn_trigger: "▶ Trigger run", btn_pause: "Pause", btn_resume: "Resume", btn_delete: "Delete",
    confirm_del_dag_title: (id) => `Delete DAG “${id}”?`,
    confirm_del_dag_body: "It will be archived (hidden from lists); run history is kept and it can be restored.",
    dag_archived: "This DAG is archived (deleted).",
    confirm_word: "Confirm", cancel_word: "Cancel", aria_theme: "Toggle theme", aria_lang: "Switch language",
    toast_run_queued: "Triggered — run queued", toast_pool_saved: "Pool saved", toast_dag_deleted: "DAG archived",
    th_id: "id", th_type: "type", th_command: "command", th_deps: "deps",
    th_logical: "logical date", th_state: "state", th_trig: "trigger", th_started: "started", th_dur: "duration",
    th_task: "task", th_try: "try", th_logs: "logs",
    no_runs: "No runs yet — trigger one.",
    k_logical: "logical date", k_trig: "trigger", k_dur: "duration", k_started: "started",
    log_word: "Log", live: "live",
    pools_sub: "Global concurrency slots, shared across all DAGs and runs.", p_name: "name", p_slots: "slots", p_save: "Save",
    p_newname: "new pool name", p_create: "Create pool", p_need: "name + positive slots required",
    trig_fail: "trigger failed", api_err: "API error",
    nx_paused: "paused", nx_due: "due", nx_in: (m) => `in ${m}m`,
    b_dag_info: "DAG info",
    f_dag_id: "DAG ID", f_start: "Start date",
    f_catchup: "Catchup", f_maxactive: "Max active", f_defretries: "Default retries",
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
    sched_manual_hint: "Manual trigger or triggered by an upstream DAG only", sched_every_pre: "Every",
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
    vd_run_id: "this run's unique id", vd_dag_id: "the DAG id", vd_task_id: "this task's id", vd_try_number: "attempt number (increments on retry)",
    vd_var: "shared variable", vd_conn: "connection field", vd_params: "manual-trigger param",
    vg_builtin: "built-in", vg_var: "variables", vg_conn: "connections", vg_params: "params",
    graph_connect_hint: "Tip: click an upstream task then a downstream task to add/remove a dependency",
    nav_graph: "Graph", graph_title: "DAG Graph", graph_sub: "Trigger dependencies between DAGs via trigger_after",
    graph_none: "No cross-DAG dependencies yet (no DAG declares trigger_after)", graph_view_hint: "Tip: arrows point in the trigger-after direction; click a node to open that DAG; dashed nodes are unknown DAGs",
    ss_saved: "Saved", ss_saving: "Saving…", ss_invalid: "Fix errors to save", ss_error: "Save failed",
    dag_no_tasks_title: "No tasks yet", dag_no_tasks_sub: "Add a task to enable this DAG", dag_disabled_hint: "Add a task to enable triggering",
    nd_title: "New DAG", nd_create: "Create", nd_cancel: "Cancel", nd_dagid_dup: "A DAG with this id already exists",
    tpl_start: "Start from a template", tpl_tasks: "tasks",
    tpl_blank: "Blank", tpl_blank_d: "Start empty and add tasks yourself",
    tpl_etl: "Daily ETL", tpl_etl_d: "Three-step extract → transform → load pipeline",
    tpl_report: "Scheduled report", tpl_report_d: "Fetch → render, preset to run daily at 08:00",
    tpl_fanout: "Fan-out / fan-in", tpl_fanout_d: "start → two parallel branches → join",
    coach_tpl_ready: "Template ready — hit “Trigger run” to watch it execute, then tweak the tasks",
    sp_every: (n, u) => `Runs every ${n} ${u}`, sp_next: "Next", sp_invalid: "Invalid expression — cannot compute fire times",
    tz_note: "Schedules evaluate in UTC; times shown in your local timezone",
    btn_duplicate: "⧉ Duplicate", dup_dag_title: "Duplicate as a new DAG (enter a new id)", dup_done: "Duplicated",
    y_copy: "Copy", y_download: "Download", y_close: "Close", y_copied: "YAML copied to clipboard", y_copy_fail: "Copy failed — select the text manually",
    nd_import_yaml: "or paste YAML to import…", nd_back_form: "← back to the form", nd_import: "Import", nd_yaml_empty: "Paste some YAML first", nd_imported: "YAML imported",
    gs_title: "Getting started", gs_create: "Create your first DAG", gs_trigger: "Trigger a run", gs_green: "Get a green run",
    adv_options: "Advanced options", log_find_ph: "Find in log…", log_download: "Download full log", log_matches: (n) => `${n} matching lines`, log_capped: (n) => `showing last ${n} lines`,
    back_dag: (d) => `← Back to ${d}`, confirm_del_task_title: (id) => `Delete task “${id}”?`,
  },
};
const STATE = {
  zh: { success: "成功", failed: "失败", running: "运行中", queued: "排队", scheduled: "待运行", up_for_retry: "重试中", upstream_failed: "上游失败", skipped: "跳过", cancelled: "已取消", timed_out: "超时", "": "未运行", none: "未运行" },
  en: { success: "success", failed: "failed", running: "running", queued: "queued", scheduled: "scheduled", up_for_retry: "retrying", upstream_failed: "upstream failed", skipped: "skipped", cancelled: "cancelled", timed_out: "timed out", "": "no runs", none: "no runs" },
};
const TYPEL = {
  zh: { schedule: "定时", manual: "手动", dependency: "依赖", event: "事件" },
  en: { schedule: "scheduled", manual: "manual", dependency: "dependency", event: "event" },
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
    let m = r.statusText; try { m = (await r.json()).error || m; } catch (_) {}
    const err = new Error(m); err.status = r.status;
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
function badge(s) { const k = s || "none"; return `<span class="badge s-${k}"><span class="d"></span>${stateLabel(s)}</span>`; }
function closeLog() { if (logES) { logES.close(); logES = null; } }
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
  const byId = {}; tasks.forEach((t2) => byId[t2.id] = t2);
  const level = {};
  const lvl = (id, seen) => { if (level[id] != null) return level[id]; if (seen.has(id)) return 0; seen.add(id); const deps = (byId[id]?.deps || []).filter((d) => byId[d]); return level[id] = deps.length ? 1 + Math.max(...deps.map((d) => lvl(d, seen))) : 0; };
  tasks.forEach((t2) => lvl(t2.id, new Set()));
  const cols = {}; tasks.forEach((t2) => (cols[level[t2.id]] ||= []).push(t2.id));
  const NW = 150, NH = 36, CG = 200, RG = 52, PAD = 16, pos = {};
  Object.keys(cols).forEach((L) => cols[L].forEach((id, i) => pos[id] = { x: PAD + L * CG, y: PAD + i * RG }));
  const maxL = Math.max(...Object.keys(cols).map(Number)), maxR = Math.max(...Object.values(cols).map((c) => c.length));
  const W = PAD * 2 + maxL * CG + NW, H = PAD * 2 + (maxR - 1) * RG + NH;
  let edges = "", nodes = "";
  tasks.forEach((t2) => (t2.deps || []).forEach((d) => { if (!pos[d]) return; const x1 = pos[d].x + NW, y1 = pos[d].y + NH / 2, x2 = pos[t2.id].x, y2 = pos[t2.id].y + NH / 2, mx = (x1 + x2) / 2; edges += `<path class="graph-edge" d="M${x1} ${y1} C ${mx} ${y1}, ${mx} ${y2}, ${x2} ${y2}"/>`; }));
  tasks.forEach((t2) => {
    let [f, st] = colorForState(stateByTask ? stateByTask[t2.id] : null);
    let sw = 1.2;
    if (opts.pending === t2.id) { st = "var(--accent)"; sw = 2.6; }
    const dash = opts.dashed && opts.dashed.has(t2.id) ? ` stroke-dasharray="5 4"` : "";
    const p = pos[t2.id];
    // tag: expose data-node for live patching without making the node clickable
    const clickable = opts.editable || opts.clickable;
    const attrs = clickable ? ` data-node="${esc(t2.id)}" style="cursor:pointer"` : (opts.tag ? ` data-node="${esc(t2.id)}"` : "");
    const cls = opts.tag && stateByTask && stateByTask[t2.id] === "running" ? "graph-node g-running" : "graph-node";
    // fill/stroke via inline style (SVG presentation attributes don't resolve color-mix reliably)
    nodes += `<g class="${cls}"${attrs}><rect x="${p.x}" y="${p.y}" width="${NW}" height="${NH}" rx="8" style="fill:${f};stroke:${st}" stroke-width="${sw}"${dash}/><text x="${p.x + NW / 2}" y="${p.y + NH / 2 + 4}" text-anchor="middle">${esc(t2.id)}</text></g>`;
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
  wrap.addEventListener("pointerdown", (e) => {
    if (e.button !== 0) return;
    wrap._sup = 0; // any fresh press clears a stale suppress-latch (e.g. drag released off-element)
    if (e.target.closest(".graph-zoom")) return; // let the zoom buttons handle themselves
    drag = true; moved = false; sx = e.clientX; sy = e.clientY; otx = tx; oty = ty;
    try { wrap.setPointerCapture(e.pointerId); } catch (_) {}
  });
  wrap.addEventListener("pointermove", (e) => {
    if (!drag) return;
    const dx = e.clientX - sx, dy = e.clientY - sy;
    if (!moved && Math.hypot(dx, dy) < 4) return; // below threshold: still a click
    moved = true; wrap.classList.add("panning");
    tx = otx + dx; ty = oty + dy; clamp(); apply();
  });
  const end = () => { if (!drag) return; drag = false; wrap.classList.remove("panning"); if (moved) wrap._sup = 1; };
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
// replace=true rewrites the current entry (no new history entry, no hashchange) —
// use it for in-page normalization (e.g. tab canonicalization) so Back isn't trapped.
function setHash(h, replace) {
  if (location.hash === h) return;
  if (replace) { history.replaceState(null, "", h); return; }
  suppressHash = true; location.hash = h;
}
function applyRoute() {
  if (typeof closeConnMenu === "function") closeConnMenu(); // dismiss any open popover on navigation
  const seg = location.hash.replace(/^#\/?/, "").split("/").map(decodeURIComponent).filter(Boolean);
  if (!seg.length || seg[0] === "dags") return loadDags();
  if (seg[0] === "pools") return showPools();
  if (seg[0] === "resources") return showResources();
  if (seg[0] === "graph") return showGraph();
  if (seg[0] === "audit") return showAudit();
  if (seg[0] === "api") return showApi();
  if (seg[0] === "run" && seg[1]) return showRun(seg[1]);
  if (seg[0] === "dag" && seg[1] && seg[2] === "task" && seg[3]) {
    return showDag(seg[1]).then(() => { if (D && D.dag.dag_id === seg[1] && D.tasks.some((x) => x.id === seg[3])) showTask(seg[1], seg[3]); });
  }
  if (seg[0] === "dag" && seg[1]) return showDag(seg[1], seg[2]); // seg[2]: runs|structure|settings (optional)
  return loadDags();
}
window.addEventListener("hashchange", () => { if (suppressHash) { suppressHash = false; return; } Promise.resolve(applyRoute()).catch(() => {}); });

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
async function loadInfo() { try { const i = await api("/api/info"); serverTZ = i.tz || ""; $("f-exec").textContent = i.executor || "—"; $("f-tick").textContent = "tick " + (i.tick || "—"); $("tick").textContent = "tick " + (i.tick || "—"); const z = $("tzlab"); if (z) { z.textContent = serverTZ; z.title = t("tz_note"); } } catch (_) {} }
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
function startApp() {
  loadInfo();
  Promise.resolve(applyRoute()).catch((e) => { main.innerHTML = `<div class="empty err">${t("api_err")}: ${esc(e.message)}</div>`; });
}

// navKey highlights a sidebar item; crumb (optional) overrides the topbar breadcrumb text.
let lastNavLabel = null;
function setNav(navKey, crumb) {
  document.querySelectorAll(".nav-item[data-nav]").forEach((n) => n.classList.toggle("active", n.dataset.nav === navKey));
  const label = crumb != null ? crumb : (navKey === "pools" ? "Pools" : navKey === "graph" ? t("graph_title") : navKey === "resources" ? t("nav_resources") : navKey === "audit" ? t("nav_audit") : navKey === "api" ? t("nav_api") : "DAGs");
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
}
function setLang(l) {
  lang = l; localStorage.setItem("cnv_lang", l); applyStaticI18n();
  // dag/task re-render from in-memory D (no refetch) so unsaved edits survive.
  if (view === "dags") renderDags();
  else if (view === "dag") renderDagPage();
  else if (view === "task") renderTaskPage();
  else if (view === "run") showRun(currentRun);
  else if (view === "pools") showPools();
  else if (view === "resources") renderResources(); // from in-memory RES, no refetch
  else if (view === "graph") showGraph();
  else if (view === "audit") showAudit();
  else if (view === "api") renderApi(); // from in-memory TOKENS, no refetch
}
