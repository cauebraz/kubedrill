# Scenario format

The reference for every file and field of a scenario. For the steps to add
one, see [CONTRIBUTING.md](../CONTRIBUTING.md).

- Every directory under `scenarios/<level>/` is a scenario.
- `scenarios/_template` is the scaffold `kubedrill new` copies.
- `internal/scenario` is the only code that reads the directory.
- `kubedrill lint` reports every problem at once, each line prefixed with
  the file.

## Directory layout

```
scenarios/L0/001-storefront-down/
|-- scenario.toml        metadata, concepts, fix_targets and [[variant]] tables
|-- instruction.md       the ticket: symptom, environment, "## Done when", constraints
|-- hints.md             the hint ladder: 3 or 4 "## Hint N: <rung>" sections
|-- story.md             optional: the ticket told from zero for a learner new to its objects
|-- simulation.yaml      optional: customer meeting, diagram, quiz and guide of the browser workspace
|-- setup/
|   |-- configmap.yaml   Go text/template manifests, applied in file-name order
|   |-- deployment.yaml
|   |-- service.yaml
|   `-- hook.sh          optional, executable, runs after the manifests
|-- checks.yaml          declarative grading: source, validators, why, depends_on, weight, critical
|-- solution/
|   |-- README.md        walkthrough under the seven troubleshooting-loop headings
|   `-- solve.sh         executable, non-interactive reference fix
|-- shortcuts/           optional: wrong fixes smoke proves leave a check failing
|   |-- rollout-restart.sh
|   `-- service-port.sh
`-- approaches/          optional, one directory per valid way to fix
    |-- set-image/
    |   |-- snippet.txt  1 to 8 lines, listed after a solve
    |   `-- content.md   when to choose it and its trade-offs
    `-- fix-manifest/
```

The directory name is the scenario id: three digits, a hyphen and a
kebab-case slug (`^\d{3}-[a-z0-9-]+$`). The parent directory is the level,
`L0` to `L6`. Both must match `scenario.toml`. Ids are unique across levels.

## scenario.toml

Abridged from `001-storefront-down` (one variant left out):

```toml
[scenario]
id = "001-storefront-down"
title = "Storefront is down after the cluster migration"
level = "L0"
module = "L0.1"
type = "break-fix"
minutes = 10
namespace = "shop"
exam = ["CKA:troubleshooting"]
concepts = ["pod-lifecycle", "image-pull", "readiness-and-endpoints"]
fix_targets = ["apps/deployments/storefront"]
images = ["nginx:1.27-alpine@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10"]

[[variant]]
name = "old-registry"
vars = { image = "registry.old-dc.example/mirror/nginx:1.27-alpine" }
root_cause = "The Deployment image still points at the old datacenter's registry mirror registry.old-dc.example, which does not resolve from the new cluster's nodes, so every pull fails."

[[variant]]
name = "typo-tag"
vars = { image = "nginx:1.27-alpnie" }
root_cause = "The Deployment image tag 1.27-alpnie does not exist in docker.io/library/nginx, so every pull fails with not found."
```

| Key | Required | Rule |
|-----|----------|------|
| `id` | yes | Matches the directory name and `^\d{3}-[a-z0-9-]+$` |
| `title` | yes | One line in the reporter's words; never the root cause |
| `level` | yes | `L0` to `L6`, matches the parent directory |
| `module` | yes | A module id from `curriculum.yaml`, for example `L0.1` |
| `type` | yes | `break-fix`, `build`, `design` or `incident` |
| `minutes` | yes | Greater than 0. The expected time at the target level, the limit of a clean solve and the exam time box |
| `namespace` | yes | Base of the episode namespace (see below) |
| `fix_targets` | `break-fix` | Objects a root-cause fix writes (see below) |
| `concepts` | `break-fix`, `build` | Two to four ids of pages under `docs/concepts/`, the most central first; lint enforces at least one and a file per id |
| `requires` | no | Scenario ids that should be solved first |
| `addons` | no | Addon names as `curriculum.yaml` writes them (`metrics-server`) |
| `exam` | no | Entries shaped `CKA:domain`, `CKAD:domain` or `CKS:domain` |
| `profile` | no | A kind profile under `cluster/profiles/` when `base.yaml` is not enough |
| `allow_privileged` | no | Default `false`. `true` lifts the sandbox rule of [setup/](#setup-and-hooksh) and needs maintainer review |
| `restart_is_fix` | no | Default `false`. `true` counts a `kubectl rollout restart` of a fix target as a fix in the debrief verdict, for scenarios where pods read their configuration only at start (env vars, `subPath` mounts); otherwise a restart is a metadata-only change and the verdict says symptom-only |
| `images` | no | Images the fix needs that no setup manifest or probe names with a digest, each `name:tag@sha256:<64 hex>`, for example the image the fixed Deployment runs |

Notes:

- Unknown keys are rejected.
- `requires`, `addons` and `profile` are parsed but not validated or acted
  on by `start` yet; the browser workspace shows `requires` as a lock.
- `images`: `kubedrill up` preloads these, every probe image and every
  setup image pinned by digest on the nodes. An image written
  `name:tag@sha256:...` is pulled by digest and tagged `name:tag` on the
  node, so a fix that writes only the tag (`nginx:1.27-alpine`) starts
  without the registry.
- `namespace`: each episode runs in `<namespace>-<5 random lowercase
  alphanumerics>`. It may use template syntax (`shop-{{ .Seed }}`).
  Rendered, it must be a DNS-1123 label of at most 57 characters, so the
  suffixed name fits the 63 of a label. It must not be `default`,
  `kube-system`, `kube-public`, `kube-node-lease`, `local-path-storage`,
  `kubedrill-debug` (node debugging pods) or any `kube-*` name.
- `fix_targets`: each entry is `group/resource/name`, with the core group
  written `core` and the resource as the lowercase plural
  (`apps/deployments/storefront`, `core/services/storefront`). No duplicates.
  Telemetry compares the learner's writes with this list for the verdict
  (`root-cause` or `symptom-only`).
- `concepts`: `kubedrill learn` prints the pages. Reuse a page when it names
  the mechanism; `kubedrill concepts` lists the existing ones.

Scenario types:

- `break-fix`: deploys a broken state to repair.
- `build`: asks for something new.
- `design`: grades a written answer with a structural rubric.
- `incident`: a timed drill with several simultaneous faults.

## Variants and template data

Each `[[variant]]` is a named root cause or parameter set.

- Names are unique. The learner sees the name only after the episode ends,
  so a descriptive name such as `typo-tag` is fine.
- `vars` is a string-to-string table, reachable as `{{ .Vars.<key> }}`.
- The seed picks the variant by a rendezvous hash of the seed and each
  variant name. Reordering the variants changes no pick, and adding one
  moves only the seeds the new variant wins. Seeds start at 1.
- An episode records its variant name and always renders with it, so a
  variant added or reordered later does not change an episode in progress.
  Removing a variant makes the episodes that ran it fail to render; the
  learner starts a new one.
- Lint and `kubedrill smoke` render each variant with the smallest seed
  that picks it, so the seeds they print are not 1, 2, 3.
- Without any variant the name is `default` and `Vars` is empty.
- L1 and above need at least two variants; L0 may ship one.
- Lint renders every variant, so every variant defines every key a template
  uses.

`root_cause` is the reference diagnosis of the variant. It is one concrete
sentence of 20 to 300 characters, naming the faulty field and the fact that
makes it wrong. Every variant of a `break-fix` scenario needs one, and a
`break-fix` scenario needs at least one variant. Elsewhere it is optional
but checked when set. The debrief prints it next to the learner's
`kubedrill diagnose` text, so write it as the line a senior engineer would
post in the incident channel.

These files are Go `text/template` documents parsed with `missingkey=error`,
so an undefined key fails the render instead of printing an empty string:

- the setup manifests
- the string fields of `checks.yaml` (listed under [checks.yaml](#checksyaml))
- `instruction.md`, `hints.md`, `solution/README.md`, `story.md` and
  `simulation.yaml`
- `approaches/*/snippet.txt` and `approaches/*/content.md`

| Field | Value |
|-------|-------|
| `{{ .Seed }}` | The episode seed, int64, at least 1 |
| `{{ .Namespace }}` | The episode namespace: rendered `namespace` plus its suffix (`shop-k3x9p`) |
| `{{ .Variant }}` | The selected variant name |
| `{{ .Vars.key }}` | The variant's `vars.key` |

Write `{{ .Namespace }}` wherever a file names the namespace: the ticket,
hints, walkthrough commands, `metadata.namespace` and check `namespace`.
Never write the literal value, because the suffix changes every episode.
The walkthrough a learner reads afterwards is rendered with that episode's
seed and namespace.

## instruction.md

The ticket, printed by `kubedrill start` and `kubedrill ticket`. It is
written in the reporter's words: symptoms, expectations and what changed as
far as the reporter knows. It never names the root cause.

Sections, in order:

1. `# <title>`
2. `## Ticket`: a quote with `From`, `Priority` and the report.
3. `## Before you start` (optional): one mental model the learner needs, at
   most 8 lines.
4. `## Environment`: the namespace as `{{ .Namespace }}`, the resources and
   what the learner has access to.
5. `## Done when` (required): one bullet per check, in `checks.yaml` order,
   with the same text as the check `description`. Lint compares them
   trimmed and ignoring case, and names every mismatch. A `#` line inside a
   fenced code block is a comment, not a heading, so it does not end the
   section.
6. `## Constraints`: names to keep, things not to delete.

## story.md

Optional, and recommended for L0 and for any scenario that introduces an
object for the first time. The ticket told as a story for a learner who has
never used Kubernetes: what happened in plain words, every object the ticket
involves explained from zero, the read-only commands that follow the trail
and what each output means, then the fix and why it works. `kubedrill story`
prints it, the Story tab of the page shows it, and the ticket points
at it with `New to Kubernetes? kubedrill story tells this ticket from zero`.
Exam mode hides it.

Sections, in order, with exactly these headings (lint checks them):

1. `# <title>`
2. `## What happened`: the incident in the reporter's terms, and why the
   reported success did not mean it worked.
3. `## The pieces`: each object the ticket involves, top to bottom, with one
   diagram of the chain from the request to the outcome, written as a
   ` ```mermaid ` `flowchart TD` block: one node per object, `<b>name</b>`
   then `<br/>` and what it does, `-->` between them. The workspace renders
   it with the theme of the page, GitHub renders it in the repository, the
   terminal (`kubedrill story`, `kubedrill learn`) draws it as a text chain,
   and a block that does not parse falls back to its source text.
4. `## Following the trail`: the read-only commands in the order of the
   troubleshooting loop, what each output field means, and how to get an
   object name without typing it (TAB completion, `-l` selectors, copy).
5. `## The fix and why it works`: the change, the chain of effects that makes
   it work, and why the tempting wrong moves do not.
6. `## Check your work`: the verification commands and the habit to keep.

The story must hold for every variant: it names the observation that tells
the variants apart (an Event message, a status reason) and lets the
learner's cluster answer which one this episode runs. Unlike `hints.md` it
may show the mutating command of the fix, so a learner who reads it before
the ticket gets the method and the vocabulary, and still has to observe.

## simulation.yaml

Optional, and recommended for L0 and L1. It feeds the stages of the browser
workspace (`kubedrill web`, see [cli.md](cli.md#web)) that run before and
around the episode. Without it the workspace skips the meeting and the
quiz. The file is a template like the others, so `{{ .Namespace }}` and
`{{ .Vars.key }}` work; everything in it must hold for every variant except
the `guide`, which runs on the first variant only (the DIY run picks
another one).

```yaml
customer:            # who opened the ticket
  name: Dana
  role: Storefront team lead
  company: ACME Retail
mentor:
  intro: >-          # first message of the mentor panel (optional)
    Ask me for a hint whenever you are stuck.
meeting:
  opening: >-        # the customer's first line
    The storefront has been down since this morning.
  turns:             # at least 2
    - prompt: What do you ask first?
      choices:       # at least 2, exactly one best: true; the page shuffles them
        - say: What changed right before it stopped working?
          reply: We moved it to the new cluster this morning.
          feedback: A failure that starts after a change usually comes from it.
          best: true
          fact: It broke right after the move to the new cluster.   # goes on the requirements board
        - say: Can you restart the pods?
          reply: We did, they came back the same.
          feedback: Acting before observing hides evidence.
  closing: Ping me when it is back.
diagram:             # the Learn stage; 2 to 9 nodes, laid out top to bottom
  nodes:
    - {id: deploy, label: Deployment storefront, note: keeps 3 pods}
    - {id: pods, label: Pods x3, column: 1}   # column (optional) orders nodes inside a row
  edges:
    - {from: deploy, to: pods, label: creates}
quiz:                # at least 2 questions, exactly one correct choice each
  - question: Every pod shows ImagePullBackOff. What does that rule out?
    choices:
      - text: A crash or a failing probe, because no container started.
        correct: true
        explain: The pull happens before the container exists.
      - text: Nothing.
        explain: A pod that never starts a container cannot crash.
guide:               # at least 3 steps of the guided practice
  - title: List the pods
    body: Read the STATUS column.            # markdown
    command: kubectl get pods                 # optional, shown with Type it
  - title: Change the image
    body: Change one thing.
    command: kubectl set image deployment/storefront web=nginx:1.27-alpine
    check: image-correct                      # optional: green when this check passes
```

- The meeting rewards the questions of the troubleshooting loop: what
  changed, the exact symptom, the reference that works, what done means.
  Each wrong choice gets a reply and feedback that say why it helps less;
  the learner can pick again.
- The guide may show mutating commands, unlike `hints.md`: it is the
  practice run, and the DIY run plants a different cause.
- A step without `check` is marked done by the learner; a step whose
  `check` passes turns green with every step before it.
- Unknown keys are errors (strict YAML).
- The diagram puts each node one row below its deepest parent; a node
  with no incoming edge sits one row above its nearest child, so arrows
  stay short. An arrow that still skips rows is routed around the boxes
  it passes. Inside a row, nodes follow `column` (lower first) when any
  node of the row sets it, otherwise the order that crosses the fewest
  arrows. Labels sit below the source box and never cover a box. More
  than 9 nodes is an error: split the story instead.
- Writing choices: a wrong choice is a plausible move that reveals a wrong
  mental model (acting before observing, scaling a failure, blaming a
  layer the symptom rules out), its reply still gives a real fact, and its
  feedback names what a better question learns. A quiz distractor is a
  belief a learner actually holds, and its `explain` corrects it.
- `kubedrill draft-guide <ref>` drafts the guide from the solution's
  Observation, Change and Verify commands, and `kubedrill draft-guide <ref>
  --check` lists guide commands the walkthrough never runs (see
  [cli.md](cli.md#draft-guide)).
- `kubedrill web --preview <ref>` opens the assignment without the cluster
  and rereads the files on every request, so the meeting, quiz and diagram
  can be written with the page open.

## hints.md

The hint ladder: three or four sections with exactly these headings, in
this order. The fourth is optional.

| Heading | Content |
|---------|---------|
| `## Hint 1: where to look` | The subsystem or object, and a read command that lists it |
| `## Hint 2: what to observe` | The field, column or event line that explains the symptom, and the read command that shows it; no interpretation |
| `## Hint 3: what it means` | The interpretation of that observation |
| `## Hint 4: what to change` | The object and field to change, without the exact value or a copy-paste command |

- A hint is shown without its heading, trimmed. No hint may be empty.
- `kubedrill hint` labels each one with its rung (`hint 2 of 4 (what to
  observe)`).
- The last hint waits for a recorded diagnosis (`kubedrill diagnose`),
  because it names what to change.
- No hint contains `kubectl` (or its workstation alias `k`) followed by a
  mutating verb: `set`, `edit`,
  `patch`, `apply`, `create`, `replace`, `delete`, `scale`, `label`,
  `annotate`, `expose`, `run`, `autoscale`, `cordon`, `uncordon`, `drain`,
  `taint`, or `rollout` with `undo`, `restart`, `pause` or `resume`. The
  loader rejects one (`"## Hint 4: what to change" contains the mutating
  command "kubectl set"`). Read commands (`get`, `describe`, `logs`,
  `events`, `explain`, `top`, `diff`, `exec`, `debug`) are fine. The
  learner types the change. Lint applies the rule and the ladder headings
  to hints.md as rendered for every variant too, so a variable cannot carry
  a mutating command. A `## Hint N` line inside a fenced code block is not
  a hint.

## checks.yaml

```yaml
checks:
  - id: ready-replicas
    description: deployment storefront has 3 ready replicas
    why: readyReplicas counts pods that pass readiness; replicas that are Running but not Ready receive no traffic, so this is the number users feel.
    depends_on: [deploy-exists]
    weight: 2
    resource:
      apiVersion: apps/v1
      kind: Deployment
      namespace: "{{ .Namespace }}"
      name: storefront
    path: "{.status.readyReplicas}"
    equals: "3"
```

Checks assert outcomes (ready replicas, endpoints, a response code), never
command history, so several valid fixes pass.

### How grading works

- The grader runs the checks as a DAG in `depends_on` order.
- A check whose dependency did not pass is `[skip]`.
- The score is the weighted fraction of passed checks, in `[0, 1]`.
- A grader problem (API error, bad JSONPath, script error) is an `[err ]`
  result, never a crash. It counts as not passed.
- A `critical` check that fails, errors or is skipped forces the score to 0.
- Every check has a 30 second budget. A probe check gets
  `max(30s, probe.timeout + 30s)`.
- A probe or script check that expects success (no `exit_code`, or
  `exit_code: 0`) and exits non-zero runs once more after 3 seconds, when
  at least 6 seconds of its budget remain: an endpoint that became ready
  just before the check can refuse connections until kube-proxy syncs, and
  the second run tells that apart from a Service that is broken. The second
  result is the one graded, unless the second run itself errors (a killed
  script, an API error), which keeps the first. A check that expects a
  non-zero exit, exits 0 with the wrong output, or errors on the first run
  runs once. A script check therefore needs no retry loop of its own for
  kube-proxy or endpoint propagation.
- A probe fails at once, without waiting for its timeout, on
  `InvalidImageName`, `ErrImageNeverPull`, `CreateContainerConfigError`
  (with the kubelet message) and on a Forbidden or Unauthorized answer.
  A probe that times out names the last error it saw (`last error: ...`).
- A script check runs in its own process group. The group is killed when
  the script exits, so a background child (`kubectl port-forward ... &`)
  never outlives the check and never turns a passing check into an error.
  Its output is capped at 64 KiB.

### Check fields

| Field | Rule |
|-------|------|
| `id` | Required, unique, `^[a-z0-9][a-z0-9-]*$` |
| `description` | Required, learner-facing, states the expected outcome |
| `why` | Required, 20 to 400 characters (see below) |
| `depends_on` | Optional list of existing check ids; no self reference, no cycles |
| `weight` | Optional, default 1, never negative |
| `critical` | Optional; when it does not pass, the score is 0 |
| `spoiler` | Optional boolean; `true` when the expected or actual value names the root cause or the fix |

`why` is printed under the check in `kubedrill check` and in the live checks
of the page while it fails. Write it as the sentence under a failing production alert:
the consequence (`A Service without a ready endpoint refuses every
connection`), one or two sentences, never the root cause of a variant. It
starts with a capital letter unless the first word is a field name
(`readyReplicas counts ...`). It is printed verbatim, not templated.

`spoiler: true` fits a check whose expected or actual value names the root
cause or the fix, such as an image reference, a selector value or the regex
the fix must match. The grader copies the flag into the result; while the
run goes on, `check` prints `expected and actual values hidden until the run
ends` in place of both values, `check --explain` hides the validators, and
the page's live checks and failing Validate receive neither value. The
grade of a submitted or expired exam shows both. `001-storefront-down` sets it
on `image-correct`.

Templated fields: `description`, `resource.namespace`, `resource.name`,
`resource.selector`, `path`, `equals`, `not_equals`, `matches`, `contains`,
`gte`, `lte`, `script`, `rollout.namespace`, `rollout.name`, `probe.image`
and each `probe.command` element.

### Sources

Each check has exactly one source.

| Source | Fields | Value handed to the validators |
|--------|--------|--------------------------------|
| `resource` | `apiVersion`, `kind`, `namespace`, `name` or `selector` | The object (or List with `selector`), then `path` |
| `probe` | `image` (pinned `image:tag@sha256:...`), `command`, `timeout` (Go duration, at least 1s, default 60s) | Exit code and combined output of a one-shot pod in the scenario namespace |
| `script` | A bash command line | Exit code and combined output of the local command |
| `rollout` | `kind` (`Deployment` or `StatefulSet`), `namespace`, `name` | None: the source is its own assertion and takes no validator |

`resource`:

- `name` and `selector` are mutually exclusive.
- An empty `namespace` means the scenario namespace.
- With `selector` the value is a List, so `path` starts at `{.items...}`.
- `path` is a JSONPath in the `kubectl` dialect, valid only with
  `resource`. A path that selects nothing reports `actual: no value at
  <path>`.
- Several JSONPath results are joined with a space for `equals`, `matches`
  and `contains`, and counted for `not_empty`.

`probe`: a pod named `kubedrill-probe-<suffix>`.

- Labeled `app.kubernetes.io/managed-by=kubedrill`, `restartPolicy: Never`,
  `activeDeadlineSeconds` equal to `timeout`, deleted when the check ends.
- Hardened: `automountServiceAccountToken: false`, `runAsNonRoot: true`
  with `runAsUser: 65532`, `allowPrivilegeEscalation: false`, all
  capabilities dropped, `seccompProfile: RuntimeDefault`, requests `10m`
  CPU and `16Mi` memory, limit `64Mi` memory.
- So the image must run as non-root without capabilities.
  `curlimages/curl` and distroless images do. Stock `busybox` or `nginx`
  entrypoints that need root fail to start.
- Logs are capped at 64 KiB.
- The probe has no cluster credentials: it tests the network datapath, not
  the API.

`script`: the escape hatch when `resource` and `probe` cannot express the
outcome.

- Runs with `bash -c` from the scenario directory, with `kubectl` on `PATH`.
- Gets an allowlisted environment: `PATH`, `HOME`, `TMPDIR`, `LANG`,
  `LC_ALL`, `TERM` and `USER` from the process, plus `TASK_NS`, `SEED`,
  `VARIANT`, `VAR_<KEY>` and `KUBECONFIG` ([table below](#script-environment)).
  `KUBEDRILL_BIN` and `KUBEDRILL_EPISODE` are not passed.
- The first `kubectl` on `PATH` is a wrapper, `<cluster state
  dir>/bin/kubectl` (`grader.WriteKubectlWrapper`). It runs the real
  kubectl with argv[0] `kubedrill-check`, so the user agent tags the
  requests in the audit log and telemetry drops them: grading never counts
  as the learner's reads. When the wrapper cannot be written, kubedrill
  prints a warning and the plain kubectl runs.
- Call `kubectl` by name. An absolute path skips the wrapper and the
  requests count as the learner's.

`rollout`: the workload finished rolling out its current pod template.

```yaml
  - id: api-rolled-out
    description: "deployment api finished rolling out: every pod runs the current template"
    why: Old pods that are still healthy keep readyReplicas up while the new template crash-loops; only a finished rollout proves the change runs.
    depends_on: [api-ready]
    rollout:
      kind: Deployment
      namespace: "{{ .Namespace }}"
      name: api
```

- Passes when all four hold, read from the `apps/v1` object:
  `status.observedGeneration >= metadata.generation` (the controller saw
  the latest template), `status.updatedReplicas`, `status.availableReplicas`
  and `status.replicas` each equal to `spec.replicas` (default 1). The last
  one means no pod of an old template is left.
- Why it exists: a Deployment rolls out a new template by surging new pods
  before it removes old ones. When the new template is broken (an image tag
  that does not exist, a crash-looping command), the old pods stay Running
  and Ready, so `readyReplicas` and the Service endpoints look healthy while
  the rollout is stuck. A check on `readyReplicas` alone passes after a fix
  followed by a broken edit; the rollout check does not.
- A failure reads `expected: 2 updated and available replicas of generation
  3, 2 in total  actual: 1 updated, 2 available, 3 in total, observed
  generation 3`. A missing workload is `expected: exists  actual: not
  found`.
- An empty `namespace` means the scenario namespace.
- Fits every scenario whose fix changes a Deployment or StatefulSet pod
  template. Put it after the ready check with `depends_on` on it, so it
  adds no information about the variant and fails, or is skipped, right
  after setup.
- A StatefulSet with `updateStrategy: OnDelete` never updates its pods on
  its own, so the check fails until the pods are deleted.

### Validators

At least one per check, except a `rollout` check, which takes none. All compare after trimming whitespace.

| Validator | Passes when | Valid with |
|-----------|-------------|------------|
| `exists` | `true`: the object (or at least one selector match) exists; `false`: it does not | `resource` without `path` only (use `not_empty` for a path) |
| `equals` | value equals the string | any |
| `not_equals` | value differs from the string | any |
| `matches` | value matches the RE2 regular expression | any |
| `contains` | value contains the substring | any |
| `gte` | value and bound parse as numbers, value >= bound | any |
| `lte` | value and bound parse as numbers, value <= bound | any |
| `not_empty` | at least one non-empty value | any |
| `exit_code` | the process exited with this code | `probe`, `script` |

- Validators run in table order. The first failure sets `expected` and
  `actual` and stops.
- A passing result carries the actual value (`exit code N` for a probe or
  script without output).
- `exists` is decided at the object level, before any value is read:
  `expected: exists  actual: not found`, or `expected: absent  actual:
  exists`. With a `selector`: `expected: at least one matching object
  actual: 0 matching objects`, and for `exists: false`, `expected: no
  matching objects  actual: N matching objects`.
- A `checks.yaml` that lint never saw and that combines `exists` with
  `path`, `probe` or `script`, or a `rollout` check with a validator, is an
  `[err ]` result with the same message lint gives.

## setup/ and hook.sh

`setup/` holds `*.yaml` or `*.yml` templates and, optionally, `hook.sh`.
Any other file fails lint; subdirectories are ignored.

On every start, kubedrill:

1. creates a new episode namespace (`<namespace>-<suffix>`);
2. applies the rendered manifests in file-name order with server-side apply
   (field manager `kubedrill`);
3. runs `hook.sh` when present; its output is not printed (the learner's
   terminal and the `kubedrill web` server log stay clean), and a failed
   hook's error ends with the last 4 KiB of it;
4. deletes the previous episode namespaces of the same journal without
   waiting, so the learner never waits for a namespace to terminate.

Rules for manifests:

- Pin images as `image:tag@sha256:<digest>`, except when the tag is the bug.
- Every Deployment sets `spec.template.spec.terminationGracePeriodSeconds:
  1`. Lint rejects one without it (`Deployment "hello":
  spec.template.spec.terminationGracePeriodSeconds is unset (default 30),
  want 1 so the reset does not wait for old pods`). Set the same field on
  StatefulSet, Job and DaemonSet pod templates; lint checks Deployments
  only.
- Every object has `metadata.name`. Objects are applied by name, so
  `generateName` alone is refused (`has no metadata.name (generateName is
  not supported: objects are applied by name)`).
- Write `metadata.namespace: {{ .Namespace }}` in every manifest. An object
  without it lands in the episode namespace, but the explicit form keeps the
  file readable on its own.
- Only namespaced objects in the episode namespace. A cluster-scoped kind
  (Namespace, ClusterRole, CRD, PriorityClass, Node) fails the start
  (`cluster-scoped <Kind> <name> is not allowed in a scenario setup`), and
  so does another namespace (`object names namespace "x" but the scenario
  namespace is "y"`). Cluster-scoped setup is not supported yet.
- Stay inside the pod sandbox. Unless `allow_privileged = true`, lint
  rejects a rendered manifest that sets `privileged: true`, `hostNetwork:
  true`, `hostPID: true` or `hostIPC: true`, binds a non-zero `hostPort`,
  declares a `hostPath` volume, lists `capabilities.add`, sets `procMount:
  Unmasked` or an `Unconfined` `seccompProfile` or `appArmorProfile`,
  declares a PersistentVolume with `spec.local`, or names `docker.sock` in
  any string. A scenario that needs one of these, such as a node-level
  drill, sets the key and gets a maintainer review, because smoke applies
  the setup on the contributor's cluster. `start` repeats the check on the
  rendered manifests and labels the episode namespace with the Pod
  Security `baseline` level (`pod-security.kubernetes.io/enforce`), so a
  pod the learner creates there is held to the same sandbox.

The sandbox error reads:

```
setup/pod.yaml: Pod "p": spec.hostNetwork is true; the listed fields escape the pod sandbox and are refused unless scenario.toml sets allow_privileged = true, which needs maintainer review
```

### Namespace ownership

The episode namespace carries the labels
`app.kubernetes.io/managed-by=kubedrill`, `kubedrill.io/scenario=<id>`,
`kubedrill.io/seed=<seed>` and `kubedrill.io/journal=<journal id>`.

- Old namespaces are deleted only when they carry the managed-by and the
  journal labels.
- An existing namespace with the episode name but without the managed-by
  label is never removed (`refuse to reset namespace <ns>: it was not
  created by kubedrill`).

### Node faults

A hook that needs a node-level fault calls the running binary:
`"$KUBEDRILL_BIN" fault stop-kubelet worker`.

- Actions: `pause`, `unpause`, `stop-kubelet`, `start-kubelet`, `netem`
  (`--delay 200ms --loss 5`) and `clear-netem`.
- Nodes: `control-plane`, `worker` or `worker2`.
- Faults are recorded in `<cluster state dir>/faults.json`. The next
  `start` and `kubedrill heal` undo them, so a scenario never leaves a
  broken node behind.
- A fault call cut short (Ctrl-C, timeout) keeps its record, because
  Docker may still apply it; the error ends with `run kubedrill heal`.
- `netem` needs the `sch_netem` module in the Docker VM kernel and fails
  with `tc netem is not available in this Docker VM kernel` without it.

### Script environment

`hook.sh`, `solution/solve.sh` and `shortcuts/*.sh` must be executable. They run with `bash`
from their own directory through `internal/scriptrun`, the runner script
checks use, with the same allowlist (`PATH`, `HOME`, `TMPDIR`, `LANG`, `LC_ALL`, `TERM`, `USER`)
plus:

| Variable | Value |
|----------|-------|
| `TASK_NS` | The episode namespace |
| `SEED` | The episode seed |
| `VARIANT` | The variant name |
| `VAR_<KEY>` | One per `vars` entry, key upper-cased (`VAR_IMAGE`) |
| `KUBEDRILL_BIN` | `hook.sh`, and `solve.sh` and shortcuts under smoke: the path of the running `kubedrill` binary |
| `KUBEDRILL_EPISODE` | `hook.sh`, and `solve.sh` and shortcuts under smoke: the id of the episode |
| `KUBECONFIG` | A pinned kubeconfig holding only the `kind-kubedrill` context, its cluster and its user, at `<cluster state dir>/kubeconfig-scripts` (`cluster.ScriptKubeconfigPath()`, mode 0600) |

- The pinned file is rewritten from the learner's kubeconfig on every
  command that runs scripts, so a script cannot reach another context by
  name.
- When the cluster does not exist yet, the scripts receive the original
  kubeconfig path. `start` checks the cluster first and stops with
  `cluster kubedrill: missing`.
- Script checks always get the pinned path. When it cannot be written, the
  command stops with `script checks need a kubeconfig pinned to that
  context`; it never falls back to your own kubeconfig.
- Scripts are bash 3.2 compatible (macOS default): no `mapfile`, no
  associative arrays, no `${var,,}`.

## solution/

### README.md

The walkthrough. It is printed when the learner surrenders, and by
`kubedrill solution <id>` once the latest episode of the scenario ended
solved or abandoned (before that it asks for confirmation). It is rendered
for the episode's seed and namespace and follows the troubleshooting loop of
`docs/concepts/troubleshooting-loop.md`.

The sections are required, exactly these, in this order:

1. `# Solution - <id> <title>`
2. `## Symptom`: what the reporter sees, and why the obvious signal (a green
   pipeline, a Running pod) did not catch it.
3. `## Candidate causes`: the causes that fit the symptom, each with the
   trace it would leave.
4. `## Observation that splits them`: the read commands that rule all but
   one cause out, with the output line that matters, per variant when it
   differs. `001-storefront-down` runs `kubectl -n {{ .Namespace }} get events
   --sort-by=.lastTimestamp` and `kubectl -n {{ .Namespace }} describe pod
   <pod-name>`.
5. `## Change`: one change in a fenced code block, with a second valid path
   when one exists.
6. `## Verify`: the commands that show the symptom is gone, in a fenced
   code block.
7. `## In the real world`: how this reaches production and what catches it.
8. `## Go deeper`: upstream documentation links.

The CLI reads these headings:

- On a later exposure it stops before `## Observation that splits them` to
  ask which principle made that observation necessary.
- `Your path vs the reference` lists the command lines of the code blocks
  under `## Change` and `## Verify` next to the learner's kubectl commands.
- Lint ignores headings inside fenced code blocks.

### solve.sh

The reference fix, run by `kubedrill smoke` and CI. It is executable,
non-interactive and bash 3.2 compatible. It starts with `: "${TASK_NS:?}"
"${KUBECONFIG:?}"` and makes every check pass for every variant, branching
on `VARIANT` or `VAR_<KEY>` when the fix differs.

## shortcuts/

Optional. One executable bash script per wrong or symptom-only fix a
learner would plausibly try, named `<kebab-name>.sh`
(`rollout-restart.sh`, `remove-liveness-probe.sh`). `kubedrill smoke` runs
each one on a fresh setup and fails when every check passes afterwards, so
a check that a later edit weakens shows up before a learner finds the
bypass.

The comment lines right after the shebang are the header:

```bash
#!/usr/bin/env bash
# why: a restart recreates the pods from the same template, which still names an image no node can pull
# variants: typo-tag
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

kubectl -n "$TASK_NS" rollout restart deployment/storefront
kubectl -n "$TASK_NS" rollout status deployment/storefront --timeout=20s || true
```

- `# why:` is required, once, 10 to 200 characters: what the shortcut gets
  wrong. Smoke prints it when the shortcut passes every check.
- `# variants:` is optional, at most once: comma-separated variant names
  from `scenario.toml`. Without it the shortcut runs on every variant.
  Most shortcuts are wrong for one root cause only (scaling a stale pod to
  0 and back is the fix of `012-stale-configmap` variant `env-var`), so
  name the variant the shortcut targets.
- The header ends at the first line that is not a comment.
- A shortcut runs like `solve.sh`, with the variables above. It waits for
  the state it produces (`rollout status`, a fixed sleep the checks need),
  so a check fails because the fix is wrong, not because the grade came
  too early. A command that fails is a broken shortcut and fails smoke;
  append `|| true` to a wait that is expected to time out.
- `just shellcheck` covers the scripts.

## approaches/

Optional. One directory per valid way to fix the scenario, named in
lowercase words joined by hyphens (`set-image`, `fix-manifest`). Each holds
exactly two files:

- `snippet.txt`: 1 to 8 lines, the commands or the diff of that approach.
  `kubedrill solution` lists every approach name with its snippet after the
  walkthrough.
- `content.md`: when to choose this approach and its trade-offs, a short
  paragraph or two. `kubedrill solution --approach <name>` prints it.

Both are templates, so a snippet names the namespace with `-n {{ .Namespace
}}`: `kubectl -n {{ .Namespace }} set image deployment/storefront
web=nginx:1.27-alpine`.

## Packs

A pack ships scenarios outside this repository. Its root holds `pack.yaml`
and `scenarios/<level>/<id>/` directories in the format above:

```yaml
name: incidents
title: Incidents from the on-call rota
source: https://example.com/postmortems
authors: [alice]
modules:
  - level: L2
    id: L2.incidents
    title: Incidents
    summary: Tickets from the rota
    exam: ["CKA:troubleshooting"]
    concepts: [troubleshooting-loop]
    scenarios:
      - id: 101-coredns-oom
        ticket: CoreDNS OOMKilled under a retry storm
        type: break-fix
        feasibility: kind
```

`name` matches `^[a-z0-9]+(-[a-z0-9]+)*$` and becomes the install
directory; `source` and `authors` are optional; each module names an
existing level, has an id that starts with it and is unique across the
ladder, and lists only scenario ids that exist in the pack. Each
scenario's `scenario.toml` sets `module` to one of the pack's module ids.
`kubedrill pack add` lints the pack with the rules below before enabling
it; scenario ids must not collide with the catalog, and `allow_privileged =
true` needs `--allow-privileged`. Pack scripts run on the learner's
machine, so `pack add` lists them and asks first ([cli.md](cli.md#pack)).

## What lint and smoke enforce

`kubedrill lint` (part of `just check`) loads every scenario and the
template and reports every problem.

`scenario.toml`:

- required keys, patterns, `type` and `level` values, `minutes > 0`, no
  unknown keys, unique variant names, id and level matching the
  directories, unique ids across the tree
- `module` is a module id of `curriculum.yaml`
- `break-fix`: at least one variant, `root_cause` on every variant (20 to
  300 characters, also checked when set on other types) and at least one
  `fix_targets` entry, each a valid `group/resource/name`, no duplicates
- `concepts` present for `break-fix` and `build`, every id matching
  `^[a-z0-9]+(-[a-z0-9]+)*$` with a file `docs/concepts/<id>.md`
- `allow_privileged` is a boolean
- every `images` entry is `name:tag@sha256:<64 hex>`
- `namespace`, rendered for every seed: a DNS-1123 label of at most 57
  characters and not a reserved name
- at least two variants for L1 and above

Files:

- `instruction.md`: `## Done when` with one bullet per check, equal to the
  check descriptions in order; `## Before you start` at most 8 lines
- `hints.md`: 3 or 4 non-empty sections with the ladder headings in order,
  no mutating `kubectl` or `k` command, raw and rendered for every variant
- `setup/`: only `*.yaml`, `*.yml` and an executable `hook.sh`; every
  template parses; every Deployment sets `terminationGracePeriodSeconds:
  1`; the sandbox rule unless `allow_privileged = true`; every container
  image pinned as `image:tag@sha256:<digest>`, except an image that is the
  value of a variant variable (the bug the variant plants). Lint cuts setup
  files with `yamldoc.Split`, the splitter `kube` and the addon loader use,
  so it sees the same documents the start applies. Text after `--- ` that
  is not a comment starts the next document.
- `checks.yaml`: strict YAML (unknown fields rejected), at least one check,
  the field rules above, `why` of 20 to 400 characters, `depends_on`
  targets that exist and form no cycle, `exists` only on a `resource`
  without `path`, `exit_code` only on a `probe` or `script`, no validator
  on a `rollout`, `rollout.kind` `Deployment` or `StatefulSet` with a
  `rollout.name`, `matches` a
  valid RE2 regexp, `spoiler` a boolean, `probe.image` pinned by digest,
  `probe.timeout` a Go duration of at least 1s; on every rendered variant,
  `path` parses as a JSONPath and `gte`/`lte` parse as finite numbers
- `solution/`: `README.md` with the seven sections in order, `solve.sh`
  executable
- `shortcuts/` (when present): only executable regular files named
  `<kebab-name>.sh`, each starting with a bash shebang and a header with
  one `# why:` line of 10 to 200 characters and at most one `# variants:`
  line naming declared variants, each once
- `story.md` (when present): the five sections in order; it parses as a
  template
- `simulation.yaml` (when present): parses as a template and, rendered for
  the first variant, as strict YAML; a customer name and role, an opening
  and a closing, at least 2 meeting turns with at least 2 choices and
  exactly one best choice (with a `fact`) each, every choice with `say`,
  `reply` and `feedback`; 2 to 9 diagram nodes with unique lowercase ids
  and edges between them; at least 2 quiz questions with exactly one
  correct choice and an `explain` on every choice; at least 3 guide steps
  with a title and a body, each `check` naming a check of `checks.yaml`.
  The same rules run again on the rendering of every variant. Messages
  quote the start of the line they are about, for example `meeting turn 2
  choice 1 ("Can you restart the pods?") needs say, reply and feedback`
- `approaches/`: only directories, each named in lowercase words joined by
  hyphens, holding exactly a 1 to 8 line `snippet.txt` and a non-empty
  `content.md`; both parse as templates
- template markers (`kubedrill-template`, `Title in the reporter's words`,
  `namespace = "template"`) absent everywhere except `_template`, including
  the template's `shortcuts/rollout-restart.sh`

Across the tree:

- template output is capped at 4 MiB, a `range` over an integer literal
  above 100000 is refused, and a render stops after 1000000 steps (range
  iterations plus `template` calls, even ones that print nothing), so a
  template cannot hang lint or start
- one seed per variant (the smallest seed that picks it) renders setup, checks, `instruction.md`,
  `hints.md`, `solution/README.md`, `story.md`, `simulation.yaml` and `approaches/`, so an undefined
  `.Vars` key shows up before a learner hits it
- every page under `docs/concepts/` loads (id, title, intro, the four
  sections in order), and every concept a `curriculum.yaml` module lists
  has a page
- `curriculum.yaml`: level and module ids, exam references, no scenario
  listed twice
- every scenario `exam` entry is a domain of the `curriculum.yaml` exams
- every scenario is listed in `curriculum.yaml` (an `id:` on a planned
  scenario) under the module its `scenario.module` names, and every `id:`
  in `curriculum.yaml` has a scenario directory

`kubedrill smoke [refs...]` needs the cluster (`just smoke 001`, or `just
check-ci` for all). For each scenario and each variant seed it:

1. starts an episode in a fresh namespace;
2. asserts that the checks fail right after setup;
3. runs `solution/solve.sh` with the variables above;
4. asserts that every check passes;
5. for each shortcut that applies to the variant, starts another episode,
   runs the shortcut and asserts that at least one check does not pass,
   printing the checks that still fail;
6. deletes the namespaces it used.

A shortcut that passes every check fails the run with
`shortcut <name> passed every check: <why>`; either the checks miss the
bypass or the shortcut is a valid fix and belongs in `approaches/`.
`--no-shortcuts` skips step 5 for a quick local run; CI runs them. Each
run line ends with its duration, and the summary line with the total.

Smoke uses a throwaway journal with its own journal label, so your episodes
and their namespaces are untouched. It refuses a scenario you have an active
episode of, unless `--force`:

```
an active episode of <id> runs in namespace <ns>; setup hooks may change cluster-wide state, so finish it (kubedrill reset, or kubedrill diagnose "<cause>" then kubedrill solution --yes) or pass --force
```

`solve.sh` and shortcut output is printed only for failing runs; `-v`
prints it for every run. CI runs the same command on every pull request.
`hook.sh`, `solve.sh`, shortcuts and `script` checks run on the machine that
runs smoke, so review them in a third-party scenario first.

## Concept pages

`concepts` in `scenario.toml` points at pages under `docs/concepts/`, one
file per id. The loader validates the intro paragraph and four headings
(`The mechanism`, `What you see on call`, `Common failures`, `Go deeper`).
The format, the writing rules and the list of pages are in
[docs/concepts/README.md](concepts/README.md).

## Scaffolding

```sh
kubedrill new L0 011-slug
just new-scenario L0 011 slug
```

Both copy `scenarios/_template` to `scenarios/L0/011-slug/`, rewrite `id`
and `level` in `scenario.toml`, and print the next steps: edit the files,
`kubedrill lint`, `kubedrill smoke 011-slug`. The destination must not
exist. The template's comments explain each file. Delete them and the
marker lines in a real scenario, or lint rejects it. The template includes
`simulation.yaml`; delete it for a lab-only scenario, or replace its
placeholders (`kubedrill draft-guide <id>` drafts the guide once the
solution exists).
