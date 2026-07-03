"use strict";
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
document.querySelectorAll(".nav-item[data-nav]").forEach((n) => n.onclick = () => { const v = n.dataset.nav; v === "pools" ? showPools() : v === "graph" ? showGraph() : v === "resources" ? showResources() : loadDags(); });
// One delegated keydown (on the stable document, survives every innerHTML swap):
// Enter/Space activates any focusable widget we expose with a role (rows, toggles,
// chips, nav items) — so the focus ring lands on something operable. (#5)
document.addEventListener("keydown", (e) => {
  if (e.key !== "Enter" && e.key !== " ") return;
  const el = e.target;
  if (el && el.matches && el.matches('[tabindex="0"][role]:not(input):not(textarea):not(select)')) { e.preventDefault(); el.click(); }
});
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
      overviewCache = fresh; $("nav-dags").textContent = fresh.stats.total_dags; renderDags();
    }
  } catch (_) {}
}, 6000);