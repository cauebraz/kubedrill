# Workspace front end

The browser workspace is plain HTML, CSS and ES modules served from the
binary; there is no build step. `vendor/` holds xterm.js and mermaid
(sources and checksums in its README); `js/mermaid.js` loads mermaid on
the first page with a ```mermaid block and themes it from the tokens.

| File | Role |
|------|------|
| `index.html` | Shell: top bar with the mark, banner, toast, the theme bootstrap that runs before the first paint |
| `favicon.svg` | The mark (a five-step ring, one step and the center in the brand teal) with its own light and dark colors through `prefers-color-scheme`; the top bar draws the same paths with `--fg` and `--brand` |
| `app.js` | Entry: routes (`#/`, `#/catalog`, `#/exams`, `#/a/<id>/<stage>`, `#/a/<id>/debrief/<episode>`, `#/kit`), navigation, theme, preview and server-down banners |
| `js/api.js` | The JSON API, the NDJSON launch stream, the connection listener |
| `js/dom.js` | `el`, `svg`, `clear`, `toast`, `store` (localStorage), `onLeave` cleanups, `shuffledOrder` (the choice order of a question, seeded by its text), icons |
| `js/keys.js` | Shortcuts per screen (`bind`), the overlay stack Esc closes, the `?` help |
| `js/ui.js` | Shared components: stages, rings, dots, mastery meter (clean solves out of 3), status pill and its vocabulary (`statusOf`: not started, in progress, live, complete, mastered), assignment row, `hideFixSection`, skeleton, error state |
| `js/welcome.js` | The welcome flow (`#/welcome`): the five steps of the loop on a worked ticket, shown once per journal before the workspace opens |
| `js/mermaid.js` | Renders ```mermaid blocks of scenario markdown with the vendored mermaid, themed from the tokens, redrawn on theme change |
| `js/home.js`, `js/catalog.js`, `js/exams.js` | The three top-level screens |
| `js/assignment.js` | The assignment shell, stepper and concept drawer |
| `js/meeting.js`, `js/quiz.js`, `js/learn.js`, `js/lab.js`, `js/result.js` | The stages: meeting, quiz, learn and diagram, practice and DIY lab, debrief and result |
| `js/kit.js` | The component kit (`#/kit`, linked when the server runs with `--static-dir`) |
| `app.css` | The design system: tokens, then primitives, components and screens |

A screen is a function `(app or stage, data, ...)` that builds its DOM with
`el`, registers shortcuts with `bind` and timers or listeners with
`onLeave`; the router runs every `onLeave` before the next screen, so
nothing outlives its screen.

## Design system

Everything visual comes from the custom properties at the top of `app.css`.
Dark is the default; `<html data-theme="light">` switches, and the toggle in
the top bar stores the choice under `kd:theme`. A component uses tokens,
never literal colors, so both themes stay in step (the terminal is the one
surface that is dark in both).

| Group | Tokens | Use |
|-------|--------|-----|
| Surfaces | `--bg`, `--bg-1`, `--bg-2`, `--bg-3` | page, card, inset, hover; each step one level up |
| Text | `--fg`, `--fg-1`, `--fg-2`, `--fg-3` | headings and body, secondary, labels, decoration; `--fg-2` and up meet 4.5:1 on every surface in both themes, `--fg-3` is for separators, icons and disabled states only |
| Lines | `--line`, `--line-1`, `--line-2` | dividers, control borders, hover borders |
| Accent | `--accent`, `--accent-hover`, `--on-accent` | the primary button (white on dark, black on light) |
| Brand | `--brand`, `--brand-dim` | one teal for progress, focus, live states and labels; never large areas |
| Semantic | `--good`, `--warn`, `--bad`, `--info`, each with `-dim` | check results, feedback, callouts, banners |
| Terminal | `--term-bg` | the xterm pane and command blocks |
| Radii | `--r-1` 6, `--r-2` 10, `--r-3` 14, `--r-4` 20 | controls, cards, panels, modals |
| Spacing | `--s-1` 4 to `--s-8` 72 | paddings and gaps |
| Type | `--t-xs` 11 to `--t-3xl` 40; `--font-sans`, `--font-mono` | Inter-first system stack; mono for labels, ids and numbers |
| Motion | `--fast` 120ms, `--base` 200ms, `--slow` 360ms, `--ease` | every transition; `prefers-reduced-motion` turns them off |
| Effects | `--shadow-1..3`, `--glow`, `--ring` | depth, the home and badge glow, the focus ring |
| Backdrops | `--backdrop`, `--backdrop-soft` | behind a dialog, behind a drawer |

Rules:

- One primary button per screen, for the action with consequences
  (Validate, Launch, Save the debrief, Start). Moving to the next stage is
  a default `.btn`.
- Labels that classify (stage, level, ids, counts) are `.eyebrow`: mono,
  uppercase, tracked, once per block. Prose is sans.
- Status is a `.pill` with a leading dot and a semantic class, never a
  colored word alone; progress dots and meters carry an `aria-label`.
- Learner and scenario text goes through `textContent`, or through the
  server's markdown renderer (raw HTML dropped) into `innerHTML`.
- Every interactive element has a hover state and the `:focus-visible`
  ring; drawers, popovers and dialogs register with `pushOverlay` so Esc
  closes them.
- Loading shows `skeleton()`, failures show `errorState()` with a retry;
  no screen stays blank.
- Single-key shortcuts never fire while typing (`isTyping`).
- Choices (meeting, quiz, welcome) are shown in `shuffledOrder`, never in
  file order, and the number keys follow the shown order.
- One status vocabulary everywhere: an assignment is complete when its Do
  it yourself run passed, which is what the plan rings, the exam coverage
  and the catalog filter count; a plan badge needs every assignment of the
  plan complete.
- Esc that closes an overlay stops there and never reaches the terminal.
  The lab's mentor is a docked column beside the terminal at 961 pixels and
  up (Esc inside it closes it), a drawer below that.

## Checking a change

- `just web-dev` serves the page on the in-memory backend of
  `../webtest` with these files read from disk: edit, reload. `POST
  /dev/fix` (or the backend's `Fix`) makes the live checks pass.
- `just ui-test` drives every stage in headless Chrome (meeting and quiz by
  keyboard, the lab, the debrief, the result, the catalog filters, the
  server-down banner) and fails on a broken flow; `just ui-test /tmp/shots`
  saves each screen in both themes for review.
- The kit (`#/kit` under `--static-dir` or `just web-dev`) shows every
  component in its states.
- `go test ./internal/web -run TestSnapshots` pins the JSON the page reads;
  `-update` rewrites `testdata/` after a deliberate change.
