"use strict";
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
  const prev = d.schedMode !== "manual" ? `<div class="sched-preview" id="${idp}-schedprev" aria-live="polite"></div>` : "";
  const sd = d.schedMode !== "manual"
    ? `<div class="b-field" style="max-width:220px;margin-top:12px"><label>${t("f_start")}</label><input id="${idp}-start" type="date" value="${esc(d.start_date || "")}"></div>` : "";
  const cu = `<div class="b-field b-check" style="margin-top:12px"><input type="checkbox" id="${idp}-catchup" ${d.catchup ? "checked" : ""} disabled><label for="${idp}-catchup" style="color:var(--faint)">${t("f_catchup")} ${t("disabled_note")}</label></div>`;
  return body + prev + sd + cu;
}
// plain-language gloss of the schedule — ONLY for shapes we know for sure
// (@every, preset-matched cron); anything else gets just the server-computed
// fire times, never a guessed description.
function schedSentence(d) {
  if (d.schedMode === "every" && d.schedEvery > 0) return t("sp_every", d.schedEvery, t("unit_" + d.schedUnit));
  if (d.schedMode === "cron") { const p = CRON_PRESETS.find((x) => x.v === (d.schedCron || "").trim()); if (p) return t(p.k); }
  return "";
}
let spTimer = null, spSeq = 0;
function schedPreviewSoon() { clearTimeout(spTimer); spTimer = setTimeout(updateSchedPreview, 350); }
async function updateSchedPreview() {
  if (!SCHED) return;
  const { state, idp } = SCHED, el = $(idp + "-schedprev");
  if (!el) return;
  const d = state.dag;
  if (d.schedMode === "manual" || !d.schedule) { el.innerHTML = ""; return; }
  const seq = ++spSeq, snap = d.schedule;
  try {
    const r = await api(`/api/schedule/preview?schedule=${encodeURIComponent(snap)}&start_date=${encodeURIComponent(d.start_date || "")}&n=3`);
    if (seq !== spSeq || d.schedule !== snap || !$(idp + "-schedprev")) return; // stale response or view changed
    const sent = schedSentence(d);
    const times = (r.fires || []).map((f) => new Date(f).toLocaleString()).join("  ·  ");
    el.classList.remove("err");
    el.innerHTML = `${sent ? `<b>${esc(sent)}</b> — ` : ""}${t("sp_next")}: ${esc(times) || "—"}`;
  } catch (_) {
    if (seq !== spSeq || d.schedule !== snap || !$(idp + "-schedprev")) return;
    el.classList.add("err");
    el.textContent = t("sp_invalid");
  }
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
  // save on input (debounced by saveDag) so a button-click close never strands an
  // unsaved schedule edit; fire() is a no-op where onChange is null (new-DAG modal).
  const ev = $(idp + "-ev"); if (ev) { ev.oninput = () => { d.schedEvery = +ev.value || 0; computeSchedule(state); schedPreviewSoon(); fire(); }; }
  const eu = $(idp + "-eu"); if (eu) eu.onchange = () => { d.schedUnit = eu.value; computeSchedule(state); schedPreviewSoon(); fire(); };
  const cron = $(idp + "-cron"); if (cron) { cron.oninput = () => { d.schedCron = cron.value; computeSchedule(state); schedPreviewSoon(); fire(); }; }
  body.querySelectorAll(".chip.cronp").forEach((c) => c.onclick = () => { d.schedCron = c.dataset.cron; computeSchedule(state); renderSchedBody(); fire(); });
  const hb = $(idp + "-cronhelp"); if (hb) hb.onclick = () => { const p = $(idp + "-cronpop"); if (p) p.hidden = !p.hidden; };
  const hc = $(idp + "-cronclose"); if (hc) hc.onclick = () => { const p = $(idp + "-cronpop"); if (p) p.hidden = true; };
  body.querySelectorAll(".ch-ex").forEach((c) => c.onclick = () => { d.schedCron = c.dataset.cron; computeSchedule(state); renderSchedBody(); fire(); });
  const sd = $(idp + "-start"); if (sd) sd.oninput = () => { d.start_date = sd.value; schedPreviewSoon(); fire(); };
  updateSchedPreview(); // initial fill (renderSchedBody re-runs this on preset clicks/mode switches)
}

// ============================================================================
// New-DAG minimal modal: collects dag_id (+ optional schedule), creates a
// 0-task shell, then drops into its operation page to add tasks.
// ============================================================================
// starter templates: selecting one creates a populated, editable DAG (client-side
// spec -> the existing /api/dags/build). "blank" is the 0-task shell.
const DAG_TEMPLATES = [
  { key: "blank", name: "tpl_blank", desc: "tpl_blank_d", tasks: [] },
  {
    key: "etl", name: "tpl_etl", desc: "tpl_etl_d", tasks: [
      { id: "extract", type: "shell", command: "echo extract {{ logical_date }} && sleep 1" },
      { id: "transform", type: "shell", command: "echo transform && sleep 1", deps: ["extract"] },
      { id: "load", type: "shell", command: "echo load run={{ run_id }} && sleep 1", deps: ["transform"] },
    ],
  },
  {
    key: "report", name: "tpl_report", desc: "tpl_report_d", schedule: "0 8 * * *", tasks: [
      { id: "fetch", type: "shell", command: "echo fetch data for {{ logical_date }} && sleep 1" },
      { id: "render", type: "shell", command: "echo render report && sleep 1", deps: ["fetch"] },
    ],
  },
  {
    key: "fanout", name: "tpl_fanout", desc: "tpl_fanout_d", tasks: [
      { id: "start", type: "shell", command: "echo start && sleep 1" },
      { id: "branch_a", type: "shell", command: "echo branch A && sleep 1", deps: ["start"] },
      { id: "branch_b", type: "shell", command: "echo branch B && sleep 1", deps: ["start"] },
      { id: "join", type: "shell", command: "echo join && sleep 1", deps: ["branch_a", "branch_b"] },
    ],
  },
];
function newDagModal() {
  const blank = () => parseScheduleState({ dag_id: "", schedule: "", start_date: "", catchup: false, max_active_runs: 1, default_retries: 0 });
  api("/api/dags").then((list) => { ND = { existing: new Set(list.map((d) => d.dag_id)), dag: blank(), template: "blank" }; renderNewDag(); })
    .catch(() => { ND = { existing: new Set(), dag: blank(), template: "blank" }; renderNewDag(); });
}
function renderNewDag() {
  const opener = document.activeElement; // restore focus here on close
  // minimal by default: pick a template, name it, create. Schedule + YAML import
  // live behind one "more options" fold — schedule is editable any time later
  // from the DAG's Settings tab, so the happy path stays two decisions.
  const advanced = `
      <div class="b-sec" style="margin-top:14px">${t("sched")}</div>
      <div id="nd-sched"></div>
      <div style="margin-top:14px"><a class="raw-toggle" id="nd-toyaml">${t("nd_import_yaml")}</a></div>`;
  const formBody = `
      <div class="b-sec">${t("tpl_start")}</div>
      <div class="tpl-cards">${DAG_TEMPLATES.map((tp) => `<div class="tpl-card ${ND.template === tp.key ? "on" : ""}" data-tpl="${tp.key}" role="button" tabindex="0" aria-pressed="${ND.template === tp.key}"><div class="tpl-name">${t(tp.name)}</div><div class="tpl-desc">${t(tp.desc)}</div>${tp.tasks.length ? `<div class="tpl-meta">${tp.tasks.length} ${t("tpl_tasks")}${tp.schedule ? " · cron" : ""}</div>` : ""}</div>`).join("")}</div>
      <div class="b-field" style="margin-top:14px"><label>${t("f_dag_id")}</label><input id="nd-id" placeholder="my_workflow" value="${esc(ND.dag.dag_id)}"></div>
      <div class="nd-err" id="nd-err"></div>
      <div style="margin-top:12px"><a class="raw-toggle" id="nd-adv">${ND.advanced ? t("nd_less") : t("nd_more")}</a></div>
      ${ND.advanced ? advanced : ""}`;
  const yamlBody = `
      <div class="b-sec">${t("nd_import_yaml")}</div>
      <textarea id="nd-yaml" class="yaml-input" rows="14" spellcheck="false" placeholder="dag_id: my_workflow\ntasks:\n  - id: hello\n    command: echo hi"></textarea>
      <div style="margin-top:10px"><a class="raw-toggle" id="nd-toform">${t("nd_back_form")}</a></div>`;
  $("modal-root").innerHTML = `<div class="overlay" id="ovl"><div class="modal" role="dialog" aria-modal="true" aria-labelledby="nd-h2">
    <h2 id="nd-h2">${t("nd_title")}</h2>
    <div class="body">${ND.yamlMode ? yamlBody : formBody}</div>
    <div class="foot"><span class="err" id="nd-srv"></span><button id="nd-cancel">${t("nd_cancel")}</button><button class="primary" id="nd-create">${ND.yamlMode ? t("nd_import") : t("nd_create")}</button></div>
  </div></div>`;
  const close = () => {
    document.removeEventListener("keydown", onKey); $("modal-root").innerHTML = "";
    if (opener && opener.focus) opener.focus();
    // the modal's advanced fold rebinds the global SCHED to ND; if a DAG settings
    // schedule row is open underneath, re-render it to restore SCHED → D.
    if (view === "dag" && D && D.editKey === "sched") renderDagTab();
  };
  // Escape closes the cron-help popover first (if open), else the modal. Enter
  // submits when the create button is enabled (not while typing in a textarea).
  const onKey = (e) => {
    if (!$("nd-create")) { document.removeEventListener("keydown", onKey); return; } // modal gone (e.g. after submit)
    if (e.key === "Escape") {
      const pop = $("nd-cronpop");
      if (pop && !pop.hidden) { pop.hidden = true; return; }
      close();
    } else if (e.key === "Enter" && e.target.tagName !== "TEXTAREA" && !$("nd-create").disabled) {
      ND.yamlMode ? importYamlDag() : submitNewDag();
    }
  };
  document.addEventListener("keydown", onKey);
  $("nd-cancel").onclick = close;
  $("ovl").onclick = (e) => { if (e.target.id === "ovl") close(); };
  if (ND.yamlMode) {
    $("nd-toform").onclick = () => { ND.yamlMode = false; renderNewDag(); };
    $("nd-create").onclick = importYamlDag;
    $("nd-yaml").focus();
    return;
  }
  main.ownerDocument.querySelectorAll("#modal-root .tpl-card").forEach((c) => c.onclick = () => {
    ND.template = c.dataset.tpl;
    const tp = DAG_TEMPLATES.find((x) => x.key === ND.template);
    // reset the schedule to THIS template's (empty for schedule-less ones) — else a
    // previously-explored template's cron sticks invisibly behind the collapsed fold.
    ND.dag.schedule = tp && tp.schedule ? tp.schedule : "";
    if (tp && tp.schedule) ND.advanced = true; // a seeded schedule must stay visible/correctable
    parseScheduleState(ND.dag);
    renderNewDag();
  });
  $("nd-adv").onclick = () => { ND.advanced = !ND.advanced; renderNewDag(); };
  const yl = $("nd-toyaml"); if (yl) yl.onclick = () => { ND.yamlMode = true; renderNewDag(); };
  const idEl = $("nd-id");
  idEl.oninput = () => { ND.dag.dag_id = idEl.value.trim(); updateNewDagValidity(); };
  idEl.focus();
  if (ND.advanced) { SCHED = { state: ND, idp: "nd", host: "nd-sched", onChange: null }; renderSchedUI(); }
  $("nd-create").onclick = submitNewDag;
  updateNewDagValidity();
}
// import a pasted YAML definition via the raw-create endpoint (same parser,
// same validation — the console never re-implements it)
async function importYamlDag() {
  const btn = $("nd-create"), yml = $("nd-yaml").value;
  if (!yml.trim()) { $("nd-srv").textContent = t("nd_yaml_empty"); return; }
  btn.disabled = true; $("nd-srv").textContent = "";
  try {
    const r = await api("/api/dags", { method: "POST", headers: { "Content-Type": "text/yaml" }, body: yml });
    $("modal-root").innerHTML = ""; toast(t("nd_imported"), "ok"); showDag(r.dag_id);
  } catch (e) { $("nd-srv").textContent = e.message; btn.disabled = false; }
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
  const tp = DAG_TEMPLATES.find((x) => x.key === ND.template) || DAG_TEMPLATES[0];
  const tasks = (tp.tasks || []).map((tk) => ({ id: tk.id, type: tk.type || "shell", command: tk.command, deps: tk.deps || [], pool: "default", priority: 0, timeout: 0, trigger_rule: "all_success", retries: null, retry_delay: null }));
  const spec = { dag_id: ND.dag.dag_id, schedule: ND.dag.schedule, start_date: ND.dag.start_date, catchup: false, max_active_runs: 1, default_retries: 0, trigger_after: [], tasks };
  try { await api("/api/dags/build", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(spec) }); if (tasks.length) coachDag = ND.dag.dag_id; $("modal-root").innerHTML = ""; showDag(ND.dag.dag_id); }
  catch (e) { $("nd-srv").textContent = e.message; btn.disabled = false; }
}
// ============================================================================
// Novice wizard (#/new): 3 steps — pick a scenario, confirm commands, schedule —
// then create via /api/dags/build and jump straight into a trial run.
// Templates deliberately use commands that SUCCEED out of the box (echo/sleep),
// so the promised "run once, watch it go green" actually happens; users swap in
// their real scripts in step 2 or later from the detail page.
// ============================================================================
let W = null; // wizard state; survives navigating away mid-flow, cleared on create
const NV_TEMPLATES = {
  etl: { defName: "my_daily_etl", steps: [
    { id: "extract", cmd: "sleep 1 && echo extract" },
    { id: "transform", cmd: "sleep 1 && echo transform", deps: ["extract"] },
    { id: "load", cmd: "sleep 1 && echo load run={{ run_id }}", deps: ["transform"] },
  ] },
  report: { defName: "daily_report", daily: "08:00", steps: [
    { id: "fetch", cmd: "sleep 1 && echo fetch data" },
    { id: "render", cmd: "sleep 1 && echo render report", deps: ["fetch"] },
  ] },
  blank: { defName: "my_first_flow", steps: [
    { id: "step_1", cmd: "echo hello cronova" },
  ] },
};
function wzSteps() {
  return NV_TEMPLATES[W.tpl].steps.map((s, i) => ({ ...s, cmd: W.cmdEdits[s.id] ?? s.cmd, num: i + 1 }));
}
function showWizard() {
  if (document.body.dataset.role === "viewer") { loadDags(); return; } // viewers can't create
  view = "wizard"; activeDag = null; closeLog(); stopDagRunsPoll();
  setNav("dags", t("wz_crumb"));
  setHash("#/new");
  if (!W) {
    W = { step: 1, tpl: "etl", name: NV_TEMPLATES.etl.defName, nameTouched: false, cmdEdits: {},
      schedMode: "daily", dailyTime: "02:00", intervalN: 30, existing: null, err: "" };
    api("/api/dags").then((l) => { if (W) W.existing = new Set(l.map((d) => d.dag_id)); }).catch(() => { if (W) W.existing = new Set(); });
  }
  renderWizard();
  finishRouteRender();
}
function wzValidate() {
  const name = (W.name || "").trim();
  if (!ID_RE.test(name)) return t("err_dagid");
  if (W.existing && W.existing.has(name)) return t("nd_dagid_dup");
  if (wzSteps().some((s) => !s.cmd.trim())) return t("err_emptycmd");
  return "";
}
function wzFlowChips(key) {
  const labels = key === "blank" ? [t("wz_tpl_blank_chip")] : NV_TEMPLATES[key].steps.map((s) => nvTaskLabel(s.id));
  return `<div class="wz-flow">${labels.map((l) => `<span class="fchip">${esc(l)}</span>`).join(`<span class="farr">→</span>`)}</div>`;
}
function renderWizard() {
  if (!W) return;
  const dots = [1, 2, 3].map((n) => `<span class="wz-dot ${W.step >= n ? "on" : ""}"></span>`).join("");
  const head = `<div class="wz-head">
      <a id="wz-back" role="button" tabindex="0">${esc(t("wz_back"))}</a>
      <div class="grow" style="flex:1"></div>
      <div class="wz-dots">${dots}<span class="wz-stepn">${esc(t("wz_stepof", W.step))}</span></div>
    </div>`;
  let body = "";
  if (W.step === 1) {
    const card = (key) => `<div class="tpl-card ${W.tpl === key ? "on" : ""}" role="button" tabindex="0" data-wztpl="${key}" aria-pressed="${W.tpl === key}" aria-label="${esc(t("wz_tpl_" + key))} — ${esc(t("wz_tpl_" + key + "_d"))}">
        <div class="tpl-name">${esc(t("wz_tpl_" + key))}</div>
        <div class="tpl-desc">${esc(t("wz_tpl_" + key + "_d"))}</div>
        ${wzFlowChips(key)}
        <div class="tpl-meta">${esc(t("wz_tpl_" + key + "_m"))}</div>
      </div>`;
    body = `
      <h1>${esc(t("wz1_title"))}</h1>
      <div class="page-sub" style="margin-bottom:18px">${esc(t("wz1_sub"))}</div>
      <div class="tpl-grid">${card("etl")}${card("report")}${card("blank")}</div>
      <div class="wz-foot end"><button class="primary" style="padding:9px 22px" id="wz-next">${esc(t("wz1_next"))}</button></div>`;
  } else if (W.step === 2) {
    const steps = wzSteps();
    const inserted = steps[0].cmd.includes("logical_date");
    body = `
      <h1>${esc(t("wz2_title"))}</h1>
      <div class="page-sub" style="margin-bottom:18px">${esc(t("wz2_sub"))}</div>
      <div class="b-field" style="max-width:340px;margin-bottom:16px">
        <label>${esc(t("wz2_name_label"))}</label>
        <input class="mono" id="wz-name" value="${esc(W.name)}" spellcheck="false">
        <div class="field-hint">${esc(t("wz2_name_hint"))}</div>
      </div>
      ${steps.map((s) => `<div class="wz-step-row">
        <span class="nv-num">${s.num}</span>
        <div class="wz-step-main">
          <div class="wz-step-label">${esc(nvTaskLabel(s.id))}</div>
          <input class="mono" data-wzcmd="${esc(s.id)}" value="${esc(s.cmd)}" spellcheck="false">
        </div>
      </div>`).join("")}
      <div class="nv-dashed" style="margin-top:6px">
        <div style="font-size:12.5px;color:var(--muted);margin-bottom:9px">${esc(t("wz2_adv"))}</div>
        <div style="display:flex;gap:9px;align-items:center;flex-wrap:wrap">
          <span class="chip varchip" role="button" tabindex="0" id="wz-datevar">${esc(t("wz2_datevar"))}</span>
          <span class="nv-hint">${esc(t("wz2_datevar_note"))} <code class="mono" style="color:var(--accent)">--date {{ logical_date }}</code></span>
        </div>
        ${inserted ? `<div class="sched-preview" style="margin-top:9px">${esc(t("wz2_inserted", new Date().toISOString().slice(0, 10)))}</div>` : ""}
      </div>
      <div class="wz-err" id="wz-err">${esc(W.err || "")}</div>
      <div class="wz-foot">
        <button id="wz-prev">${esc(t("wz_prev"))}</button>
        <button class="primary" style="padding:9px 22px" id="wz-next">${esc(t("wz2_next"))}</button>
      </div>`;
  } else {
    const on = (m) => W.schedMode === m ? "on" : "";
    body = `
      <h1>${esc(t("wz3_title"))}</h1>
      <div class="page-sub" style="margin-bottom:18px">${esc(t("wz3_sub"))}</div>
      <div class="sched-grid">
        <div class="tpl-card sched-card ${on("daily")}" role="button" tabindex="0" data-wzsched="daily" aria-pressed="${W.schedMode === "daily"}" aria-label="${esc(t("wz3_daily"))}">
          <div><div class="tpl-name">${esc(t("wz3_daily"))}</div><div class="tpl-desc">${esc(t("wz3_daily_d"))}</div></div>
          <input type="time" id="wz-time" value="${esc(W.dailyTime)}" style="width:110px">
        </div>
        <div class="tpl-card sched-card ${on("interval")}" role="button" tabindex="0" data-wzsched="interval" aria-pressed="${W.schedMode === "interval"}" aria-label="${esc(t("wz3_interval"))}">
          <div><div class="tpl-name">${esc(t("wz3_interval"))}</div><div class="tpl-desc">${esc(t("wz3_interval_d"))}</div></div>
          <div style="display:flex;align-items:center;gap:8px" id="wz-int-wrap">
            <span class="muted" style="font-size:12.5px">${esc(t("wz3_every"))}</span>
            <input type="number" min="1" id="wz-int" value="${W.intervalN}" style="width:76px">
            <span class="muted" style="font-size:12.5px">${esc(t("wz3_minutes"))}</span>
          </div>
        </div>
        <div class="tpl-card sched-card ${on("manual")}" role="button" tabindex="0" data-wzsched="manual" aria-pressed="${W.schedMode === "manual"}" aria-label="${esc(t("wz3_manual"))}">
          <div><div class="tpl-name">${esc(t("wz3_manual"))}</div><div class="tpl-desc">${esc(t("wz3_manual_d"))}</div></div>
        </div>
      </div>
      <div class="nv-note" style="max-width:560px;margin-top:16px">
        <b id="wz-sent">${esc(wzSchedSentence())}</b><br>
        <span class="muted">${esc(t("wz3_note"))}</span>
      </div>
      <div class="nv-hint" style="margin-top:10px">${esc(t("wz3_cron_hint"))}</div>
      <div class="wz-err" id="wz-err">${esc(W.err || "")}</div>
      <div class="wz-foot" style="max-width:560px">
        <button id="wz-prev">${esc(t("wz_prev"))}</button>
        <button class="primary" style="padding:9px 22px" id="wz-create">${esc(t("wz3_create"))}</button>
      </div>`;
  }
  main.innerHTML = `<div class="wz" data-screen-label="wizard">${head}${body}</div>`;
  wireWizard();
}
function wzSchedSentence() {
  if (W.schedMode === "daily") return t("nv_sched_daily", W.dailyTime);
  if (W.schedMode === "interval") return t("nv_sched_interval", Math.max(1, W.intervalN));
  return t("nv_sched_manual");
}
function wireWizard() {
  $("wz-back").onclick = () => { W.err = ""; if (W.step === 1) loadDags(); else { W.step--; renderWizard(); } };
  const prev = $("wz-prev"); if (prev) prev.onclick = () => { W.err = ""; W.step--; renderWizard(); };
  const next = $("wz-next"); if (next) next.onclick = () => {
    if (W.step === 2) { const err = wzValidate(); if (err) { W.err = err; $("wz-err").textContent = err; return; } }
    W.err = ""; W.step++; renderWizard();
  };
  main.querySelectorAll("[data-wztpl]").forEach((c) => c.onclick = () => {
    const key = c.dataset.wztpl;
    if (W.tpl !== key) {
      W.tpl = key; W.cmdEdits = {};
      if (!W.nameTouched) W.name = NV_TEMPLATES[key].defName;
      // a template with a preset time seeds the schedule (report → daily 08:00)
      if (NV_TEMPLATES[key].daily) { W.schedMode = "daily"; W.dailyTime = NV_TEMPLATES[key].daily; }
    }
    renderWizard();
  });
  const nameEl = $("wz-name");
  if (nameEl) nameEl.oninput = () => {
    W.name = nameEl.value.trim(); W.nameTouched = true;
    const name = W.name; let err = "";
    if (name && !ID_RE.test(name)) err = t("err_dagid");
    else if (name && W.existing && W.existing.has(name)) err = t("nd_dagid_dup");
    W.err = err; $("wz-err").textContent = err;
  };
  main.querySelectorAll("[data-wzcmd]").forEach((inp) => inp.oninput = () => { W.cmdEdits[inp.dataset.wzcmd] = inp.value; });
  const dv = $("wz-datevar");
  if (dv) dv.onclick = () => {
    const first = wzSteps()[0];
    if (first.cmd.includes("logical_date")) return;
    W.cmdEdits[first.id] = first.cmd + " --date {{ logical_date }}";
    renderWizard();
  };
  main.querySelectorAll("[data-wzsched]").forEach((c) => c.onclick = (e) => {
    if (e.target.closest("#wz-time, #wz-int-wrap")) return; // typing in the inputs isn't a card pick
    W.schedMode = c.dataset.wzsched; renderWizard();
  });
  const tm = $("wz-time"); if (tm) {
    tm.onclick = (e) => e.stopPropagation();
    tm.onchange = () => { if (tm.value) W.dailyTime = tm.value; W.schedMode = "daily"; renderWizard(); };
  }
  const iv = $("wz-int"); if (iv) {
    iv.onclick = (e) => e.stopPropagation();
    iv.oninput = () => { W.intervalN = Math.max(1, +iv.value || 1); W.schedMode = "interval"; const s = $("wz-sent"); if (s) s.textContent = wzSchedSentence(); };
  }
  const create = $("wz-create"); if (create) create.onclick = createAndRunWizard;
}
async function createAndRunWizard() {
  const btn = $("wz-create"); if (btn) btn.disabled = true;
  const err = wzValidate();
  if (err) { W.err = err; renderWizard(); return; }
  const name = W.name.trim(), steps = wzSteps();
  const schedule = W.schedMode === "daily" ? wzDailyCron(W.dailyTime)
    : W.schedMode === "interval" ? `@every ${Math.max(1, W.intervalN)}m` : "";
  const tasks = steps.map((s) => ({ id: s.id, type: "shell", command: s.cmd, deps: s.deps || [], pool: "default", priority: 0, timeout: 0, trigger_rule: "all_success", retries: null, retry_delay: null }));
  // default_retries: 2 — the novice copy promises automatic retries; expert
  // settings can dial it back any time.
  const spec = { dag_id: name, schedule, start_date: "", catchup: false, max_active_runs: 1, default_retries: 2, trigger_after: [], tasks };
  try { await api("/api/dags/build", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(spec) }); }
  catch (e) { W.err = e.message; renderWizard(); return; }
  let runID = null;
  try { runID = (await api(`/api/dags/${encodeURIComponent(name)}/trigger`, { method: "POST" })).run_id; }
  catch (e) { toast(t("trig_fail") + ": " + e.message, "fail"); } // DAG exists — land on its page instead
  W = null;
  runID ? showRun(runID) : showDag(name);
}
// "HH:MM" -> five-field cron at that time every day
function wzDailyCron(hm) {
  const m = /^(\d{1,2}):(\d{1,2})$/.exec(hm || "") || [null, "2", "0"];
  return `${+m[2]} ${+m[1]} * * *`;
}
