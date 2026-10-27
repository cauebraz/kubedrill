# Study flow

This guide covers what a study session looks like and how kubedrill judges
your work. It trains one habit: going from a vague symptom to a verified fix
with a method, not by guessing. Every message and option is listed in
[cli.md](cli.md).

## A session in short

`./start.sh` opens the browser workspace (`kubedrill web`). Pick an
assignment from a learning plan and walk its stages:

1. Meet the customer: pick the questions that gather the requirements; the
   mentor explains why a question helps or not.
2. Check understanding: a short quiz on what the symptoms rule in and out.
3. Learn the pieces: the object diagram, the story from zero, the ticket.
4. Guided practice: the first variant on a live namespace, with the steps
   beside a terminal into the workstation and the checks updating every 5
   seconds. Validate grades it like `kubedrill check`.
5. Do it yourself: another variant, the ticket only, the mentor's hint
   ladder and your diagnosis. A pass completes the assignment.
6. Result: the badge, your stage scores and the mastery rule with your
   numbers; every DIY of a learning plan passed earns the plan's badge.

After each passed run the page opens the debrief: the same summary and
questions as `kubedrill debrief`, stored on the episode. The home page
starts with one next step (the open run, or the recommendation of
`kubedrill list` with its reason), the catalog filters the whole
curriculum, and `?` lists the keyboard shortcuts of the screen.

The practice and DIY runs are ordinary episodes: everything below about
grading, hints, diagnosis, telemetry and mastery applies to them. The
meeting and quiz scores are kept in `web-progress.json` in your journal
and never touch the grade.

The same loop runs in the terminal. `kubedrill` takes you to the
recommended scenario, or back to the one you were on. The commands of a
session:

```sh
kubedrill start 000               # or pick one yourself: fresh namespace, the workstation opens with the ticket
kubedrill check --explain         # what will be graded, nothing graded yet
kubectl get pods; kubectl describe pod ...   # observe before you change anything
kubedrill diagnose "<the cause>"  # commit to a cause in one line
# change one thing, then watch the checks in the right pane turn to [pass]
kubedrill solution                # after the solve: compare with the reference
```

When every check passes, kubedrill records the solve and runs a short
debrief. Then it recommends the next scenario.

## The method

The method is the troubleshooting loop (`kubedrill learn
troubleshooting-loop`):

1. Describe the symptom.
2. List two or three candidate causes.
3. Pick the one observation that tells them apart, and run it before any
   write.
4. Change one thing. If the symptom does not move, undo it before the next
   change.
5. Verify.

Every scenario walkthrough uses these steps as its headings.

## Start an episode

```sh
kubedrill              # the recommended scenario, after a [Y/n] prompt
kubedrill start <id>   # a scenario you pick
```

`start` creates a new namespace for this episode, for example
`shop-k3x9p`, and applies the broken setup there. The ticket, hints
and walkthrough all name this namespace. The namespaces of your older
episodes are deleted in the background.

If an episode is already active, `start` asks before leaving it. The episode
you leave is recorded as `failed`.

The first time you meet a concept, `start` offers a worked example from
another variant. It shows only the symptom and the candidate causes. The
observation, the change and the verification stay yours.

`kubedrill reset` restarts the same scenario with a new seed. The ticket
stays the same, and the root cause usually changes.

## The workstation

`kubedrill start` opens a tmux session with three panes:

- Top left: the ticket. Scroll it with the mouse wheel while you type;
  click it and press `s` for the story when the scenario has one.
- Bottom left: a shell with `kubectl` (alias `k`), `helm`, `k9s` and `stern`,
  already pointed at the episode namespace.
- Right: `kubedrill watch`, re-grading every 5 seconds.

Read at the top, work at the bottom. Below 120 columns you get a single
shell instead. `--no-shell` keeps your own terminal, and `kubedrill shell`
reopens the workstation after you close it. Tools, panes and options are in
[workstation.md](workstation.md).

## Read the ticket

The ticket is written in the words of the person who reported the problem.
It never names the root cause.

Read `## Done when` twice. It lists the checks one per line, so it is the
definition of done. The footer says how many checks grade the ticket and how
long it should take:

```
Checked by 4 checks: kubedrill check --explain
Expected: 10 min (yours: 7.2 min median over 3 solves)
```

`Expected` is the time for someone at that level who has seen the problem
before. A first attempt that takes three times longer is normal.

If Kubernetes itself is new, start with `kubedrill story` (or `s` in the
ticket pane) when the ticket names it: it tells this ticket from
zero, explains each object as it appears and walks the read-only commands
that follow the trail. If only the vocabulary is new, `kubedrill learn`
prints the concept pages this scenario uses. Otherwise read them after the solve, to name what you did.
`kubedrill concepts` lists every page.

## How grading works

kubedrill grades the end state of the cluster, never the commands you typed.
Any fix that produces the required state passes.

Each check prints what it expected and what it found:

```
[fail] service storefront has at least one ready endpoint
       expected: at least one value  actual: no value at {.items[*].endpoints[?(@.conditions.ready==true)].addresses[*]}
       why: A Service is only a selector; EndpointSlices hold the pod IPs that
            are Ready. With no ready endpoint kube-proxy rejects every
            connection to the storefront Service (connection refused), and
            the edge answers 503.
```

- **`why:`** says what the check protects in production. It is not a hint
  and never names the root cause. Passing checks print no `why`.
- **`[skip]`** means a check this one depends on has not passed yet. Fix the
  earlier one first.
- **Score:** each check has a weight. The score is the weight of the passed
  checks divided by the total weight.
- **Critical checks:** if a critical check fails, errors or is skipped, the
  score is 0.
- **Spoiler checks:** some actual values would name the root cause. Those
  stay hidden until you take a hint or record a diagnosis.

`kubedrill check --explain` lists every check with its assertion,
dependencies and `why`, without grading anything.

## watch or check

`kubedrill watch` runs in the right pane. It re-grades every 5 seconds and
shows elapsed time and hints used. Useful keys:

| Key | Effect |
|-----|--------|
| `c` | Grade now |
| `h` | Next hint |
| `w` | Show `why` for every failing check |
| `x` twice | Restart with a new seed |
| `s` twice | Surrender and see the solution |
| `n` | After a solve: debrief, then start the recommended scenario |
| `?` | All keys |
| `q` | Quit |

A solve never advances on its own. Before recording it, watch grades once
more, so a state that passes for one tick only is not recorded.

`kubedrill check` grades once. It exits with code 2 while a check fails, so
it works in scripts. On a solve it prints the same banner and debrief.

## Hints

`kubedrill hint` reveals the next hint. Each step gives away a little more:

| Hint | Label | What it gives |
|------|-------|---------------|
| 1 | where to look | The object or subsystem, and a read command |
| 2 | what to observe | The field, column or event line to read |
| 3 | what it means | How to interpret that observation |
| 4 | what to change | The object and field, without the value or a command |

No hint contains a command that changes the cluster. You type the change.
The last hint waits until you record a diagnosis.

Take a hint after about ten minutes without a new observation, not after the
first failed command. A hinted solve is not a clean solve. Treat it as a
learning episode, then solve a fresh seed without hints.

## Diagnose

```sh
kubedrill diagnose "the image tag does not exist"
```

This records one line, 10 to 300 characters, naming the cause you suspect. A
later `diagnose` replaces it. It is required before the last hint and before
you surrender, and it works in exam mode. The debrief prints it next to the
real root cause.

Committing to a cause before you see the answer helps you notice and correct
a wrong belief.

## Surrender

Giving up after a real attempt is a valid outcome. `kubedrill solution`
during an episode, or `s` twice in watch, asks for confirmation. It then
records the episode as `abandoned` and prints the walkthrough. A diagnosis is
needed first. Start the scenario again with a new seed the same day.

## Solve and debrief

When every check passes, the debrief prints a summary:

```
Debrief: 001-storefront-down (episode <id>, solved)
  duration 4.1 min, 0 hints
  observations before the first write: 5 (telemetry: audit+shell)
  first write: apps/deployments/storefront (a fix target)
  verdict: root-cause
  your diagnosis: <your text>
  root cause: <root cause of the variant>
```

Then it asks up to three questions:

- Did your diagnosis match?
- Which observation would have ruled out your first hypothesis earlier?
- What will you check first next time?

Press Enter to skip any of them. Your "check first" answer comes back the
next time you start a scenario that shares a concept. `kubedrill debrief`
shows the last debrief again, and `--redo` asks the questions again.

## Compare with the reference

```sh
kubedrill solution
```

Read it even when your fix passed. After an episode ends, it shows the
walkthrough under the loop headings: Symptom, Candidate causes, Observation
that splits them, Change, Verify, In the real world, Go deeper. After a solve
it also shows:

- **Your path vs the reference:** your kubectl commands next to the
  reference commands.
- **Approaches:** the other valid fixes. `kubedrill solution --approach
  <name>` shows one with its trade-offs.
- **A principle question:** if you already solved a related scenario, it
  asks which principle made the key observation necessary before showing
  that section.

## What to run next

`kubedrill list`, `kubedrill path` and the solved banner print one
recommendation:

1. **A due review comes first.** A mastered concept is due 1 day after its
   last clean solve. The interval then doubles with each clean review: 2, 4
   and 8 days. The review uses a seed you never get in practice.
2. **Otherwise, the right difficulty.** kubedrill takes the lowest level that
   still has an unmastered scenario. In it, it picks the scenario you solve
   about half the time. A scenario you never tried counts as half.

## Progress and mastery

Every episode ends with one outcome:

| Outcome | When |
|---------|------|
| `solved` | Every check passed |
| `abandoned` | You surrendered and saw the solution |
| `failed` | You left without solving: another `start`, a `reset`, or a failing `submit` |
| `timed out` | An exam time box ran out without a passing grade (`timed_out` in the journal file) |

A failed, abandoned or timed-out episode has its namespace deleted at once.
A solved one keeps it until your next `start`, so you can still look around.

A **clean solve** has zero hints and fits the expected minutes. When the
audit log covers the episode, a clean solve also has at least one read
before the first write.

A scenario, or a concept, is **mastered** after 3 clean solves among your
last 4 attempts. Those solves must use 3 different seeds, cover 3 different
variants (or every variant when the scenario has fewer) and span at least 2
sessions; a new session starts after a 3-hour gap. A hinted or failed attempt
takes a slot in those 4. It does not reset your count to zero.

Where to see progress:

- `kubedrill list`: per scenario, the expected minutes, your median, clean
  solves in the last 4 (`3/4`) and status (`todo`, `active`, `solved`,
  `mastered`).
- `kubedrill path`: the ladder with progress per module and concept.
- `kubedrill stats`: solves by level, median time, hints per solve,
  outcomes, verdicts and concepts.
- `kubedrill journal`: every episode.

### Variants and seeds

Each scenario has several variants: different root causes or parameters
behind the same ticket. The seed picks the variant. Repeating a scenario
usually gives you a different variant. To replay one, pass its seed:

```sh
kubedrill start 000 --seed 3
```

### Where your data lives

Episodes are stored in `~/.local/share/kubedrill` (or `$XDG_DATA_HOME/kubedrill`,
or `KUBEDRILL_DATA`). `episodes.jsonl` holds the history and `current.json`
the active episode.

## Telemetry

kubedrill records what you did so the debrief can show it back to you.
Nothing is sent anywhere, and the checks never read these files.

| Source | What it records | Where |
|--------|-----------------|-------|
| API audit log | Your reads and writes in the episode namespace | `~/.local/state/kubedrill/kubedrill/audit/audit.log` |
| Shell log | Each command typed in the workstation shell | `shell/commands.jsonl` in the data directory |
| Recording | The shell session, for `kubedrill replay` | `recordings/` in the data directory |

Commands typed on the host, outside the workstation, are not logged.
kubedrill's own grading requests are left out, so grading never counts as
your reads.

From these files the debrief derives the reads before your first write, the
target of that first write, and a verdict:

| Verdict | Meaning |
|---------|---------|
| `root-cause` | A successful write changed more than the labels or annotations of an object the scenario names as a fix target |
| `symptom-only` | You wrote, but only to other objects, for example deleting pods instead of fixing the Deployment |
| `no-writes` | No successful write in the episode namespace |
| `unknown` | No audit log for this episode |

The verdict describes what you changed. Only the checks decide `solved`.

If `start` prints `telemetry: audit log off`, recreate the cluster to turn
the audit log on:

```sh
kubedrill down --yes && kubedrill up
```

The full field list is in [cli.md](cli.md#telemetry).

## Exam mode

```sh
kubedrill start <id> --exam
```

An exam is timed. The deadline is the start plus the scenario's expected
minutes, and the ticket footer shows it.

- No hints, no solution, no watch and no reset.
- `check --explain`, `ticket` and `diagnose` still work.
- `kubedrill submit` grades once and ends the exam as `solved` or `failed`.
- The workstation has the ticket pane but no watch pane. `k9s`, `stern`,
  `kubectx` and `kubens` are off the `PATH`, as in the real exam.
- If the deadline passes, the next kubedrill command grades one last time and
  records `timed out`. A pass within 30 seconds of the deadline still counts
  as `solved`. Time is taken when the command starts, so a `submit` you
  started in time counts even if grading finishes late.

`kubedrill exam CKA` (or `CKAD`, `CKS`) shows your coverage per exam domain.
Details are in [cli.md](cli.md#exam-mode).

## When something fails

- **`cluster kubedrill: missing`:** run `kubedrill up`.
- **`API services unavailable: ...`:** an add-on API such as metrics-server
  is down, and namespace deletion would hang on it. Run `kubedrill up`, which
  reinstalls the add-ons and waits for them.
- **A setup or hook error naming a scenario file:** it is a bug in the
  scenario, not something you did.
- **`check` says the namespace is missing or belongs to another episode:**
  run `kubedrill reset`.
- **An old namespace stuck in `Terminating`:** it does not block the next
  start. `kubectl get ns <ns> -o yaml` shows the blocker under
  `status.conditions`.

Two journals can share one cluster. Each one deletes only its own
namespaces.

## Suggested order

- **L0:** in id order. It builds the observation habit one object at a time.
- **L1:** one module at a time. Master each scenario before the next module.
- **L2 and above:** after the capstone of the previous level. `requires` in a
  scenario lists what to solve first. It is advice, and nothing blocks a
  start.
- **Exam preparation:** check `kubedrill exam CKA`, then rehearse tasks with
  `--exam`.

The levels, hours and planned scenarios are in `curriculum.yaml`; `kubedrill
path` prints them with your progress.
