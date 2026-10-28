# kubedrill command line

The exact reference: every command, flag, printed message, key, environment
variable and exit code. For how to study with these commands, read
[study-flow.md](study-flow.md); for the container, read
[workstation.md](workstation.md).

## Running kubedrill

`./start.sh` builds `bin/kubedrill` and runs it with the same arguments.

- It builds with the `go` on `PATH`. Without one, and with Docker running,
  it prints `go is not on PATH; building kubedrill inside a golang container
  (the first build also downloads the golang image)` and builds inside
  `GO_BUILD_IMAGE` of `workstation/builder.env`, with the module and build
  caches under `<state dir>/go`; a later build takes about 2 s. Without
  either it says Docker is needed and exits 1.
- `./start.sh` without arguments is the one command a learner needs: it
  runs [`kubedrill web`](#web), so the first run sets everything up and
  opens the browser workspace on `http://127.0.0.1:8642`, and every later
  run opens it again with the progress kept. `kubedrill` without a command
  does the same.
- Before that it makes `kubedrill` typeable when it is not yet: when
  `~/.local/bin` is on `PATH` and holds no `kubedrill` other than a symlink,
  it links `~/.local/bin/kubedrill` to `bin/kubedrill` and prints `kubedrill
  linked at ~/.local/bin/kubedrill`; otherwise it prints `tip: ./start.sh is
  all you need; to type kubedrill directly, add <repo>/bin to PATH`. Nothing
  is printed when `kubedrill` already resolves to this binary or to that
  link.
- `./start.sh <command>` hands over to the binary without the PATH step.

Conventions shared by every command:

- Every command accepts the global flag `--root <dir>`, the repository root
  (see `KUBEDRILL_ROOT` in [Environment variables](#environment-variables)).
  A command that reads scenarios, run outside the checkout without either,
  exits 1 with `repository root not found from <dir> (a directory with
  curriculum.yaml and scenarios/); run inside the checkout or set --root or
  KUBEDRILL_ROOT`; a `--root` or `KUBEDRILL_ROOT` that is not a checkout
  exits 1 with `root <dir> (from --root) lacks curriculum.yaml or
  scenarios/; point --root at the kubedrill checkout`.
- `<ref>` is a scenario id (`001-storefront-down`), its numeric prefix (`001`) or
  any id prefix that matches one scenario.
- `kubedrill <command> --help` prints a short description (two to eight lines) for
  `start`, `check`, `hint`, `diagnose`, `debrief`, `submit`, `replay`,
  `fault`, `up`, `namespace`, `node`, `path`, `concepts`, `lint`, `learn`,
  `solution`, `smoke` and `profile render`.

## Overview

| Command | Purpose | Needs cluster |
|---------|---------|---------------|
| **Browser workspace** | | |
| [`web`](#web) | Doctor, `up`, then the workspace on `127.0.0.1` | yes (creates it) |
| [`pack`](#pack) | Add, list or remove scenario packs from other repositories | no |
| **Learner loop** | | |
| [`start`](#start) | Start a scenario in a fresh namespace and print its ticket | yes (checked first) |
| [`ticket`](#ticket) | Print the ticket again | no |
| [`namespace`](#namespace) | Print the namespace of the active episode | no |
| [`check`](#check) | Grade once | yes |
| [`hint`](#hint) | Next hint of the ladder | no |
| [`diagnose`](#diagnose) | Record your one-line diagnosis | no |
| [`solution`](#solution) | Walkthrough; surrenders an active episode | no |
| [`reset`](#reset) | Start the active scenario again | yes |
| [`submit`](#submit) | Grade an exam once and end it | yes |
| [`debrief`](#debrief) | Debrief of an ended episode | no |
| **Progress** | | |
| [`list`](#list) | Scenarios with your times and status | no |
| [`path`](#path) | The ladder with your progress | no |
| [`stats`](#stats) | Solves, outcomes, verdicts, concepts | no |
| [`journal`](#journal) | Episodes, newest first | no |
| [`status`](#status) | Cluster, active episode, solved count | no (reads kind) |
| [`exam`](#exam) | Exam domains and coverage | no |
| [`learn`](#learn) | Concept pages of the active scenario | no |
| [`story`](#story) | The ticket told from zero for a newcomer | no |
| [`concepts`](#concepts) | Every concept page | no |
| [`replay`](#replay) | Play a recording of an episode | no (docker) |
| **Cluster and nodes** | | |
| [`kubedrill`](#kubedrill-without-a-command) | Doctor, `up`, then the browser workspace (`kubedrill web`) | yes (creates it) |
| [`doctor`](#doctor) | Check docker, kubectl and kind | no |
| [`up`](#up) | Create the cluster and install addons | yes (creates it) |
| [`down`](#down) | Delete the cluster | yes |
| [`fault`](#fault) | Inject a node fault | yes (docker) |
| [`heal`](#heal) | Undo every recorded fault | yes (docker) |
| [`node`](#node) | Root shell on a kind node | yes |
| **Authoring** | | |
| [`lint`](#lint) | Validate scenarios, curriculum and concept pages | no |
| [`new`](#new) | Scaffold a scenario | no |
| [`draft-guide`](#draft-guide) | Draft or check the guide of `simulation.yaml` | no |
| [`smoke`](#smoke) | Run every scenario end to end | yes |
| [`profile render`](#profile-render) | Render a kind profile to a file | no |
| [`version`](#version) | Version, revision and Go version | no |

## Browser workspace

### web

```
kubedrill web [--port 8642] [--no-open] [--preview <ref>] [--static-dir <dir>]
```

Runs the doctor (it stops when docker is missing), offers to recreate a
cluster built with an older audit policy when no episode is active (see
[kubedrill without a command](#kubedrill-without-a-command)), runs `up` with the base profile, prepares the workstation while
the cluster starts, then serves the workspace on `127.0.0.1:<port>` until
Ctrl-C and opens it in the browser. `kubedrill` without a command runs it
with the default port.

- It prints `kubedrill web is ready: http://127.0.0.1:<port>/?t=<token>`.
  The token is new on every run; the link stores it in an HttpOnly,
  SameSite=Strict cookie. A request without it gets `Open the link kubedrill
  web printed in your terminal; it carries the token of this run.` (401).
- The server answers only requests whose Host is `127.0.0.1`, `localhost` or
  `::1` (421 otherwise), requests that change state need the `X-Kubedrill:
  1` header, and the terminal websocket needs the page's own origin.
- `--no-open` or `KUBEDRILL_NO_BROWSER=1` prints the link without opening a
  browser (`open` on macOS, `xdg-open` on Linux). A busy port is an error
  that names `--port`.
- `--preview <ref>` is for authors: it skips the doctor and the cluster,
  opens the assignment at its meeting, prints `kubedrill web preview of
  <id>: <link>`, and reads the scenario files again on every request, so an
  edit to `simulation.yaml`, `story.md` or the ticket shows on reload. The
  page shows a preview banner; the practice and DIY runs still need the
  cluster.
- `--static-dir <dir>` serves the page files from `<dir>` (normally
  `internal/web/static`) instead of the copy in the binary, for front-end
  work; the top bar then links the component kit. A directory without
  `index.html` is an error.
- Inside the workstation it refuses to run.
- The page is dark by default and follows the system preference on the
  first visit; the toggle in the top bar switches and remembers the choice
  in the browser (`kd:theme`). The design system is described in
  `internal/web/static/README.md`.
- When the server stops answering, the page says `kubedrill web stopped
  answering. Run ./start.sh again; your progress is kept.` with a Retry
  link.

Screens:

| Screen | What it shows |
|--------|---------------|
| Welcome | The first visit of a journal: the five steps of the troubleshooting loop, each on a worked ticket with one pick and the reason the step exists, then the way into the workspace. Every other route redirects here until it is finished or skipped (`POST /api/onboarded`, stored in `web-progress.json`), then opens the route that was asked for; `--preview` skips it, and Home links it again as "How kubedrill works" |
| Home | A Continue card (the live run with its namespace and elapsed time, or the assignment `kubedrill list` recommends with its reason, or "all caught up"), the plans in progress, and links to the catalog, the exams and the GitHub repository. A profile line counts completed assignments, plan badges, reviews due and planned assignments |
| Catalog | Every level and plan, with search (title, id, plan, concept) and filters for level, status (not started, in progress, live, complete, mastered; complete means the Do it yourself run passed, the rule the plan rings use), exam domain (only domains with an assignment, with their count) and concept (by title). The filters live in the URL. A level with nothing written yet collapses to one line with its planned count. An assignment whose `requires` are not solved shows a lock and stays open |
| Exams | Each exam domain with its weight, the assignments that train it and how many are complete (Do it yourself passed, as in the plans) |
| Assignment | The stages below, with the concept pages as chips (each opens in a drawer) and the mastery meter with the clean solves and the distinct variants among them, for example `2 of 3 clean solves, 2 of 3 variants solved clean` |

An assignment walks through six stages, plus the exam rehearsal:

| Stage | What happens | Recorded |
|-------|--------------|----------|
| Meet the customer | A scripted conversation from `simulation.yaml`: the learner picks questions (shown in an order shuffled per question and stable across reloads; keys 1 to 9 follow the shown order), the customer answers, the mentor comments, and each best question adds a fact to the requirements board. A wrong pick can be retried | best questions on the first try |
| Check understanding | The quiz of `simulation.yaml`, one question at a time, choices shuffled as in the meeting, with an explanation for every answer | right on the first try |
| Learn the pieces | The object diagram of `simulation.yaml`, `story.md`, the ticket and the concept pages as tabs | read |
| Guided practice | `start` on the first variant, recorded `"guided": true`; the steps beside a terminal into the workstation, the live checks and a timer. A guided solve is never a clean solve | passed |
| Do it yourself | `start` on another variant at random, the ticket and the story (its fix section closed until the run ends or hint 4) instead of the steps, the mentor | passed |
| Exam rehearsal | `start` on a random variant in a time box, the ticket and a terminal, no hints, guide or live checks; one Submit grades it (see [Exam mode](#exam-mode)) | passed |
| Result | Assignment complete or not, the plan badge (locked until every assignment of the plan is complete), the stage results with redo links, and the mastery rule with the learner's numbers: clean solves in the last attempts, variants among the clean solves (`cleanVariants`, `2 of 3 variants solved clean`; the denominator is the variants mastery needs, at most 3) and variants solved in any run | - |

Launching a run:

- `POST /api/start` streams newline-delimited JSON: one `{"step": ...}`
  per setup step (`creating namespace shop-k3x9p`, `applying setup`), then
  `{"session": ...}`, or `{"error": ..., "status": n}`. The page lists the
  steps as they arrive.
- Launching while another episode is active says which one and links to
  it; confirming leaves it as `start --yes` does (recorded as failed).
- Below 960 pixels the launch panel warns that the lab needs a wider
  window.

In the lab:

- The side panel has tabs: Steps (practice only), Ticket and Story; the tab
  is remembered per episode.
- A step with `check` turns green when that check passes, and every step
  before it with it; the others have a Mark done button. Type it puts the
  step's command at the prompt without running it; Shift-click also runs
  it.
- The live checks poll every 5 seconds while the tab is visible, slow to 15
  seconds after a minute without change, and speed up again on terminal
  input. The footer shows `Checks n/m`, one dot per check and the first
  check that does not pass; the count opens the list of every check, and a
  dot or a list row opens a popover with its `why`, the expected value and
  the actual one. A spoiler check that did not pass carries neither value
  until the run ends; the API sends `expected and actual values hidden
  until the run ends` as its message instead.
- The terminal bar shows the time since the run started against the
  expected minutes (amber past them). A dropped terminal shows Reconnect,
  which opens a new container on the same namespace. Leaving the page
  while the terminal is connected asks first.
- The mentor opens as a panel beside the terminal, which refits to the
  narrower width (below 961 pixels it is a drawer over the page). It
  explains, before the first hint, that a hint never
  lowers the grade but takes the attempt out of the clean solves mastery
  counts; after it, the footer shows `<n> hints used`.
- A failing Validate lists every check with expected, actual and `why`; in
  a DIY run it adds that the cause may differ from the practice run. A passing
  one opens the debrief.
- A run that ends outside the page (`kubedrill check` passing in the
  terminal, a reset) sends the page to its debrief; reading the debrief of
  a solved run records its stage.

The debrief of an ended run is `kubedrill debrief` on the page (the API
refuses one of a running episode, so it never shows the walkthrough
early): the
duration against the expected minutes, the hints, the learner's diagnosis
next to the variant's `root_cause`, the trajectory from telemetry (verdict,
API reads before the first change, the first change and whether it hit
a fix target), and the two reflection questions plus "did your diagnosis
match", stored on the episode with `POST /api/debrief/<episode>` exactly as
the terminal stores them.

Keyboard (`?` lists the shortcuts of the current screen):

| Keys | Where | Action |
|------|-------|--------|
| `1` to `3` | welcome | pick an answer |
| `1` to `9` | meeting, quiz | pick an option |
| `Enter` | welcome, home, meeting, quiz, learn, launch, result | the screen's main action |
| `/` | catalog | focus the search |
| `c` | home | open the catalog |
| `Cmd`/`Ctrl` `Enter` | lab, debrief | validate (submit in an exam), save the debrief |
| `Cmd`/`Ctrl` `.` | lab | open the mentor |
| `Cmd`/`Ctrl` `=` and `-` | lab | terminal text size (remembered) |
| `Cmd`/`Ctrl` arrows | assignment | previous and next stage |
| `Esc` | anywhere | close the drawer, popover or dialog |

Single keys never fire while typing in a field or the terminal.

Storage:

- Each terminal is one `docker run --rm -it` of the workstation (`KUBEDRILL_WEB=1`,
  exam mode during an exam) in a pseudo-terminal on the host, named
  `kubedrill-web-<8 hex>`. Closing the tab kills the docker client and runs
  `docker rm -f` on the container.
- Stage progress lives in `web-progress.json` in the journal directory
  (`assignments` per scenario id, the stage of each episode the
  workspace started and `onboarded`, when the welcome flow was finished
  or skipped); the episodes, grades, hints, diagnoses, debriefs and
  telemetry are the journal's, as for every other command.
- The browser keeps per-assignment conveniences in `localStorage`: a
  meeting or quiz in progress, the guide steps marked done, the side tab,
  the terminal text size and the theme. Clearing them loses nothing graded.

## Learner loop

### start

```
kubedrill start <ref> [--seed N] [--yes|-y]
```

Creates the episode namespace, applies the setup, runs `setup/hook.sh`,
records the episode and prints the ticket. The browser workspace starts its
practice, DIY and exam runs the same way; `start` is that step from a
terminal, for example the one in the page.

Flags:

- `--seed N`: seed for the variant (0 or omitted: random).
- `--yes`, `-y`: leave an active episode without confirmation.

Namespace and progress:

- The namespace is `<scenario namespace>-<5 random characters>`.
- Every other episode namespace of this journal except the active
  episode's is deleted without waiting: the safety net behind the delete at
  the end of each episode. Namespaces of other journals are not touched.
- Progress goes to stderr as `creating namespace <ns>... done (1.2s)`.
- The output of `setup/hook.sh` is not printed; when the hook fails, the
  error ends with `its output ends with:` and the last 4 KiB of it.
- A step under 0.1 s ends in `done`; a longer one ends in `done (1.2s)`.

Over an active episode:

- It asks `Leave the active episode of <id>? It will be recorded as failed.
  [y/N]`, or `Leave the exam of <id>? It will be recorded as failed. [y/N]`
  for an exam; `--yes` skips the question.
- It first heals the recorded faults, one progress line `healed <action> on
  <node>... done` each.
- Then it prints `leaving the episode of <id> (recorded as failed)` before
  the other progress lines.
- `kept the episode of <id> active` follows when the start, the heal
  included, fails before the episode is left.

Telemetry notices (stderr):

- When the cluster answers that the audit log is off it prints `telemetry:
  audit log off; recreate the cluster to enable it (kubedrill down --yes &&
  kubedrill up)`.
- That notice prints once per journal and once more per recreated cluster.
  The marker `<journal dir>/.telemetry-notice` records the creation time of
  the cluster it was shown for (`cluster.Manager.Created`, the creation time
  of the control plane container).
- Nothing is printed when the audit log is on, or when the question fails or
  takes over 5 s.
- In the same way, with the marker `<journal dir>/.audit-policy-notice`, it
  prints `telemetry: the audit policy changed since the cluster was created;
  recreate it to apply (kubedrill down --yes && kubedrill up)` when the
  running cluster's audit policy differs from `cluster/audit-policy.yaml`.

Before the ticket:

- After `started <id> (seed N) in namespace <ns>` it prints `Last time you
  said you would check first: <line>` when a debrief of a scenario sharing a
  concept recorded one (not in exam mode).
- On a terminal, outside exam mode, the first time you meet a concept it asks
  `First time with <concepts>. See a worked example from another variant
  before your ticket? [y/N]`.
- `y` prints `Worked example (variant <name>, not yours)`, then only the `##
  Symptom` and `## Candidate causes` sections of a sibling variant's
  walkthrough, rendered in the throwaway namespace `worked-example`, then the
  line `Observation, change and verify are yours; the method is in kubedrill
  learn troubleshooting-loop`.
- The `## Change` and `## Verify` sections are never shown.
- An empty answer (for example one typed before the prompt and lost) prints
  `worked example skipped`.

The ticket:

- It prints the ticket, the Fundamentals line, the footer and the next
  steps; an exam leaves out the Fundamentals line.
- On the host it ends with `Practice it in the browser with a terminal next
  to the ticket: kubedrill web`.

### ticket

```
kubedrill ticket
```

Prints the ticket of the active episode again, with its namespace and the
elapsed time; the page shows it beside the terminal. It allows exam mode.

### namespace

```
kubedrill namespace
```

Prints the namespace of the active episode, one line, for prompts and
scripts; the workstation entrypoint and bashrc use it.

- Without an active episode: exit 1 with the no-episode message.

### check

```
kubedrill check [--explain]
```

Grades once, prints one line per check and records the grade.

- Each line shows `expected: X  actual: Y`; a pair wider than 78 columns puts
  `actual: Y` on its own line.
- Under every failed or errored check it prints `why: <text>`, the check's
  `why` from `checks.yaml`, wrapped at 78 columns.
- The last line is `N of M checks passed (weighted score S)`.
- A check marked `spoiler` in `checks.yaml` (its values name the root cause
  or the fix, an image reference or the regex it must match for example)
  prints `expected and actual values hidden until the run ends` in place of
  the expected/actual line while it fails; hints and a diagnosis do not
  reveal it. `check --explain` writes its assertion as `has the expected
  value (expected and actual values hidden until the run ends)`.
- When all pass, in this order: the solved banner (`Solved: <title> in 4.1
  min with 0 hints (variant <name>)`), the `Recommended:` lines, `Next:
  kubedrill solution <id> (compare with the reference) | kubedrill list` and
  the debrief (see [Diagnosis and debrief](#diagnosis-and-debrief)).
- A pass ends the episode and deletes its namespace without waiting, as
  `solution`, `submit`, an expired exam and another `start` do; the debrief
  reads the journal, not the namespace.
- When another terminal ended the episode meanwhile, it prints the grade and
  `the episode of <id> already ended as <outcome> in another terminal;
  kubedrill debrief shows it`.
- A missing or foreign scenario namespace is one error line, printed once:
  `kubedrill: namespace <ns> is missing; it was deleted or belongs to another
  episode; run: kubedrill reset`, exit 1.
- In exam mode it refuses with `check is not available in exam mode;
  kubedrill submit grades once`; `--explain` still works.

`--explain` lists every check, what it asserts and why, and grades nothing:

- Header: `<id> is checked by N checks; kubedrill check grades them, this
  list grades nothing` (`kubedrill submit grades them once` in exam mode).
- For every check: `N. <id>: <description>`, then `asserts: <subject>
  <validators>` built from the check fields, for example `asserts:
  Deployment storefront in namespace shop-ab12c: {.status.readyReplicas}
  equals "3"`. The namespace part appears when the check names one, which a
  rendered check always does.
- Then, when they apply: `runs after <ids>`, `critical: if it does not pass
  (failed, errored or skipped) the score is 0` and `why: ...`.

### hint

```
kubedrill hint
```

Prints the next unseen hint as `hint N of M (<rung>)` and records its reveal
time.

- The rungs are `where to look`, `what to observe`, `what it means`, `what
  to change`.
- The last hint needs a diagnosis: without one it prints `the last hint
  needs your diagnosis first: kubedrill diagnose "<what you think is
  wrong>"` and exits 1.
- When the previous hint was revealed less than 10 seconds ago it prints
  `tip: hints are spaced; try one more observation before the next one` on
  stderr and still reveals. It is a timer, not a check of what you ran.
- Once exhausted it prints `all N hints shown. Next: kubedrill solution
  (surrender) or keep investigating` and exits 0.
- Exam mode: refused like `check`.

### diagnose

```
kubedrill diagnose "<text>"
```

Records your one-line diagnosis (10 to 300 characters) on the active
episode, replacing an earlier one. The last hint and the practice solution
wait for it; the debrief compares it with the variant's root cause.

- Prints `diagnosis recorded for <id>: <text>`, then `Next: change one
  thing, then kubedrill check | kubedrill hint`.
- In exam mode it is allowed and the second line is `Next: change one thing,
  then kubedrill submit`.

### solution

```
kubedrill solution [ref] [--yes|-y] [--approach <name>]
```

Prints the walkthrough, rendered for the episode's seed and namespace.

- `--yes`, `-y`: skip the confirmation.
- `--approach <name>`: print one approach; see below.

Active practice episode:

- It needs a diagnosis, `--yes` included: `the solution needs your diagnosis
  first (--yes skips only the confirmation): kubedrill diagnose "<cause>"
  then kubedrill solution --yes`, exit 1.
- Then it confirms, records the episode as abandoned and prints the
  walkthrough.
- Exam mode refuses before the deadline.

No active episode:

- It prints the walkthrough of `<ref>`, or of the most recent finished
  episode when `<ref>` is omitted.
- A scenario never attempted asks `Scenario <id> was never solved or
  abandoned. Show the solution anyway? [y/N]`.
- After a solved exam it asks `Your latest episode of <id> was an exam
  (solved); show the solution? [y/N]`.
- When the latest episode was an unsolved exam or ended neither solved nor
  abandoned it asks `Your latest episode of <id> ended <outcome>; show the
  solution anyway? [y/N]`.

After a solve:

- It adds `Your path vs the reference`: your kubectl commands from the shell
  log, numbered, next to the commands of the `## Change` and `## Verify`
  code blocks.
- With none recorded it prints `Your path: no kubectl commands recorded
  (telemetry: <source>)` and `commands typed outside the workstation are not
  logged; the terminal of kubedrill web records them`.
- When you already solved, before this episode, another scenario sharing a
  concept or another variant of this scenario, a terminal first shows the
  text up to `## Observation that splits them` and that heading, rendered
  like the rest; the section itself stays hidden.
- It then asks `Which principle made this observation necessary? (one
  line)`, stores the answer in the debrief (`principle`) and reveals the
  section.

Approaches:

- A walkthrough shown without surrendering (no active episode of that
  scenario) ends with `Approaches (kubedrill solution --approach <name>
  prints one)`: each name with its snippet, rendered for the episode,
  namespace included.
- The surrender of an active episode does not list them.
- `--approach <name>` prints `Approach <name> (<id>)`, the rendered snippet
  indented, then the rendered `content.md`.

### reset

```
kubedrill reset [--same-seed]
```

Starts the active scenario again in a new episode namespace with a new seed.

- `--same-seed`: keep the current seed and variant, printed as `(same
  seed)`.
- The previous episode is recorded as failed and hints go back to 0.
- During an exam, before its deadline: `reset is not available in exam
  mode; kubedrill submit ends the exam`, exit 1.

### submit

```
kubedrill submit
```

Exam mode only: grades once, prints every check, records the outcome and
ends the exam.

- The outcome line is `exam submitted: solved in 12.1 min` or `failed`.
- Exit 2 when a check fails.
- A solved exam is followed by the banner and the debrief.
- On a practice episode: `submit grades exam episodes (the exam rehearsal of
  kubedrill web); a practice episode is graded with kubedrill check`, exit 1.

### debrief

```
kubedrill debrief [episode-id] [--redo]
```

The debrief of the most recent ended episode, or of the one named by id or
id prefix from `kubedrill journal`. The content is under
[Diagnosis and debrief](#diagnosis-and-debrief).

- On a terminal it asks the questions when none are recorded.
- `--redo` asks them again; each prompt shows the previous answer in
  brackets and Enter keeps it. `--redo` without a terminal: exit 1.
- An id prefix matching several episodes: `episode "<id>" is ambiguous (N
  matches); kubedrill journal lists them`, exit 1.
- The active episode: `episode <id> is still active; the debrief comes after
  kubedrill check passes or the episode ends`, exit 1.

## Progress

### list

```
kubedrill list [--level L0]
```

Columns `ID TITLE LEVEL TYPE MIN YOURS LAST4 STATUS`.

- `--level`: only this level, for example `L0`.
- `MIN`: the expected minutes.
- `YOURS`: your median solve in minutes with one decimal (`0.8 min`). This
  is the unit of every median and duration in `list`, `stats`, the ticket
  footer and the banners.
- `LAST4`: clean solves among your last attempts, at most 4, for example
  `3/4`; `-` without attempts.
- `STATUS`: `todo`, `active`, `solved` or `mastered`.
- On a terminal under 100 columns (width as in
  [Markdown rendering](#markdown-rendering)) the table drops the `TYPE`
  column and cuts titles with `...` so the widest row fits; the title column
  keeps at least 12 columns.
- The legend under the table ends with `clean = solved with 0 hints within
  MIN`. On a terminal the legend and the `Next:` line wrap to its width with
  a two-space hanging indent.
- Below it, with no active episode, the recommendation (see
  [Next scenario](#next-scenario)).
- With an active episode: `Active: <id> (seed N); continue with: kubedrill
  check` (`kubedrill submit` for an exam).

### path

```
kubedrill path
```

The ladder from `curriculum.yaml`: levels, hours, and modules with planned,
existing and solved scenario counts. A guided practice run of the browser
workspace does not make a scenario solved here.

- A module line wider than the terminal wraps under the text after the
  module id, the same hanging indent as the concepts line.
- Each module has a `concepts:` line with the mastery of each concept it
  lists, for example `concepts: pod-lifecycle mastered, image-pull new,
  readiness-and-endpoints 2/3 clean`, wrapped to the terminal width under
  the text after `concepts: `.
- It ends with the recommendation (see [Next scenario](#next-scenario)).
- While an episode is active it ends with `Active: <id> (seed N); the
  recommendation returns once it ends` instead.

### stats

```
kubedrill stats
```

Tables, no streaks:

- `Solves by level`: `LEVEL ATTEMPTS SOLVED`, then `median solve` (`3.0
  min`) and `hints per solve` (one decimal, `0.3`).
- `Outcomes`: `OUTCOME EPISODES`.
- `Verdicts`: `VERDICT SOLVES`, solved episodes only.
- `Concepts`: `CONCEPT ATTEMPTS SOLVED LAST4 SESSIONS MASTERED`.
- A legend follows, wrapped to the terminal width: `LAST4` clean solves
  among the last 4 attempts, `SESSIONS` the sittings among those clean
  solves split by a 3 h gap, `MASTERED` 3 clean of the last 4 on distinct
  seeds over 2 sessions.

### journal

```
kubedrill journal [--scenario <ref>] [--last 20]
```

Table of episodes, newest first.

- `--scenario <ref>`: only episodes of this scenario.
- `--last N`: number of episodes to show, default 20; `--last 0` shows all;
  a negative value is `--last must be >= 0, got -1`, exit 1.
- Columns: started, scenario, seed, mode, variant, outcome, duration, hints.
  Outcomes print as words: `timed_out` in the journal file is `timed out`.
- Mode is `practice`, `review` (a practice episode on a review seed) or
  `exam`.
- Variant is `-` while the episode is active, with the footer `- : hidden
  while the episode is active`.
- Duration is in minutes with one decimal (`12.5 min`); under 0.1 min it
  prints `0.1 min`.
- Corrupt journal lines are reported on stderr.

### status

```
kubedrill status
```

- Cluster `present` or `missing`; `present (inside the workstation)` when
  `KUBEDRILL_WORKSTATION=1`.
- It asks kind whether the cluster exists and prints `unknown` when docker
  is unreachable.
- The active episode with seed, mode, elapsed time and hints.
- `solved: X of Y scenarios (mastered: Z)`.
- The journal directory.

### exam

```
kubedrill exam <CKA|CKAD|CKS>
```

Exam domains with weight and `DONE/TOTAL`.

- `DONE` counts the scenarios with a solve outside the guided practice of
  the browser workspace (a DIY run, an exam or a terminal `start`); the
  page's exam coverage reads the same count.
- A domain without scenarios shows `0/0`.
- The argument is case-insensitive.

### learn

```
kubedrill learn [id]
```

- Without `id`: `Fundamentals of <title> (<id>)`, then, for each concept the
  active scenario lists, its `id  Title` header line and the rendered page.
- With `id`: that header line and page only, no summary.
- Unknown id: `concept "x" not found; see kubedrill concepts`.
- No active episode and no id: exit 1 with `no active episode; kubedrill
  learn <id> prints one concept: <id>, <id>, ...`.
- In exam mode, with or without an id: `learn is not available in exam mode;
  kubedrill submit grades once`, exit 1.

### story

```
kubedrill story [ref]
```

- Without `ref`: `Story of <title> (<id>)`, then the rendered `story.md` of
  the active episode's scenario, with the episode namespace.
- With `ref`: the story of that scenario, rendered with the active episode
  when it runs that scenario, with seed 1 and the scenario namespace
  otherwise.
- While an episode of that scenario runs, `## The fix and why it works`
  keeps its heading but its body reads `This section opens when the run
  ends, after the fourth hint, or in the guided practice.` It opens when
  the episode is a guided practice run of the browser workspace or the
  learner revealed hint 4 (`what to change`) of a four-rung ladder. The
  page's Story (the assignment and the active run) follows the same rule.
- A scenario without `story.md`: `<id> has no story yet; kubedrill learn
  prints its fundamentals`, exit 0.
- No active episode and no ref: exit 1 with `no active episode; kubedrill
  story <id> prints the story of one scenario`.
- In exam mode, with or without a ref: `story is not available in exam mode;
  kubedrill submit grades once`, exit 1.
- It reads no cluster state.

### concepts

```
kubedrill concepts
```

Table of every page under `docs/concepts/`.

- Columns `ID`, `TITLE` and `SCENARIOS`, the number of scenarios whose
  `scenario.toml` lists the concept.
- Each row is followed by the summary, wrapped on indented lines at 78
  columns.

### replay

```
kubedrill replay [episode-id]
```

Plays the latest asciinema recording of the most recent episode, or of the
one named (the active one included), inside the workstation image.

- An ambiguous prefix fails as in `debrief`.
- The recording is the last one the episode telemetry lists, else the newest
  `<journal dir>/recordings/<namespace>-<unix>.cast` within the episode
  window.
- A recorded path is read by its file name under `<journal
  dir>/recordings/`, so a path written inside the workstation
  (`/data/recordings/...`) plays on the host.
- Prints one line, `replaying <file> (space pause, q quit)`.
- Without a recording: `episode <id> (<scenario>) has no recording; ...`,
  exit 1.
- Needs a terminal and docker.

## Cluster and nodes

### kubedrill without a command

```
kubedrill
```

Runs [`kubedrill web`](#web) on the default port. Its setup, before the
server starts:

- On a terminal, outside the workstation, when the cluster exists, was
  created with an older audit policy and no episode is active, it prints
  `the cluster was created with an older audit policy, so the debrief
  misses part of what you change; no episode is active, so recreating it
  loses nothing and takes about 2 minutes` and asks `Recreate the cluster
  now? [Y/n]`. Enter, `y` or `yes` deletes the cluster (`cluster kubedrill
  deleted`) and `up` creates it again; any other answer prints `kept the
  cluster; kubedrill down --yes && kubedrill up recreates it`, and the
  question is not asked again for that cluster (`.recreate-offer` in the
  journal directory).
- While `up` runs, it builds the workstation image and the linux binary in
  the background and prints their output afterwards, behind a `finishing
  the workstation build` progress line when they are still running. A
  failure there is a `warning: ...; kubedrill tries again when the page
  opens a terminal` on stderr.

### doctor

```
kubedrill doctor
```

Table of docker, kubectl, kind and, on a Linux host, inotify: found,
version and detail (`-` when there is none). Exits 1 when docker is
missing.

- docker: the detail warns when Docker has less than 3 GiB of memory
  (`<n> GiB of memory, below the 3 GiB the cluster needs; raise it in Docker
  Desktop, Settings > Resources`); the idle cluster uses about 1.3 GiB.
- kubectl is optional: the workstation has its own. When it is missing the
  detail reads `optional, the workstation has its own; for your own
  terminal, smoke and scenario hooks: <url>`.
- inotify (Linux only): `fs.inotify.max_user_instances` and
  `max_user_watches`; below 512 and 524288, the limits kind documents for
  multi-node clusters, the detail gives the `sudo sysctl` command that
  raises them.

### up

```
kubedrill up [--profile base] [--addons a,b]
```

Creates the cluster when it is missing, installs the addons, then preloads
images on every node.

- `--profile`: kind profile under `cluster/profiles`, default `base`.
- `--addons`: comma-separated addons to install besides metrics-server, for
  example `gateway,kwok`.

Cluster:

- Prints `present` when the cluster exists, after the same readiness wait
  as a new cluster: an `up` started while another one is still creating
  the cluster does not continue early. A stopped node container (for
  example after a Docker restart) fails at once with the `docker start`
  command that brings it back. A removed node container (a `docker rm`
  that took one node) fails at once with `node containers missing: ...`
  and the `kubedrill down --yes && kubedrill up` remedy.
- When `up` has to create the cluster (missing, or every node container
  removed), it also drops the fault records of the old one.
- kind always uses the docker provider, the same runtime every other
  kubedrill call uses, even when podman or nerdctl is installed.
- Otherwise it creates it from `cluster/profiles/<profile>.yaml` (about 2
  minutes), then prints `waiting up to 2m0s for the nodes to be Ready...`
  and waits. A second `up` that loses the race to create the same cluster
  waits the same way instead of continuing early.
- Ctrl-C cancels, but kind cannot be interrupted while it creates or
  deletes nodes: the command stops once kind returns. A second Ctrl-C ends
  the process at once; `kubedrill down --yes` then removes a half-created
  cluster.
- The API audit log is written to `<state dir>/audit/audit.log`
  (`cluster.AuditDir()`), with the policy `cluster/audit-policy.yaml`.
- The state directory is `$XDG_STATE_HOME/kubedrill/kubedrill`, default
  `~/.local/state/kubedrill/kubedrill`, created with mode 0700.
- On an existing cluster it still runs the idempotent ensure step: the
  kubeconfig context check and the best-effort audit log permissions.
- Preload: prints `preloading N images on the nodes`, then pulls the node
  debug image and every scenario image (`images` in `scenario.toml`, probe
  images and setup images pinned by digest) into each node's containerd. A
  `name:tag@sha256:...` image is also tagged `name:tag`, so a fix that
  writes only the tag needs no registry. Each node's image store is listed
  first (`ctr --namespace k8s.io images ls --quiet`) and an image it already
  holds under the digest and the tag is skipped, so `up` on an existing
  cluster pulls only what changed; an image named without a tag or a digest
  is always pulled. A failed pull is a `warning:` on stderr; the node then
  pulls the image when a pod first needs it.
- A changed profile or `cluster/audit-policy.yaml` applies only to a new
  cluster: `kubedrill down --yes && kubedrill up`.
- When the running cluster's audit policy differs from the repository file,
  `up` prints `telemetry: the audit policy changed since the cluster was
  created; recreate it to apply (kubedrill down --yes && kubedrill up)` on
  stderr every time. The comparison (`cluster.Manager.AuditPolicyStale`)
  uses the sha256 recorded at creation in `<state
  dir>/audit-policy.sha256`, or the file inside the control plane for an
  older cluster.

Addons:

- It installs the base addons (`metrics-server`) and every addon `--addons`
  names from `cluster/addons/` (`gateway`, `kwok`).
- While an addon's Deployments become Available the cluster manager prints
  `waiting for <addon>: <deployment>...`, then `<addon> ready (12s)`.
- Then it prints `addons: metrics-server, gateway`.
- While an aggregated API is not served yet it prints `waiting for API
  services: ...`, then `API services available`.
- Then it pulls the node debugging image (pinned busybox) on every node
  with `crictl`, so `kubedrill node` inside the workstation needs no
  registry. A failed pull is the line `warning: pull ...; kubedrill node
  pulls it on first use inside the workstation`, and `up` still succeeds.
- An unknown name is `unknown addon(s) x; valid: ...`, exit 1.

### down

```
kubedrill down [--yes|-y]
```

Deletes the kind cluster after confirmation.

- `--yes`, `-y`: skip the confirmation.
- A declined confirmation prints `kept the cluster` and exits 0.
- It also removes what described the deleted cluster from the state
  directory: the audit policy record, `kubeconfig-internal`,
  `kubeconfig-scripts` and the fault records in `faults.json`, so the next
  `kubedrill fault` on a new cluster is not refused as already active.

### fault

```
kubedrill fault <pause|unpause|stop-kubelet|start-kubelet|netem|clear-netem> <control-plane|worker|worker2> [--delay 200ms] [--loss 5]
```

A tool for scenario hooks and practice: it freezes a node container
(`pause`), stops its kubelet (`stop-kubelet`) or adds `tc netem` delay and
loss on its network (`netem`). The `un`, `start` and `clear` actions undo
one.

- `--delay`: netem added latency, for example `200ms`; must be 0 or more
  (`--delay must be >= 0, got -1s`).
- `--loss`: netem packet loss in percent, for example `5`; must be between 0
  and 100 (`--loss must be between 0 and 100, got 120`).
- `netem` needs one of `--delay` or `--loss`, and replaces the previous
  parameters.
- The actions are `faults.Actions()`. Another word is `unknown action
  "bogus"; valid: pause, unpause, stop-kubelet, start-kubelet, netem,
  clear-netem`, exit 1.
- A wrong argument count prints the usage.
- Success prints `fault <action> applied to <node>; kubedrill heal undoes
  every recorded fault`.
- A call cut short by Ctrl-C or the 30 s docker timeout keeps the record,
  because Docker may still apply the fault: `fault <action> on <node>: <err>;
  the fault may still apply, so its record is kept: run kubedrill heal`.
- Repeating an active fault is `<node> is already paused` (`<node> already
  has its kubelet stopped`), exit 1.
- An unknown node is `unknown node "x"; valid: control-plane, worker,
  worker2`, exit 1, whether the CLI or the fault manager notices it.
- Faults are recorded in `<state dir>/faults.json`, the cluster state
  directory, shared by every journal and by `smoke`, since they belong to the
  one cluster.
- Every `start` heals them first.
- Not available inside the workstation.

### heal

```
kubedrill heal
```

Undoes every recorded fault and prints one line `healed <action> on <node>`
each, or `no recorded faults`.

### node

```
kubedrill node <control-plane|worker|worker2>
```

Opens a root shell on the named kind node. Changes made there persist
until `kubedrill down`.

- On the host it runs `docker exec` into the kind node container
  `kubedrill-<node>` and first prints `kubedrill node: root on
  kubedrill-<node> (kind node; changes persist until kubedrill down)`.
- Inside the workstation, which has no docker, it runs `kubectl debug
  node/kubedrill-<node> --profile=sysadmin` and chroots into the node
  filesystem at `/host`, the way you reach a node on a real cluster
  without ssh. It first prints `kubedrill node: root on kubedrill-<node>
  through kubectl debug node/kubedrill-<node> --profile=sysadmin -- chroot
  /host (changes persist until kubedrill down)`. The debugging pod runs
  in the `kubedrill-debug` namespace, which the checks and the debrief do
  not read, and is deleted when the shell ends.
- In the workstation, `ssh control-plane`, `ssh worker` and `ssh worker2`
  (also `ssh kubedrill-worker` or `ssh root@worker`) run `kubedrill node`,
  as `ssh <node>` does in the exam. Every other `ssh` is the real client.
- Any other argument is the usage error `usage: kubedrill node
  <control-plane|worker|worker2> (kubectl get nodes lists them as
  kubedrill-<node>)`.

## Workstation

The terminal of the browser workspace is the containerized workstation of
`internal/workstation` ([workstation.md](workstation.md)): a docker image
with `kubectl`, `helm`, `k9s` and `stern`, plus a Linux build of `kubedrill`
mounted in.

For each terminal the page opens, `kubedrill web`:

1. Writes `<cluster state dir>/kubeconfig-internal`: the cluster context
   rewritten to the address reachable from inside docker. It sits outside
   the journal, so the read-write `/data` mount cannot rewrite it.
2. Calls `workstation.ShellCommand`, which builds the binary when the
   sources are newer and the image when missing, and runs the container in
   a pseudo-terminal on the host, in exam mode during an exam.

Inside the container:

- `KUBEDRILL_WORKSTATION=1` is set and `KUBECONFIG` points at the internal
  kubeconfig, which holds a single context.
- `kubedrill` there hands that path unchanged to scenario scripts and
  connects with it directly, so `check`, `hint` and `solution` grade the
  same episode as the page.
- `start` inside it prints the ticket without the `kubedrill web` line.

## Packs

### pack

```
kubedrill pack add <directory|git url> [--ref <tag|branch|commit>] [--allow-privileged] [--yes|-y]
kubedrill pack list
kubedrill pack remove <name> [--yes|-y]
```

A pack is a directory or git repository with a `pack.yaml` and a
`scenarios/<level>/<id>/` tree in the format of this repository
([scenario-format.md](scenario-format.md#packs)). `pack add`:

1. Copies a directory, or clones a git url (`--ref` pins a tag, branch or
   commit; the default is the default branch head) into a staging
   directory under `<journal>/packs/` and removes `.git`; the commit is
   recorded.
2. Refuses a clone that holds anything but directories and regular files
   (a symlink could point outside the pinned commit), and a pack whose name
   is already enabled.
3. Loads `pack.yaml` and every scenario, and lints them as `kubedrill
   lint` would (format, sandbox rules, concepts and exam domains that
   exist, every scenario listed in a module and every listed scenario
   present). Refused: a scenario id already in the catalog or planned in
   `curriculum.yaml`, a scenario number already used (`kubedrill start
   NNN` would be ambiguous), a module id already in the ladder, and
   `allow_privileged = true` without `--allow-privileged`.
4. Prints the scenarios, every `*.sh` of the pack and every check with an
   inline `script:`: all of them run on this machine when a scenario is
   set up, solved or checked. Read them.
5. Asks `Enable pack <name>? [y/N]` (`--yes` skips), then moves the pack to
   `<journal>/packs/<name>` with a `.kubedrill-pack.json` (source, ref,
   commit, added_at). On no, or on any problem, the staging directory is
   removed.

An enabled pack's modules join the ladder under the level each one names,
tagged with the pack name in `kubedrill path` and the workspace catalog; its
scenarios appear in `kubedrill list`, `start`, `smoke` and the exam
coverage like the repository's own, except `kubedrill smoke` without
arguments, which runs only the repository's scenarios (name a pack
scenario to smoke it). If a pack stops loading (a newer kubedrill, an id
the repository later takes), every command warns and leaves the packs out
until `pack remove`. `pack list` prints name, title,
scenario count and the pin; `pack remove` deletes the files and keeps the
episodes in the journal. Packs live in the journal directory, so the
workstation container sees them at `/data/packs`.

## Authoring

### lint

```
kubedrill lint
```

Runs `scenario.Lint` over `scenarios/` and prints every problem.

- It passes the module ids of `curriculum.yaml` and the concept ids of
  `docs/concepts/`.
- It adds the curriculum validation and the concept page validation.
- It checks that every concept a module names has a page: `curriculum.yaml:
  concept "x" has no page in the concept library`.

### new

```
kubedrill new <level> <id>
```

Scaffolds `scenarios/<level>/<id>/`, for example `new L0 001-my-slug`, and
prints the next steps. The scaffold includes a `simulation.yaml` with
placeholder text; lint refuses its template marker until it is edited, and
a lab-only scenario deletes the file.

### draft-guide

```
kubedrill draft-guide <ref> [--check]
```

Prints a `guide:` block for `simulation.yaml`: one step per command of the
`## Observation that splits them`, `## Change` and `## Verify` sections of
`solution/README.md`, rendered for the first variant (the one the guided
practice runs), without the `-n <namespace>` flag the workstation does not
need. Titles read `Observe: kubectl get pods`, `Change one thing: ...`,
`Verify: ...`; bodies are `TODO` placeholders.

- `--check` compares the existing guide with the walkthrough instead and
  prints one line per guide command whose kubectl verb and resource the
  walkthrough never runs, or `<id>: every guide command appears in the
  solution walkthrough`. It is advice and always exits 0.

### smoke

```
kubedrill smoke [refs...] [--force] [--no-shortcuts] [--verbose|-v]
```

For each scenario and each variant seed: reset, assert the checks fail, run
`solution/solve.sh`, assert they pass. Then, for each `shortcuts/<name>.sh`
that applies to the variant ([scenario-format.md](scenario-format.md#shortcuts)):
reset again, run the shortcut, assert that at least one check does not pass.

- Each run prints one line ending with its duration: `ok   <id> seed <n>
  (variant <v>)` for the solve run, `ok   <id> seed <n> (variant <v>)
  shortcut <name>: still failing <check ids>` for a shortcut, `FAIL ...:
  <reason>` otherwise. The last line is `smoke: <n> runs passed in
  <duration>`, or the error `smoke: <k> of <n> runs failed in <duration>`.
- A shortcut that passes every check fails with `shortcut <name> passed
  every check: <why>`; a shortcut script that exits non-zero fails with
  `shortcut <name>: <error>` and its output.
- `--no-shortcuts`: skip the shortcut runs; only the setup and `solve.sh`
  proofs run. CI runs the shortcuts.

- Episodes go to a throwaway journal and to namespaces of their own
  (scenario namespace plus a random suffix), so the learner's episode is
  untouched.
- It still refuses a scenario the learner has an active episode of, because
  setup hooks may change cluster-wide state: `an active episode of <id> runs
  in namespace <ns>; setup hooks may change cluster-wide state, so finish it
  (kubedrill reset, or kubedrill diagnose "<cause>" then kubedrill solution
  --yes) or pass --force`.
- An episode from an older build without a namespace prints `runs since
  <date time>` in that message.
- `--force`: run even when the learner has an active episode of one of the
  scenarios.
- It deletes every namespace it created at the end, also after Ctrl-C or a
  failed reset: the cleanup runs detached from the cancelled command for up
  to 30 s and also sweeps the namespaces labeled with its throwaway
  journal.
- `solve.sh` and shortcut output is shown for failing runs, or for every
  run with `-v`.

### profile render

```
kubedrill profile render [--profile base] --out <file>
```

For authoring and CI: renders `cluster/profiles/<profile>.yaml` with the data
`up` passes and writes the kind configuration to `--out`. CI renders the
smoke cluster with it.

- The data is `AuditDir`, the audit directory under the cluster state
  directory, created with mode 0700, and `AuditPolicy`,
  `cluster/audit-policy.yaml`; both are quoted by the template's `printf
  "%q"`.
- It prints `rendered profile <profile> to <file>`.
- A missing template key, a missing audit policy or a result that still
  holds `{{` is an error, exit 1.

### version

```
kubedrill version
```

Prints the module version, the VCS revision (or `revision unknown (built
from an unversioned tree)`) and the Go version, then `source:
https://github.com/cauebraz/kubedrill`.

## Next scenario

`list` and `path` print the recommendation of `progress.Next`:

- The line is `Next: <title> (<id>, <level>, <minutes> min): <reason>`,
  followed by the command `kubedrill start <id>`.
- The solved banner prints it on its second line as `Recommended: <title>
  (<id>, <level>, <minutes> min): <reason>`, followed by the command and
  `Next: kubedrill solution <id> (compare with the reference) | kubedrill
  list`.

The reason is one of these sentences:

- `you solved 1 of your last 2 attempts at L0`.
- After one attempt: `you solved your last attempt at L0` or `you did not
  solve your last attempt at L0`.
- `you have not tried it yet; it is the next one at L0`.
- `review: you mastered <concept> 4 days ago`. A spaced review of a mastered
  concept adds `--seed <seed>`; review seeds come from a range practice never
  uses.

Edge cases:

- When every scenario is mastered and no review is due, `list` says so.
- `path` hides the recommendation while an episode is active.

## Diagnosis and debrief

`kubedrill diagnose "<text>"` commits to a cause (see [diagnose](#diagnose)).
After a solve (`check`, `submit`, or the page) the debrief prints:

```
Debrief: <id> (episode <episode id>, solved)
  duration 4.1 min, 0 hints
  API reads before the first write: 5 (telemetry: audit+shell)
  first write: apps/deployments/storefront (a fix target)
  verdict: root-cause
  your diagnosis: <your text>
  root cause: <root_cause of the variant>
```

The summary block:

- `API reads before the first write` counts the learner's read requests in
  the audit log, not shell commands: one `kubectl describe` makes several
  (the object, its events), so the count runs higher than the commands
  typed.
- The first write is compared with `fix_targets` in `scenario.toml` through
  `telemetry.MatchTarget`; case and an empty core group do not matter.
- `not a fix target; the fix targets are ...` names a write that only
  touched a symptom.
- Without telemetry the block says `telemetry: none recorded (no audit log
  or shell log for this episode)` in place of the API reads, first write
  and verdict lines.
- With the audit log off the verdict reads `verdict: unknown (telemetry
  off)`.
- Every debrief line wraps to the terminal width with a hanging indent.

The questions, on a terminal:

1. `Did your diagnosis match? [y/n]`, only with a diagnosis.
2. `Which observation would have ruled out your first hypothesis earlier?
   (enter to skip)`.
3. `Next time I check first: (enter to skip)`.

After the questions:

- The answers are stored in the journal and it prints `debrief recorded;
  kubedrill debrief shows it again`.
- When neither question got an answer it prints `kubedrill debrief --redo
  answers the questions later`.
- With a previous answer (`debrief --redo`) each prompt shows it in
  brackets, and Enter keeps it. The match question shows `[y]` or `[n]`, the
  letters it accepts; the others show the text, for example `Next time I
  check first: [kubectl describe pod]`.
- Without a terminal only the summary is printed, followed by `kubedrill
  debrief --redo answers the questions later`.
- The "next time I check first" line comes back at the next `start` of a
  scenario that shares a concept.

## Exam mode

The exam rehearsal is a stage of every assignment in `kubedrill web`
(`#/a/<id>/exam`, also linked as `rehearse` from the Exams page). Starting it
(`POST /api/start` with stage `exam`) runs a random variant, seed 0, in a
time box: the deadline is the start plus the scenario minutes.

The page:

- Shows the ticket only: no story, guide, mentor or hints.
- Counts down (`9:41 left`, `1h 5m left` from an hour up), announces 5
  minutes and 1 minute left, and submits by itself at zero.
- Runs no live checks; the checks panel says `Exam mode: the checks run
  once, when you submit`.
- The Submit button (or Cmd/Ctrl+Enter) asks `Submit and end the exam`, then
  posts `/api/submit`, which grades once, ends the episode as solved or
  failed and opens the debrief with the outcome, the root cause and the
  walkthrough. The focus starts on Keep working, so a stray Enter does not
  end the exam.
- When the exam ends elsewhere (`kubedrill submit` in the terminal, or the
  deadline passing while the tab was in the background), the page opens
  the debrief of that run, also on the next visit to the stage.
- A pass is recorded as the exam stage (the Exam rehearsal passed row of
  the result page), however the exam ended.

The terminal of the page runs the workstation in exam mode
(`KUBEDRILL_EXAM=1`, `KUBEDRILL_DEADLINE`):

- `PATH` is `/opt/exam/bin:/usr/bin:/bin`. `/opt/exam/bin` holds links to
  kubectl, helm, yq, kubedrill and vim; jq and the other `/usr/bin` tools
  stay reachable.
- The banner lists the allowed documentation and says `Submit on the page
  (or kubedrill submit) when done`.
- It is a rehearsal of the exam conditions, not a sandbox: a tool called by
  its full path still runs, and the repository stays readable at
  `/workspace/repo` (an accepted risk, see [workstation.md](workstation.md)).

Commands during an exam:

- `check`, `hint`, `learn`, `story` and `solution` refuse with `<command> is
  not available in exam mode; kubedrill submit grades once`.
- `reset` refuses with `reset is not available in exam mode; kubedrill
  submit ends the exam`.
- `check --explain`, `ticket`, `diagnose` and `submit` work; `diagnose` ends
  with `Next: change one thing, then kubedrill submit`.
- `start` over an active exam asks `Leave the exam of <id>? It will be
  recorded as failed. [y/N]`.
- After an exam, `solution <id>` asks `Your latest episode of <id> was an
  exam (solved); show the solution? [y/N]` after a solved one and `Your
  latest episode of <id> ended <outcome>; show the solution anyway? [y/N]`
  otherwise.

Deadline:

- Any command run at or after the deadline, and the page's next request,
  first grade the episode one last time; a command prints `exam time box of
  <id> expired at HH:MM: outcome timed out (N of M checks passed)` on
  stderr, so `$(kubedrill namespace)` in a script never captures it.
- At or after means 12:15:00 for a 12:15:00 deadline; 12:14:59 is still
  inside it.
- The exceptions are `doctor`, `up`, `down`, `lint`, `new`, `smoke`,
  `version`, `help`, `fault`, `heal`, `profile render` and bare `kubedrill`
  (the workspace then finishes it on its first request).
- The outcome stays `timed_out` (printed `timed out`) when that grade passes, except within 30
  seconds after the deadline, when a pass counts as `solved` (`env.Expire`).
- Both the deadline and the 30 seconds are judged at the moment the command
  or request starts, before grading: a submit started before the deadline
  counts even when its grading ends after it.

## Telemetry

The debrief, `stats` verdicts, `solution` "your path" and mastery read three
files: the audit log under the cluster state directory, and two files under
the journal directory.

| File | Written by | Holds |
|------|------------|-------|
| `<state dir>/audit/audit.log` | the kind API server (audit policy `cluster/audit-policy.yaml`) | the learner's API requests: reads before the first write, the first write target, the verdict |
| `<journal dir>/shell/commands.jsonl` | the workstation shell | every command with time, exit code and duration; the kubectl lines become "your path". After an episode ends it moves to `commands.jsonl.1` when it is over 10 MiB; a line over 16 MiB is skipped, and an unreadable log prints one warning instead of being dropped silently |
| `<journal dir>/recordings/<namespace>-<unix>.cast` | asciinema in the workstation | the terminal session, played by `kubedrill replay` |

The audit log:

- The path is `cluster.AuditLogPath()`:
  `$XDG_STATE_HOME/kubedrill/kubedrill/audit/audit.log`, default
  `~/.local/state/kubedrill/kubedrill/audit/audit.log`.
- Inside the workstation it is `/audit/audit.log`, through
  `KUBEDRILL_AUDIT_LOG`.
- The file belongs to the cluster, not to one journal; episode namespaces are
  unique, so episodes do not mix.
- Requests whose user agent contains `kubedrill` are dropped: the CLI itself
  and the `kubedrill-check` kubectl of script checks.
- The audit log exists only on a cluster created by this version. When
  `start` prints `telemetry: audit log off; recreate the cluster to enable
  it (kubedrill down --yes && kubedrill up)`, run those two commands.
- The same two commands apply a changed `cluster/audit-policy.yaml`: the API
  server reads the policy once at start. `start` (once per journal) and `up`
  (every time) print the audit policy notice while the cluster runs the old
  one.
- A cluster created before the audit log moved to the cluster state
  directory writes it under the old path, so its verdicts read `unknown`
  until `kubedrill down --yes && kubedrill up` recreates it.

How it is computed:

- Telemetry is computed when an episode ends and stored on the episode;
  `source` is `audit+shell`, `audit`, `shell` or `none`.
- The CLI asks the cluster once per process whether the audit log is on,
  under the command's context with a 5 s timeout; a failure or a timeout
  counts as off.
- Script checks call kubectl through a wrapper, `<state dir>/bin/kubectl`,
  that runs the real kubectl with argv[0] `kubedrill-check`. kubectl builds
  its user agent from argv[0], so grading never counts as learner reads.
- When the wrapper cannot be written, a warning says the plain kubectl runs
  and counts. Without kubectl on the host there is nothing to wrap and no
  warning.
- The shell log and the recordings need the workstation: the terminal of
  `kubedrill web`.
- The audit policy records the request and response bodies of the
  learner's `patch` and `update` on workloads, pods, services, ConfigMaps,
  PVCs, service accounts, Ingresses and NetworkPolicies (user group
  `kubeadm:cluster-admins`), never of Secrets. A write whose body changes only labels or annotations, of the
  object or of its pod template (`kubectl annotate`, `label`, `rollout
  restart`), does not make the verdict `root-cause`, and the debrief says
  `a fix target, but only its labels or annotations changed`. On a cluster
  created with the older policy no body is recorded and every write to a
  fix target counts.
- The same rule records the response body. A no-op write is not a change:
  a `patch` or `update` that the server answers with the object as it was
  (`kubectl patch` prints `patched (no change)`) counts neither as a write
  nor as a read, so it is never the first write and never makes the
  verdict `root-cause`. The response shows it: the `managedFields` entry
  of the request's field manager (the `fieldManager` the client sends,
  `kubectl-patch` for `kubectl patch`) is missing or older than the second
  the request arrived, outside the status subresource. A request without a
  field manager is judged on every entry. On a cluster created before the policy recorded
  response bodies, a no-op write counts like any other write until
  `kubedrill down --yes && kubedrill up`.
- A get by the same client less than a second before a write that counts
  is the client's own read and is left out. A rejected write (code 400 or
  more) or an `exec` keeps that get as an observation.

## Scripts

Scripts run by the CLI are `setup/hook.sh`, `solution/solve.sh`, the
`shortcuts/*.sh` of a scenario (under `smoke`) and script checks.

- All of them receive `TASK_NS`, `SEED`, `VARIANT`, one `VAR_<KEY>` per
  variant variable and `KUBECONFIG`.
- `setup/hook.sh` and, under `smoke`, `solution/solve.sh` and the shortcuts also receive
  `KUBEDRILL_BIN`, the path of the running `kubedrill` binary, so a setup
  hook can call `"$KUBEDRILL_BIN" fault pause worker`, and
  `KUBEDRILL_EPISODE`, the id of the episode being set up or solved.
- Script checks run by the grader receive neither.

The `KUBECONFIG` they receive:

- It is a pinned copy, `<cluster state dir>/kubeconfig-scripts`
  (`cluster.ScriptKubeconfigPath()`), with mode 0600.
- It contains only the `kind-kubedrill` context, its cluster and its user,
  so a script cannot reach another cluster from the learner's kubeconfig.
- It sits outside the journal directory the workstation mounts read-write.
- It is rewritten from the learner's kubeconfig each time a command runs
  scripts.
- When the cluster does not exist yet, so the context is missing, the scripts
  receive the original kubeconfig path; `start` refuses to run in that state
  (`cluster kubedrill: missing; create it with: kubedrill up`).

## Markdown rendering

Tickets, hints, solutions and concept pages are markdown.

A ` ```mermaid ` `flowchart` block is drawn as plain text in the terminal:
a vertical chain (`|` and `v`, the arrow label beside the bar) when the
diagram is a single path, otherwise each node followed by an `Arrows:`
list (`A -> B (label)`, `<->` for a two-way arrow). A block that does not
parse prints as its source. The browser workspace renders the same block
as a diagram.

Style:

- On a terminal with color they are rendered with a style derived from
  glamour's dark style whose glyphs are ASCII: `# ` and `## ` headings, `| `
  block quotes, `- ` bullets, `[x]`/`[ ]` tasks, and inline code without the
  non-breaking space padding.
- A command copied from a ticket pastes back into the shell unchanged.
  Colors are kept.
- A hard line break (a line ending in a backslash or two spaces, as in the
  `From:` and `Priority:` lines of a ticket) stays a line break, inside a
  block quote too; glamour alone joins the two lines with a space.
- The style is fixed: the CLI never queries the terminal for its background
  color, so nothing typed ahead is consumed by a terminal reply.
  `KUBEDRILL_NO_TERM_QUERY=1`, set inside the workstation, keeps it that way.
- Without color the markdown source is printed. On a terminal (`NO_COLOR`)
  it is wrapped the same way, list items with a hanging indent and fenced
  code whole; on a pipe it is printed as is.

Width:

- Text wraps at the terminal width: `COLUMNS` when it is a positive integer,
  else the width the terminal on stdout reports (`TIOCGWINSZ`), else 80,
  kept between 40 and 120.
- A 77-column terminal wraps at 77 and never breaks a word.
- The same width wraps the long lines outside markdown on a terminal: the
  `list` and `stats` legends, `Next:` and `Recommended:` lines, the ticket
  footer, the `path` concepts lines and the debrief diagnosis and root
  cause.
- Fenced code blocks and inline code spans never wrap: a long command
  overflows the line instead of breaking, so it still pastes as one command.
- A wrapped block quote line repeats its `| ` prefix.
- A wrapped list item continues under the text after its marker (a hanging
  indent). The soft line breaks inside a list item in the source are joined
  first, so a bullet written over several lines reflows as one paragraph.

## Output rules

- While an episode is active the CLI prints only the seed, never the variant
  name, because variant names describe the root cause. The variant appears in
  the solved banner, in `journal` for ended episodes and in `smoke`.
- Confirmations (`start` over an active episode, `solution`, `down`) read the
  answer from the terminal.
- When stdin is a pipe, a file or `/dev/null`, one line is read and `y` or
  `yes` accepts. Only a real terminal counts as one (the terminal driver
  answers), so `</dev/null` in a script is not mistaken for a person.
- With no input at all the command fails with `confirmation needed (stdin is
  not a terminal): rerun with --yes`.
- One Ctrl-C at any prompt (confirmations, `Start <id> now?`, the debrief
  questions) ends the command at once with `kubedrill: interrupted`, exit
  1, and nothing is answered for you.

## Environment variables

| Variable | Effect |
|----------|--------|
| `KUBEDRILL_ROOT` | Repository root when `--root` is not given: `--root` wins, then `KUBEDRILL_ROOT`, then the first ancestor of the working directory with `curriculum.yaml` and `scenarios/` |
| `KUBEDRILL_DATA` | Journal directory. Default: `$XDG_DATA_HOME/kubedrill` (`~/.local/share/kubedrill`); holds the episodes, the shell log (`journal.ShellLogPath`), the recordings (`journal.RecordingsDir`) and the notice markers |
| `XDG_STATE_HOME` | Base of the cluster state directory (`$XDG_STATE_HOME/kubedrill/kubedrill`, default `~/.local/state/kubedrill/kubedrill`) holding the audit log (`audit/audit.log`), the kubectl wrapper of script checks and the linux binary (`bin/`), the Go caches of a build without host `go` (`go/`), `kubeconfig-internal`, `kubeconfig-scripts`, `audit-policy.sha256` and `faults.json` |
| `KUBECONFIG` | Its first entry (default `~/.kube/config`) is where the cluster manager places the kind context and the source of the pinned copy exported to scripts |
| `NO_COLOR` | Any value disables color and markdown styling; output is also plain when stdout is not a terminal |
| `COLUMNS` | Wrap width on a terminal when it is a positive integer (kept between 40 and 120); otherwise the terminal's own width, else 80 |
| `KUBEDRILL_NO_BROWSER` | `1` makes `web` print its link without opening a browser, like `--no-open` |
| `KUBEDRILL_WEB` | Set to `1` inside a workstation the browser workspace opened; the welcome text points at the ticket panel of the page |
| `KUBEDRILL_BIN` | Set by the CLI for `setup/hook.sh` and the `smoke` run of `solution/solve.sh`: the path of the running binary |
| `KUBEDRILL_EPISODE` | Set by the CLI for the same scripts: the id of the episode being set up or solved |
| `KUBEDRILL_AUDIT_LOG` | Path of the API audit log telemetry reads instead of `cluster.AuditLogPath()`; the workstation sets `/audit/audit.log` |
| `KUBEDRILL_WORKSTATION` | Set to `1` inside the workstation container. `start` prints the ticket without the `kubedrill web` line, `web` refuses to run, `status` prints `present (inside the workstation)`, and `KUBECONFIG` is used as is (no pinned copy is rewritten) |
| `KUBEDRILL_NO_TERM_QUERY` | Set to `1` inside the workstation; markdown keeps its fixed style and nothing queries the terminal background (the CLI never does) |
| `KUBEDRILL_CLUSTER` | Set inside the workstation to the kind cluster name (`kubedrill`); the prompt shows it |
| `KUBEDRILL_EXAM` | Set to `1` inside the exam workstation: no live checks, the exam `PATH`, the allowed docs listed |
| `KUBEDRILL_DEADLINE` | Set inside the exam workstation to the exam deadline, RFC 3339 in UTC |
| `KUBEDRILL_SHELL_LOG` | Path the workstation shell logs commands to; default `$KUBEDRILL_DATA/shell/commands.jsonl` (`/data/shell/commands.jsonl` in the container) |

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Error; the cases are listed below |
| 2 | `kubedrill check` or `kubedrill submit` ran and at least one check did not pass |

Exit 0 also covers:

- `hint` with every hint already shown.
- A declined confirmation (`kept the episode active`, `kept the cluster`).

Exit 1 cases:

- The last hint or the solution without a diagnosis, `--yes` included.
- A practice-only command in exam mode, `reset` included.
- `submit` on a practice episode.
- A repeated fault; an unknown addon, fault action or node.
- `debrief --redo` or `replay` without a terminal; an episode without a
  recording.
- A missing tool, an unknown scenario or an unknown concept.
- No active episode, `namespace` included.
- A missing cluster on `start`.
- `node` with an unknown node name.
- `node` without docker.
- A scenario namespace that exists without the
  `app.kubernetes.io/managed-by=kubedrill` label, or is still terminating
  after 2 minutes.
- `check` when the scenario namespace is missing or foreign
  (`env.ErrNamespaceMissing`, `env.ErrNamespaceForeign`): one line pointing
  at `kubedrill reset`.
- A confirmation needed without a terminal.
- A lint or smoke failure, or smoke refused by an active episode.
