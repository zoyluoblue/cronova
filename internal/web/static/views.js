"use strict";
// ---- DAGs dashboard ----
async function loadDags() {
  view = "dags"; activeDag = null; closeLog(); setNav("dags"); setHash("#/dags");
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
    ${gettingStartedHtml(dags)}
    <table class="tbl"><thead><tr><th style="width:42px"></th><th>${t("h_dag")}</th><th>${t("h_last")}</th><th>${t("h_spark")}</th><th>${t("h_pool")}</th><th>${t("h_next")}</th><th style="width:80px">${t("h_act")}</th></tr></thead>
    <tbody>${list.map(rowHtml).join("") || `<tr><td colspan="7"><div class="empty">${t("no_match")}</div></td></tr>`}</tbody></table>`;
  main.querySelectorAll(".pill[data-f]").forEach((b) => b.onclick = () => { filter = b.dataset.f; renderDags(); });
  const fc = main.querySelector('[data-card="failed"]'); if (fc) fc.onclick = () => { filter = "failed"; renderDags(); }; // dead number -> one-click triage
  const gx = $("gs-x"); if (gx) gx.onclick = () => { localStorage.setItem("cnv_gs_done", "1"); $("gs-box").remove(); };
  main.querySelectorAll("tr.row").forEach((tr) => tr.onclick = (e) => { if (!e.target.closest(".no-nav")) showDag(tr.dataset.id); });
  main.querySelectorAll(".toggle").forEach((tg) => tg.onclick = async (e) => { e.stopPropagation(); await api(`/api/dags/${tg.dataset.id}/pause?paused=${tg.dataset.paused !== "true"}`, { method: "POST" }); loadDags(); });
  main.querySelectorAll(".play").forEach((b) => b.onclick = async (e) => { e.stopPropagation(); b.disabled = true; try { await api(`/api/dags/${b.dataset.id}/trigger`, { method: "POST" }); toast(t("toast_run_queued"), "ok"); setTimeout(loadDags, 500); } catch (err) { toast(t("trig_fail") + ": " + err.message, "fail"); b.disabled = false; } });
}
// getting-started checklist, derived from REAL store data (auto-hides once all
// three are done, or when dismissed). Never shown again after completion.
function gettingStartedHtml(dags) {
  if (localStorage.getItem("cnv_gs_done")) return "";
  const hasDag = dags.length > 0;
  const hasRun = dags.some((x) => (x.sparkline || []).length > 0);
  const hasOk = dags.some((x) => (x.sparkline || []).includes("success") || x.latest_state === "success");
  if (hasDag && hasRun && hasOk) { localStorage.setItem("cnv_gs_done", "1"); return ""; }
  const step = (done, label) => `<span class="gs-step ${done ? "done" : ""}"><span class="gs-ic">${done ? "✓" : "○"}</span>${label}</span>`;
  return `<div class="gs-box" id="gs-box">
    <span class="gs-title">${t("gs_title")}</span>
    ${step(hasDag, t("gs_create"))} ${step(hasRun, t("gs_trigger"))} ${step(hasOk, t("gs_green"))}
    <button class="icon" id="gs-x" aria-label="${t("cancel_word")}">✕</button></div>`;
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
  setHash("#/dag/" + encodeURIComponent(id));
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
    ${coachDag === d.dag_id ? `<div class="coach-ribbon" id="coach"><span>✦ ${t("coach_tpl_ready")}</span><button class="primary" id="coach-run">${t("btn_trigger")}</button><button class="icon" id="coach-x" aria-label="${t("cancel_word")}">✕</button></div>` : ""}
    <div class="toolbar">
      <button class="primary" id="trig" ${noTasks ? "disabled" : ""}>${t("btn_trigger")}</button>
      <button id="pause">${d.paused ? t("btn_resume") : t("btn_pause")}</button>
      <button id="dup">${t("btn_duplicate")}</button>
      <button id="yaml-btn">YAML</button>
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
  $("dup").onclick = duplicateActiveDag;
  $("yaml-btn").onclick = openYamlDrawer;
  const cr = $("coach-run"); if (cr) cr.onclick = () => { coachDag = null; $("coach").remove(); triggerActiveDag(); };
  const cx = $("coach-x"); if (cx) cx.onclick = () => { coachDag = null; $("coach").remove(); };
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

// small single-input modal (Enter=ok, Escape=cancel) -> Promise<string|null>
function promptDialog(title, def) {
  return new Promise((resolve) => {
    const root = $("modal-root");
    root.innerHTML = `<div class="overlay" id="povl"><div class="modal confirm" role="dialog" aria-modal="true" aria-label="${esc(title)}">
      <h2>${esc(title)}</h2>
      <div class="body"><input id="p-input" value="${esc(def || "")}" style="width:100%"></div>
      <div class="foot"><button id="p-cancel">${t("cancel_word")}</button><button class="primary" id="p-ok">${t("confirm_word")}</button></div>
    </div></div>`;
    const close = (v) => { document.removeEventListener("keydown", onKey); root.innerHTML = ""; resolve(v); };
    const onKey = (e) => { if (e.key === "Escape") close(null); else if (e.key === "Enter") close($("p-input").value.trim()); };
    document.addEventListener("keydown", onKey);
    $("p-cancel").onclick = () => close(null);
    $("p-ok").onclick = () => close($("p-input").value.trim());
    $("povl").onclick = (e) => { if (e.target.id === "povl") close(null); };
    const inp = $("p-input"); inp.focus(); inp.select();
  });
}

// duplicate the whole DAG under a new id (same spec, fresh history)
async function duplicateActiveDag() {
  await flushPendingSaves();
  const nid = await promptDialog(t("dup_dag_title"), D.dag.dag_id + "_copy");
  if (!nid) return;
  if (!ID_RE.test(nid)) { toast(t("err_dagid"), "warn"); return; }
  if (D.allDags.includes(nid)) { toast(t("nd_dagid_dup"), "warn"); return; }
  const spec = dagSpecFrom(D); spec.dag_id = nid;
  try { await api("/api/dags/build", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(spec) }); toast(t("dup_done"), "ok"); showDag(nid); }
  catch (e) { toast(e.message, "fail"); }
}

function duplicateTask(taskID) {
  const src = D.tasks.find((x) => x.id === taskID); if (!src) return;
  let n = 1, nid;
  do { nid = `${taskID}_copy${n > 1 ? "_" + n : ""}`; n++; } while (D.tasks.some((x) => x.id === nid));
  D.tasks.push({ ...src, id: nid, deps: (src.deps || []).slice() });
  saveDag(); renderDagPage();
}

// the escape hatch stays visible: the YAML the forms wrote, one click away
async function openYamlDrawer() {
  await flushPendingSaves();
  let yml = "";
  try { yml = (await api(`/api/dags/${D.dag.dag_id}`)).definition_yaml || ""; } catch (e) { toast(e.message, "fail"); return; }
  const root = $("modal-root");
  root.innerHTML = `<div class="overlay" id="yovl"><div class="modal" role="dialog" aria-modal="true" aria-label="YAML">
    <h2>YAML · <span class="mono">${esc(D.dag.dag_id)}</span></h2>
    <div class="body"><pre class="yaml-view">${esc(yml)}</pre></div>
    <div class="foot"><button id="y-copy">${t("y_copy")}</button><button id="y-dl">${t("y_download")}</button><button class="primary" id="y-close">${t("y_close")}</button></div>
  </div></div>`;
  const close = () => { document.removeEventListener("keydown", onKey); root.innerHTML = ""; };
  const onKey = (e) => { if (e.key === "Escape") close(); };
  document.addEventListener("keydown", onKey);
  $("yovl").onclick = (e) => { if (e.target.id === "yovl") close(); };
  $("y-close").onclick = close;
  $("y-copy").onclick = async () => { try { await navigator.clipboard.writeText(yml); toast(t("y_copied"), "ok"); } catch (_) { toast(t("y_copy_fail"), "warn"); } };
  $("y-dl").onclick = () => { const a = document.createElement("a"); a.href = URL.createObjectURL(new Blob([yml], { type: "text/yaml" })); a.download = D.dag.dag_id + ".yaml"; a.click(); URL.revokeObjectURL(a.href); };
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
      <td><button class="icon no-nav" data-dup="${esc(tk.id)}" title="${t("btn_duplicate")}">⧉</button><button class="icon rm no-nav" data-del="${esc(tk.id)}" title="${t("b_remove")}">✕</button></td></tr>`).join("")}</tbody></table>`;
}
function wireDagStructure() {
  const add = $("d-addtask"); if (add) add.onclick = addTask;
  document.querySelectorAll("#d-graph [data-node]").forEach((n) => n.onclick = () => onDagGraphNodeClick(n.dataset.node));
  const sct = $("d-structure");
  sct.querySelectorAll("tr.row").forEach((tr) => tr.onclick = (e) => { if (!e.target.closest(".no-nav")) showTask(D.dag.dag_id, tr.dataset.task); });
  sct.querySelectorAll("[data-del]").forEach((b) => b.onclick = (e) => { e.stopPropagation(); deleteTask(b.dataset.del); });
  sct.querySelectorAll("[data-dup]").forEach((b) => b.onclick = (e) => { e.stopPropagation(); duplicateTask(b.dataset.dup); });
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
  setHash(`#/dag/${encodeURIComponent(dagID)}/task/${encodeURIComponent(taskID)}`);
  const tk = D.tasks.find((x) => x.id === taskID);
  cmdRaw = tk ? computeCmdRaw(tk) : true; // structured form when the command fits the type
  lastCmdField = null;
  setNav("dags", `${dagID} / ${taskID}`);
  renderTaskPage();
}

// ---- typed command builder ------------------------------------------------
// The `type` selector drives a small structured form that COMPOSES the shell
// command; the raw textarea is the escape hatch AND the stored source of truth
// (no backend change — we still persist `command`). Best-effort parse on load;
// fall back to raw when a command doesn't fit the type's shape.
const TEMPLATE_VARS = ["logical_date", "logical_datetime", "run_id", "dag_id", "task_id", "try_number"];
let cmdRaw = false, lastCmdField = null;
const CMD_BUILDERS = {
  python: {
    fields: [
      { k: "interp", label: "cb_interp", def: "python3", ph: "python3" },
      { k: "mode", label: "cb_runas", sel: ["module", "script"], def: "module" },
      { k: "target", label: "cb_target", ph: "pkg.main  ·  path/script.py", full: true },
      { k: "args", label: "cb_args", ph: "--date {{ logical_date }}", full: true },
    ],
    compose: (f) => `${f.interp || "python3"} ${f.mode === "script" ? "" : "-m "}${f.target || ""}${f.args ? " " + f.args : ""}`.replace(/\s+/g, " ").trim(),
    parse: (c) => { const m = c.match(/^(\S+)\s+(?:-m\s+(\S+)|(\S+))(?:\s+([\s\S]+))?$/); if (!m || !/python/i.test(m[1])) return null; return { interp: m[1], mode: m[2] ? "module" : "script", target: m[2] || m[3] || "", args: (m[4] || "").trim() }; },
  },
  jar: {
    fields: [
      { k: "jar", label: "cb_jar", ph: "app.jar", full: true },
      { k: "mainclass", label: "cb_mainclass", ph: "(optional) com.example.Main" },
      { k: "args", label: "cb_args", ph: "--in {{ logical_date }}", full: true },
    ],
    compose: (f) => (f.mainclass ? `java -cp ${f.jar || ""} ${f.mainclass}` : `java -jar ${f.jar || ""}`) + (f.args ? " " + f.args : ""),
    parse: (c) => { let m = c.match(/^java -jar (\S+)(?:\s+([\s\S]+))?$/); if (m) return { jar: m[1], mainclass: "", args: (m[2] || "").trim() }; m = c.match(/^java -cp (\S+) (\S+)(?:\s+([\s\S]+))?$/); if (m) return { jar: m[1], mainclass: m[2], args: (m[3] || "").trim() }; return null; },
  },
  sql: {
    fields: [
      { k: "client", label: "cb_client", def: "psql -c", ph: "psql -c" },
      { k: "query", label: "cb_query", area: true, ph: "SELECT count(*) FROM events WHERE day = '{{ logical_date }}'" },
    ],
    compose: (f) => `${f.client || "psql -c"} "${(f.query || "").replace(/"/g, '\\"')}"`,
    parse: (c) => { const m = c.match(/^([\s\S]+?)\s+"([\s\S]*)"$/); if (!m) return null; return { client: m[1], query: m[2].replace(/\\"/g, '"') }; },
  },
};
function computeCmdRaw(tk) { const b = CMD_BUILDERS[tk.type]; if (!b) return true; if (!tk.command) return false; return !b.parse(tk.command); }
function insertAtCaret(el, text) {
  el.focus();
  const s = el.selectionStart ?? el.value.length, e = el.selectionEnd ?? el.value.length;
  el.value = el.value.slice(0, s) + text + el.value.slice(e);
  const p = s + text.length; if (el.setSelectionRange) el.setSelectionRange(p, p);
}
// escape, then tint {{ template }} tokens so substitution is visible in previews
function hlVars(cmd) { return esc(cmd).replace(/\{\{\s*\w+\s*\}\}/g, (m) => `<span class="varhl">${m}</span>`); }
function commandFieldHtml(tk) {
  const chips = `<div class="varchips" title="${t("var_insert")}">${TEMPLATE_VARS.map((v) => `<span class="chip varchip" data-var="${v}">{{ ${v} }}</span>`).join("")}</div>`;
  const b = CMD_BUILDERS[tk.type];
  if (cmdRaw || !b) {
    const toForm = b ? ` <a class="raw-toggle" id="cmd-toform">${t("cmd_use_form")}</a>` : "";
    return `<div class="b-field full"><label>${t("t_command")}${toForm}</label>${chips}
      <textarea class="tf cmd" data-k="command" rows="4" spellcheck="false" placeholder="echo running {{ logical_date }}">${esc(tk.command)}</textarea>
      <div class="cmd-preview"><span class="cp-label">${t("cmd_will_run")}</span> <code id="cmd-preview">${hlVars(tk.command || "")}</code></div></div>`;
  }
  const f = b.parse(tk.command) || {};
  const fields = b.fields.map((fd) => {
    const val = f[fd.k] ?? fd.def ?? "";
    if (fd.sel) return `<div class="b-field"><label>${t(fd.label)}</label><select class="cf" data-cf="${fd.k}">${fd.sel.map((o) => `<option value="${o}" ${val === o ? "selected" : ""}>${t("cmdopt_" + o)}</option>`).join("")}</select></div>`;
    if (fd.area) return `<div class="b-field full"><label>${t(fd.label)}</label><textarea class="cf cmd" data-cf="${fd.k}" rows="3" spellcheck="false" placeholder="${esc(fd.ph || "")}">${esc(val)}</textarea></div>`;
    return `<div class="b-field${fd.full ? " full" : ""}"><label>${t(fd.label)}</label><input class="cf" data-cf="${fd.k}" value="${esc(val)}" placeholder="${esc(fd.ph || "")}"></div>`;
  }).join("");
  return `<div class="b-field full"><label>${t("t_command")} <span class="tag">${esc(tk.type)}</span> <a class="raw-toggle" id="cmd-toraw">${t("cmd_edit_raw")}</a></label>${chips}
    <div class="cmd-builder tc-grid">${fields}</div>
    <div class="cmd-preview"><span class="cp-label">${t("cmd_will_run")}</span> <code id="cmd-preview">${hlVars(tk.command || b.compose(f))}</code></div></div>`;
}
function wireCommandField(tk) {
  const b = CMD_BUILDERS[tk.type];
  const toForm = $("cmd-toform"); if (toForm) toForm.onclick = () => { if (!tk.command || (b && b.parse(tk.command))) { cmdRaw = false; renderTaskPage(); } else toast(t("cmd_cant_parse"), "warn"); };
  const toRaw = $("cmd-toraw"); if (toRaw) toRaw.onclick = () => { cmdRaw = true; renderTaskPage(); };
  if (!cmdRaw && b) {
    const recompose = () => { const f = {}; main.querySelectorAll(".cf").forEach((el) => f[el.dataset.cf] = el.value); tk.command = b.compose(f); const pv = $("cmd-preview"); if (pv) pv.innerHTML = hlVars(tk.command); };
    main.querySelectorAll(".cf").forEach((el) => {
      el.onfocus = () => lastCmdField = el;
      if (el.tagName === "SELECT") { el.onchange = () => { recompose(); saveDag(); }; return; }
      el.oninput = recompose; el.onblur = () => { recompose(); saveDag(); };
    });
  }
  main.querySelectorAll(".varchip").forEach((c) => c.onclick = () => {
    const target = (lastCmdField && main.contains(lastCmdField)) ? lastCmdField
      : main.querySelector('[data-k="command"]') || main.querySelector('.cf[data-cf="args"]') || main.querySelector('.cf[data-cf="query"]') || main.querySelector('.cf[data-cf="target"]');
    if (!target) return;
    insertAtCaret(target, `{{ ${c.dataset.var} }}`);
    target.dispatchEvent(new Event("input", { bubbles: true }));
    if (target.dataset.k === "command") { tk.command = target.value; } // raw textarea path
  });
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
      </div>
      ${commandFieldHtml(tk)}
      <div class="section-h">${t("t_deps")}</div>
      <div class="b-deps">${siblings.length ? siblings.map((id) => `<span class="chip dep ${tk.deps.includes(id) ? "on" : ""}" role="checkbox" tabindex="0" aria-checked="${tk.deps.includes(id)}" data-dep="${esc(id)}">${esc(id)}</span>`).join("") : `<span class="chip empty-hint">${t("t_nodeps")}</span>`}</div>
      <div class="tc-grid" style="margin-top:14px">
        <div class="b-field"><label>${t("t_rule")}</label><select class="tf" data-k="trigger_rule">${TRIGGER_RULES.map((r) => `<option value="${r}" ${tk.trigger_rule === r ? "selected" : ""}>${t("tr_" + r)}</option>`).join("")}</select><div class="field-hint" id="rule-desc">${t("trd_" + (tk.trigger_rule || "all_success"))}</div></div>
      </div>
      <details class="adv-box"${(tk.pool !== "default" || +tk.priority || tk.retries !== "" || tk.retry_delay !== "" || tk.timeout) ? " open" : ""}>
        <summary>${t("adv_options")}</summary>
        <div class="tc-grid" style="margin-top:10px">
          <div class="b-field"><label>${t("t_pool")}</label><input class="tf" data-k="pool" value="${esc(tk.pool)}" placeholder="default"><div class="field-hint">${t("pool_hint")}</div></div>
          <div class="b-field"><label>${t("t_priority")}</label><input class="tf" data-k="priority" type="number" value="${esc(tk.priority)}"></div>
          <div class="b-field"><label>${t("t_retries")}</label><input class="tf" data-k="retries" type="number" min="0" value="${esc(tk.retries)}"></div>
          <div class="b-field"><label>${t("t_retrydelay")}</label><input class="tf" data-k="retry_delay" type="number" min="0" value="${esc(tk.retry_delay)}"></div>
          <div class="b-field"><label>${t("t_timeout")}</label><input class="tf" data-k="timeout" type="number" min="0" value="${esc(tk.timeout)}"></div>
        </div>
      </details>
      <div class="b-errors" id="task-errors"></div>
    </div>
    <div class="form-foot"><button id="t-back2">${t("back_dag", D.dag.dag_id)}</button></div>`;

  $("t-back").onclick = loadDags;
  $("t-dag").onclick = gotoDagPage;
  $("t-back2").onclick = gotoDagPage;

  main.querySelectorAll(".tf").forEach((el) => {
    const k = el.dataset.k;
    if (k === "type") { el.onchange = () => { tk.type = el.value; cmdRaw = computeCmdRaw(tk); renderTaskPage(); saveDag(); }; return; } // switch the command builder
    if (k === "trigger_rule") { el.onchange = () => { tk.trigger_rule = el.value; const rd = $("rule-desc"); if (rd) rd.textContent = t("trd_" + el.value); saveDag(); }; return; }
    if (el.tagName === "SELECT") { el.onchange = () => { tk[k] = el.value; saveDag(); }; return; }
    if (k === "id") { el.onblur = () => renameActiveTask(tk, el.value.trim()); return; } // keep old id stable until blur
    if (k === "command") { el.oninput = () => { tk.command = el.value; const pv = $("cmd-preview"); if (pv) pv.innerHTML = hlVars(tk.command); }; el.onblur = () => saveDag(); el.onfocus = () => lastCmdField = el; return; }
    el.oninput = () => { tk[k] = el.type === "number" ? (el.value === "" ? "" : +el.value) : el.value; };
    el.onblur = () => saveDag();
  });
  wireCommandField(tk);
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
let runPoll = null, runTab = "instances", runDag = null;
const TASK_TERMINAL = { success: 1, failed: 1, upstream_failed: 1, skipped: 1 };
const runLive = (s) => s === "queued" || s === "running";

async function showRun(runID) {
  view = "run"; currentRun = runID; closeLog(); clearInterval(runPoll); setHash("#/run/" + encodeURIComponent(runID));
  let data;
  try { data = await api(`/api/runs/${encodeURIComponent(runID)}`); } catch (e) { main.innerHTML = `<div class="empty err">${t("api_err")}: ${esc(e.message)}</div>`; return; }
  const r = data.run;
  runDag = await api(`/api/dags/${r.dag_id}`);
  setNav("dags", `${r.dag_id} / ${t("run_word")}`);
  const initSbt = {}; (data.tasks || []).forEach((tk) => initSbt[tk.task_id] = tk.state);
  // static shell — the graph and #logwrap are built ONCE; polling patches only
  // the leaf containers below, so a live log stream (in #logwrap) is never torn down.
  main.innerHTML = `
    <div class="crumb-bar"><a id="back">← ${esc(r.dag_id)}</a> / ${t("run_word")}</div>
    <div class="page-h"><h1 class="mono" style="font-size:16px">${esc(r.run_id)}</h1><span id="run-badge">${badge(r.state)}</span><span class="run-prog" id="run-progress"></span></div>
    <div class="kv" style="margin:14px 0 4px">
      <div class="card"><div class="k">${t("k_logical")}</div><div class="v mono" style="font-size:13px">${esc(r.logical_date)}</div></div>
      <div class="card"><div class="k">${t("k_trig")}</div><div class="v">${typeLabel(r.trigger_type)}</div></div>
      <div class="card"><div class="k">${t("k_dur")}</div><div class="v" id="run-dur">${dur(r.started_at, r.finished_at)}</div></div>
      <div class="card"><div class="k">${t("k_started")}</div><div class="v" style="font-size:13px">${fmt(r.started_at)}</div></div></div>
    <div class="section-h">${t("sec_graph")}</div><div id="run-graph">${renderGraph(runDag.tasks, initSbt, { tag: true })}</div>
    <div class="run-tabs" id="run-tabs">
      <button class="pill ${runTab === "instances" ? "active" : ""}" data-rt="instances">${t("sec_instances")}</button>
      <button class="pill ${runTab === "timeline" ? "active" : ""}" data-rt="timeline">${t("g_timeline")}</button>
    </div>
    <div id="run-body"></div>
    <div id="logwrap"></div>`;
  $("back").onclick = () => showDag(r.dag_id);
  $("run-tabs").querySelectorAll("[data-rt]").forEach((b) => b.onclick = () => {
    runTab = b.dataset.rt;
    $("run-tabs").querySelectorAll(".pill").forEach((x) => x.classList.toggle("active", x === b));
    renderRunBody(runDataCache);
  });
  renderRunDynamic(data);
  if (runLive(r.state)) startRunPoll(runID);
}
let runDataCache = null;
function startRunPoll(runID) {
  runPoll = setInterval(async () => {
    if (view !== "run" || currentRun !== runID) { clearInterval(runPoll); return; } // navigated away
    let data; try { data = await api(`/api/runs/${encodeURIComponent(runID)}`); } catch (_) { return; }
    if (view !== "run" || currentRun !== runID) return;
    renderRunDynamic(data);
    if (!runLive(data.run.state)) {
      clearInterval(runPoll);
      toast(data.run.state === "success" ? t("run_done_ok") : t("run_done_fail"), data.run.state === "success" ? "ok" : "fail");
    }
  }, 2000);
}
function renderRunDynamic(data) {
  runDataCache = data;
  const r = data.run, tasks = data.tasks || [];
  const sbt = {}; tasks.forEach((tk) => sbt[tk.task_id] = tk.state);
  $("run-badge").innerHTML = badge(r.state);
  $("run-dur").textContent = dur(r.started_at, r.finished_at);
  const done = tasks.filter((tk) => TASK_TERMINAL[tk.state]).length, running = tasks.filter((tk) => tk.state === "running").length;
  $("run-progress").textContent = tasks.length ? `${done}/${tasks.length}${running ? ` · ${running} ${stateLabel("running")}` : ""}` : "";
  patchGraphStates(sbt);
  renderRunBody(data);
}
// patch existing graph node fills/strokes (never rebuild) so nodes light up with
// the CSS fill transition as tasks execute
function patchGraphStates(sbt) {
  document.querySelectorAll("#run-graph [data-node]").forEach((g) => {
    const s = sbt[g.dataset.node], [f, st] = colorForState(s), rect = g.querySelector("rect");
    if (rect) { rect.style.fill = f; rect.style.stroke = st; }
    g.classList.toggle("g-running", s === "running");
  });
}
function renderRunBody(data) {
  const el = $("run-body"); if (!el) return;
  el.innerHTML = runTab === "timeline" ? ganttHtml(data) : instancesTableHtml(data);
  el.querySelectorAll(".logbtn").forEach((b) => b.onclick = () => showLog(b.dataset.ti, b.dataset.task));
  el.querySelectorAll(".gantt-row[data-ti]").forEach((row) => row.onclick = () => showLog(row.dataset.ti, row.dataset.task));
}
function instancesTableHtml(data) {
  const tasks = data.tasks || [];
  if (!tasks.length) return `<div class="empty">${t("run_no_tasks")}</div>`;
  return `<table class="tbl"><thead><tr><th>${t("th_task")}</th><th>${t("th_state")}</th><th>${t("th_try")}</th><th>${t("h_pool")}</th><th class="num-col">${t("th_dur")}</th><th></th></tr></thead>
    <tbody>${tasks.map((tk) => `<tr><td class="mono">${esc(tk.task_id)}</td><td>${badge(tk.state)}</td><td>${tk.try_number}/${tk.max_retries + 1}</td><td class="mono">${esc(tk.pool)}</td><td class="num-col">${dur(tk.started_at, tk.finished_at)}</td><td><button class="icon logbtn" data-ti="${tk.id}" data-task="${esc(tk.task_id)}">${t("th_logs")}</button></td></tr>`).join("")}</tbody></table>`;
}
// honest Gantt: bars positioned by real started_at/finished_at; tasks that never
// ran show a muted marker (no fabricated "queued" segment); one bar per task
// (the store keeps a single started/finished pair — no per-try segments).
function ganttHtml(data) {
  const r = data.run, tasks = data.tasks || [];
  if (!tasks.length) return `<div class="empty">${t("run_no_tasks")}</div>`;
  const ms = (x) => x ? new Date(x).getTime() : null;
  const starts = tasks.map((tk) => ms(tk.started_at)).filter(Boolean);
  const ends = tasks.map((tk) => ms(tk.finished_at)).filter(Boolean);
  let t0 = ms(r.started_at) || (starts.length ? Math.min(...starts) : Date.now());
  let t1 = ms(r.finished_at) || (runLive(r.state) ? Date.now() : (ends.length ? Math.max(...ends) : t0 + 1000));
  const span = Math.max(1000, t1 - t0);
  const rows = tasks.map((tk) => {
    const s = ms(tk.started_at);
    if (!s) return `<div class="gantt-row"><div class="gantt-label mono">${esc(tk.task_id)}</div><div class="gantt-track"><span class="gantt-none">${stateLabel(tk.state) || t("g_never_ran")}</span></div></div>`;
    const f = ms(tk.finished_at) || (tk.state === "running" ? Date.now() : s);
    const left = (s - t0) / span * 100, w = Math.max(0.4, (Math.min(f, t1) - s) / span * 100);
    const title = `${tk.task_id} · ${stateLabel(tk.state)} · ${dur(tk.started_at, tk.finished_at)} · ${fmt(tk.started_at)} → ${tk.finished_at ? fmt(tk.finished_at) : "…"}`;
    const tryb = tk.try_number > 1 ? `<span class="g-try" title="${t("th_try")} ${tk.try_number}">×${tk.try_number}</span>` : "";
    return `<div class="gantt-row" data-ti="${tk.id}" data-task="${esc(tk.task_id)}"><div class="gantt-label mono">${esc(tk.task_id)}${tryb}</div><div class="gantt-track"><div class="gantt-bar g-${esc(tk.state)}" style="left:${left.toFixed(2)}%;width:${w.toFixed(2)}%" title="${esc(title)}"></div></div></div>`;
  }).join("");
  return `<div class="gantt"><div class="gantt-rows">${rows}</div>
    <div class="gantt-axis"><span>${esc(fmt(new Date(t0).toISOString()))}</span><span class="mono">${esc(dur(new Date(t0).toISOString(), new Date(t1).toISOString()))}</span><span>${runLive(r.state) ? "…" : esc(fmt(new Date(t1).toISOString()))}</span></div></div>`;
}
const LOG_CAP = 5000; // live-view buffer cap; the download link always serves the full file
function showLog(tiID, taskID) {
  closeLog();
  $("logwrap").innerHTML = `
    <div class="section-h">${t("log_word")} · <span class="mono">${esc(taskID)}</span> <span class="live" id="live"></span></div>
    <div class="log-toolbar">
      <input id="log-find" placeholder="${t("log_find_ph")}" style="max-width:220px">
      <span class="muted" id="log-count"></span>
      <a id="log-dl" href="/api/tasks/${tiID}/log" download="${esc(taskID)}.log" style="margin-left:auto">${t("log_download")}</a>
    </div>
    <div class="logbox" id="logbox"></div>`;
  const box = $("logbox");
  let lines = [], filter = "";
  const render = () => { // full rebuild — only on filter change or cap trim
    const shown = filter ? lines.filter((l) => l.toLowerCase().includes(filter)) : lines;
    box.textContent = shown.join("\n");
    $("log-count").textContent = filter ? t("log_matches", shown.length) : (lines.length >= LOG_CAP ? t("log_capped", LOG_CAP) : "");
    box.scrollTop = box.scrollHeight;
  };
  $("log-find").oninput = (e) => { filter = e.target.value.toLowerCase(); render(); };
  logES = new EventSource(`/api/tasks/${tiID}/log/stream`);
  $("live").innerHTML = `<span class="p"></span>${t("live")}`;
  logES.onmessage = (e) => {
    lines.push(e.data);
    const capped = lines.length > LOG_CAP;
    if (capped) lines = lines.slice(-LOG_CAP);
    if (filter || capped) { render(); return; }
    // fast path: append without rebuilding the whole box
    box.appendChild(document.createTextNode((box.firstChild ? "\n" : "") + e.data));
    box.scrollTop = box.scrollHeight;
  };
  logES.addEventListener("done", () => { closeLog(); $("live").textContent = ""; });
  logES.onerror = () => { closeLog(); $("live").textContent = ""; };
}

// ---- pools ----
async function showPools() {
  view = "pools"; activeDag = null; closeLog(); setNav("pools"); setHash("#/pools");
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
  view = "graph"; activeDag = null; closeLog(); setNav("graph"); setHash("#/graph");
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