"use strict";
// ---- theme ----
function applyTheme() {
  document.documentElement.dataset.theme = theme;
  const b = $("theme"); if (b) { b.textContent = theme === "dark" ? "☀" : "☾"; b.setAttribute("aria-pressed", theme === "light"); b.setAttribute("aria-label", t("aria_theme")); }
}

// ---- boot ----
// topbar search: filters the dashboard AND is a global jump-to-DAG box everywhere
$("search").oninput = (e) => { query = e.target.value.toLowerCase(); if (view === "dags") renderDags(); updateJump(e.target.value); };
$("search").onfocus = () => { ensureJumpDags().then(() => { if ($("search").value) updateJump($("search").value); }); };
$("search").onblur = () => setTimeout(closeJump, 150); // let a menu mousedown land first
$("search").onkeydown = (e) => {
  if (e.key === "ArrowDown") { e.preventDefault(); jumpMove(1); }
  else if (e.key === "ArrowUp") { e.preventDefault(); jumpMove(-1); }
  else if (e.key === "Enter") { if (jumpEnter()) e.preventDefault(); }
  else if (e.key === "Escape") { closeJump(); }
};
$("newdag").onclick = () => { nvMode() ? showWizard() : newDagModal(); };
$("lang").onclick = () => setLang(lang === "zh" ? "en" : "zh");
$("theme").onclick = () => { theme = theme === "dark" ? "light" : "dark"; localStorage.setItem("cnv_theme", theme); applyTheme(); };
$("mode-nov").onclick = () => setMode("novice");
$("mode-exp").onclick = () => setMode("expert");
applyTheme();
document.querySelectorAll(".nav-item[data-nav]").forEach((n) => n.onclick = () => { const v = n.dataset.nav; v === "pools" ? showPools() : v === "graph" ? showGraph() : v === "resources" ? showResources() : v === "audit" ? showAudit() : v === "api" ? showApi() : v === "help" ? window.open(DOCS_URL, "_blank", "noopener") : loadDags(); });
// One delegated keydown (on the stable document, survives every innerHTML swap):
// Enter/Space activates any focusable widget we expose with a role (rows, toggles,
// chips, nav items) — so the focus ring lands on something operable. (#5)
document.addEventListener("keydown", (e) => {
  if (e.key !== "Enter" && e.key !== " ") return;
  const el = e.target;
  if (el && el.matches && el.matches('[tabindex="0"][role]:not(input):not(textarea):not(select)')) { e.preventDefault(); el.click(); }
});
// Escape closes an open help bubble without moving pointer or focus (WCAG 1.4.13
// dismissable); the .hlp-off class clears itself when the trigger is left.
document.addEventListener("keydown", (e) => {
  if (e.key !== "Escape") return;
  const off = (el, ev) => { el.classList.add("hlp-off"); el.addEventListener(ev, () => el.classList.remove("hlp-off"), { once: true }); };
  const h = document.activeElement;
  if (h && h.classList && h.classList.contains("hlp")) off(h, "blur");
  document.querySelectorAll(".hlp:hover").forEach((el) => off(el, "mouseleave"));
});
// One delegated click for copy-to-clipboard: any [data-copy] element copies its
// value. stopPropagation so a copyable inside a clickable row copies without also
// triggering the row's navigation.
document.addEventListener("click", (e) => {
  const el = e.target.closest && e.target.closest("[data-copy]"); if (!el) return;
  e.stopPropagation();
  copyText(el.dataset.copy).then((ok) => toast(ok ? t("copied") : t("copy_fail"), ok ? "ok" : "warn"));
}, true); // capture phase: run before a row's bubble-phase onclick
applyStaticI18n();
// auth gate: resolve identity first. If a login is required, initAuth shows the
// overlay and startApp() runs only after a successful sign-in; otherwise start now.
initAuth().then((ok) => { if (ok) startApp(); });
// Heartbeat: refresh the executor/scheduler footer (honest tick) every cycle,
// and the dashboard ONLY when it's showing AND the data actually changed (no
// gratuitous table rebuild / flash). Never touches the edit pages; pauses
// entirely while the tab is hidden.
setInterval(async () => {
  if (document.hidden || !authUser) return; // paused until signed in
  loadInfo();
  if (view !== "dags" || logES || $("modal-root").innerHTML) return;
  try {
    const fresh = await api("/api/overview");
    if (JSON.stringify(fresh) !== JSON.stringify(overviewCache)) {
      overviewCache = fresh; setDagCounts(fresh.stats.total_dags); renderDags();
    }
  } catch (_) {}
}, 6000);// Touch affordance for hover-only details: sparkline cells and activity ticks
// describe themselves via title, which never shows on touch. A tap surfaces
// the same text as a toast (harmless no-op for mouse users who saw the tooltip).
document.addEventListener("click", (e) => {
  const el = e.target.closest && e.target.closest(".spark i[title], .gantt-bar[title]");
  if (!el || !el.title) return;
  if (window.matchMedia && window.matchMedia("(hover: none)").matches) { e.stopPropagation(); toast(el.title, "info"); }
}, true);
