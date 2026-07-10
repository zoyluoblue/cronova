# cronova Console — Design System & Principles

The web console is an embedded **vanilla-JS SPA** (`internal/web/static/`:
`index.html` + `styles.css` + four classic scripts loaded in order sharing one
global scope — `base.js` state/i18n/helpers, `views.js` pages, `builder.js`
schedule/templates/new-DAG, `boot.js` wiring), served by the Go binary via
`go:embed`. No build step, no framework, no external libraries, web fonts, or
icon packs. This document is the contract a polish/feature pass must respect.

## Direction

A **calm, honest, keyboard-first operator's console**: the restraint of a
developer-admin tool with the finish of one built by people who sweat details.
Polish **subtracts dishonesty and adds access** — it does not add chrome. We
elevate the existing language (CSS-variable theming, monospace ids, tiny
uppercase eyebrows, flat 1px surfaces) rather than replacing it.

Principles:
- **Never fabricate data.** A viz that invents variance erodes trust in a
  monitoring tool (see the sparkline rule below).
- **Single source of truth.** Color, state, and copy each come from one
  definition so both themes and both languages fall out for free.
- **Access is not optional.** Visible focus, full keyboard operation, AA
  contrast, reduced-motion support are table stakes, not delight.
- **Instant beats animated.** Motion is reserved for meaningful, small moments:
  navigation gets a single 120ms fade (`.main.enter`, applied only when the
  breadcrumb actually changes — data refreshes never animate); everything else
  is an instant cut.
- **Times are honest.** Schedules evaluate in **UTC** (the engine anchors on UTC
  timestamps) — the topbar label says so; fire-time previews come from the
  server's own parser (`GET /api/schedule/preview`), never client-side cron math.

## Foundations (CSS variables — `styles.css` `:root` + `:root[data-theme="light"]`)

- **Theme:** dark default, light via `data-theme="light"` on `<html>`. Drive
  **everything** off vars; never hardcode a hex that differs per theme.
- **Status palette is single-sourced:** state tints are derived with
  `color-mix(in srgb, var(--<state>) N%, transparent)` — for badges (`.s-*`),
  the sparkline, and the dependency graph (`colorForState`). Never hardcode
  `rgba()` tints; change a state var once and every surface tracks it.
  - SVG node fill/stroke must go in an **inline `style=`** (SVG *presentation
    attributes* don't resolve `color-mix`). Only literal token strings there —
    never interpolate user data into `fill`/`stroke`.
- **State vars:** `--ok --fail --run --warn --skip --upstream` (+ `--accent`,
  `--accent-2`). Light-theme `--ok`/`--warn`/`--faint`/`--upstream` are tuned for
  ≥4.5:1 text contrast on their tinted fills.
- **Focus:** one global `:focus-visible { outline: 2px solid var(--focus); ... }`.
  Never `outline: none` without an equivalent ring.
- **Motion:** `--dur` / `--ease`; **all** animation/transition is neutralized
  under `@media (prefers-reduced-motion: reduce)`.
- **Radii/elevation:** `--r` / `--r-sm`. Flat surfaces are the identity — no
  shadow/elevation system (floating things — toast, popover, modal — may use a
  modest drop shadow; passive containers stay flat 1px).

## Component conventions

- **Feedback:** use `toast(msg, kind)` (`ok|fail|warn|info`) and
  `await confirmDialog(title, body, {danger, okLabel})`. **Never** native
  `alert()`/`confirm()` (un-themed, un-localized, blocking). Successful
  side-effecting actions (trigger, delete, pool save) get a success toast;
  errors get a persistent one.
- **i18n:** every user-facing string lives in `DICT` with **both `zh` and `en`**.
  Use `t("key", ...args)`; function-valued entries interpolate. Proper-noun
  product terms ("DAGs", "Pools") are intentionally not translated.
- **Keyboard:** interactive non-`<button>` widgets (nav items, the pause toggle,
  dependency/trigger-after chips, clickable rows/cards) carry `role` +
  `tabindex="0"` + the right `aria-*` (`aria-checked`/`aria-pressed`), and are
  activated by **one delegated `keydown` on `document`** (Enter/Space → `.click()`,
  bound once at boot so it survives `innerHTML` re-renders). Keep `aria-checked`
  in sync at the toggle site.
- **Sparkline honesty:** bars now **honestly encode real run duration** — height
  ∝ `ms` (from the overview payload), scaled against a **dashboard-wide** max so
  "taller = slower" reads consistently across DAG rows. No-run/skipped stay short
  stubs; running/queued (no duration yet) get a neutral mid bar — **never** a
  fabricated one. Color still carries state; the exact duration is in the bar's
  title. Do **not** re-introduce decorative/pseudo-random height.
- **Activity timeline honesty (`activityStrip`):** the dashboard's recent-run
  strip places one state-colored tick per run at its **real** start time on a
  shared axis (last ~24 runs across all live DAGs, from `/api/overview`). A tick
  exists only where a run actually ran; hover shows dag·state·duration·time,
  click opens the run. No fabricated cadence — regular ticks mean a regular
  schedule.
- **Empty states:** a truly empty instance gets a first-run hero with a CTA
  (`.empty-state`), distinct from the filtered "no matches" copy.
- **Save indicator:** the `.savestate` pill (`saved`/`saving`/`invalid`/`error`)
  reflects the real in-memory validity after every render — never hardcode
  "saved" at the end of a re-render.
- **DAG page = hero + tabs (one focus per screen):** the detail page opens with
  a hero card (name, type, honest facts — last run, schedule, recent success
  rate — and the primary actions) and then exactly ONE concern below it, chosen
  by tabs: **Runs** (default; a 0-task shell defaults to Structure instead),
  **Structure** (graph + tasks), **Settings**. Tabs are linkable
  (`#/dag/<id>/<tab>`) and refresh-safe. Never re-flatten all sections onto one
  scroll — the density was the original complaint.
- **Click-to-edit settings (immediate save, no resident forms):** every setting
  renders as a one-line summary row (`.set-row`); clicking it swaps in the
  editor in place, edits save immediately (same debounced pipeline), and "完成"
  collapses back to the summary (full page re-render so the hero facts stay in
  sync). Only one row edits at a time. Destructive actions live in a separate
  danger zone at the bottom of Settings — never in the hero.
- **Variables / connections / params (secret honesty):** the "变量 & 连接" page
  manages shared config that tasks reference in commands via the template engine
  — `{{ var.KEY }}`, `{{ conn.ID.field }}` (host/port/login/password/type/extra.X),
  and `{{ params.KEY }}` (free-form key-values supplied at manual trigger,
  injected as `CRONOVA_PARAM_*`). Resolution happens **server-side at execution**,
  and only *referenced* values enter a task — variables/connections are never
  blanket-injected as env, so a secret reaches only the tasks that ask for it.
  Connection **passwords are write-only**: the API never returns them (the model
  field is `json:"-"`), the UI shows `••••••` with a `has_password` flag, and a
  blank-password edit preserves the stored secret. A run records the exact params
  it was triggered with (honest replay); the run page shows them as chips.
  (Passwords are stored plaintext — protect the SQLite file with fs perms.)
- **Auth gate (`initAuth`):** boot resolves identity via `GET /api/me` before
  rendering anything data-bearing. 200 → start the app; 401 → a full-screen
  login overlay (`#login-root`), and `startApp()` runs only after a successful
  `POST /api/login`. When auth is **disabled** the server reports an implicit
  admin (`auth:false`) so the console opens exactly as before — no login, no
  user chip. A 401 from any later `api()` call bounces back to the overlay
  ("session expired"). Role is mirrored to `body[data-role]`; the write CTA is
  hidden for `viewer`, and the **server** is the real gate (writes → 403), so
  the frontend only avoids offering doomed actions, never relies on hiding for
  security. Cookies are httpOnly; the token never touches JS.
- **Live run view (read-only):** while a run is `queued`/`running`, `showRun`
  polls `/api/runs/{id}` every 2s and patches only the leaf containers
  (`#run-badge`, `#run-dur`, `#run-progress`, `#run-body`) plus the graph node
  fills (`patchGraphStates` — the run graph is built once with `renderGraph(…,
  {tag})` and thereafter only re-tinted, so the running-node `.g-running` pulse
  and the fill transition read as live). `#logwrap` is **never** rebuilt, so an
  open log stream survives a refresh. The poll **self-terminates** on a terminal
  run state (then fires one success/fail toast) or on navigating away — this is
  safe precisely because the page is read-only (no save pipeline to race). This
  is the one sanctioned exception to whole-app re-render; it does not generalize
  to the editable pages.
- **Graph pan/zoom (`attachPanZoom`):** every `renderGraph` surface (editable
  builder, global dependency, live run) is pannable/zoomable via a CSS transform
  on the inner `<svg>` (vectors stay crisp; node click handlers and live rect-fill
  patching are untouched). Drag to pan; **Ctrl/⌘ + wheel** to zoom (plain wheel is
  never trapped — the page scrolls); `+ / ⤢ / −` chrome reveals on hover/focus. A
  graph larger than its box **auto-fits on attach** (replacing the old
  `overflow:auto` scroll); one that fits keeps the identity transform (natural,
  top-left). The transform is instant (no motion to reduce). The editable graph is
  rebuilt on every dependency edit, so its view is persisted in a per-DAG-id store
  (`graphViews`, kept out of the serialized model) and reseeded — otherwise
  click-to-connect would reset the pan on every click.
- **Run actions (cancel / retry):** the run page is no longer read-only. While a
  run is queued/running a **Cancel** button appears (kills running tasks via the
  executor and marks the run + its non-terminal tasks `cancelled` — a distinct,
  honest state, not a fake "failed"); once a run ends with failures/cancellations
  a **Retry failed** button (run-level) and a per-task **↻** (in the instances
  table) clear that task + its downstream closure back to `scheduled` and
  reactivate the run. Cancellation is race-safe: the run is marked cancelled
  first so in-flight polling goroutines skip overwriting the outcome (guarded by
  an optimistic CAS). Retry does **not** reset `try_number` — the executor ref
  derives from it and `Launch` is idempotent per ref, so reusing an old ref would
  replay a stale result instead of a fresh attempt.
  - **Concurrency safety** is enforced by a store-level CAS: task finalize/running
    writes go through `UpdateTaskInstanceGuarded` (`WHERE id=? AND executor_ref=?
    AND state NOT IN (terminal)`), so a polling goroutine can never clobber a
    concurrent cancel (row → terminal) or retry (ref cleared). **Retry is refused
    on a still-active run** (cancel first) — a terminal run has no in-flight task
    goroutines to race — and only reactivates tasks still present in the DAG (a
    removed task has no dispatch path and would wedge the run). Initial attempts
    use the run's immutable DAG snapshot; an explicit retry deliberately adopts
    the latest DAG and records its definition hash on reset/new task instances.
    A run with a
    leftover `cancelled` task finalizes as **cancelled**, never a clean success,
    and does not trigger downstreams.
- **Gantt honesty:** the Timeline tab positions one bar per task from its real
  `started_at`/`finished_at`; the empty track between bars is genuine waiting
  (queued time isn't stored — we do **not** draw a fabricated queued segment).
  A task that never ran shows a muted state marker, not a zero-width bar. One bar
  per task with a `×N` try badge — **no** per-try segments (the store keeps a
  single started/finished pair). Bar colors are the same single-sourced state
  vars as badges/graph (`.gantt-bar.g-<state>`).

## Explicitly NOT doing (anti-over-engineering)

Decided against in the design review; do not add without a real, measured reason:
- Elevation/shadow system, hover-lift, inset highlights — the flat look is the brand.
- A full `--fs-*`/`--space-*` type/space scale retrofit — low payoff, theme-parity risk.
- Loading skeletons / shimmer — localhost+SQLite fetches are sub-50ms; a flash reads as jank.
  (We instead guard the auto-refresh against no-op table rebuilds.)
- Optimistic synthetic run rows — fabricating a run that may never exist is a trust violation.
- A surgical no-re-render path for the graph on the **editable/dashboard** pages —
  an inconsistency tax and bug farm there. (The **live run view** is the single
  sanctioned exception: it is read-only, so patching leaf nodes + graph fills has
  no save pipeline to race, and it must preserve a live log stream. See the
  live-run-view convention above.)
- Responsive sidebar collapse / mobile reflow — this is a desktop operator tool.
- Brand gradients / decorative chrome — restraint is the identity.

## Verification checklist (after any UI change)

Run through **dark × light × zh × en**, plus keyboard and a narrow window:
- Toggle theme: badges, sparkline, graph nodes re-theme live; no stale dark color in light.
- Tab through controls: visible ring on each; Enter/Space activates toggles/chips/nav.
- Feedback: trigger → success toast; an error → persistent toast; delete → in-app confirm.
- `prefers-reduced-motion`: dots/cursors/crossfades don't loop or animate.
- Contrast spot-check on `--faint` text and the `upstream_failed` badge (≥4.5:1).

See [ARCHITECTURE.md](ARCHITECTURE.md) for the backend/scheduler design.
