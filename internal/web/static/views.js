"use strict";
// ---- DAGs dashboard ----
async function loadDags() {
  view = "dags"; activeDag = null; closeLog(); stopDagRunsPoll(); setNav("dags"); setHash("#/dags");
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
  // dashboard-wide longest run → sparkline bars scale consistently across DAGs
  const sparkScaleMs = Math.max(1, ...dags.flatMap((d) => (d.sparkline || []).map((p) => (p && p.ms) || 0)));
  const list = dags.filter((d) => {
    if (query && !d.dag_id.toLowerCase().includes(query)) return false;
    if (filter === "running") return d.latest_state === "running";
    if (filter === "failed") return d.latest_state === "failed" || d.latest_state === "timed_out";
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
    ${activityStrip(overviewCache.activity)}
    ${gettingStartedHtml(dags)}
    <table class="tbl"><thead><tr><th style="width:42px"></th><th>${t("h_dag")}</th><th>${t("h_last")}</th><th>${t("h_spark")}</th><th>${t("h_pool")}</th><th>${t("h_next")}</th><th style="width:80px">${t("h_act")}</th></tr></thead>
    <tbody>${list.map((d) => rowHtml(d, sparkScaleMs)).join("") || `<tr><td colspan="7"><div class="empty">${t("no_match")}</div></td></tr>`}</tbody></table>`;
  main.querySelectorAll(".act-tick[data-run]").forEach((x) => x.onclick = () => showRun(x.dataset.run));
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
  const sState = (p) => (typeof p === "string" ? p : p && p.state); // sparkline is now [{state,ms}]
  const hasOk = dags.some((x) => (x.sparkline || []).some((p) => sState(p) === "success") || x.latest_state === "success");
  if (hasDag && hasRun && hasOk) { localStorage.setItem("cnv_gs_done", "1"); return ""; }
  const step = (done, label) => `<span class="gs-step ${done ? "done" : ""}"><span class="gs-ic">${done ? "✓" : "○"}</span>${label}</span>`;
  return `<div class="gs-box" id="gs-box">
    <span class="gs-title">${t("gs_title")}</span>
    ${step(hasDag, t("gs_create"))} ${step(hasRun, t("gs_trigger"))} ${step(hasOk, t("gs_green"))}
    <button class="icon" id="gs-x" aria-label="${t("cancel_word")}">✕</button></div>`;
}

// global recent-run timeline: the last ~24 runs across all live DAGs, drawn as
// state-colored ticks positioned by their REAL start time on a shared axis.
// Honest — a tick only where a run actually ran; hover = detail, click = open run.
function activityStrip(activity) {
  const items = (activity || []).filter((a) => a.started || a.finished);
  if (!items.length) return `<div class="act-strip act-empty">${t("act_none")}</div>`;
  const ms = (x) => (x ? new Date(x).getTime() : null);
  const times = items.flatMap((a) => [ms(a.started), ms(a.finished)]).filter(Boolean);
  const t0 = Math.min(...times), t1 = Math.max(Math.max(...times), Date.now());
  const span = Math.max(1000, t1 - t0);
  // time-sort ascending, then nudge apart so a burst of near-simultaneous runs
  // stays individually hoverable/clickable instead of stacking into one pixel
  // (honest for the common case: natural gaps exceed MINGAP and aren't touched).
  const MINGAP = 1.4; // percent
  const placed = items.map((a) => ({ a, s: ms(a.started) || ms(a.finished) })).sort((x, y) => x.s - y.s);
  let prev = -Infinity;
  placed.forEach((o) => { let l = (o.s - t0) / span * 100; if (l < prev + MINGAP) l = prev + MINGAP; o.left = Math.min(100, l); prev = o.left; });
  const ticks = placed.map(({ a, left }) => {
    const title = `${a.dag_id} · ${stateLabel(a.state)}${a.ms ? ` · ${fmtMs(a.ms)}` : ""} · ${fmt(a.started || a.finished)}`;
    return `<span class="act-tick a-${esc(a.state)}" style="left:${left.toFixed(2)}%" data-run="${esc(a.run_id)}" role="button" tabindex="0" title="${esc(title)}" aria-label="${esc(title)}"></span>`;
  }).join("");
  return `<div class="section-h" style="margin:6px 0 8px">${t("act_recent")}</div>
    <div class="act-strip"><div class="act-track">${ticks}</div>
    <div class="act-axis"><span>${esc(fmt(new Date(t0).toISOString()))}</span><span>${t("act_now")}</span></div></div>`;
}

function rowHtml(d, scaleMs) {
  return `<tr class="row" data-id="${esc(d.dag_id)}">
    <td><div class="toggle no-nav ${d.paused ? "" : "on"}" role="switch" tabindex="0" aria-checked="${!d.paused}" aria-label="${esc(d.dag_id)} — ${t(d.paused ? "btn_resume" : "btn_pause")}" data-id="${esc(d.dag_id)}" data-paused="${d.paused}"></div></td>
    <td><div class="dag-name" role="button" tabindex="0" aria-label="${esc(d.dag_id)}">${esc(d.dag_id)} <span class="tag">${typeLabel(d.type)}</span></div><div class="dag-desc">${esc(descLabel(d.description))}</div></td>
    <td>${badge(d.latest_state)}</td><td>${sparkline(d.sparkline, scaleMs)}</td>
    <td class="mono muted">${esc(d.pool)}</td><td class="mono muted">${esc(nextLabel(d.next_schedule))}</td>
    <td><button class="icon play no-nav" data-id="${esc(d.dag_id)}" title="${t("trigger")}">▶</button></td></tr>`;
}

// ============================================================================
// DAG operation page (view='dag') — integrated: info + structure (editable
// graph + task list) + schedule + run history. Edits persist immediately.
// ============================================================================
async function showDag(id, tab) {
  closeLog(); stopDagRunsPoll(); // tear down the outgoing DAG's live poll before the async refetch
  setHash("#/dag/" + encodeURIComponent(id) + (tab && tab !== "runs" ? "/" + tab : ""));
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
      notify_url: dag.notify_url || "", notify_on: (dag.notify_on || []).slice(),
      sla: dag.sla || 0, dagrun_timeout: dag.dagrun_timeout || 0,
    }),
    tasks: (dag.tasks || []).map((tk) => { const h = tk.http || {}; return { id: tk.id, type: tk.type || "shell", command: tk.command || "", conn: tk.conn || "", pool: tk.pool || "default", priority: tk.priority || 0, retries: tk.retries ?? "", retry_delay: tk.retry_delay ?? "", timeout: tk.timeout || "", sla: tk.sla || "", deps: (tk.deps || []).slice(), trigger_rule: tk.trigger_rule || "all_success",
      httpMethod: h.method || "GET", httpUrl: h.url || "", httpHeaders: h.headers ? Object.entries(h.headers).map(([k, v]) => `${k}: ${v}`).join("\n") : "", httpBody: h.body || "", httpStatus: (h.expected_status || []).join(", ") }; }),
    runs: runs || [], allDags, graphPending: null, activeTaskId: null,
    // default tab: a 0-task shell opens on Structure (its obvious next step is
    // adding tasks); anything else opens on Runs (the monitoring intent).
    tab: tab === "structure" || tab === "settings" ? tab : (tab === "runs" || (dag.tasks || []).length ? "runs" : "structure"),
    editKey: null, // which settings row is expanded for editing
  };
  view = "dag"; activeDag = id;
  setDagHash();
  setNav("dags", id);
  renderDagPage();
}
// hash mirrors the active tab so every tab is linkable; Runs (default) stays clean
function setDagHash() {
  if (!D) return;
  const base = "#/dag/" + encodeURIComponent(D.dag.dag_id);
  // replace, not push: tabs are in-page state, not navigation — Back should leave
  // the DAG page in one press, not cycle through canonicalized tab entries.
  setHash(D.tab === "runs" ? base : base + "/" + D.tab, true);
}
// re-render the operation page from the in-memory D (no refetch) — used when
// returning from the task page so unsaved/just-saved edits are never clobbered.
function gotoDagPage() {
  if (!D) { loadDags(); return; }
  view = "dag"; activeDag = D.dag.dag_id; closeLog();
  setDagHash();
  setNav("dags", D.dag.dag_id);
  renderDagPage();
}

// one-line schedule summary for the hero + settings row (honest: only glosses
// shapes we know; otherwise shows the raw expression)
function schedSummary(d) {
  if (d.schedMode === "manual" || !d.schedule) return t("sub_manual");
  const s = schedSentence(d);
  return s ? `${s} · ${d.schedule}` : d.schedule;
}
// hero facts, honestly derived from the runs we already have — extracted so the
// live poll can patch #dh-stats in place without tearing down the whole page.
function dagHeroStatsHtml() {
  const d = D.dag, last = D.runs[0];
  const terminal = D.runs.filter((r) => r.state === "success" || r.state === "failed" || r.state === "timed_out");
  const okN = terminal.filter((r) => r.state === "success").length;
  return `<div class="dh-stat"><span class="k">${t("dh_last")}</span>
      ${last ? `<span class="v">${badge(last.state)} <span class="muted">${fmt(last.started_at)} · ${dur(last.started_at, last.finished_at)}</span></span>` : `<span class="v muted">${t("dh_never")}</span>`}</div>
    <div class="dh-stat"><span class="k">${t("dh_next")}</span><span class="v">${esc(schedSummary(d))}</span></div>
    <div class="dh-stat"><span class="k">${t("dh_rate")}</span><span class="v">${terminal.length ? `${okN}/${terminal.length} ${stateLabel("success")}` : t("dh_norate")}</span></div>`;
}
function patchDagHero() { const el = $("dh-stats"); if (el) el.innerHTML = dagHeroStatsHtml(); }
function renderDagPage() {
  if (!D) return;
  const d = D.dag;
  const typ = d.schedule ? "schedule" : (d.trigger_after.length ? "dependency" : "manual");
  const noTasks = D.tasks.length === 0;
  main.innerHTML = `
    <div class="crumb-bar"><a id="back">${t("back_dags")}</a> / ${esc(d.dag_id)}</div>
    <div class="dag-hero">
      <div class="dh-top">
        <h1 class="mono">${copySpan(d.dag_id)}</h1>
        <span class="tag">${typeLabel(typ)}</span>
        ${d.paused ? `<span class="tag warn">${t("f_paused")}</span>` : ""}
        <span class="savestate ss-saved" id="d-save"></span>
        <div class="dh-actions">
          <button class="primary" id="trig" ${noTasks ? "disabled" : ""}>${t("btn_trigger")}</button>
          <button class="icon" id="trig-params" title="${t("trig_params")}" ${noTasks ? "disabled" : ""} aria-label="${t("trig_params")}">⋯</button>
          <button id="pause">${d.paused ? t("btn_resume") : t("btn_pause")}</button>
          <button class="icon" id="dup" title="${t("btn_duplicate")}">⧉ ${t("btn_duplicate")}</button>
          <button class="icon" id="yaml-btn">YAML</button>
        </div>
      </div>
      <div class="dh-stats" id="dh-stats">${dagHeroStatsHtml()}
      </div>
      ${noTasks ? `<div class="page-sub" style="margin:8px 0 0">${t("dag_disabled_hint")}</div>` : ""}
    </div>
    ${coachDag === d.dag_id ? `<div class="coach-ribbon" id="coach"><span>✦ ${t("coach_tpl_ready")}</span><button class="primary" id="coach-run">${t("btn_trigger")}</button><button class="icon" id="coach-x" aria-label="${t("cancel_word")}">✕</button></div>` : ""}
    <div class="run-tabs dag-tabs" id="dag-tabs">
      ${["runs", "structure", "settings"].map((k) => `<button class="pill ${D.tab === k ? "active" : ""}"${D.tab === k ? ' aria-current="true"' : ""} data-dt="${k}">${t("tab_" + k)}${k === "structure" ? ` <span class="tab-n">${D.tasks.length}</span>` : ""}</button>`).join("")}
    </div>
    <div class="b-errors" id="dag-errors"></div>
    <div id="dag-tab-body"></div>`;

  $("back").onclick = loadDags;
  $("trig").onclick = () => triggerActiveDag();
  const tp = $("trig-params"); if (tp) tp.onclick = () => triggerParamsDialog();
  $("pause").onclick = async () => { await api(`/api/dags/${d.dag_id}/pause?paused=${!d.paused}`, { method: "POST" }); d.paused = !d.paused; renderDagPage(); };
  $("dup").onclick = duplicateActiveDag;
  $("yaml-btn").onclick = openYamlDrawer;
  const cr = $("coach-run"); if (cr) cr.onclick = () => { coachDag = null; $("coach").remove(); triggerActiveDag(); };
  const cx = $("coach-x"); if (cx) cx.onclick = () => { coachDag = null; $("coach").remove(); };
  $("dag-tabs").querySelectorAll("[data-dt]").forEach((b) => b.onclick = () => {
    if (D.tab === b.dataset.dt) return;
    D.tab = b.dataset.dt; D.editKey = null; setDagHash(); renderDagPage();
    const active = $("dag-tabs") && $("dag-tabs").querySelector(".pill.active"); if (active) active.focus(); // keep keyboard place
  });
  renderDagTab();
  reflectSaveState();
}
// renders the active tab's body only (hero + tabs stay put)
function renderDagTab() {
  const el = $("dag-tab-body"); if (!el) return;
  if (D.tab === "structure") { el.innerHTML = `<div id="d-structure"></div>`; renderDagStructure(); }
  else if (D.tab === "settings") { el.innerHTML = settingsTabHtml(); wireSettingsTab(); }
  else { el.innerHTML = `<div id="d-runs"></div>`; renderDagRuns(); }
  maybePollDagRuns(); // live-refresh the Runs tab while a run is active (self-stops otherwise)
}
// poll the run history while on the Runs tab AND a run is active, so the list and
// hero facts update without a manual refresh (read-only tab → no save race).
let dagRunsPoll = null;
function stopDagRunsPoll() { clearInterval(dagRunsPoll); dagRunsPoll = null; }
function maybePollDagRuns() {
  stopDagRunsPoll();
  if (view !== "dag" || !D || D.tab !== "runs" || D.editKey) return;
  if (!D.runs.some((r) => runLive(r.state))) return; // nothing active → static
  const dagID = D.dag.dag_id;
  dagRunsPoll = setInterval(async () => {
    if (view !== "dag" || !D || D.dag.dag_id !== dagID || D.tab !== "runs" || D.editKey) { stopDagRunsPoll(); return; }
    let runs; try { runs = await api(`/api/dags/${encodeURIComponent(dagID)}/runs?limit=25`); } catch (_) { return; }
    if (view !== "dag" || !D || D.dag.dag_id !== dagID || D.tab !== "runs") return;
    if (JSON.stringify(runs) === JSON.stringify(D.runs)) {
      if (!(runs || []).some((r) => runLive(r.state))) stopDagRunsPoll(); // settled
      return;
    }
    D.runs = runs || [];
    // patch in place (never main.innerHTML): keep the hero/tabs and — crucially —
    // don't tear down a run-row action button the user is focused on / pressing.
    patchDagHero();
    const runsBox = $("d-runs");
    // skip the table rebuild while the user is focused inside it (don't yank a
    // focused/pressed action button); the interval keeps ticking and catches up.
    if (!(runsBox && document.activeElement && runsBox.contains(document.activeElement))) renderDagRuns();
  }, 3000);
}

// --- settings tab: each setting is a one-line summary; click to edit in place
// (immediate-save preserved — the form appears only while you're changing it) ---
// collapsed-row summary for a seconds threshold: mono label, or muted "off".
function secsSummary(sec) {
  return +sec > 0 ? `<span class="mono">${secsLabel(sec)}</span>` : `<span class="muted">${t("set_off")}</span>`;
}
function settingsTabHtml() {
  const d = D.dag;
  const others = [...new Set([...D.allDags.filter((x) => x !== d.dag_id), ...d.trigger_after])];
  // summaryText: plain-text value folded into the collapsed row's accessible name
  // (aria-label on role=button overrides the child summary, so SR users would
  // otherwise hear only "Schedule — Edit" with no value).
  const row = (key, label, summary, summaryText, editor, hint) => {
    const open = D.editKey === key;
    return `<div class="set-row ${open ? "editing" : ""}" data-set="${key}">
      <div class="set-head" ${open ? "" : `role="button" tabindex="0" aria-label="${esc(label)}: ${esc(summaryText)} — ${t("set_edit")}"`}>
        <span class="set-k">${esc(label)}</span>
        ${open ? `<button class="icon set-close" data-close="${key}">${t("set_done")}</button>` : `<span class="set-v">${summary}</span><span class="set-pen" aria-hidden="true">✎</span>`}
      </div>
      ${open ? `<div class="set-body">${hint ? `<div class="field-hint" style="margin-bottom:8px">${esc(hint)}</div>` : ""}${editor}</div>` : ""}</div>`;
  };
  const depsText = d.trigger_after.length ? d.trigger_after.join(", ") : t("set_none");
  const depsSummary = d.trigger_after.length ? d.trigger_after.map((x) => `<span class="mono">${esc(x)}</span>`).join(", ") : `<span class="muted">${t("set_none")}</span>`;
  const depsEditor = others.length
    ? `<div class="b-deps">${others.map((x) => `<span class="chip ta ${d.trigger_after.includes(x) ? "on" : ""}" role="checkbox" tabindex="0" aria-checked="${d.trigger_after.includes(x)}" data-ta="${esc(x)}">${esc(x)}</span>`).join("")}</div>`
    : `<div class="muted">${t("set_no_deps_avail")}</div>`;
  // notify: outbound webhook fired when a run finishes in a selected state.
  const nOn = d.notify_on || [];
  const nEvents = ["failure", "success"];
  const notifText = d.notify_url ? `${d.notify_url} · ${nOn.length ? nOn.join(", ") : t("notify_off")}` : t("set_none");
  const notifSummary = d.notify_url
    ? `${nEvents.filter((e) => nOn.includes(e)).map((e) => `<span class="tag ${e === "failure" ? "bad" : "ok"}">${t("notify_" + e)}</span>`).join(" ") || `<span class="muted">${t("notify_off")}</span>`} <span class="mono set-url" title="${esc(d.notify_url)}">${esc(d.notify_url)}</span>`
    : `<span class="muted">${t("set_none")}</span>`;
  const hasUrl = !!(d.notify_url || "").trim();
  // events require a URL; without one the chips are disabled (and never selectable),
  // so the editor, the collapsed summary, and the persisted state can't diverge.
  const notifEditor = `<input id="d-nurl" type="url" placeholder="https://hooks.slack.com/services/…" value="${esc(d.notify_url)}" style="width:100%;margin-bottom:8px" aria-label="${esc(t("set_notify"))} URL">
    <div class="b-deps">${nEvents.map((e) => `<span class="chip non ${hasUrl && nOn.includes(e) ? "on" : ""} ${hasUrl ? "" : "dis"}" role="checkbox" tabindex="${hasUrl ? "0" : "-1"}" aria-checked="${hasUrl && nOn.includes(e)}" aria-disabled="${!hasUrl}" data-non="${e}">${t("notify_" + e)}</span>`).join("")}</div>
    <div class="field-hint" id="d-nhint" style="margin-top:6px"${hasUrl ? " hidden" : ""}>${esc(t("notify_need_url"))}</div>`;
  return `<div class="set-list">
    ${row("sched", t("set_sched"), esc(schedSummary(d)), schedSummary(d), `<div id="d-sched"></div>`)}
    ${row("max", t("set_max"), `<span class="mono">${d.max_active_runs}</span>`, String(d.max_active_runs), `<input id="d-max" type="number" min="1" value="${d.max_active_runs}" style="width:110px">`)}
    ${row("retries", t("set_retries"), `<span class="mono">${d.default_retries}</span>`, String(d.default_retries), `<input id="d-defr" type="number" min="0" value="${d.default_retries}" style="width:110px">`)}
    ${row("sla", t("set_sla"), secsSummary(d.sla), String(d.sla || 0), `<input id="d-sla" type="number" min="0" value="${d.sla || 0}" style="width:110px"> <span class="muted">${t("secs")}</span>`, t("set_sla_hint"))}
    ${row("timeout", t("set_timeout"), secsSummary(d.dagrun_timeout), String(d.dagrun_timeout || 0), `<input id="d-timeout" type="number" min="0" value="${d.dagrun_timeout || 0}" style="width:110px"> <span class="muted">${t("secs")}</span>`, t("set_timeout_hint"))}
    ${row("deps", t("set_deps"), depsSummary, depsText, depsEditor, t("set_deps_hint"))}
    ${row("notify", t("set_notify"), notifSummary, notifText, notifEditor, t("set_notify_hint"))}
  </div>
  <div class="danger-zone">
    <div class="dz-t">${t("danger_title")}</div>
    <div class="dz-row"><span class="muted">${t("danger_del_hint")}</span><button class="danger" id="del">${t("btn_delete")}</button></div>
  </div>`;
}
function wireSettingsTab() {
  const d = D.dag;
  const body = $("dag-tab-body");
  body.querySelectorAll(".set-row:not(.editing) .set-head").forEach((h) => h.onclick = () => {
    D.editKey = h.parentElement.dataset.set;
    renderDagTab();
    // focus the first control in the freshly opened editor, else the Done button
    const b2 = $("dag-tab-body");
    const first = b2.querySelector(".set-row.editing input, .set-row.editing .pill, .set-row.editing .chip") || b2.querySelector(".set-close");
    if (first) first.focus();
  });
  // full re-render on close (hero facts may have changed), then return focus to
  // the collapsed row's head so keyboard users don't get dumped to <body>.
  body.querySelectorAll(".set-close").forEach((b) => b.onclick = (e) => {
    e.stopPropagation(); const key = b.dataset.close; D.editKey = null; renderDagPage();
    const head = main.querySelector(`.set-row[data-set="${key}"] .set-head`); if (head) head.focus();
  });
  if (D.editKey === "sched") { SCHED = { state: D, idp: "d", host: "d-sched", onChange: saveDag }; renderSchedUI(); }
  // immediate-save on input (matches the rest of the model) — never leave an
  // unsaved mutation that a button-click close could strand while the pill reads
  // "saved" (blur doesn't fire when a <button> is clicked in some browsers).
  const max = $("d-max"); if (max) max.oninput = () => { d.max_active_runs = +max.value || 1; saveDag(); };
  const defr = $("d-defr"); if (defr) defr.oninput = () => { d.default_retries = +defr.value || 0; saveDag(); };
  const sla = $("d-sla"); if (sla) sla.oninput = () => { d.sla = Math.max(0, +sla.value || 0); saveDag(); };
  const tmo = $("d-timeout"); if (tmo) tmo.oninput = () => { d.dagrun_timeout = Math.max(0, +tmo.value || 0); saveDag(); };
  body.querySelectorAll(".chip.ta").forEach((c) => c.onclick = () => { const x = c.dataset.ta, i = d.trigger_after.indexOf(x); i < 0 ? d.trigger_after.push(x) : d.trigger_after.splice(i, 1); c.classList.toggle("on"); c.setAttribute("aria-checked", c.classList.contains("on")); saveDag(); });
  const nurl = $("d-nurl"); if (nurl) nurl.oninput = () => {
    d.notify_url = nurl.value.trim();
    const has = !!d.notify_url;
    if (!has) d.notify_on = []; // events are meaningless without a URL
    body.querySelectorAll(".chip.non").forEach((c) => {
      c.classList.toggle("dis", !has); c.setAttribute("aria-disabled", String(!has)); c.setAttribute("tabindex", has ? "0" : "-1");
      if (!has) { c.classList.remove("on"); c.setAttribute("aria-checked", "false"); }
    });
    const hint = $("d-nhint"); if (hint) hint.hidden = has;
    saveDag();
  };
  body.querySelectorAll(".chip.non").forEach((c) => c.onclick = () => {
    if (!(d.notify_url || "").trim()) return; // events require a URL (chip is disabled)
    d.notify_on = d.notify_on || [];
    const x = c.dataset.non, i = d.notify_on.indexOf(x);
    i < 0 ? d.notify_on.push(x) : d.notify_on.splice(i, 1);
    c.classList.toggle("on"); c.setAttribute("aria-checked", c.classList.contains("on")); saveDag();
  });
  const del = $("del"); if (del) del.onclick = deleteActiveDag;
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
  $("y-copy").onclick = () => copyText(yml).then((ok) => toast(ok ? t("y_copied") : t("y_copy_fail"), ok ? "ok" : "warn"));
  $("y-dl").onclick = () => { const a = document.createElement("a"); a.href = URL.createObjectURL(new Blob([yml], { type: "text/yaml" })); a.download = D.dag.dag_id + ".yaml"; a.click(); URL.revokeObjectURL(a.href); };
}

async function triggerActiveDag(params) {
  const b = $("trig"); if (b) b.disabled = true;
  await flushPendingSaves(); // run the latest saved definition, not a stale one
  const opts = params ? { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ params }) } : { method: "POST" };
  try { await api(`/api/dags/${D.dag.dag_id}/trigger`, opts); toast(t("toast_run_queued"), "ok"); setTimeout(refreshDagRuns, 500); }
  catch (e) { toast(e.message, "fail"); }
  finally { if ($("trig")) $("trig").disabled = D.tasks.length === 0; }
}
// key-value form → trigger with params (empty rows ignored)
function triggerParamsDialog() {
  const root = $("modal-root");
  let rows = [{ k: "", v: "" }];
  const close = () => { document.removeEventListener("keydown", onKey); root.innerHTML = ""; };
  const onKey = (e) => { if (e.key === "Escape") close(); };
  document.addEventListener("keydown", onKey);
  const render = () => {
    root.innerHTML = `<div class="overlay" id="tpovl"><div class="modal confirm" role="dialog" aria-modal="true" aria-label="${t("trig_params")}">
      <h2>${t("trig_params")}</h2>
      <div class="body">
        <div class="field-hint" style="margin-bottom:12px">${esc(t("p_hint"))}</div>
        <div id="tp-rows">${rows.map((r, i) => `<div class="tp-row"><input class="tp-k mono" data-i="${i}" placeholder="${t("p_key")}" value="${esc(r.k)}"><input class="tp-v mono" data-i="${i}" placeholder="${t("p_val")}" value="${esc(r.v)}"><button class="icon tp-rm" data-i="${i}" aria-label="${t("btn_delete")}">✕</button></div>`).join("")}</div>
        <button class="icon" id="tp-add" style="margin-top:8px">+ ${t("p_add")}</button>
      </div>
      <div class="foot"><button id="tp-cancel">${t("nd_cancel")}</button><button class="primary" id="tp-go">${t("p_trigger")}</button></div>
    </div></div>`;
    root.querySelectorAll(".tp-k").forEach((el) => el.oninput = () => rows[+el.dataset.i].k = el.value);
    root.querySelectorAll(".tp-v").forEach((el) => el.oninput = () => rows[+el.dataset.i].v = el.value);
    root.querySelectorAll(".tp-rm").forEach((el) => el.onclick = () => { rows.splice(+el.dataset.i, 1); if (!rows.length) rows = [{ k: "", v: "" }]; render(); });
    $("tp-add").onclick = () => { rows.push({ k: "", v: "" }); render(); const ks = root.querySelectorAll(".tp-k"); if (ks.length) ks[ks.length - 1].focus(); };
    $("tp-cancel").onclick = close;
    $("tpovl").onclick = (e) => { if (e.target.id === "tpovl") close(); };
    $("tp-go").onclick = () => { const params = {}; rows.forEach((r) => { const k = r.k.trim(); if (k) params[k] = r.v; }); close(); triggerActiveDag(params); };
    const f = root.querySelector(".tp-k"); if (f) f.focus();
  };
  render();
}
async function refreshDagRuns() {
  if (view !== "dag" || !D) return;
  // re-render the whole page so the hero facts (last run, success rate) refresh
  // alongside the runs table — unless a settings row is open (don't clobber the edit).
  try { D.runs = (await api(`/api/dags/${D.dag.dag_id}/runs?limit=25`)) || []; if (D.editKey) renderDagRuns(); else renderDagPage(); } catch (_) {}
}
function renderDagRuns() {
  const el = $("d-runs"); if (!el) return;
  if (!D.runs.length) { el.innerHTML = `<div class="empty">${t("no_runs")}</div>`; return; }
  const canAct = document.body.dataset.role !== "viewer";
  el.innerHTML = `<table class="tbl"><thead><tr><th>${t("th_logical")}</th><th>${t("th_state")}</th><th>${t("th_trig")}</th><th>${t("th_started")}</th><th>${t("th_dur")}</th><th style="width:56px"></th></tr></thead>
    <tbody>${D.runs.map((r) => {
    const act = !canAct ? "" : runLive(r.state)
      ? `<button class="icon rr-act no-nav" data-rr-cancel="${esc(r.run_id)}" title="${t("run_cancel")}" aria-label="${t("run_cancel")}">✕</button>`
      : (r.state === "failed" || r.state === "cancelled" || r.state === "timed_out")
        ? `<button class="icon rr-act no-nav" data-rr-retry="${esc(r.run_id)}" title="${t("run_retry")}" aria-label="${t("run_retry")}">↻</button>` : "";
    return `<tr class="row" data-run="${esc(r.run_id)}"><td class="mono">${esc(r.logical_date)}</td><td>${badge(r.state)}</td><td>${typeLabel(r.trigger_type)}</td><td>${fmt(r.started_at)}</td><td>${dur(r.started_at, r.finished_at)}</td><td class="run-row-act">${act}</td></tr>`;
  }).join("")}</tbody></table>`;
  el.querySelectorAll("tr.row").forEach((tr) => tr.onclick = (e) => { if (!e.target.closest(".no-nav")) showRun(tr.dataset.run); });
  el.querySelectorAll("[data-rr-cancel]").forEach((b) => b.onclick = (e) => { e.stopPropagation(); inlineCancelRun(b.dataset.rrCancel); });
  el.querySelectorAll("[data-rr-retry]").forEach((b) => b.onclick = (e) => { e.stopPropagation(); b.disabled = true; inlineRetryRun(b.dataset.rrRetry); }); // disable to swallow a double-click (2nd → 409)
}
// inline run ops from the history list — refresh the list in place (stay on the DAG page)
async function inlineCancelRun(runID) {
  if (!(await confirmDialog(t("confirm_cancel_title", runID), t("confirm_cancel_body"), { danger: true, okLabel: t("run_cancel") }))) return;
  try { await api(`/api/runs/${encodeURIComponent(runID)}/cancel`, { method: "POST" }); toast(t("run_cancelled_toast"), "ok"); refreshDagRuns(); }
  catch (e) { toast(e.message, "fail"); }
}
async function inlineRetryRun(runID) {
  try { await api(`/api/runs/${encodeURIComponent(runID)}/retry`, { method: "POST" }); toast(t("run_retried_toast"), "ok"); refreshDagRuns(); }
  catch (e) { toast(e.message, "fail"); }
}

// --- structure section (editable graph + task list) ---
function renderDagStructure() {
  const el = $("d-structure"); if (!el) return; // structure tab not active
  el.innerHTML = dagStructureHtml();
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
      <td class="muted mono cmd-cell no-nav">${tk.command ? copySpan(tk.command, "", tk.command) : "—"}</td>
      <td class="mono">${esc(tk.pool)}</td><td class="muted">${t("tr_" + (tk.trigger_rule || "all_success"))}</td>
      <td class="muted">${esc((tk.deps || []).join(", ") || "—")}</td>
      <td><button class="icon no-nav" data-dup="${esc(tk.id)}" title="${t("btn_duplicate")}">⧉</button><button class="icon rm no-nav" data-del="${esc(tk.id)}" title="${t("b_remove")}">✕</button></td></tr>`).join("")}</tbody></table>`;
}
// per-DAG pan/zoom view, kept OUT of the serialized DAG model so it survives the
// immediate-save re-render (click-to-connect rebuilds #d-graph) without leaking
// into the API payload. Cleared per DAG-id, so switching DAGs starts fresh.
const graphViews = {};
function wireDagStructure() {
  const add = $("d-addtask"); if (add) add.onclick = addTask;
  document.querySelectorAll("#d-graph [data-node]").forEach((n) => n.onclick = () => onDagGraphNodeClick(n.dataset.node));
  const dgid = D.dag.dag_id;
  attachPanZoom(document.querySelector("#d-graph .graph-wrap"), graphViews[dgid] || (graphViews[dgid] = {}));
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
  D.tab = "structure"; // task pages are entered from Structure — back lands there
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
  jar: {
    fields: [
      { k: "jar", label: "cb_jar", ph: "app.jar", full: true },
      { k: "mainclass", label: "cb_mainclass", ph: "(optional) com.example.Main" },
      { k: "args", label: "cb_args", ph: "--in {{ logical_date }}", full: true },
    ],
    compose: (f) => (f.mainclass ? `java -cp ${f.jar || ""} ${f.mainclass}` : `java -jar ${f.jar || ""}`) + (f.args ? " " + f.args : ""),
    parse: (c) => { let m = c.match(/^java -jar (\S+)(?:\s+([\s\S]+))?$/); if (m) return { jar: m[1], mainclass: "", args: (m[2] || "").trim() }; m = c.match(/^java -cp (\S+) (\S+)(?:\s+([\s\S]+))?$/); if (m) return { jar: m[1], mainclass: m[2], args: (m[3] || "").trim() }; return null; },
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
  if (tk.type === "http") {
    const methods = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"];
    const m = tk.httpMethod || "GET";
    return `<div class="b-field full"><label>${t("t_http")}</label>${chips}
      <div class="tc-grid">
        <div class="b-field"><label>${t("http_method")}</label><select class="hf" data-h="httpMethod">${methods.map((o) => `<option ${m === o ? "selected" : ""}>${o}</option>`).join("")}</select></div>
        <div class="b-field full"><label>${t("http_url")}</label><input class="hf cmd" data-h="httpUrl" value="${esc(tk.httpUrl || "")}" placeholder="https://{{ conn.api.host }}/path"></div>
      </div>
      <div class="b-field full"><label>${t("http_headers")}</label><textarea class="hf cmd" data-h="httpHeaders" rows="3" spellcheck="false" placeholder="Authorization: Bearer {{ var.TOKEN }}">${esc(tk.httpHeaders || "")}</textarea><div class="field-hint">${t("http_headers_hint")}</div></div>
      <div class="b-field full"><label>${t("http_body")}</label><textarea class="hf cmd" data-h="httpBody" rows="3" spellcheck="false" placeholder='{"k":"{{ var.X }}"}'>${esc(tk.httpBody || "")}</textarea></div>
      <div class="b-field"><label>${t("http_status")}</label><input class="hf" data-h="httpStatus" value="${esc(tk.httpStatus || "")}" placeholder="200, 201"><div class="field-hint">${t("http_status_hint")}</div></div></div>`;
  }
  if (tk.type === "python") {
    return `<div class="b-field full"><label>${t("t_python")}</label>${chips}
      <textarea class="tf cmd" data-k="command" rows="8" spellcheck="false" placeholder="import os&#10;print(os.environ['CRONOVA_LOGICAL_DATE'])">${esc(tk.command)}</textarea>
      <div class="field-hint">${t("python_hint")}</div></div>`;
  }
  if (tk.type === "sql") {
    return `<div class="b-field full"><label>${t("t_sql")}</label>${chips}
      <div class="b-field"><label>${t("sql_conn")}</label><input class="tf" data-k="conn" value="${esc(tk.conn || "")}" placeholder="warehouse"><div class="field-hint">${t("sql_conn_hint")}</div></div>
      <textarea class="tf cmd" data-k="command" rows="6" spellcheck="false" placeholder="SELECT count(*) FROM events WHERE day = '{{ params.day }}'">${esc(tk.command)}</textarea></div>`;
  }
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
      : main.querySelector('[data-k="command"]') || main.querySelector('.hf.cmd[data-h="httpUrl"]') || main.querySelector('.cf[data-cf="args"]') || main.querySelector('.cf[data-cf="query"]') || main.querySelector('.cf[data-cf="target"]');
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
        <div class="b-field"><label>${t("t_type")}</label><select class="tf" data-k="type">${["shell", "python", "sql", "jar", "http"].map((o) => `<option ${tk.type === o ? "selected" : ""}>${o}</option>`).join("")}</select></div>
      </div>
      ${commandFieldHtml(tk)}
      <div class="section-h">${t("t_deps")}</div>
      <div class="b-deps">${siblings.length ? siblings.map((id) => `<span class="chip dep ${tk.deps.includes(id) ? "on" : ""}" role="checkbox" tabindex="0" aria-checked="${tk.deps.includes(id)}" data-dep="${esc(id)}">${esc(id)}</span>`).join("") : `<span class="chip empty-hint">${t("t_nodeps")}</span>`}</div>
      <div class="tc-grid" style="margin-top:14px">
        <div class="b-field"><label>${t("t_rule")}</label><select class="tf" data-k="trigger_rule">${TRIGGER_RULES.map((r) => `<option value="${r}" ${tk.trigger_rule === r ? "selected" : ""}>${t("tr_" + r)}</option>`).join("")}</select><div class="field-hint" id="rule-desc">${t("trd_" + (tk.trigger_rule || "all_success"))}</div></div>
      </div>
      <details class="adv-box"${(tk.pool !== "default" || +tk.priority || tk.retries !== "" || tk.retry_delay !== "" || tk.timeout || tk.sla) ? " open" : ""}>
        <summary>${t("adv_options")}</summary>
        <div class="tc-grid" style="margin-top:10px">
          <div class="b-field"><label>${t("t_pool")}</label><input class="tf" data-k="pool" value="${esc(tk.pool)}" placeholder="default"><div class="field-hint">${t("pool_hint")}</div></div>
          <div class="b-field"><label>${t("t_priority")}</label><input class="tf" data-k="priority" type="number" value="${esc(tk.priority)}"></div>
          <div class="b-field"><label>${t("t_retries")}</label><input class="tf" data-k="retries" type="number" min="0" value="${esc(tk.retries)}"></div>
          <div class="b-field"><label>${t("t_retrydelay")}</label><input class="tf" data-k="retry_delay" type="number" min="0" value="${esc(tk.retry_delay)}"></div>
          <div class="b-field"><label>${t("t_timeout")}</label><input class="tf" data-k="timeout" type="number" min="0" value="${esc(tk.timeout)}"><div class="field-hint">${t("t_timeout_hint")}</div></div>
          <div class="b-field"><label>${t("t_sla")}</label><input class="tf" data-k="sla" type="number" min="0" value="${esc(tk.sla)}"><div class="field-hint">${t("t_sla_hint")}</div></div>
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
  main.querySelectorAll(".hf").forEach((el) => {
    const h = el.dataset.h;
    if (el.tagName === "SELECT") { el.onchange = () => { tk[h] = el.value; saveDag(); }; return; }
    el.oninput = () => { tk[h] = el.value; };
    el.onblur = () => saveDag();
    if (el.classList.contains("cmd")) el.onfocus = () => lastCmdField = el; // template chips insert here
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
  if (D.tasks.some((x) => x.id && x.type !== "http" && !String(x.command).trim())) e.push(t("err_emptycmd"));
  if (D.tasks.some((x) => x.id && x.type === "http" && !String(x.httpUrl || "").trim())) e.push(t("err_httpurl"));
  if (D.tasks.some((x) => x.id && x.type === "sql" && !String(x.conn || "").trim())) e.push(t("err_sqlconn"));
  if (hasCycle(D.tasks.filter((x) => x.id))) e.push(t("err_cycle"));
  const nurl = (D.dag && D.dag.notify_url || "").trim();
  if (nurl && !/^https?:\/\//i.test(nurl)) e.push(t("err_notify_url"));
  return e;
}
function dagSpecFrom(st) {
  const d = st.dag;
  return {
    dag_id: d.dag_id, schedule: d.schedule, start_date: d.start_date,
    catchup: !!d.catchup, max_active_runs: +d.max_active_runs || 1, default_retries: +d.default_retries || 0,
    trigger_after: (d.trigger_after || []).slice(),
    // events are meaningless without a URL — keep the persisted state consistent
    notify_url: (d.notify_url || "").trim(), notify_on: (d.notify_url || "").trim() ? (d.notify_on || []).slice() : [],
    sla: Math.max(0, +d.sla || 0), dagrun_timeout: Math.max(0, +d.dagrun_timeout || 0),
    tasks: st.tasks.filter((tk) => tk.id).map((tk) => {
      const o = {
        id: tk.id, type: tk.type, pool: tk.pool || "default",
        priority: +tk.priority || 0, deps: (tk.deps || []).filter((dep) => st.tasks.some((x) => x.id === dep)),
        timeout: Math.max(0, +tk.timeout || 0), sla: Math.max(0, +tk.sla || 0), trigger_rule: tk.trigger_rule || "all_success",
        retries: tk.retries === "" || tk.retries == null ? null : +tk.retries,
        retry_delay: tk.retry_delay === "" || tk.retry_delay == null ? null : +tk.retry_delay,
      };
      if (tk.type === "http") {
        o.http = { method: tk.httpMethod || "GET", url: (tk.httpUrl || "").trim(), headers: parseHeaderLines(tk.httpHeaders), body: tk.httpBody || "", expected_status: parseStatusList(tk.httpStatus) };
      } else {
        o.command = tk.command;
        if (tk.type === "sql") o.conn = (tk.conn || "").trim();
      }
      return o;
    }),
  };
}
// "Key: Value" lines → header map (blank/invalid lines skipped).
function parseHeaderLines(text) {
  const h = {};
  String(text || "").split("\n").forEach((line) => {
    const i = line.indexOf(":");
    if (i > 0) { const k = line.slice(0, i).trim(), v = line.slice(i + 1).trim(); if (k) h[k] = v; }
  });
  return h;
}
// "200, 201" → [200, 201] (invalid entries dropped).
function parseStatusList(text) {
  return String(text || "").split(",").map((s) => parseInt(s.trim(), 10)).filter((n) => n >= 100 && n <= 599);
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
let runPoll = null, runPollGen = 0, runTab = "instances", runDag = null;
const TASK_TERMINAL = { success: 1, failed: 1, upstream_failed: 1, skipped: 1, cancelled: 1, timed_out: 1 };
const TASK_RETRYABLE = { failed: 1, upstream_failed: 1, cancelled: 1, timed_out: 1 }; // states a per-task retry clears
const runLive = (s) => s === "queued" || s === "running";

async function showRun(runID) {
  view = "run"; currentRun = runID; closeLog(); stopDagRunsPoll(); clearInterval(runPoll); const gen = ++runPollGen; setHash("#/run/" + encodeURIComponent(runID));
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
    <div class="page-h"><h1 class="mono" style="font-size:16px">${copySpan(r.run_id)}</h1><span id="run-badge">${badge(r.state)}</span><span class="run-prog" id="run-progress"></span><span class="run-actions" id="run-actions"></span></div>
    <div class="kv" style="margin:14px 0 4px">
      <div class="card"><div class="k">${t("k_logical")}</div><div class="v mono" style="font-size:13px">${copySpan(r.logical_date)}</div></div>
      <div class="card"><div class="k">${t("k_trig")}</div><div class="v">${typeLabel(r.trigger_type)}</div></div>
      <div class="card"><div class="k">${t("k_dur")}</div><div class="v" id="run-dur">${dur(r.started_at, r.finished_at)}</div></div>
      <div class="card"><div class="k">${t("k_started")}</div><div class="v" style="font-size:13px">${fmt(r.started_at)}</div></div></div>
    ${r.params && Object.keys(r.params).length ? `<div class="run-parambar"><span class="rp-k">${t("run_params")}</span>${Object.entries(r.params).map(([k, v]) => `<span class="rp-chip mono">${esc(k)}=<b>${esc(v)}</b></span>`).join("")}</div>` : ""}
    <div class="section-h">${t("sec_graph")}</div><div id="run-graph">${renderGraph(runDag.tasks, initSbt, { tag: true })}</div>
    <div class="run-tabs" id="run-tabs">
      <button class="pill ${runTab === "instances" ? "active" : ""}" data-rt="instances">${t("sec_instances")}</button>
      <button class="pill ${runTab === "timeline" ? "active" : ""}" data-rt="timeline">${t("g_timeline")}</button>
    </div>
    <div id="run-body"></div>
    <div id="logwrap"></div>`;
  attachPanZoom(main.querySelector("#run-graph .graph-wrap"));
  $("back").onclick = () => showDag(r.dag_id);
  $("run-tabs").querySelectorAll("[data-rt]").forEach((b) => b.onclick = () => {
    runTab = b.dataset.rt;
    $("run-tabs").querySelectorAll(".pill").forEach((x) => x.classList.toggle("active", x === b));
    renderRunBody(runDataCache);
  });
  renderRunDynamic(data);
  if (runLive(r.state)) startRunPoll(runID, gen);
}
let runDataCache = null;
function startRunPoll(runID, gen) {
  const p = setInterval(async () => {
    // gen guards against a stale callback (a later showRun — e.g. after retry —
    // started a fresh poll); clearInterval can't abort an already-parked await.
    if (gen !== runPollGen) { clearInterval(p); return; }
    let data; try { data = await api(`/api/runs/${encodeURIComponent(runID)}`); } catch (_) { return; }
    if (gen !== runPollGen || view !== "run" || currentRun !== runID) return;
    renderRunDynamic(data);
    if (!runLive(data.run.state)) {
      clearInterval(p);
      const st = data.run.state;
      toast(st === "success" ? t("run_done_ok") : st === "cancelled" ? t("run_cancelled_toast") : st === "timed_out" ? t("run_done_timeout") : t("run_done_fail"), st === "success" ? "ok" : st === "cancelled" ? "info" : "fail");
    }
  }, 2000);
  runPoll = p;
}
function renderRunDynamic(data) {
  runDataCache = data;
  const r = data.run, tasks = data.tasks || [];
  const sbt = {}; tasks.forEach((tk) => sbt[tk.task_id] = tk.state);
  $("run-badge").innerHTML = badge(r.state);
  $("run-dur").textContent = dur(r.started_at, r.finished_at);
  const done = tasks.filter((tk) => TASK_TERMINAL[tk.state]).length, running = tasks.filter((tk) => tk.state === "running").length;
  $("run-progress").textContent = tasks.length ? `${done}/${tasks.length}${running ? ` · ${running} ${stateLabel("running")}` : ""}` : "";
  renderRunActions(r, tasks);
  patchGraphStates(sbt);
  renderRunBody(data);
}
// state-dependent run actions (live-patched): cancel while active, retry-failed
// once it ends with failures/cancellations. Only rewrites the DOM when the action
// mode actually changes, so a keyboard user's focus on the button survives the 2s
// poll. Hidden for viewers (writes are admin-only server-side anyway).
function renderRunActions(r, tasks) {
  const el = $("run-actions"); if (!el) return;
  const isViewer = document.body.dataset.role === "viewer";
  const canRetry = tasks.some((tk) => TASK_RETRYABLE[tk.state]);
  // mode encodes retryability so a terminal run whose retryable set changes (e.g.
  // after a mark) re-renders; unchanged mode preserves the node + keyboard focus.
  const mode = isViewer ? "none" : runLive(r.state) ? "cancel" : "term" + (canRetry ? "R" : "");
  if (el.dataset.mode === mode) return;
  el.dataset.mode = mode;
  if (mode === "cancel") {
    el.innerHTML = `<button class="danger" id="run-cancel">${t("run_cancel")}</button>`;
    $("run-cancel").onclick = () => cancelRunUI(r.run_id);
  } else if (mode === "termR" || mode === "term") {
    // terminal run (admin): retry failed subtree (if any) + override the outcome
    el.innerHTML = `${canRetry ? `<button class="primary" id="run-retry">${t("run_retry")}</button>` : ""}<button id="run-mark">${t("run_mark")}</button>`;
    if (canRetry) $("run-retry").onclick = () => retryRunUI(r.run_id);
    $("run-mark").onclick = () => markRunUI(r.run_id);
  } else {
    el.innerHTML = "";
  }
}
async function cancelRunUI(runID) {
  if (!(await confirmDialog(t("confirm_cancel_title", runID), t("confirm_cancel_body"), { danger: true, okLabel: t("run_cancel") }))) return;
  try { await api(`/api/runs/${encodeURIComponent(runID)}/cancel`, { method: "POST" }); toast(t("run_cancelled_toast"), "ok"); showRun(runID); }
  catch (e) { toast(e.message, "fail"); }
}
async function retryRunUI(runID) {
  try { await api(`/api/runs/${encodeURIComponent(runID)}/retry`, { method: "POST" }); toast(t("run_retried_toast"), "ok"); showRun(runID); } // re-fetch + restart the poll
  catch (e) { toast(e.message, "fail"); }
}
async function retryTaskUI(runID, taskID) {
  // per-task retry re-runs this task AND its downstream — confirm first (larger blast radius than the bare ↻ implies)
  if (!(await confirmDialog(t("confirm_retry_title", taskID), t("confirm_retry_body"), { okLabel: t("task_retry") }))) return;
  try { await api(`/api/runs/${encodeURIComponent(runID)}/tasks/${encodeURIComponent(taskID)}/retry`, { method: "POST" }); toast(t("run_retried_toast"), "ok"); showRun(runID); }
  catch (e) { toast(e.message, "fail"); }
}
async function markTaskUI(runID, taskID) {
  const state = await pickDialog(t("mark_task_title", taskID), t("mark_task_body"), [
    { value: "success", label: t("notify_success") },
    { value: "skipped", label: t("mark_skip") },
    { value: "failed", label: t("notify_failure"), danger: true },
  ]);
  if (!state) return;
  try { await api(`/api/runs/${encodeURIComponent(runID)}/tasks/${encodeURIComponent(taskID)}/mark`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ state }) }); toast(t("mark_done_toast"), "ok"); showRun(runID); }
  catch (e) { toast(e.message, "fail"); }
}
async function markRunUI(runID) {
  const state = await pickDialog(t("mark_run_title", runID), t("mark_run_body"), [
    { value: "success", label: t("notify_success") },
    { value: "failed", label: t("notify_failure"), danger: true },
  ]);
  if (!state) return;
  try { await api(`/api/runs/${encodeURIComponent(runID)}/mark`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ state }) }); toast(t("mark_done_toast"), "ok"); showRun(runID); }
  catch (e) { toast(e.message, "fail"); }
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
  el.querySelectorAll(".retrybtn").forEach((b) => b.onclick = () => retryTaskUI(data.run.run_id, b.dataset.rtask));
  el.querySelectorAll(".markbtn").forEach((b) => b.onclick = () => markTaskUI(data.run.run_id, b.dataset.mtask));
  el.querySelectorAll(".gantt-row[data-ti]").forEach((row) => row.onclick = () => showLog(row.dataset.ti, row.dataset.task));
}
function instancesTableHtml(data) {
  const tasks = data.tasks || [];
  if (!tasks.length) return `<div class="empty">${t("run_no_tasks")}</div>`;
  // per-task retry only on a finished run (the backend refuses retry on an active
  // run) and not for viewers (writes are admin-only)
  const isAdmin = document.body.dataset.role !== "viewer";
  const canRetry = !runLive(data.run.state) && isAdmin;
  return `<table class="tbl"><thead><tr><th>${t("th_task")}</th><th>${t("th_state")}</th><th>${t("th_try")}</th><th>${t("h_pool")}</th><th class="num-col">${t("th_dur")}</th><th style="width:140px">${t("th_act")}</th></tr></thead>
    <tbody>${tasks.map((tk) => `<tr><td class="mono">${esc(tk.task_id)}</td><td>${badge(tk.state)}</td><td>${tk.try_number}/${tk.max_retries + 1}</td><td class="mono">${esc(tk.pool)}</td><td class="num-col">${dur(tk.started_at, tk.finished_at)}</td>
      <td><button class="icon logbtn" data-ti="${tk.id}" data-task="${esc(tk.task_id)}">${t("th_logs")}</button>${TASK_RETRYABLE[tk.state] && canRetry ? ` <button class="icon retrybtn" data-rtask="${esc(tk.task_id)}" title="${t("task_retry")}" aria-label="${t("task_retry")}">↻</button>` : ""}${isAdmin ? ` <button class="icon markbtn" data-mtask="${esc(tk.task_id)}" title="${t("task_mark")}" aria-label="${t("task_mark")}">⚑</button>` : ""}</td></tr>`).join("")}</tbody></table>`;
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

// ---- audit trail (operations log) ----
// t() returns the key itself when unknown, so guard: show the raw action verb
// for an action that has no act_* label rather than the literal "act_foo".
function auditActionLabel(a) { const v = t("act_" + a); return v === "act_" + a ? a : v; }
function auditRows(entries) {
  return entries.map((e) => `<tr><td style="font-size:12.5px">${fmt(e.ts)}</td><td class="mono">${esc(e.actor)}</td><td><span class="tag">${esc(auditActionLabel(e.action))}</span></td><td class="mono">${esc(e.target || "—")}${e.detail ? ` <span class="muted">${esc(e.detail)}</span>` : ""}</td></tr>`).join("");
}
async function showAudit() {
  view = "audit"; activeDag = null; closeLog(); stopDagRunsPoll(); setNav("audit"); setHash("#/audit");
  let entries = [];
  try { entries = await api("/api/audit?limit=200"); } catch (e) { main.innerHTML = `<div class="empty err">${t("api_err")}: ${esc(e.message)}</div>`; return; }
  main.innerHTML = `
    <div class="page-h"><h1>${t("nav_audit")}</h1><span class="num">${entries.length}</span></div>
    <div class="page-sub">${t("audit_sub")}</div>
    ${entries.length
      ? `<table class="tbl"><thead><tr><th style="width:180px">${t("au_time")}</th><th>${t("au_actor")}</th><th>${t("au_action")}</th><th>${t("au_target")}</th></tr></thead><tbody>${auditRows(entries)}</tbody></table>`
      : `<div class="empty">${t("audit_empty")}</div>`}`;
}

// ---- variables & connections (UI-managed shared config) ----
const CFG_KEY_RE = /^[A-Za-z0-9_.-]+$/; // mirrors the backend cfgKeyRe
let RES = null, resTab = "vars";
async function showResources() {
  view = "resources"; activeDag = null; closeLog(); setNav("resources"); setHash("#/resources");
  try { const [vars, conns] = await Promise.all([api("/api/variables"), api("/api/connections")]); RES = { vars, conns }; }
  catch (e) { main.innerHTML = `<div class="empty err">${t("api_err")}: ${esc(e.message)}</div>`; return; }
  renderResources();
}
function renderResources() {
  if (view !== "resources" || !RES) return;
  const { vars, conns } = RES;
  main.innerHTML = `
    <div class="page-h"><h1>${t("nav_resources")}</h1></div>
    <div class="page-sub">${esc(t("res_sub"))}</div>
    <div class="run-tabs" id="res-tabs">
      <button class="pill ${resTab === "vars" ? "active" : ""}"${resTab === "vars" ? ' aria-current="true"' : ""} data-rt="vars">${t("res_vars")} <span class="tab-n">${vars.length}</span></button>
      <button class="pill ${resTab === "conns" ? "active" : ""}"${resTab === "conns" ? ' aria-current="true"' : ""} data-rt="conns">${t("res_conns")} <span class="tab-n">${conns.length}</span></button>
    </div>
    <div id="res-body"></div>`;
  $("res-tabs").querySelectorAll("[data-rt]").forEach((b) => b.onclick = () => { if (resTab === b.dataset.rt) return; resTab = b.dataset.rt; renderResources(); const a = $("res-tabs").querySelector(".pill.active"); if (a) a.focus(); });
  $("res-body").innerHTML = resTab === "vars" ? varsTabHtml() : connsTabHtml();
  resTab === "vars" ? wireVarsTab() : wireConnsTab();
}
function varsTabHtml() {
  const vars = RES.vars;
  return `<table class="tbl"><thead><tr><th style="width:240px">${t("v_key")}</th><th>${t("v_value")}</th><th style="width:130px"></th></tr></thead>
    <tbody>${vars.length ? vars.map((v) => `<tr><td class="mono">${esc(v.key)}</td><td><input class="v-val mono" data-key="${esc(v.key)}" value="${esc(v.value)}" style="width:100%"></td>
      <td><button data-vsave="${esc(v.key)}">${t("v_save")}</button> <button class="icon danger" data-vdel="${esc(v.key)}" aria-label="${t("btn_delete")}">✕</button></td></tr>`).join("") : `<tr><td colspan="3"><div class="empty">${t("v_none")}</div></td></tr>`}</tbody></table>
    <div class="toolbar" style="margin-top:16px"><input id="nv-key" class="mono" placeholder="${t("v_key")}" style="width:240px"><input id="nv-val" class="mono" placeholder="${t("v_value")}"><button class="primary" id="nv-add">${t("v_add")}</button></div>`;
}
function wireVarsTab() {
  // keep RES in sync with each row input so a re-render (add/delete/lang toggle)
  // preserves sibling rows' unsaved edits instead of reverting them.
  main.querySelectorAll(".v-val").forEach((el) => el.oninput = () => { const v = RES.vars.find((x) => x.key === el.dataset.key); if (v) v.value = el.value; });
  main.querySelectorAll("[data-vsave]").forEach((b) => b.onclick = () => saveVar(b.dataset.vsave, main.querySelector(`.v-val[data-key="${CSS.escape(b.dataset.vsave)}"]`).value));
  main.querySelectorAll("[data-vdel]").forEach((b) => b.onclick = () => delVar(b.dataset.vdel));
  $("nv-add").onclick = () => { const k = $("nv-key").value.trim(); if (!k) { toast(t("v_key"), "warn"); return; } if (!CFG_KEY_RE.test(k)) { toast(t("err_key"), "warn"); return; } saveVar(k, $("nv-val").value); };
}
async function saveVar(key, value) {
  try {
    await api(`/api/variables/${encodeURIComponent(key)}`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ value }) });
    // update RES in place (no refetch — that would wipe unsaved sibling rows). An
    // existing row needs no re-render (its input already shows the value); a new
    // one re-renders, and the input sync above keeps the other rows intact.
    const v = RES.vars.find((x) => x.key === key);
    if (v) { v.value = value; } else { RES.vars.push({ key, value }); RES.vars.sort((a, b) => (a.key < b.key ? -1 : 1)); renderResources(); }
    toast(t("res_saved"), "ok");
  } catch (e) { toast(e.message, "fail"); }
}
async function delVar(key) {
  if (!(await confirmDialog(t("v_del_title", key), t("del_body"), { danger: true, okLabel: t("btn_delete") }))) return;
  try { await api(`/api/variables/${encodeURIComponent(key)}`, { method: "DELETE" }); RES.vars = RES.vars.filter((v) => v.key !== key); renderResources(); toast(t("res_deleted"), "ok"); }
  catch (e) { toast(e.message, "fail"); }
}
function connsTabHtml() {
  const conns = RES.conns;
  return `<table class="tbl"><thead><tr><th>${t("c_id")}</th><th>${t("c_type")}</th><th>${t("c_host")}</th><th>${t("c_login")}</th><th>${t("c_password")}</th><th style="width:130px"></th></tr></thead>
    <tbody>${conns.length ? conns.map((c) => `<tr><td class="mono">${esc(c.id)}</td><td>${esc(c.type || "—")}</td>
      <td class="mono muted">${esc(c.host || "—")}${c.port ? ":" + c.port : ""}</td><td class="mono muted">${esc(c.login || "—")}</td>
      <td>${c.has_password ? `<span class="mono">••••••</span>` : `<span class="muted">${t("c_pw_none")}</span>`}</td>
      <td><button data-cedit="${esc(c.id)}">${t("set_edit")}</button> <button class="icon danger" data-cdel="${esc(c.id)}" aria-label="${t("btn_delete")}">✕</button></td></tr>`).join("") : `<tr><td colspan="6"><div class="empty">${t("c_none")}</div></td></tr>`}</tbody></table>
    <div class="toolbar" style="margin-top:16px"><button class="primary" id="nc-add">${t("c_add")}</button></div>`;
}
function wireConnsTab() {
  main.querySelectorAll("[data-cedit]").forEach((b) => b.onclick = () => connDialog(RES.conns.find((c) => c.id === b.dataset.cedit)));
  main.querySelectorAll("[data-cdel]").forEach((b) => b.onclick = () => delConn(b.dataset.cdel));
  $("nc-add").onclick = () => connDialog(null);
}
async function delConn(id) {
  if (!(await confirmDialog(t("c_del_title", id), t("del_body"), { danger: true, okLabel: t("btn_delete") }))) return;
  try { await api(`/api/connections/${encodeURIComponent(id)}`, { method: "DELETE" }); RES.conns = RES.conns.filter((c) => c.id !== id); renderResources(); toast(t("res_deleted"), "ok"); }
  catch (e) { toast(e.message, "fail"); }
}
// connection editor modal. Password is write-only: on edit it starts blank and a
// blank submit preserves the stored secret (the UI never receives it).
function connDialog(conn) {
  const isEdit = !!conn, c = conn || { id: "", type: "", host: "", port: "", login: "", extra: "" };
  const root = $("modal-root");
  root.innerHTML = `<div class="overlay" id="covl"><div class="modal" role="dialog" aria-modal="true" aria-label="${isEdit ? t("c_edit") : t("c_add")}">
    <h2>${isEdit ? t("c_edit") : t("c_add")}</h2>
    <div class="body">
      <div class="b-field"><label>${t("c_id")}</label><input id="c-id" class="mono" value="${esc(c.id)}" ${isEdit ? "disabled" : ""} placeholder="mysql_prod"></div>
      <div class="b-grid">
        <div class="b-field"><label>${t("c_type")}</label><input id="c-type" value="${esc(c.type || "")}" placeholder="mysql"></div>
        <div class="b-field"><label>${t("c_host")}</label><input id="c-host" class="mono" value="${esc(c.host || "")}"></div>
        <div class="b-field"><label>${t("c_port")}</label><input id="c-port" type="number" value="${c.port || ""}"></div>
        <div class="b-field"><label>${t("c_login")}</label><input id="c-login" class="mono" value="${esc(c.login || "")}"></div>
      </div>
      <div class="b-field"><label>${t("c_password")}</label><input id="c-pw" type="password" placeholder="${isEdit && conn.has_password ? t("c_pw_keep") : ""}"></div>
      <div class="b-field"><label>${t("c_extra")}</label><textarea id="c-extra" class="mono" rows="3" spellcheck="false" placeholder='{"schema":"prod"}'>${esc(c.extra || "")}</textarea></div>
      <div class="nd-err" id="c-err"></div>
    </div>
    <div class="foot"><button id="c-cancel">${t("nd_cancel")}</button><button class="primary" id="c-save">${t("v_save")}</button></div>
  </div></div>`;
  const close = () => { document.removeEventListener("keydown", onKey); root.innerHTML = ""; };
  const onKey = (e) => { if (e.key === "Escape") close(); };
  document.addEventListener("keydown", onKey);
  $("covl").onclick = (e) => { if (e.target.id === "covl") close(); };
  $("c-cancel").onclick = close;
  $("c-save").onclick = async () => {
    const id = isEdit ? c.id : $("c-id").value.trim();
    if (!id) { $("c-err").textContent = t("c_id"); return; }
    if (!CFG_KEY_RE.test(id)) { $("c-err").textContent = t("err_key"); return; }
    const body = { type: $("c-type").value.trim(), host: $("c-host").value.trim(), port: +$("c-port").value || 0, login: $("c-login").value.trim(), password: $("c-pw").value, extra: $("c-extra").value.trim() };
    try {
      const resp = await api(`/api/connections/${encodeURIComponent(id)}`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
      const i = RES.conns.findIndex((x) => x.id === id); // masked resp (has_password, no secret)
      if (i >= 0) RES.conns[i] = resp; else { RES.conns.push(resp); RES.conns.sort((a, b) => (a.id < b.id ? -1 : 1)); }
      close(); renderResources(); toast(t("res_saved"), "ok");
    } catch (e) { $("c-err").textContent = e.message; }
  };
  $(isEdit ? "c-type" : "c-id").focus();
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
  attachPanZoom(main.querySelector("#dag-graph .graph-wrap"));
}