"use strict";

const $ = (id) => document.getElementById(id);
const main = $("main");

let view = "dags";       // dags | dag | task | run | pools | graph
let activeDag = null;
let currentRun = null;
let filter = "all";
let query = "";
let overviewCache = null;
let logES = null;
let lang = localStorage.getItem("cnv_lang") || "zh";
let theme = localStorage.getItem("cnv_theme") || "dark";

// D: in-memory editable spec for the active DAG operation page (immediate-save).
let D = null;
// ND: transient state for the minimal new-DAG modal.
let ND = null;
// SCHED: binding for the shared schedule UI {state, idp, host, onChange}.
let SCHED = null;

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
    sec_graph: "依赖图", sec_structure: "结构", sec_runs: "运行历史", sec_instances: "任务实例",
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
    t_retries: "重试 (空=默认)", t_retrydelay: "重试间隔(秒)", t_timeout: "超时(秒)", t_deps: "依赖",
    t_nodeps: "暂无其他任务",
    err_dagid: "请填写合法 DAG ID（字母/数字/_-.）", err_taskid: "任务 ID 不合法（字母/数字/_-.）",
    err_dup: "任务 ID 重复", err_emptyid: "存在空任务 ID", err_emptycmd: "存在空命令", err_cycle: "依赖存在环",
    sched: "调度", sm_manual: "手动", sm_every: "固定间隔", sm_cron: "Cron 表达式",
    sched_manual_hint: "仅手动触发或被上游 DAG 触发", sched_every_pre: "每隔",
    unit_s: "秒", unit_m: "分钟", unit_h: "小时", disabled_note: "(暂不可用)",
    cp_min: "每分钟", cp_hour: "每小时", cp_day: "每天 0:00", cp_2am: "每天 2:00", cp_mon: "每周一 0:00",
    cron_help: "用法", ch_title: "Cron 写法", ch_format: "格式：分 时 日 月 周（5 段，空格分隔）",
    ch_fields: "字段", ch_ops: "符号", ch_examples: "常用示例（点击填入）", ch_shortcuts: "快捷写法",
    t_rule: "触发规则", tr_all_success: "全部成功", tr_all_done: "全部完成", tr_one_success: "任一成功", tr_one_failed: "任一失败", tr_all_failed: "全部失败", tr_none_failed: "无失败",
    graph_connect_hint: "提示：点上游任务、再点下游任务，即可连接/断开依赖",
    nav_graph: "关系图", graph_title: "DAG 关系图", graph_sub: "按 trigger_after 展示各 DAG 之间的触发依赖",
    graph_none: "暂无跨 DAG 依赖（没有 DAG 配置 trigger_after）", graph_view_hint: "提示：箭头表示「触发后」方向；点击节点查看该 DAG；虚线节点为未找到的 DAG",
    ss_saved: "已保存", ss_saving: "保存中…", ss_invalid: "待修复后保存", ss_error: "保存失败",
    dag_no_tasks_title: "暂无任务", dag_no_tasks_sub: "添加一个任务以启用此 DAG", dag_disabled_hint: "添加任务后可触发",
    nd_title: "新建 DAG", nd_create: "创建", nd_cancel: "取消", nd_dagid_dup: "该 DAG ID 已存在",
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
    sec_graph: "Dependency graph", sec_structure: "Structure", sec_runs: "Run history", sec_instances: "Task instances",
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
    t_retries: "Retries (empty=default)", t_retrydelay: "Retry delay (s)", t_timeout: "Timeout (s)", t_deps: "Depends on",
    t_nodeps: "no other tasks",
    err_dagid: "Valid DAG ID required (letters/digits/_-.)", err_taskid: "Invalid task ID (letters/digits/_-.)",
    err_dup: "Duplicate task ID", err_emptyid: "Empty task ID", err_emptycmd: "Empty command", err_cycle: "Dependency cycle detected",
    sched: "Schedule", sm_manual: "Manual", sm_every: "Interval", sm_cron: "Cron expression",
    sched_manual_hint: "Manual trigger or triggered by an upstream DAG only", sched_every_pre: "Every",
    unit_s: "sec", unit_m: "min", unit_h: "hr", disabled_note: "(coming soon)",
    cp_min: "every minute", cp_hour: "hourly", cp_day: "daily 0:00", cp_2am: "daily 2:00", cp_mon: "Mon 0:00",
    cron_help: "help", ch_title: "Cron format", ch_format: "Format: min hour day month weekday (5 space-separated fields)",
    ch_fields: "Fields", ch_ops: "Operators", ch_examples: "Examples (click to fill)", ch_shortcuts: "Shortcuts",
    t_rule: "Trigger rule", tr_all_success: "all success", tr_all_done: "all done", tr_one_success: "one success", tr_one_failed: "one failed", tr_all_failed: "all failed", tr_none_failed: "none failed",
    graph_connect_hint: "Tip: click an upstream task then a downstream task to add/remove a dependency",
    nav_graph: "Graph", graph_title: "DAG Graph", graph_sub: "Trigger dependencies between DAGs via trigger_after",
    graph_none: "No cross-DAG dependencies yet (no DAG declares trigger_after)", graph_view_hint: "Tip: arrows point in the trigger-after direction; click a node to open that DAG; dashed nodes are unknown DAGs",
    ss_saved: "Saved", ss_saving: "Saving…", ss_invalid: "Fix errors to save", ss_error: "Save failed",
    dag_no_tasks_title: "No tasks yet", dag_no_tasks_sub: "Add a task to enable this DAG", dag_disabled_hint: "Add a task to enable triggering",
    nd_title: "New DAG", nd_create: "Create", nd_cancel: "Cancel", nd_dagid_dup: "A DAG with this id already exists",
    back_dag: (d) => `← Back to ${d}`, confirm_del_task_title: (id) => `Delete task “${id}”?`,
  },
};
const STATE = {
  zh: { success: "成功", failed: "失败", running: "运行中", queued: "排队", scheduled: "待运行", up_for_retry: "重试中", upstream_failed: "上游失败", skipped: "跳过", "": "未运行", none: "未运行" },
  en: { success: "success", failed: "failed", running: "running", queued: "queued", scheduled: "scheduled", up_for_retry: "retrying", upstream_failed: "upstream failed", skipped: "skipped", "": "no runs", none: "no runs" },
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
  if (!r.ok) { let m = r.statusText; try { m = (await r.json()).error || m; } catch (_) {} throw new Error(m); }
  const ct = r.headers.get("content-type") || "";
  return ct.includes("json") ? r.json() : r.text();
}
const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
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
function dur(a, b) { if (!a) return "—"; const ms = (b ? new Date(b) : new Date()) - new Date(a); if (ms < 0) return "—"; const s = Math.round(ms / 1000); return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m${s % 60}s`; }
function badge(s) { const k = s || "none"; return `<span class="badge s-${k}"><span class="d"></span>${stateLabel(s)}</span>`; }
function closeLog() { if (logES) { logES.close(); logES = null; } }
function sparkline(states) {
  const arr = (states || []).slice(-14); while (arr.length < 14) arr.unshift("noruns");
  // Honest: every real-state bar is the same height (color carries the state);
  // only no-run / skipped slots are short stubs. The old pseudo-random height
  // (12 + i*5%11) fabricated a skyline unrelated to the runs. Height-encoding
  // run duration is the upgrade once the overview payload carries it.
  return `<div class="spark">${arr.map((s) => {
    const k = s || "noruns", stub = k === "noruns" || k === "skipped";
    const label = k === "noruns" ? stateLabel("none") : stateLabel(s);
    return `<i class="${esc(k)}" style="height:${stub ? 6 : 16}px" title="${esc(label)}"></i>`;
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
    upstream_failed: tint("--upstream", 12), skipped: tint("--skip", 18),
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
    const attrs = (opts.editable || opts.clickable) ? ` data-node="${esc(t2.id)}" style="cursor:pointer"` : "";
    // fill/stroke via inline style (SVG presentation attributes don't resolve color-mix reliably)
    nodes += `<g class="graph-node"${attrs}><rect x="${p.x}" y="${p.y}" width="${NW}" height="${NH}" rx="8" style="fill:${f};stroke:${st}" stroke-width="${sw}"${dash}/><text x="${p.x + NW / 2}" y="${p.y + NH / 2 + 4}" text-anchor="middle">${esc(t2.id)}</text></g>`;
  });
  return `<div class="graph-wrap"><svg width="${W}" height="${H}" viewBox="0 0 ${W} ${H}">${edges}${nodes}</svg></div>`;
}

// ---- sidebar/topbar ----
async function loadInfo() { try { const i = await api("/api/info"); $("f-exec").textContent = i.executor || "—"; $("f-tick").textContent = "tick " + (i.tick || "—"); $("tick").textContent = "tick " + (i.tick || "—"); } catch (_) {} }
// navKey highlights a sidebar item; crumb (optional) overrides the topbar breadcrumb text.
function setNav(navKey, crumb) {
  document.querySelectorAll(".nav-item[data-nav]").forEach((n) => n.classList.toggle("active", n.dataset.nav === navKey));
  $("crumb").textContent = crumb != null ? crumb : (navKey === "pools" ? "Pools" : navKey === "graph" ? t("graph_title") : "DAGs");
  // the topbar search only filters the dashboard list — hide it elsewhere.
  const s = document.querySelector(".search"); if (s) s.classList.toggle("off", navKey !== "dags");
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
  else if (view === "graph") showGraph();
}

// ---- DAGs dashboard ----
async function loadDags() {
  view = "dags"; activeDag = null; closeLog(); setNav("dags");
  overviewCache = await api("/api/overview");
  $("nav-dags").textContent = overviewCache.stats.total_dags;
  renderDags();
}
function renderDags() {
  if (view !== "dags" || !overviewCache) return;
  const { stats, dags } = overviewCache;
  if (dags.length === 0) { // empty instance: a genuine first-run hero, not the "no matching" copy
    main.innerHTML = `
      <div class="page-h"><h1>DAGs</h1><span class="num">0</span></div>
      <div class="page-sub">${t("dags_sub")}</div>
      <div class="empty-state" style="margin-top:20px"><div class="es-ic">✦</div><div class="es-t">${t("no_dags_title")}</div><div class="es-s">${t("no_dags_sub")}</div><div style="margin-top:16px"><button class="primary" id="es-new">${t("newdag")}</button></div></div>`;
    $("es-new").onclick = () => newDagModal();
    return;
  }
  const list = dags.filter((d) => {
    if (query && !d.dag_id.toLowerCase().includes(query)) return false;
    if (filter === "running") return d.latest_state === "running";
    if (filter === "failed") return d.latest_state === "failed";
    if (filter === "paused") return d.paused;
    return true;
  });
  main.innerHTML = `
    <div class="page-h"><h1>DAGs</h1><span class="num">${stats.total_dags}</span>
      <div class="filters">${["all", "running", "failed", "paused"].map((f) => `<button class="pill ${filter === f ? "active" : ""}" data-f="${f}">${t("f_" + f)}</button>`).join("")}</div></div>
    <div class="page-sub">${t("dags_sub")}</div>
    <div class="cards">
      <div class="card"><div class="k"><span class="d" style="background:var(--accent)"></span>${t("c_active")}</div><div class="v">${stats.active_dags}</div><div class="s">${t("c_active_s", stats.total_dags)}</div></div>
      <div class="card"><div class="k"><span class="d" style="background:var(--run)"></span>${t("c_running")}</div><div class="v">${stats.running_runs}</div><div class="s">${t("c_running_s")}</div></div>
      <div class="card"><div class="k"><span class="d" style="background:var(--ok)"></span>${t("c_rate")}</div><div class="v">${stats.success_rate.toFixed(1)}%</div><div class="s">${t("c_rate_s")}</div></div>
      <div class="card${stats.failed ? " clickable" : ""}" ${stats.failed ? `data-card="failed" role="button" tabindex="0" aria-label="${t("c_failed")}"` : ""}><div class="k"><span class="d" style="background:var(--fail)"></span>${t("c_failed")}</div><div class="v">${stats.failed}</div><div class="s">${t("c_failed_s")}</div></div></div>
    <table class="tbl"><thead><tr><th style="width:42px"></th><th>${t("h_dag")}</th><th>${t("h_last")}</th><th>${t("h_spark")}</th><th>${t("h_pool")}</th><th>${t("h_next")}</th><th style="width:80px">${t("h_act")}</th></tr></thead>
    <tbody>${list.map(rowHtml).join("") || `<tr><td colspan="7"><div class="empty">${t("no_match")}</div></td></tr>`}</tbody></table>`;
  main.querySelectorAll(".pill[data-f]").forEach((b) => b.onclick = () => { filter = b.dataset.f; renderDags(); });
  const fc = main.querySelector('[data-card="failed"]'); if (fc) fc.onclick = () => { filter = "failed"; renderDags(); }; // dead number -> one-click triage
  main.querySelectorAll("tr.row").forEach((tr) => tr.onclick = (e) => { if (!e.target.closest(".no-nav")) showDag(tr.dataset.id); });
  main.querySelectorAll(".toggle").forEach((tg) => tg.onclick = async (e) => { e.stopPropagation(); await api(`/api/dags/${tg.dataset.id}/pause?paused=${tg.dataset.paused !== "true"}`, { method: "POST" }); loadDags(); });
  main.querySelectorAll(".play").forEach((b) => b.onclick = async (e) => { e.stopPropagation(); b.disabled = true; try { await api(`/api/dags/${b.dataset.id}/trigger`, { method: "POST" }); toast(t("toast_run_queued"), "ok"); setTimeout(loadDags, 500); } catch (err) { toast(t("trig_fail") + ": " + err.message, "fail"); b.disabled = false; } });
}
function rowHtml(d) {
  return `<tr class="row" data-id="${esc(d.dag_id)}">
    <td><div class="toggle no-nav ${d.paused ? "" : "on"}" role="switch" tabindex="0" aria-checked="${!d.paused}" aria-label="${esc(d.dag_id)} — ${t(d.paused ? "btn_resume" : "btn_pause")}" data-id="${esc(d.dag_id)}" data-paused="${d.paused}"></div></td>
    <td><div class="dag-name" role="button" tabindex="0" aria-label="${esc(d.dag_id)}">${esc(d.dag_id)} <span class="tag">${typeLabel(d.type)}</span></div><div class="dag-desc">${esc(descLabel(d.description))}</div></td>
    <td>${badge(d.latest_state)}</td><td>${sparkline(d.sparkline)}</td>
    <td class="mono muted">${esc(d.pool)}</td><td class="mono muted">${esc(nextLabel(d.next_schedule))}</td>
    <td><button class="icon play no-nav" data-id="${esc(d.dag_id)}" title="${t("trigger")}">▶</button></td></tr>`;
}

// ============================================================================
// DAG operation page (view='dag') — integrated: info + structure (editable
// graph + task list) + schedule + run history. Edits persist immediately.
// ============================================================================
async function showDag(id) {
  closeLog();
  await flushPendingSaves(); // land any debounced edit before we refetch + replace D
  let dag, runs, allDags = [];
  try { [dag, runs] = await Promise.all([api(`/api/dags/${id}`), api(`/api/dags/${id}/runs?limit=25`)]); }
  catch (e) { D = null; view = "dag"; activeDag = id; setNav("dags", id); main.innerHTML = `<div class="empty err">${t("api_err")}: ${esc(e.message)}</div>`; return; }
  if (dag.deleted_at) { // archived DAG: do NOT open the editor (a save would silently revive it)
    D = null; view = "dag"; activeDag = id; setNav("dags", id);
    main.innerHTML = `<div class="crumb-bar"><a id="back">${t("back_dags")}</a> / ${esc(id)}</div><div class="empty">${t("dag_archived")}</div>`;
    $("back").onclick = loadDags;
    return;
  }
  try { allDags = (await api("/api/dags")).map((d) => d.dag_id); } catch (_) {}
  D = {
    dag: parseScheduleState({
      dag_id: dag.dag_id, schedule: dag.schedule || "",
      start_date: dag.start_date ? String(dag.start_date).slice(0, 10) : "",
      catchup: !!dag.catchup, paused: !!dag.paused,
      max_active_runs: dag.max_active_runs || 1, default_retries: dag.default_retries || 0,
      trigger_after: (dag.trigger_after || []).slice(),
    }),
    tasks: (dag.tasks || []).map((tk) => ({ id: tk.id, type: tk.type || "shell", command: tk.command || "", pool: tk.pool || "default", priority: tk.priority || 0, retries: tk.retries ?? "", retry_delay: tk.retry_delay ?? "", timeout: tk.timeout || "", deps: (tk.deps || []).slice(), trigger_rule: tk.trigger_rule || "all_success" })),
    runs: runs || [], allDags, graphPending: null, activeTaskId: null,
  };
  view = "dag"; activeDag = id;
  setNav("dags", id);
  renderDagPage();
}
// re-render the operation page from the in-memory D (no refetch) — used when
// returning from the task page so unsaved/just-saved edits are never clobbered.
function gotoDagPage() {
  if (!D) { loadDags(); return; }
  view = "dag"; activeDag = D.dag.dag_id; closeLog();
  setNav("dags", D.dag.dag_id);
  renderDagPage();
}

function renderDagPage() {
  if (!D) return;
  const d = D.dag;
  const typ = d.schedule ? "schedule" : (d.trigger_after.length ? "dependency" : "manual");
  const noTasks = D.tasks.length === 0;
  // include any existing trigger_after target even if it's not (or no longer) a
  // known DAG, so a dangling upstream ref shows as a removable chip.
  const others = [...new Set([...D.allDags.filter((x) => x !== d.dag_id), ...d.trigger_after])];
  main.innerHTML = `
    <div class="crumb-bar"><a id="back">${t("back_dags")}</a> / ${esc(d.dag_id)}</div>
    <div class="page-h"><h1 class="mono">${esc(d.dag_id)}</h1><span class="tag">${typeLabel(typ)}</span><span class="savestate ss-saved" id="d-save"></span></div>
    <div class="page-sub">${esc(d.schedule || t("sub_manual"))} · ${t("max_active")} ${d.max_active_runs}</div>
    <div class="toolbar">
      <button class="primary" id="trig" ${noTasks ? "disabled" : ""}>${t("btn_trigger")}</button>
      <button id="pause">${d.paused ? t("btn_resume") : t("btn_pause")}</button>
      ${noTasks ? `<span class="muted hint-inline">${t("dag_disabled_hint")}</span>` : ""}
      <button class="danger" id="del" style="margin-left:auto">${t("btn_delete")}</button>
    </div>

    <div class="section-h">${t("b_dag_info")}</div>
    <div class="b-grid">
      <div class="b-field"><label>${t("f_maxactive")}</label><input id="d-max" type="number" min="1" value="${d.max_active_runs}"></div>
      <div class="b-field"><label>${t("f_defretries")}</label><input id="d-defr" type="number" min="0" value="${d.default_retries}"></div>
      ${others.length ? `<div class="b-field full"><label>${t("f_trigger_after")}</label><div class="b-deps">${others.map((x) => `<span class="chip ta ${d.trigger_after.includes(x) ? "on" : ""}" role="checkbox" tabindex="0" aria-checked="${d.trigger_after.includes(x)}" data-ta="${esc(x)}">${esc(x)}</span>`).join("")}</div></div>` : ""}
    </div>

    <div class="section-h">${t("sec_structure")}</div>
    <div id="d-structure"></div>

    <div class="section-h">${t("sched")}</div>
    <div id="d-sched"></div>

    <div class="b-errors" id="dag-errors"></div>

    <div class="section-h">${t("sec_runs")}</div><div id="d-runs"></div>`;

  $("back").onclick = loadDags;
  $("trig").onclick = triggerActiveDag;
  $("pause").onclick = async () => { await api(`/api/dags/${d.dag_id}/pause?paused=${!d.paused}`, { method: "POST" }); d.paused = !d.paused; renderDagPage(); };
  $("del").onclick = deleteActiveDag;
  const max = $("d-max"); max.onblur = () => { d.max_active_runs = +max.value || 1; saveDag(); };
  const defr = $("d-defr"); defr.onblur = () => { d.default_retries = +defr.value || 0; saveDag(); };
  main.querySelectorAll(".chip.ta").forEach((c) => c.onclick = () => { const x = c.dataset.ta, i = d.trigger_after.indexOf(x); i < 0 ? d.trigger_after.push(x) : d.trigger_after.splice(i, 1); c.classList.toggle("on"); c.setAttribute("aria-checked", c.classList.contains("on")); saveDag(); });

  SCHED = { state: D, idp: "d", host: "d-sched", onChange: saveDag };
  renderSchedUI();
  renderDagStructure();
  renderDagRuns();
  reflectSaveState();
}

async function deleteActiveDag() {
  const id = D.dag.dag_id;
  if (!(await confirmDialog(t("confirm_del_dag_title", id), t("confirm_del_dag_body"), { danger: true, okLabel: t("btn_delete") }))) return;
  // Block any pending/in-flight/re-entrant save — otherwise a debounced or
  // queued save could fire after the delete and re-create (revive) the DAG.
  deleting = true;
  clearTimeout(saveTimer); saveTimer = null; savePending = false;
  while (saveInflight) await new Promise((r) => setTimeout(r, 30));
  try { await api(`/api/dags/${id}`, { method: "DELETE" }); toast(t("toast_dag_deleted"), "ok"); loadDags(); }
  catch (e) { toast(e.message, "fail"); } // e.g. 409 "has active runs"
  finally { deleting = false; }
}

async function triggerActiveDag() {
  const b = $("trig"); b.disabled = true;
  await flushPendingSaves(); // run the latest saved definition, not a stale one
  try { await api(`/api/dags/${D.dag.dag_id}/trigger`, { method: "POST" }); toast(t("toast_run_queued"), "ok"); setTimeout(refreshDagRuns, 500); }
  catch (e) { toast(e.message, "fail"); }
  finally { if ($("trig")) b.disabled = D.tasks.length === 0; }
}
async function refreshDagRuns() {
  if (view !== "dag" || !D) return;
  try { D.runs = (await api(`/api/dags/${D.dag.dag_id}/runs?limit=25`)) || []; renderDagRuns(); } catch (_) {}
}
function renderDagRuns() {
  const el = $("d-runs"); if (!el) return;
  if (!D.runs.length) { el.innerHTML = `<div class="empty">${t("no_runs")}</div>`; return; }
  el.innerHTML = `<table class="tbl"><thead><tr><th>${t("th_logical")}</th><th>${t("th_state")}</th><th>${t("th_trig")}</th><th>${t("th_started")}</th><th>${t("th_dur")}</th></tr></thead>
    <tbody>${D.runs.map((r) => `<tr class="row" data-run="${esc(r.run_id)}"><td class="mono">${esc(r.logical_date)}</td><td>${badge(r.state)}</td><td>${typeLabel(r.trigger_type)}</td><td>${fmt(r.started_at)}</td><td>${dur(r.started_at, r.finished_at)}</td></tr>`).join("")}</tbody></table>`;
  el.querySelectorAll("tr.row").forEach((tr) => tr.onclick = () => showRun(tr.dataset.run));
}

// --- structure section (editable graph + task list) ---
function renderDagStructure() {
  $("d-structure").innerHTML = dagStructureHtml();
  wireDagStructure();
}
function dagStructureHtml() {
  const empty = D.tasks.length === 0;
  const tasks = D.tasks.filter((tk) => tk.id).map((tk) => ({ id: tk.id, deps: (tk.deps || []).filter((dep) => D.tasks.some((x) => x.id === dep)) }));
  const graph = tasks.length
    ? `<div class="page-sub" style="margin:-2px 0 8px">${t("graph_connect_hint")}</div><div class="b-graph" id="d-graph">${renderGraph(tasks, null, { editable: true, pending: D.graphPending })}</div>`
    : "";
  return `${graph}
    <div class="toolbar" style="margin:10px 0 6px"><button class="primary" id="d-addtask">${t("b_addtask")}</button></div>
    ${empty
      ? `<div class="empty-state"><div class="es-ic">▦</div><div class="es-t">${t("dag_no_tasks_title")}</div><div class="es-s">${t("dag_no_tasks_sub")}</div></div>`
      : dagTaskTableHtml()}`;
}
function dagTaskTableHtml() {
  return `<table class="tbl tasks"><thead><tr><th>${t("th_id")}</th><th>${t("th_type")}</th><th>${t("th_command")}</th><th>${t("h_pool")}</th><th>${t("t_rule")}</th><th>${t("th_deps")}</th><th style="width:44px"></th></tr></thead>
    <tbody>${D.tasks.map((tk) => `<tr class="row" data-task="${esc(tk.id)}">
      <td class="mono">${esc(tk.id || "—")}</td><td>${esc(tk.type)}</td>
      <td class="muted mono cmd-cell" title="${esc(tk.command || "")}">${esc(tk.command || "—")}</td>
      <td class="mono">${esc(tk.pool)}</td><td class="muted">${t("tr_" + (tk.trigger_rule || "all_success"))}</td>
      <td class="muted">${esc((tk.deps || []).join(", ") || "—")}</td>
      <td><button class="icon rm no-nav" data-del="${esc(tk.id)}" title="${t("b_remove")}">✕</button></td></tr>`).join("")}</tbody></table>`;
}
function wireDagStructure() {
  const add = $("d-addtask"); if (add) add.onclick = addTask;
  document.querySelectorAll("#d-graph [data-node]").forEach((n) => n.onclick = () => onDagGraphNodeClick(n.dataset.node));
  const sct = $("d-structure");
  sct.querySelectorAll("tr.row").forEach((tr) => tr.onclick = (e) => { if (!e.target.closest(".no-nav")) showTask(D.dag.dag_id, tr.dataset.task); });
  sct.querySelectorAll("[data-del]").forEach((b) => b.onclick = (e) => { e.stopPropagation(); deleteTask(b.dataset.del); });
}
// click upstream then downstream to add/remove the downstream's dependency
function onDagGraphNodeClick(id) {
  if (D.graphPending === null) { D.graphPending = id; renderDagStructure(); return; }
  if (D.graphPending === id) { D.graphPending = null; renderDagStructure(); return; }
  const up = D.graphPending, down = id, dt = D.tasks.find((x) => x.id === down);
  D.graphPending = null;
  if (!dt) { renderDagStructure(); return; }
  const j = dt.deps.indexOf(up);
  if (j >= 0) { dt.deps.splice(j, 1); renderDagStructure(); saveDag(); return; }
  dt.deps.push(up);
  if (hasCycle(D.tasks.filter((x) => x.id))) { dt.deps.pop(); toast(t("err_cycle"), "warn"); renderDagStructure(); return; }
  renderDagStructure(); saveDag();
}
function addTask() {
  let n = D.tasks.length + 1, id;
  do { id = "task_" + n; n++; } while (D.tasks.some((x) => x.id === id));
  const tk = blankTask(); tk.id = id;
  D.tasks.push(tk);
  saveDag(); // held (empty command) until the task page fills it in
  showTask(D.dag.dag_id, id);
}
async function deleteTask(taskID) {
  if (!(await confirmDialog(t("confirm_del_task_title", taskID), "", { danger: true, okLabel: t("btn_delete") }))) return;
  D.tasks = D.tasks.filter((x) => x.id !== taskID);
  D.tasks.forEach((x) => { x.deps = (x.deps || []).filter((dep) => dep !== taskID); }); // scrub deps to the removed task
  saveDag();
  renderDagPage();
}

// ============================================================================
// Task edit page (view='task') — full page, auto-saves the whole DAG on change.
// ============================================================================
function showTask(dagID, taskID) {
  view = "task"; activeDag = dagID; D.activeTaskId = taskID; closeLog();
  setNav("dags", `${dagID} / ${taskID}`);
  renderTaskPage();
}
function renderTaskPage() {
  if (!D) { loadDags(); return; }
  const tk = D.tasks.find((x) => x.id === D.activeTaskId);
  if (!tk) { showDag(D.dag.dag_id); return; }
  const siblings = D.tasks.map((x) => x.id).filter((id) => id && id !== tk.id);
  main.innerHTML = `
    <div class="crumb-bar"><a id="t-back">${t("back_dags")}</a> / <a id="t-dag">${esc(D.dag.dag_id)}</a> / ${esc(tk.id || "—")}</div>
    <div class="page-h"><h1 class="mono">${esc(tk.id || "—")}</h1><span class="savestate ss-saved" id="t-save"></span></div>
    <div class="form-page">
      <div class="tc-grid">
        <div class="b-field"><label>${t("t_id")}</label><input class="tf" data-k="id" value="${esc(tk.id)}" placeholder="step_a"></div>
        <div class="b-field"><label>${t("t_type")}</label><select class="tf" data-k="type">${["shell", "python", "sql", "jar"].map((o) => `<option ${tk.type === o ? "selected" : ""}>${o}</option>`).join("")}</select></div>
        <div class="b-field"><label>${t("t_pool")}</label><input class="tf" data-k="pool" value="${esc(tk.pool)}" placeholder="default"></div>
        <div class="b-field full"><label>${t("t_command")}</label><textarea class="tf cmd" data-k="command" rows="4" spellcheck="false" placeholder="echo running {{ logical_date }}">${esc(tk.command)}</textarea></div>
        <div class="b-field"><label>${t("t_priority")}</label><input class="tf" data-k="priority" type="number" value="${esc(tk.priority)}"></div>
        <div class="b-field"><label>${t("t_retries")}</label><input class="tf" data-k="retries" type="number" min="0" value="${esc(tk.retries)}"></div>
        <div class="b-field"><label>${t("t_retrydelay")}</label><input class="tf" data-k="retry_delay" type="number" min="0" value="${esc(tk.retry_delay)}"></div>
        <div class="b-field"><label>${t("t_timeout")}</label><input class="tf" data-k="timeout" type="number" min="0" value="${esc(tk.timeout)}"></div>
        <div class="b-field"><label>${t("t_rule")}</label><select class="tf" data-k="trigger_rule">${TRIGGER_RULES.map((r) => `<option value="${r}" ${tk.trigger_rule === r ? "selected" : ""}>${t("tr_" + r)}</option>`).join("")}</select></div>
      </div>
      <div class="section-h">${t("t_deps")}</div>
      <div class="b-deps">${siblings.length ? siblings.map((id) => `<span class="chip dep ${tk.deps.includes(id) ? "on" : ""}" role="checkbox" tabindex="0" aria-checked="${tk.deps.includes(id)}" data-dep="${esc(id)}">${esc(id)}</span>`).join("") : `<span class="chip empty-hint">${t("t_nodeps")}</span>`}</div>
      <div class="b-errors" id="task-errors"></div>
    </div>
    <div class="form-foot"><button id="t-back2">${t("back_dag", D.dag.dag_id)}</button></div>`;

  $("t-back").onclick = loadDags;
  $("t-dag").onclick = gotoDagPage;
  $("t-back2").onclick = gotoDagPage;

  main.querySelectorAll(".tf").forEach((el) => {
    const k = el.dataset.k;
    if (el.tagName === "SELECT") { el.onchange = () => { tk[k] = el.value; saveDag(); }; return; }
    if (k === "id") { el.onblur = () => renameActiveTask(tk, el.value.trim()); return; } // keep old id stable until blur
    el.oninput = () => { tk[k] = el.type === "number" ? (el.value === "" ? "" : +el.value) : el.value; };
    el.onblur = () => saveDag();
  });
  main.querySelectorAll(".chip.dep").forEach((c) => c.onclick = () => {
    const dep = c.dataset.dep, arr = tk.deps, j = arr.indexOf(dep);
    j < 0 ? arr.push(dep) : arr.splice(j, 1); c.classList.toggle("on");
    if (hasCycle(D.tasks.filter((x) => x.id))) { // revert on cycle
      const k = arr.indexOf(dep); k >= 0 ? arr.splice(k, 1) : arr.push(dep); c.classList.toggle("on"); toast(t("err_cycle"), "warn"); return;
    }
    c.setAttribute("aria-checked", c.classList.contains("on"));
    saveDag();
  });
  reflectSaveState();
}
// rename the task and rewrite sibling deps that referenced the old id
function renameActiveTask(tk, newId) {
  const oldId = tk.id;
  if (newId === oldId) return;
  // Reject empty / invalid-charset / duplicate ids: revert and tell the user.
  // (An empty intermediate id would drop inbound deps; a dup would alias two
  // tasks to one id and corrupt find-by-id.)
  if (!newId || !ID_RE.test(newId) || D.tasks.some((x) => x !== tk && x.id === newId)) {
    toast(!newId ? t("err_emptyid") : !ID_RE.test(newId) ? t("err_taskid") : t("err_dup"), "warn");
    renderTaskPage(); // input snaps back to the unchanged old id
    return;
  }
  tk.id = newId;
  D.tasks.forEach((x) => { if (x !== tk) { const i = (x.deps || []).indexOf(oldId); if (i >= 0) x.deps[i] = newId; } }); // rewrite inbound deps
  D.activeTaskId = newId;
  saveDag();
  renderTaskPage(); // refresh breadcrumb + dependency chips
}

// ---- shared validation + immediate save ----
function hasCycle(tasks) {
  const byId = {}; tasks.forEach((tk) => byId[tk.id] = tk);
  const color = {}; let cyc = false;
  const visit = (id) => { if (color[id] === 1) { cyc = true; return; } if (color[id] === 2) return; color[id] = 1; (byId[id]?.deps || []).forEach((d) => { if (byId[d]) visit(d); }); color[id] = 2; };
  tasks.forEach((tk) => { if (tk.id && color[tk.id] === undefined) visit(tk.id); });
  return cyc;
}
// whole-DAG validity (0 tasks is allowed — a shell). Returns localized errors.
function validateDag() {
  const e = [], ids = D.tasks.map((x) => x.id), nonEmpty = ids.filter(Boolean);
  if (ids.some((id) => !id)) e.push(t("err_emptyid"));
  if (nonEmpty.some((id) => !ID_RE.test(id))) e.push(t("err_taskid"));
  if (new Set(nonEmpty).size !== nonEmpty.length) e.push(t("err_dup"));
  if (D.tasks.some((x) => x.id && !String(x.command).trim())) e.push(t("err_emptycmd"));
  if (hasCycle(D.tasks.filter((x) => x.id))) e.push(t("err_cycle"));
  return e;
}
function dagSpecFrom(st) {
  const d = st.dag;
  return {
    dag_id: d.dag_id, schedule: d.schedule, start_date: d.start_date,
    catchup: !!d.catchup, max_active_runs: +d.max_active_runs || 1, default_retries: +d.default_retries || 0,
    trigger_after: (d.trigger_after || []).slice(),
    tasks: st.tasks.filter((tk) => tk.id).map((tk) => ({
      id: tk.id, type: tk.type, command: tk.command, pool: tk.pool || "default",
      priority: +tk.priority || 0, deps: (tk.deps || []).filter((dep) => st.tasks.some((x) => x.id === dep)),
      timeout: +tk.timeout || 0, trigger_rule: tk.trigger_rule || "all_success",
      retries: tk.retries === "" || tk.retries == null ? null : +tk.retries,
      retry_delay: tk.retry_delay === "" || tk.retry_delay == null ? null : +tk.retry_delay,
    })),
  };
}
function setSaveState(state, msg) {
  document.querySelectorAll(".savestate").forEach((el) => {
    el.className = "savestate ss-" + state;
    el.textContent = state === "error" ? (t("ss_error") + (msg ? ": " + msg : "")) : t("ss_" + state);
  });
}
function showErrors(errs, id) { const el = $(id); if (el) el.innerHTML = errs.map((x) => `<div class="e fatal">${esc(x)}</div>`).join(""); }
// set the save indicator from the CURRENT in-memory validity — called at the end
// of a render so an invalid edit shows "invalid" + errors, not a stale "Saved".
function reflectSaveState() {
  const errs = validateDag();
  showErrors(errs, view === "task" ? "task-errors" : "dag-errors");
  if (errs.length) { setSaveState("invalid"); return; }
  setSaveState(saveTimer || saveInflight ? "saving" : "saved");
}

let saveTimer = null, saveSeq = 0, saveInflight = false, savePending = false, deleting = false;
// Debounced, serialized, valid-only whole-DAG save (immediate-save model).
function saveDag() {
  if (deleting) return; // a delete is committing; never re-create the DAG
  const errs = validateDag();
  showErrors(errs, view === "task" ? "task-errors" : "dag-errors");
  if (errs.length) { setSaveState("invalid"); return; }
  setSaveState("saving");
  clearTimeout(saveTimer);
  saveTimer = setTimeout(flushSave, 400);
}
// Run any debounced/in-flight save to completion (called before a refetch that
// would replace D, so a pending edit lands against the correct DAG first).
async function flushPendingSaves() {
  if (saveTimer) { clearTimeout(saveTimer); saveTimer = null; await flushSave(); }
  while (saveInflight) await new Promise((r) => setTimeout(r, 30));
}
async function flushSave() {
  if (deleting) return; // do not (re-)issue a save while a delete is committing
  if (saveInflight) { savePending = true; return; }
  saveInflight = true;
  const seq = ++saveSeq;
  computeSchedule(D);
  const spec = dagSpecFrom(D);
  try {
    await api("/api/dags/build", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(spec) });
    if (seq === saveSeq) setSaveState("saved");
  } catch (e) {
    if (seq === saveSeq) setSaveState("error", e.message);
  } finally {
    saveInflight = false;
    if (savePending) { savePending = false; flushSave(); }
  }
}

// ---- run detail ----
async function showRun(runID) {
  view = "run"; currentRun = runID; closeLog();
  const data = await api(`/api/runs/${encodeURIComponent(runID)}`);
  const r = data.run, dag = await api(`/api/dags/${r.dag_id}`);
  setNav("dags", `${r.dag_id} / ${t("run_word")}`);
  const tasks = data.tasks || []; // a freshly-queued run has no task instances yet
  const sbt = {}; tasks.forEach((tk) => sbt[tk.task_id] = tk.state);
  main.innerHTML = `
    <div class="crumb-bar"><a id="back">← ${esc(r.dag_id)}</a> / ${t("run_word")}</div>
    <div class="page-h"><h1 class="mono" style="font-size:16px">${esc(r.run_id)}</h1>${badge(r.state)}</div>
    <div class="kv" style="margin:14px 0 4px">
      <div class="card"><div class="k">${t("k_logical")}</div><div class="v mono" style="font-size:13px">${esc(r.logical_date)}</div></div>
      <div class="card"><div class="k">${t("k_trig")}</div><div class="v">${typeLabel(r.trigger_type)}</div></div>
      <div class="card"><div class="k">${t("k_dur")}</div><div class="v">${dur(r.started_at, r.finished_at)}</div></div>
      <div class="card"><div class="k">${t("k_started")}</div><div class="v" style="font-size:13px">${fmt(r.started_at)}</div></div></div>
    <div class="section-h">${t("sec_graph")}</div>${renderGraph(dag.tasks, sbt)}
    <div class="section-h">${t("sec_instances")}</div>
    <table class="tbl"><thead><tr><th>${t("th_task")}</th><th>${t("th_state")}</th><th>${t("th_try")}</th><th>${t("h_pool")}</th><th>${t("th_dur")}</th><th></th></tr></thead>
    <tbody>${tasks.map((tk) => `<tr><td class="mono">${esc(tk.task_id)}</td><td>${badge(tk.state)}</td><td>${tk.try_number}/${tk.max_retries + 1}</td><td class="mono">${esc(tk.pool)}</td><td>${dur(tk.started_at, tk.finished_at)}</td><td><button class="icon logbtn" data-ti="${tk.id}" data-task="${esc(tk.task_id)}">${t("th_logs")}</button></td></tr>`).join("")}</tbody></table>
    <div id="logwrap"></div>`;
  $("back").onclick = () => showDag(r.dag_id);
  main.querySelectorAll(".logbtn").forEach((b) => b.onclick = () => showLog(b.dataset.ti, b.dataset.task));
}
function showLog(tiID, taskID) {
  closeLog();
  $("logwrap").innerHTML = `<div class="section-h">${t("log_word")} · <span class="mono">${esc(taskID)}</span> <span class="live" id="live"></span></div><div class="logbox" id="logbox"></div>`;
  const box = $("logbox");
  logES = new EventSource(`/api/tasks/${tiID}/log/stream`);
  $("live").innerHTML = `<span class="p"></span>${t("live")}`;
  logES.onmessage = (e) => { box.textContent += e.data + "\n"; box.scrollTop = box.scrollHeight; };
  logES.addEventListener("done", () => { closeLog(); $("live").textContent = ""; });
  logES.onerror = () => { closeLog(); $("live").textContent = ""; };
}

// ---- pools ----
async function showPools() {
  view = "pools"; activeDag = null; closeLog(); setNav("pools");
  const pools = await api("/api/pools");
  $("nav-pools").textContent = pools.length;
  main.innerHTML = `
    <div class="page-h"><h1>Pools</h1><span class="num">${pools.length}</span></div>
    <div class="page-sub">${t("pools_sub")}</div>
    <table class="tbl"><thead><tr><th>${t("p_name")}</th><th>${t("p_slots")}</th><th></th></tr></thead>
    <tbody>${pools.map((p) => `<tr><td class="mono">${esc(p.name)}</td><td><input type="number" min="1" value="${p.slots}" data-pool="${esc(p.name)}" style="width:90px"></td><td><button data-save="${esc(p.name)}">${t("p_save")}</button></td></tr>`).join("")}</tbody></table>
    <div class="toolbar" style="margin-top:16px"><input id="np" placeholder="${t("p_newname")}"><input id="ns" type="number" min="1" value="4" style="width:90px"><button class="primary" id="addp">${t("p_create")}</button></div>`;
  const save = async (name, slots) => { if (!name || !(slots > 0)) { toast(t("p_need"), "warn"); return; } try { await api(`/api/pools/${encodeURIComponent(name)}?slots=${slots}`, { method: "POST" }); toast(t("toast_pool_saved"), "ok"); showPools(); } catch (e) { toast(e.message, "fail"); } };
  main.querySelectorAll("button[data-save]").forEach((b) => b.onclick = () => save(b.dataset.save, +main.querySelector(`input[data-pool="${CSS.escape(b.dataset.save)}"]`).value));
  $("addp").onclick = () => save($("np").value.trim(), +$("ns").value);
}

// ---- global DAG relationship graph (cross-DAG trigger_after) ----
async function showGraph() {
  view = "graph"; activeDag = null; closeLog(); setNav("graph");
  let g;
  try { g = await api("/api/dag-graph"); } catch (e) { main.innerHTML = `<div class="empty">${esc(String(e))}</div>`; return; }
  const nodes = g.nodes || [], edges = g.edges || [];
  // map each DAG to a graph node whose "deps" are its upstream DAGs (trigger_after)
  const depMap = {}; nodes.forEach((n) => depMap[n.dag_id] = []);
  edges.forEach((e) => { (depMap[e.to] ||= []).push(e.from); });
  const tasks = nodes.map((n) => ({ id: n.dag_id, deps: depMap[n.dag_id] || [] }));
  const stateByTask = {}; const dashed = new Set(); const known = new Set();
  nodes.forEach((n) => { if (n.latest_state) stateByTask[n.dag_id] = n.latest_state; if (n.missing) dashed.add(n.dag_id); else known.add(n.dag_id); });
  const linked = edges.length;
  main.innerHTML = `
    <div class="page-h"><h1>${t("graph_title")}</h1><span class="num">${nodes.length}</span></div>
    <div class="page-sub">${t("graph_sub")}</div>
    ${linked === 0 ? `<div class="empty">${t("graph_none")}</div>` : `
      <div class="page-sub" style="margin:-6px 0 10px">${t("graph_view_hint")}</div>
      <div class="b-graph" id="dag-graph">${renderGraph(tasks, stateByTask, { clickable: true, dashed })}</div>`}`;
  main.querySelectorAll("#dag-graph [data-node]").forEach((n) => {
    if (known.has(n.dataset.node)) n.onclick = () => showDag(n.dataset.node);
  });
}

// ============================================================================
// Shared building blocks: blank task, trigger rules, cron data + the schedule
// UI (used by both the DAG operation page and the minimal new-DAG modal).
// ============================================================================
const blankTask = () => ({ id: "", type: "shell", command: "", pool: "default", priority: 0, retries: "", retry_delay: "", timeout: "", deps: [], trigger_rule: "all_success" });
const TRIGGER_RULES = ["all_success", "all_done", "one_success", "one_failed", "all_failed", "none_failed"];

const CRON_PRESETS = [
  { k: "cp_min", v: "* * * * *" }, { k: "cp_hour", v: "0 * * * *" },
  { k: "cp_day", v: "0 0 * * *" }, { k: "cp_2am", v: "0 2 * * *" }, { k: "cp_mon", v: "0 0 * * 1" },
];

// cron cheat-sheet content (bilingual); picked by current lang at render time
const CRON_HELP = {
  fields: {
    zh: [["分", "0-59"], ["时", "0-23"], ["日", "1-31"], ["月", "1-12"], ["周", "0-6（0=周日）"]],
    en: [["min", "0-59"], ["hour", "0-23"], ["day", "1-31"], ["month", "1-12"], ["weekday", "0-6 (0=Sun)"]],
  },
  ops: {
    zh: [["*", "任意值"], [",", "列举，如 1,15"], ["-", "范围，如 1-5"], ["/", "步进，如 */5"]],
    en: [["*", "any value"], [",", "list, e.g. 1,15"], ["-", "range, e.g. 1-5"], ["/", "step, e.g. */5"]],
  },
  examples: {
    zh: [["* * * * *", "每分钟"], ["*/5 * * * *", "每 5 分钟"], ["0 * * * *", "每小时整点"], ["0 9 * * *", "每天 09:00"], ["30 2 * * *", "每天 02:30"], ["0 0 * * 1", "每周一 00:00"], ["0 9 * * 1-5", "工作日 09:00"], ["0 0 1 * *", "每月 1 号 00:00"]],
    en: [["* * * * *", "every minute"], ["*/5 * * * *", "every 5 min"], ["0 * * * *", "every hour"], ["0 9 * * *", "daily 09:00"], ["30 2 * * *", "daily 02:30"], ["0 0 * * 1", "Mon 00:00"], ["0 9 * * 1-5", "weekdays 09:00"], ["0 0 1 * *", "1st of month 00:00"]],
  },
  shortcuts: {
    zh: [["@hourly", "每小时"], ["@daily", "每天"], ["@weekly", "每周"], ["@every 30s", "每 30 秒"], ["@every 5m", "每 5 分钟"]],
    en: [["@hourly", "hourly"], ["@daily", "daily"], ["@weekly", "weekly"], ["@every 30s", "every 30s"], ["@every 5m", "every 5 min"]],
  },
};
function cronHelpHtml(idp) {
  const L = (k) => CRON_HELP[k][lang] || CRON_HELP[k].zh;
  const rows = (arr, clickable) => arr.map(([a, b]) =>
    `<div class="ch-row"><code class="${clickable ? "ch-ex" : ""}" ${clickable ? `data-cron="${esc(a)}"` : ""}>${esc(a)}</code><span>${esc(b)}</span></div>`).join("");
  return `<div class="cron-help" id="${idp}-cronpop" hidden>
    <div class="ch-head"><b>${t("ch_title")}</b><button class="icon" id="${idp}-cronclose">✕</button></div>
    <div class="ch-fmt">${t("ch_format")}</div>
    <div class="ch-cols">
      <div><div class="ch-h">${t("ch_fields")}</div>${rows(L("fields"))}</div>
      <div><div class="ch-h">${t("ch_ops")}</div>${rows(L("ops"))}</div>
    </div>
    <div class="ch-h" style="margin-top:8px">${t("ch_examples")}</div>${rows(L("examples"), true)}
    <div class="ch-h" style="margin-top:8px">${t("ch_shortcuts")}</div>${rows(L("shortcuts"), true)}
  </div>`;
}

// derive the schedule UI mode (manual / every / cron) from a schedule string
function parseScheduleState(dag) {
  const s = dag.schedule || "";
  dag.schedMode = "manual"; dag.schedEvery = 5; dag.schedUnit = "m"; dag.schedCron = "";
  if (s) {
    const m = /^@every\s+(\d+)(s|m|h)$/.exec(s);
    if (m) { dag.schedMode = "every"; dag.schedEvery = +m[1]; dag.schedUnit = m[2]; }
    else { dag.schedMode = "cron"; dag.schedCron = s; }
  }
  return dag;
}
// rebuild the schedule string the backend receives, from the UI state
function computeSchedule(state) {
  const d = state.dag;
  if (d.schedMode === "manual") d.schedule = "";
  else if (d.schedMode === "every") d.schedule = d.schedEvery > 0 ? `@every ${d.schedEvery}${d.schedUnit}` : "";
  else d.schedule = (d.schedCron || "").trim();
}
function schedModesHtml(state) {
  return `<div class="sched-modes">${["manual", "every", "cron"].map((m) => `<button class="pill ${state.dag.schedMode === m ? "active" : ""}" data-mode="${m}">${t("sm_" + m)}</button>`).join("")}</div>`;
}
function schedBodyHtml(state, idp) {
  const d = state.dag;
  let body = "";
  if (d.schedMode === "manual") {
    body = `<div class="page-sub" style="margin:6px 0">${t("sched_manual_hint")}</div>`;
  } else if (d.schedMode === "every") {
    body = `<div class="sched-every"><span class="muted">${t("sched_every_pre")}</span>
      <input id="${idp}-ev" type="number" min="1" value="${d.schedEvery}" style="width:90px">
      <select id="${idp}-eu">${["s", "m", "h"].map((u) => `<option value="${u}" ${d.schedUnit === u ? "selected" : ""}>${t("unit_" + u)}</option>`).join("")}</select></div>`;
  } else {
    body = `<div class="cron-wrap">
      <div style="display:flex;align-items:center;gap:8px">
        <input id="${idp}-cron" value="${esc(d.schedCron)}" placeholder="0 2 * * *" style="max-width:260px">
        <button class="icon" id="${idp}-cronhelp" type="button">${t("cron_help")} ?</button>
      </div>
      <div class="b-deps" style="margin-top:8px">${CRON_PRESETS.map((p) => `<span class="chip cronp" data-cron="${esc(p.v)}">${t(p.k)}</span>`).join("")}</div>
      ${cronHelpHtml(idp)}
    </div>`;
  }
  const sd = d.schedMode !== "manual"
    ? `<div class="b-field" style="max-width:220px;margin-top:12px"><label>${t("f_start")}</label><input id="${idp}-start" type="date" value="${esc(d.start_date || "")}"></div>` : "";
  const cu = `<div class="b-field b-check" style="margin-top:12px"><input type="checkbox" id="${idp}-catchup" ${d.catchup ? "checked" : ""} disabled><label for="${idp}-catchup" style="color:var(--faint)">${t("f_catchup")} ${t("disabled_note")}</label></div>`;
  return body + sd + cu;
}
// renders the full schedule UI (mode pills + body) into SCHED.host
function renderSchedUI() {
  const { state, idp, host } = SCHED;
  $(host).innerHTML = schedModesHtml(state) + `<div id="${idp}-schedbody"></div>`;
  renderSchedBody();
  wireSchedModes();
}
function renderSchedBody() {
  const { state, idp } = SCHED;
  $(idp + "-schedbody").innerHTML = schedBodyHtml(state, idp);
  wireSchedBody();
}
function wireSchedModes() {
  const { state, host, onChange } = SCHED;
  $(host).querySelectorAll(".sched-modes .pill[data-mode]").forEach((p) => p.onclick = () => {
    state.dag.schedMode = p.dataset.mode; computeSchedule(state);
    $(host).querySelectorAll(".sched-modes .pill").forEach((x) => x.classList.toggle("active", x === p));
    renderSchedBody(); if (onChange) onChange();
  });
}
function wireSchedBody() {
  const { state, idp, onChange } = SCHED, d = state.dag;
  const body = $(idp + "-schedbody");
  const fire = () => { if (onChange) onChange(); };
  const ev = $(idp + "-ev"); if (ev) { ev.oninput = () => { d.schedEvery = +ev.value || 0; computeSchedule(state); }; ev.onblur = fire; }
  const eu = $(idp + "-eu"); if (eu) eu.onchange = () => { d.schedUnit = eu.value; computeSchedule(state); fire(); };
  const cron = $(idp + "-cron"); if (cron) { cron.oninput = () => { d.schedCron = cron.value; computeSchedule(state); }; cron.onblur = fire; }
  body.querySelectorAll(".chip.cronp").forEach((c) => c.onclick = () => { d.schedCron = c.dataset.cron; computeSchedule(state); renderSchedBody(); fire(); });
  const hb = $(idp + "-cronhelp"); if (hb) hb.onclick = () => { const p = $(idp + "-cronpop"); if (p) p.hidden = !p.hidden; };
  const hc = $(idp + "-cronclose"); if (hc) hc.onclick = () => { const p = $(idp + "-cronpop"); if (p) p.hidden = true; };
  body.querySelectorAll(".ch-ex").forEach((c) => c.onclick = () => { d.schedCron = c.dataset.cron; computeSchedule(state); renderSchedBody(); fire(); });
  const sd = $(idp + "-start"); if (sd) sd.oninput = () => { d.start_date = sd.value; fire(); };
}

// ============================================================================
// New-DAG minimal modal: collects dag_id (+ optional schedule), creates a
// 0-task shell, then drops into its operation page to add tasks.
// ============================================================================
function newDagModal() {
  const blank = () => parseScheduleState({ dag_id: "", schedule: "", start_date: "", catchup: false, max_active_runs: 1, default_retries: 0 });
  api("/api/dags").then((list) => { ND = { existing: new Set(list.map((d) => d.dag_id)), dag: blank() }; renderNewDag(); })
    .catch(() => { ND = { existing: new Set(), dag: blank() }; renderNewDag(); });
}
function renderNewDag() {
  const opener = document.activeElement; // restore focus here on close
  $("modal-root").innerHTML = `<div class="overlay" id="ovl"><div class="modal" role="dialog" aria-modal="true" aria-labelledby="nd-h2">
    <h2 id="nd-h2">${t("nd_title")}</h2>
    <div class="body">
      <div class="b-field"><label>${t("f_dag_id")}</label><input id="nd-id" placeholder="my_workflow" value="${esc(ND.dag.dag_id)}"></div>
      <div class="nd-err" id="nd-err"></div>
      <div class="b-sec" style="margin-top:12px">${t("sched")}</div>
      <div id="nd-sched"></div>
    </div>
    <div class="foot"><span class="err" id="nd-srv"></span><button id="nd-cancel">${t("nd_cancel")}</button><button class="primary" id="nd-create">${t("nd_create")}</button></div>
  </div></div>`;
  const close = () => { document.removeEventListener("keydown", onKey); $("modal-root").innerHTML = ""; if (opener && opener.focus) opener.focus(); };
  // Escape closes the cron-help popover first (if open), else the modal. Enter
  // submits when the create button is enabled (not while typing in a textarea).
  const onKey = (e) => {
    if (!$("nd-create")) { document.removeEventListener("keydown", onKey); return; } // modal gone (e.g. after submit)
    if (e.key === "Escape") {
      const pop = $("nd-cronpop");
      if (pop && !pop.hidden) { pop.hidden = true; return; }
      close();
    } else if (e.key === "Enter" && e.target.tagName !== "TEXTAREA" && !$("nd-create").disabled) {
      submitNewDag();
    }
  };
  document.addEventListener("keydown", onKey);
  $("nd-cancel").onclick = close;
  $("ovl").onclick = (e) => { if (e.target.id === "ovl") close(); };
  const idEl = $("nd-id");
  idEl.oninput = () => { ND.dag.dag_id = idEl.value.trim(); updateNewDagValidity(); };
  idEl.focus();
  SCHED = { state: ND, idp: "nd", host: "nd-sched", onChange: null };
  renderSchedUI();
  $("nd-create").onclick = submitNewDag;
  updateNewDagValidity();
}
function updateNewDagValidity() {
  const id = ND.dag.dag_id; let err = "";
  if (id && !ID_RE.test(id)) err = t("err_dagid");
  else if (id && ND.existing.has(id)) err = t("nd_dagid_dup");
  $("nd-err").textContent = err;
  $("nd-create").disabled = !id || !!err;
}
async function submitNewDag() {
  const btn = $("nd-create"); btn.disabled = true; $("nd-srv").textContent = "";
  computeSchedule(ND);
  const spec = { dag_id: ND.dag.dag_id, schedule: ND.dag.schedule, start_date: ND.dag.start_date, catchup: false, max_active_runs: 1, default_retries: 0, trigger_after: [], tasks: [] };
  try { await api("/api/dags/build", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(spec) }); $("modal-root").innerHTML = ""; showDag(ND.dag.dag_id); }
  catch (e) { $("nd-srv").textContent = e.message; btn.disabled = false; }
}

// ---- theme ----
function applyTheme() {
  document.documentElement.dataset.theme = theme;
  const b = $("theme"); if (b) { b.textContent = theme === "dark" ? "☀" : "☾"; b.setAttribute("aria-pressed", theme === "light"); b.setAttribute("aria-label", t("aria_theme")); }
}

// ---- boot ----
$("search").oninput = (e) => { query = e.target.value.toLowerCase(); renderDags(); };
$("newdag").onclick = () => newDagModal();
$("lang").onclick = () => setLang(lang === "zh" ? "en" : "zh");
$("theme").onclick = () => { theme = theme === "dark" ? "light" : "dark"; localStorage.setItem("cnv_theme", theme); applyTheme(); };
applyTheme();
document.querySelectorAll(".nav-item[data-nav]").forEach((n) => n.onclick = () => { const v = n.dataset.nav; v === "pools" ? showPools() : v === "graph" ? showGraph() : loadDags(); });
// One delegated keydown (on the stable document, survives every innerHTML swap):
// Enter/Space activates any focusable widget we expose with a role (rows, toggles,
// chips, nav items) — so the focus ring lands on something operable. (#5)
document.addEventListener("keydown", (e) => {
  if (e.key !== "Enter" && e.key !== " ") return;
  const el = e.target;
  if (el && el.matches && el.matches('[tabindex="0"][role]:not(input):not(textarea):not(select)')) { e.preventDefault(); el.click(); }
});
applyStaticI18n();
loadInfo();
loadDags().catch((e) => { main.innerHTML = `<div class="empty err">${t("api_err")}: ${esc(e.message)}</div>`; });
// Heartbeat: refresh the executor/scheduler footer (honest tick) every cycle,
// and the dashboard ONLY when it's showing AND the data actually changed (no
// gratuitous table rebuild / flash). Never touches the edit pages.
setInterval(async () => {
  loadInfo();
  if (view !== "dags" || logES || $("modal-root").innerHTML) return;
  try {
    const fresh = await api("/api/overview");
    if (JSON.stringify(fresh) !== JSON.stringify(overviewCache)) {
      overviewCache = fresh; $("nav-dags").textContent = fresh.stats.total_dags; renderDags();
    }
  } catch (_) {}
}, 6000);
