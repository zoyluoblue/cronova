# cronova Console — Design System & Principles

The web console is a single embedded **vanilla-JS SPA** (`internal/web/static/`:
`index.html` + `app.js` + `styles.css`), served by the Go binary via `go:embed`.
No build step, no framework, no external libraries, web fonts, or icon packs.
This document is the contract a polish/feature pass must respect.

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
- **Instant beats animated.** The hard cut on navigation is a feature for a tool
  hit dozens of times an hour. Motion is reserved for meaningful, small moments.

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
- **Sparkline honesty:** every real-state bar is the **same height** (color
  carries the state); only no-run/skipped slots are short stubs. Do **not**
  re-introduce decorative/pseudo-random height. Height-encoding real run
  duration is the only legitimate upgrade (needs the overview payload to carry it).
- **Empty states:** a truly empty instance gets a first-run hero with a CTA
  (`.empty-state`), distinct from the filtered "no matches" copy.
- **Save indicator:** the `.savestate` pill (`saved`/`saving`/`invalid`/`error`)
  reflects the real in-memory validity after every render — never hardcode
  "saved" at the end of a re-render.

## Explicitly NOT doing (anti-over-engineering)

Decided against in the design review; do not add without a real, measured reason:
- Elevation/shadow system, hover-lift, inset highlights — the flat look is the brand.
- A full `--fs-*`/`--space-*` type/space scale retrofit — low payoff, theme-parity risk.
- Loading skeletons / shimmer — localhost+SQLite fetches are sub-50ms; a flash reads as jank.
  (We instead guard the auto-refresh against no-op table rebuilds.)
- Optimistic synthetic run rows — fabricating a run that may never exist is a trust violation.
- A surgical no-re-render path for the graph — the one exception to the whole-app
  re-render model; an inconsistency tax and bug farm.
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
