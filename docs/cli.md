# kubedrill command line

The exact reference: every command, flag, printed message, key, environment
variable and exit code. For how to study with these commands, read
[study-flow.md](study-flow.md); for the container, read
[workstation.md](workstation.md).

## Running kubedrill

`./start.sh` builds `bin/kubedrill` with the local Go toolchain and runs it
with the same arguments.

- `./start.sh` without arguments is the one command a learner needs: it
  runs `kubedrill` without a command (see
  [kubedrill without a command](#kubedrill-without-a-command)), so the first
  run sets everything up and starts the first scenario, and every later run
  reopens the active episode or offers the next one.
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
- `<ref>` is a scenario id (`000-hello-down`), its numeric prefix (`000`) or
  any id prefix that matches one scenario.
- `kubedrill <command> --help` prints a two to five line description for
  `start`, `watch`, `check`, `hint`, `diagnose`, `debrief`, `submit`,
  `replay`, `fault`, `up`, `namespace`, `node`, `path`, `concepts`, `lint`,
  `learn`, `shell`, `solution`, `smoke` and `profile render`.

## Overview

| Command | Purpose | Needs cluster |
|---------|---------|---------------|
| **Learner loop** | | |
| [`start`](#start) | Start a scenario in a fresh namespace and open the workstation | yes (checked first) |
| [`ticket`](#ticket) | Print the ticket again, or keep it on screen with `--follow` | no |
| [`namespace`](#namespace) | Print the namespace of the active episode | no |
| [`check`](#check) | Grade once | yes |
| [`watch`](#watch) | Live view of the checks | yes |
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
| [`concepts`](#concepts) | Every concept page | no |
| [`replay`](#replay) | Play a recording of an episode | no (docker) |
| **Cluster and nodes** | | |
| [`kubedrill`](#kubedrill-without-a-command) | Doctor, `up`, then the scenario list | yes (creates it) |
| [`doctor`](#doctor) | Check docker, kubectl and kind | no |
| [`up`](#up) | Create the cluster and install addons | yes (creates it) |
| [`down`](#down) | Delete the cluster | yes |
| [`fault`](#fault) | Inject a node fault | yes (docker) |
| [`heal`](#heal) | Undo every recorded fault | yes (docker) |
| [`node`](#node) | Root shell on a kind node | yes |
| **Workstation** | | |
| [`shell`](#shell) | Open the workstation again | yes (and docker) |
| **Authoring** | | |
| [`lint`](#lint) | Validate scenarios, curriculum and concept pages | no |
| [`new`](#new) | Scaffold a scenario | no |
| [`smoke`](#smoke) | Run every scenario end to end | yes |
| [`profile render`](#profile-render) | Render a kind profile to a file | no |
| [`version`](#version) | Version, revision and Go version | no |

## Learner loop

### start

```
kubedrill start <ref> [--seed N] [--yes|-y] [--no-shell] [--exam] [--layout tmux|shell]
```

Creates the episode namespace, applies the setup, runs `setup/hook.sh` and
records the episode. On a terminal it then opens the workstation.

Flags:

- `--seed N`: seed for the variant (0 or omitted: random).
- `--yes`, `-y`: leave an active episode without confirmation.
- `--no-shell`: print the ticket and stay on the host (also
  `KUBEDRILL_NO_SHELL=1`).
- `--exam`: exam mode, a time box of the scenario minutes, graded once by
  `kubedrill submit` (see [Exam mode](#exam-mode)).
- `--layout`: described as `tmux (shell, ticket and kubedrill watch panes)
  or shell`; see [Workstation](#workstation).

Namespace and progress:

- The namespace is `<scenario namespace>-<5 random characters>`.
- Older episode namespaces of this journal are deleted without waiting.
- Progress goes to stderr as `creating namespace <ns>... done (1.2s)`.
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

Where the ticket goes:

- When stdout and stdin are terminals, a workstation is wired and docker is
  reachable, it prints `ticket shown in the workstation (kubedrill ticket
  reprints it)`, the Fundamentals line and the ticket footer, then opens the
  workstation.
- With `--exam` it opens the exam workstation and leaves out the
  Fundamentals line.
- When the shell exits with an episode still active it prints `back on the
  host; kubedrill check grades from here too`.
- Otherwise it prints the ticket, the Fundamentals line, the footer and the
  next steps on the host. The cases that keep it on the host are listed under
  [Workstation](#workstation).

### ticket

```
kubedrill ticket [--follow]
```

Prints the ticket of the active episode again.

- The ticket is wrapped to the terminal width (see
  [Markdown rendering](#markdown-rendering)).
- The footer is `Checked by N checks: kubedrill check --explain` and
  `Expected: M min (yours: 0.8 min median over K solves)`; the part in
  parentheses appears only after a solve.
- An exam ticket leaves out the `Fundamentals:` line and adds `Exam
  deadline: HH:MM:SS (M min time box); no hints, no solution, no watch`.

`--follow` keeps the ticket on screen instead, in the alternate screen:

- A header `<id> (seed N), namespace <ns>`, the ticket wrapped to the pane
  width, and the footer `lines A-B of N  wheel scrolls; click here for j/k
  space b g G` (`all N lines shown` when it fits).
- Keys: the mouse wheel scrolls 3 lines, `j`/`k` one, `space`/`f`/`b` a
  page, `g`/`G` top and end, `q` quits.
- It re-reads the active episode every 2 s, keeps the scroll position for
  the same episode and goes back to the top after a reset or a new start.
- Without an episode it shows `no active episode; kubedrill start <ref>
  opens one and this pane shows its ticket` and keeps waiting.
- It allows exam mode.
- It needs a terminal on stdin and stdout: `ticket --follow needs a terminal
  on stdin and stdout; use kubedrill ticket in scripts and pipes`.

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
- A check marked `spoiler` in `checks.yaml` (its actual value names the root
  cause, an image reference for example) prints `actual: hidden until your
  first hint or diagnosis` until the episode has a hint or a diagnosis.
- When all pass, in this order: the solved banner (`Solved: <title> in 4.1
  min with 0 hints (variant <name>)`), the `Recommended:` lines, `Next:
  kubedrill solution <id> (compare with the reference) | kubedrill list` and
  the debrief (see [Diagnosis and debrief](#diagnosis-and-debrief)).
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
  Deployment hello in namespace hello-world-ab12c: {.status.readyReplicas}
  equals "2"`. The namespace part appears when the check names one, which a
  rendered check always does.
- Then, when they apply: `runs after <ids>`, `critical: if it does not pass
  (failed, errored or skipped) the score is 0` and `why: ...`.

### watch

```
kubedrill watch [--interval 5s]
```

Live view, re-graded every interval. The keys are under
[Watch keys](#watch-keys).

- `--interval`: time between grading runs, at least 1 s; less is `interval
  must be at least 1s, got 500ms`, exit 1.
- While a grade runs the header shows `checking (N checks)...`.
- Failed checks are expanded with expected/actual and why; passed checks
  collapse into one line `[pass] N checks: id, id, ...`.
- Spoiler checks hide their actual value like `check` until the episode has
  a hint or a diagnosis.
- The footer is `c check  h hint  w why  x reset  ? help  q quit` at every
  width.

Width:

- At 80 columns or more, check lines wider than the terminal are cut and end
  with `...`, measured in visible columns (color codes do not count and a
  character is never split).
- Below 80 columns (a tmux pane) they wrap with a hanging indent instead.
- At every width the status line (`elapsed ... hints ... checked ... ago`),
  the solved banner, the `Next:` and `n:` lines, the confirmations and the
  full help entries wrap to the pane width with a hanging indent; a single
  token wider than the pane is cut with `...`.
- The banner and the debrief printed after `n` or a quit wrap to the width
  the pane had.

Solve and quit:

- A solve shows the banner and never advances on its own.
- While the solve is being recorded, every key but `ctrl+c` only shows
  `recording the solve...`. The recording runs detached from the screen with
  its own timeout, so even `ctrl+c` does not lose it.
- Quitting after a solve prints the banner, the recommendation and the
  debrief. When the quit races the recording of the solve, the episode just
  recorded is used.
- Quitting before a solve prints `Next: kubedrill check | kubedrill hint`.
- When recording the solve fails (not a namespace error), watch leaves the
  solved screen, shows `recording the solve failed: <err>; the next passing
  check records it` and keeps polling. Quitting with a passing grade that
  was never recorded exits 1 with `the checks of <id> passed but the solve
  was not recorded; kubedrill check grades and records it`, without banner
  or debrief.
- A hint, surrender or start that returns after `x` started another episode
  is dropped, so it never lands on the new episode.

Errors:

- A missing or foreign scenario namespace ends watch with the same error line
  as `check`, exit 1.
- In exam mode it refuses, before the terminal check: `watch is not
  available in exam mode; kubedrill submit grades once`.

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
  logged; kubedrill shell records them`.
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
- On a practice episode: `submit grades exam episodes (kubedrill start <id>
  --exam); a practice episode is graded with kubedrill check`, exit 1.

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
  watch` (`kubedrill submit` for an exam).

### path

```
kubedrill path
```

The ladder from `curriculum.yaml`: levels, hours, and modules with planned,
existing and solved scenario counts.

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

Runs the doctor, then `up` with the base profile (base addons included),
then prints the scenario list and the next recommended scenario.

On a terminal (stdin and stdout), outside the workstation and without
`KUBEDRILL_NO_SHELL=1`, it continues into practice:

- With an active episode it prints `resuming <id>; opening the workstation`
  and opens the workstation as `kubedrill shell` does (the exam workstation
  for an exam episode; an exam past its deadline is finished first).
- Without one it asks `Start <id> now? [Y/n]` for the recommended scenario.
  Enter, `y` or `yes` runs `kubedrill start <id>` with the recommended seed;
  any other answer prints `not started; run ./start.sh again or kubedrill
  start <id> when you are ready`.

In a pipe or a script it stops after the list.

### doctor

```
kubedrill doctor
```

Table of docker, kubectl and kind: found, version and install hint (`-` when
there is no detail).

### up

```
kubedrill up [--profile base] [--addons a,b]
```

Creates the cluster when it is missing, then installs the addons.

- `--profile`: kind profile under `cluster/profiles`, default `base`.
- `--addons`: comma-separated addons to install besides metrics-server, for
  example `gateway,kwok`.

Cluster:

- Prints `present` when the cluster exists, after the same readiness wait
  as a new cluster: an `up` started while another one is still creating
  the cluster does not continue early. A stopped node container (for
  example after a Docker restart) fails at once with the `docker start`
  command that brings it back.
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

### shell

```
kubedrill shell [--layout tmux|shell] [--exam]
```

Opens the workstation again.

- With an active episode it opens the same session `start` opened (the exam
  workstation during an exam); without one, the tools against the cluster.
- `--exam`: exam workstation, a rehearsal with `PATH` limited to the exam
  tools (kubectl, helm, yq, jq, vim), no watch pane and the allowed docs
  listed; automatic for an exam episode.
- `--layout`: as in `start`.
- It needs a terminal on stdin and stdout: `shell needs a terminal on stdin
  and stdout; run it from an interactive terminal`.
- A missing cluster or unreachable docker is an error (`cluster kubedrill:
  missing; ...`, `the workstation needs docker: ...`).
- Inside the workstation it refuses to nest.
- It prints `back on the host; kubedrill check grades from here too` when the
  shell exits with an episode active.

### How start and shell use the workstation

`start` (unless told otherwise), `shell` and `node` use the containerized
workstation of `internal/workstation`: a docker image with `kubectl`,
`helm`, `k9s`, `stern` and `tmux`, plus a Linux build of `kubedrill` mounted
in.

After the episode is recorded, `start` runs this sequence:

1. Write `<cluster state dir>/kubeconfig-internal`: the cluster context
   rewritten to the address reachable from inside docker. It sits outside
   the journal, so the read-write `/data` mount cannot rewrite it.
2. Call `workstation.Shell`, which builds the binary when the sources are
   newer, builds the image when missing and opens the shell. Progress lines
   go to stderr.
3. The ticket is shown in the workstation, not on the host.

`--layout` picks:

- `tmux` (default): a ticket pane above the shell and a `kubedrill watch`
  pane.
- `shell`: one shell, no tmux.
- Another value is an error, exit 1.

`start` stays on the host and prints the ticket when any of these holds:

- `--no-shell` or `KUBEDRILL_NO_SHELL=1`.
- `KUBEDRILL_WORKSTATION=1`: already inside the container.
- stdout or stdin is not a terminal.
- No workstation is wired.
- Docker is unreachable: it prints `workstation skipped: docker is
  unavailable (<reason>); kubectl runs on the host` on stderr, prints the
  ticket and exits 0.

Inside the container:

- `KUBEDRILL_WORKSTATION=1` is set and `KUBECONFIG` points at the internal
  kubeconfig, which holds a single context.
- `kubedrill` there hands that path unchanged to scenario scripts and
  connects with it directly, so `check`, `watch`, `hint` and `solution` grade
  the same episode from either side.

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
prints the next steps.

### smoke

```
kubedrill smoke [refs...] [--force] [--verbose|-v]
```

For each scenario and each variant seed: reset, assert the checks fail, run
`solution/solve.sh`, assert they pass.

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
- `solve.sh` output is shown for failing runs, or for every run with `-v`.

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
from an unversioned tree)`) and the Go version.

## Watch keys

| Key | Effect |
|-----|--------|
| `c` | Grade now instead of waiting for the interval (`a check is already running` while one runs) |
| `h` | Next hint, labeled like `kubedrill hint`; the last one waits for `kubedrill diagnose`. A second `h` while the first is on its way only says `a hint is already on its way` |
| `s` twice | Surrender and print the solution (needs a diagnosis); any other key cancels |
| `x` twice | Start the same scenario again with a new seed, in place; any other key cancels. While a hint is on its way it only says `a hint is on its way; press x again once it shows`. The start's progress lines (`creating namespace <ns>...`) replace the status line instead of printing over the screen. Refused during an exam with the `reset` message |
| `n` | After a solve only; see below. Before a solve it only explains itself |
| `w` | Toggle why between the first failing check and every failing check |
| `?` | Full help: every key grouped under Checks (`c`, `w`), Assist (`h`, `s`) and Episode (`x`, `n`, `?`, `q`), each entry indented two spaces; `?` closes it |
| `q`, `esc`, `ctrl+c` | Quit; after a solve any key other than `n` and `?` quits, once the solve is recorded |

The `n` key, shown as `n: debrief, then start <id> (<reason>)` on the solved
screen:

1. Leaves the live view and prints the banner.
2. Runs the debrief prompts, which end with one line: `debrief recorded;
   kubedrill debrief shows it again`, or `kubedrill debrief --redo answers
   the questions later` when they get no answer.
3. Starts the recommended scenario and watches it.

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
After a solve (`check`, `watch`, `submit`) the debrief prints:

```
Debrief: <id> (episode <episode id>, solved)
  duration 4.1 min, 0 hints
  observations before the first write: 5 (telemetry: audit+shell)
  first write: apps/deployments/hello (a fix target)
  verdict: root-cause
  your diagnosis: <your text>
  root cause: <root_cause of the variant>
```

The summary block:

- The first write is compared with `fix_targets` in `scenario.toml` through
  `telemetry.MatchTarget`; case and an empty core group do not matter.
- `not a fix target; the fix targets are ...` names a write that only
  touched a symptom.
- Without telemetry the block says `telemetry: none recorded (no audit log
  or shell log for this episode)` in place of the observations, first write
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

`kubedrill start <id> --exam` starts a timed episode. The deadline is the
start plus the scenario minutes.

Ticket and commands:

- The ticket shows the deadline and `Next: kubedrill submit (grade once,
  ends the exam) | kubedrill check --explain (what is graded)`.
- It leaves out the `Fundamentals:` line and the "Last time you said"
  reminder.
- `check`, `hint`, `watch`, `learn` and `solution` refuse with `<command> is
  not available in exam mode; kubedrill submit grades once`.
- `reset` and the watch `x` key refuse with `reset is not available in exam
  mode; kubedrill submit ends the exam`.
- `check --explain`, `ticket`, `diagnose` and `submit` work; `diagnose` ends
  with `Next: change one thing, then kubedrill submit`.
- `submit` grades once and ends the exam as solved or failed.
- `start` over an active exam asks `Leave the exam of <id>? It will be
  recorded as failed. [y/N]`.
- After an exam, `solution <id>` asks `Your latest episode of <id> was an
  exam (solved); show the solution? [y/N]` after a solved one and `Your
  latest episode of <id> ended <outcome>; show the solution anyway? [y/N]`
  otherwise.

Workstation (`KUBEDRILL_EXAM=1`, `KUBEDRILL_DEADLINE`):

- No watch pane.
- `PATH` is `/opt/exam/bin:/usr/bin:/bin`. `/opt/exam/bin` holds links to
  kubectl, helm, yq, kubedrill and vim; jq and the other `/usr/bin` tools
  stay reachable.
- The allowed documentation is listed.
- It is a rehearsal of the exam conditions, not a sandbox: a tool called by
  its full path still runs, and the repository stays readable at
  `/workspace/repo` (an accepted risk, see [workstation.md](workstation.md)).

Deadline:

- Any command run at or after the deadline first grades the episode one last
  time and prints `exam time box of <id> expired at HH:MM: outcome timed_out
  (N of M checks passed)`.
- At or after means 12:15:00 for a 12:15:00 deadline; 12:14:59 is still
  inside it.
- The exceptions are `doctor`, `up`, `down`, `lint`, `new`, `smoke`,
  `version`, `help`, `fault`, `heal` and `profile render`. Bare `kubedrill`
  is an exception too, except on a terminal, where it finishes an expired
  exam before it reopens the workstation.
- The outcome stays `timed_out` when that grade passes, except within 30
  seconds after the deadline, when a pass counts as `solved` (`env.Expire`).
- Both the deadline and the 30 seconds are judged at the moment the command
  starts, before grading: a `submit` started before the deadline counts
  even when its grading ends after it.

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
  and counts.
- The shell log and the recordings need the workstation: `kubedrill start`
  on a terminal, or `kubedrill shell`.

## Scripts

Scripts run by the CLI are `setup/hook.sh`, `solution/solve.sh` and script
checks.

- All of them receive `TASK_NS`, `SEED`, `VARIANT`, one `VAR_<KEY>` per
  variant variable and `KUBECONFIG`.
- `setup/hook.sh` and, under `smoke`, `solution/solve.sh` also receive
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

Style:

- On a terminal with color they are rendered with a style derived from
  glamour's dark style whose glyphs are ASCII: `# ` and `## ` headings, `| `
  block quotes, `- ` bullets, `[x]`/`[ ]` tasks, and inline code without the
  non-breaking space padding.
- A command copied from a ticket pastes back into the shell unchanged.
  Colors are kept.
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
- A 77-column tmux pane wraps at 77 and never breaks a word.
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
- When stdin is a pipe, one line is read and `y` or `yes` accepts.
- With no input at all the command fails with `confirmation needed (stdin is
  not a terminal): rerun with --yes`.

## Environment variables

| Variable | Effect |
|----------|--------|
| `KUBEDRILL_ROOT` | Repository root when `--root` is not given: `--root` wins, then `KUBEDRILL_ROOT`, then the first ancestor of the working directory with `curriculum.yaml` and `scenarios/` |
| `KUBEDRILL_DATA` | Journal directory. Default: `$XDG_DATA_HOME/kubedrill` (`~/.local/share/kubedrill`); holds the episodes, the shell log (`journal.ShellLogPath`), the recordings (`journal.RecordingsDir`) and the notice markers |
| `XDG_STATE_HOME` | Base of the cluster state directory (`$XDG_STATE_HOME/kubedrill/kubedrill`, default `~/.local/state/kubedrill/kubedrill`) holding the audit log (`audit/audit.log`), the kubectl wrapper of script checks and the linux binary (`bin/`), `kubeconfig-internal`, `kubeconfig-scripts`, `audit-policy.sha256` and `faults.json` |
| `KUBECONFIG` | Its first entry (default `~/.kube/config`) is where the cluster manager places the kind context and the source of the pinned copy exported to scripts |
| `NO_COLOR` | Any value disables color and markdown styling; output is also plain when stdout is not a terminal |
| `COLUMNS` | Wrap width on a terminal when it is a positive integer (kept between 40 and 120); otherwise the terminal's own width, else 80 |
| `KUBEDRILL_NO_SHELL` | `1` makes `start` print the ticket and stay on the host, like `--no-shell` |
| `KUBEDRILL_BIN` | Set by the CLI for `setup/hook.sh` and the `smoke` run of `solution/solve.sh`: the path of the running binary |
| `KUBEDRILL_EPISODE` | Set by the CLI for the same scripts: the id of the episode being set up or solved |
| `KUBEDRILL_AUDIT_LOG` | Path of the API audit log telemetry reads instead of `cluster.AuditLogPath()`; the workstation sets `/audit/audit.log` |
| `KUBEDRILL_WORKSTATION` | Set to `1` inside the workstation container. `start` never opens a nested shell, `shell` refuses to run, `status` prints `present (inside the workstation)`, and `KUBECONFIG` is used as is (no pinned copy is rewritten) |
| `KUBEDRILL_NO_TERM_QUERY` | Set to `1` inside the workstation; markdown keeps its fixed style and nothing queries the terminal background (the CLI never does) |
| `KUBEDRILL_CLUSTER` | Set inside the workstation to the kind cluster name (`kubedrill`); the prompt and the tmux status line show it |
| `KUBEDRILL_EXAM` | Set to `1` inside the exam workstation: no watch pane, the exam `PATH`, the allowed docs listed |
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
- A missing cluster on `start` or `shell`.
- `--layout` other than `tmux` or `shell`.
- `shell` without a terminal on stdin and stdout.
- `node` with an unknown node name.
- `shell` or `node` without docker.
- A scenario namespace that exists without the
  `app.kubernetes.io/managed-by=kubedrill` label, or is still terminating
  after 2 minutes.
- `check` or `watch` when the scenario namespace is missing or foreign
  (`env.ErrNamespaceMissing`, `env.ErrNamespaceForeign`): one line pointing
  at `kubedrill reset`.
- A confirmation needed without a terminal.
- A lint or smoke failure, or smoke refused by an active episode.
