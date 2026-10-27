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
|-- setup/
|   |-- configmap.yaml   Go text/template manifests, applied in file-name order
|   |-- deployment.yaml
|   |-- service.yaml
|   `-- hook.sh          optional, executable, runs after the manifests
|-- checks.yaml          declarative grading: source, validators, why, depends_on, weight, critical
|-- solution/
|   |-- README.md        walkthrough under the seven troubleshooting-loop headings
|   `-- solve.sh         executable, non-interactive reference fix
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

Notes:

- Unknown keys are rejected.
- `requires`, `addons` and `profile` are parsed but not validated or acted
  on yet (see `docs/roadmap.md`).
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
- The seed picks `variants[(seed-1) mod len(variants)]`. Seeds start at 1.
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
- `instruction.md`, `hints.md`, `solution/README.md` and `story.md`
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
   trimmed and ignoring case, and names the first mismatch.
6. `## Constraints`: names to keep, things not to delete.

## story.md

Optional, and recommended for L0 and for any scenario that introduces an
object for the first time. The ticket told as a story for a learner who has
never used Kubernetes: what happened in plain words, every object the ticket
involves explained from zero, the read-only commands that follow the trail
and what each output means, then the fix and why it works. `kubedrill story`
prints it, `s` in the workstation ticket pane shows it, and the ticket points
at it with `New to Kubernetes? kubedrill story tells this ticket from zero`.
Exam mode hides it.

Sections, in order, with exactly these headings (lint checks them):

1. `# <title>`
2. `## What happened`: the incident in the reporter's terms, and why the
   reported success did not mean it worked.
3. `## The pieces`: each object the ticket involves, top to bottom, with one
   ASCII diagram of the chain from the request to the outcome.
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
- No hint contains `kubectl` followed by a mutating verb: `set`, `edit`,
  `patch`, `apply`, `create`, `replace`, `delete`, `scale`, `label`,
  `annotate`, `expose`, `run`, `autoscale`, `cordon`, `uncordon`, `drain`,
  `taint`, or `rollout` with `undo`, `restart`, `pause` or `resume`. The
  loader rejects one (`"## Hint 4: what to change" contains the mutating
  command "kubectl set"`). Read commands (`get`, `describe`, `logs`,
  `events`, `explain`, `top`, `diff`, `exec`, `debug`) are fine. The
  learner types the change.

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
| `spoiler` | Optional boolean; `true` when the actual value names the root cause |

`why` is printed under the check in `kubedrill check` and `kubedrill watch`
while it fails. Write it as the sentence under a failing production alert:
the consequence (`A Service without a ready endpoint refuses every
connection`), one or two sentences, never the root cause of a variant. It
starts with a capital letter unless the first word is a field name
(`readyReplicas counts ...`). It is printed verbatim, not templated.

`spoiler: true` fits a check whose actual value names the root cause, such
as an image reference or a selector value. The grader copies the flag into
the result, and `check` and `watch` hide the actual value until the first
hint or a recorded diagnosis. `001-storefront-down` sets it on `image-correct`.

Templated fields: `description`, `resource.namespace`, `resource.name`,
`resource.selector`, `path`, `equals`, `not_equals`, `matches`, `contains`,
`gte`, `lte`, `script`, `probe.image` and each `probe.command` element.

### Sources

Each check has exactly one source.

| Source | Fields | Value handed to the validators |
|--------|--------|--------------------------------|
| `resource` | `apiVersion`, `kind`, `namespace`, `name` or `selector` | The object (or List with `selector`), then `path` |
| `probe` | `image` (pinned `image:tag@sha256:...`), `command`, `timeout` (Go duration, at least 1s, default 60s) | Exit code and combined output of a one-shot pod in the scenario namespace |
| `script` | A bash command line | Exit code and combined output of the local command |

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

### Validators

At least one per check. All compare after trimming whitespace.

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
  `path`, `probe` or `script` is an `[err ]` result with the same message
  lint gives.

## setup/ and hook.sh

`setup/` holds `*.yaml` or `*.yml` templates and, optionally, `hook.sh`.
Any other file fails lint; subdirectories are ignored.

On every start, kubedrill:

1. creates a new episode namespace (`<namespace>-<suffix>`);
2. applies the rendered manifests in file-name order with server-side apply
   (field manager `kubedrill`);
3. runs `hook.sh` when present;
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
  namespace is "y"`). Cluster-scoped setup is on the roadmap.
- Stay inside the pod sandbox. Unless `allow_privileged = true`, lint
  rejects a rendered manifest that sets `privileged: true`, `hostNetwork:
  true`, `hostPID: true` or `hostIPC: true`, binds a non-zero `hostPort`,
  declares a `hostPath` volume, lists `capabilities.add`, sets `procMount:
  Unmasked` or an `Unconfined` `seccompProfile` or `appArmorProfile`,
  declares a PersistentVolume with `spec.local`, or names `docker.sock` in
  any string. A scenario that needs one of these, such as a node-level
  drill, sets the key and gets a maintainer review, because smoke applies
  the setup on the contributor's cluster.

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

`hook.sh` and `solution/solve.sh` must be executable. They run with `bash`
from their own directory through `internal/scriptrun`, the runner script
checks use, with the same allowlist (`PATH`, `HOME`, `TMPDIR`, `LANG`, `LC_ALL`, `TERM`, `USER`)
plus:

| Variable | Value |
|----------|-------|
| `TASK_NS` | The episode namespace |
| `SEED` | The episode seed |
| `VARIANT` | The variant name |
| `VAR_<KEY>` | One per `vars` entry, key upper-cased (`VAR_IMAGE`) |
| `KUBEDRILL_BIN` | `hook.sh`, and `solve.sh` under smoke: the path of the running `kubedrill` binary |
| `KUBEDRILL_EPISODE` | `hook.sh`, and `solve.sh` under smoke: the id of the episode |
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
- `namespace`, rendered for every seed: a DNS-1123 label of at most 57
  characters and not a reserved name
- at least two variants for L1 and above

Files:

- `instruction.md`: `## Done when` with one bullet per check, equal to the
  check descriptions in order; `## Before you start` at most 8 lines
- `hints.md`: 3 or 4 non-empty sections with the ladder headings in order,
  no mutating `kubectl` command
- `setup/`: only `*.yaml`, `*.yml` and an executable `hook.sh`; every
  template parses; every Deployment sets `terminationGracePeriodSeconds:
  1`; the sandbox rule unless `allow_privileged = true`. Lint cuts setup
  files with `yamldoc.Split`, the splitter `kube` and the addon loader use,
  so it sees the same documents the start applies. Text after `--- ` that
  is not a comment starts the next document.
- `checks.yaml`: strict YAML (unknown fields rejected), at least one check,
  the field rules above, `why` of 20 to 400 characters, `depends_on`
  targets that exist and form no cycle, `exists` only on a `resource`
  without `path`, `exit_code` only on a `probe` or `script`, `matches` a
  valid RE2 regexp, `spoiler` a boolean, `probe.image` pinned by digest,
  `probe.timeout` a Go duration of at least 1s; on every rendered variant,
  `path` parses as a JSONPath and `gte`/`lte` parse as finite numbers
- `solution/`: `README.md` with the seven sections in order, `solve.sh`
  executable
- `story.md` (when present): the five sections in order; it parses as a
  template
- `approaches/`: only directories, each named in lowercase words joined by
  hyphens, holding exactly a 1 to 8 line `snippet.txt` and a non-empty
  `content.md`; both parse as templates
- template markers (`kubedrill-template`, `Title in the reporter's words`,
  `namespace = "template"`) absent everywhere except `_template`

Across the tree:

- template output is capped at 4 MiB, a `range` over an integer literal
  above 100000 is refused, and a render stops after 1000000 steps (range
  iterations plus `template` calls, even ones that print nothing), so a
  template cannot hang lint or start
- every variant seed (1 to n) renders setup, checks, `instruction.md`,
  `hints.md`, `solution/README.md`, `story.md` and `approaches/`, so an undefined
  `.Vars` key shows up before a learner hits it
- every page under `docs/concepts/` loads (id, title, intro, the four
  sections in order), and every concept a `curriculum.yaml` module lists
  has a page
- `curriculum.yaml`: level and module ids, exam references, no scenario
  listed twice

`kubedrill smoke [refs...]` needs the cluster (`just smoke 000`, or `just
check-ci` for all). For each scenario and each variant seed it:

1. starts an episode in a fresh namespace;
2. asserts that the checks fail right after setup;
3. runs `solution/solve.sh` with the variables above;
4. asserts that every check passes;
5. deletes the namespaces it used.

Smoke uses a throwaway journal with its own journal label, so your episodes
and their namespaces are untouched. It refuses a scenario you have an active
episode of, unless `--force`:

```
an active episode of <id> runs in namespace <ns>; setup hooks may change cluster-wide state, so finish it (kubedrill reset, or kubedrill diagnose "<cause>" then kubedrill solution --yes) or pass --force
```

`solve.sh` output is printed only for failing runs; `-v` prints it for every
run. CI runs the same command on every pull request. `hook.sh`, `solve.sh`
and `script` checks run on the machine that runs smoke, so review them in a
third-party scenario first.

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
marker lines in a real scenario, or lint rejects it.
