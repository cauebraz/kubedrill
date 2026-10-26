# AGENTS.md

Conventions for anyone (human or agent) changing this repository.

## What this is

A local, offline Kubernetes training environment for one learner: a Go binary
(`kubedrill`) drives a long-lived kind cluster, starts each episode in a fresh
namespace, and grades the end state with declarative checks. The loop is
reset -> observe -> act (kubectl) -> grade -> feedback (debrief), modeled on
RL environments but designed for a human; the troubleshooting loop (describe,
candidate causes, splitting observation, change, verify) is the spine of every
walkthrough. Every decision favors the learner's experience: reset speed,
clarity of the ticket, feedback that names expected vs actual, freedom of
method (end state is graded, never the commands used). Telemetry (audit log,
shell log, recordings) feeds the debrief and mastery, never the grade, and
stays on the learner's machine.

## Layout

```
cmd/kubedrill/            composition root and main; telemetry reads the cluster audit log and the journal's shell log and recordings
internal/scenario/        on-disk scenario format: scenario.toml, instruction.md, hints.md, setup/, checks.yaml, solution/, approaches/
internal/grader/          runs checks.yaml as a DAG against the cluster, produces Grade with Results; script checks get an env allowlist (PATH, HOME, TMPDIR, LANG, LC_ALL, TERM, USER plus TASK_NS, SEED, VARIANT, VAR_*, KUBECONFIG) shared with hook.sh and solve.sh (env.RunScript); wrapper.go puts a kubectl tagged kubedrill-check first on PATH for them
internal/kube/            the Kubernetes API client (dynamic client, server-side apply, probes, namespace GC, APIService health); cluster/ uses clientcmd, grader/ uses client-go jsonpath
internal/yamldoc/         one YAML document splitter (Split) shared by kube, scenario lint and the addon loader
internal/cluster/         kind lifecycle (doctor, ensure, delete, kubeconfig, audit check, audit policy drift, profile render), profile templates and addons; the cluster state dir ($XDG_STATE_HOME/kubedrill/kubedrill) holds audit/ (the audit log), bin/ (the kubedrill-check kubectl wrapper and the linux binary), kubeconfig-internal, kubeconfig-scripts, audit-policy.sha256 and faults.json
internal/env/             learner loop and state machine: Start/Observe/Grade/Diagnose/Hint/Surrender/Submit/Expire
internal/atomicfile/      file writes through a temporary file and a rename
internal/journal/         episodes, outcomes, diagnosis, debrief and telemetry under ~/.local/share/kubedrill (XDG_DATA_HOME)
internal/progress/        pure rules over the journal: clean solve, mastery per scenario and concept, Next, Stats
internal/telemetry/       reads the audit log and the shell log (drops kubedrill's own requests), summarizes them into journal.Telemetry and a verdict
internal/faults/          node faults through docker (pause, stop kubelet, tc netem), recorded in faults.json in the cluster state dir, Heal
internal/curriculum/      curriculum.yaml: levels L0-L6, modules, exam map
internal/concepts/        loads and validates docs/concepts/*.md (title, intro, four sections)
internal/workstation/     the workstation container: image tag, linux binary and kubeconfig-internal (both in the cluster state dir, mounted read-only), docker run, exam mode, replay, node shell
internal/cli/             cobra commands and the Bubble Tea watch mode
scenarios/<level>/<id>/   one directory per scenario, id = NNN-kebab-slug
cluster/profiles/         kind configurations as Go templates over the audit paths (base.yaml is the default)
cluster/addons/<name>/    addon.yaml plus vendored upstream manifests: metrics-server (base), gateway, kwok; README.md lists sources and local changes
LICENSES/                 Apache-2.0 text for the vendored addon manifests
cluster/audit-policy.yaml API audit policy mounted on the control plane by base.yaml
workstation/              Dockerfile, versions.env, checksums.txt, bashrc (shell logger), tmux.conf, entrypoint.sh (recording, exam PATH)
curriculum.yaml           the ladder
docs/                     learner and contributor documentation
docs/concepts/            concept pages, one file per id, referenced by scenario.toml concepts
private/                  git-ignored local notes (research, reviews); never linked from tracked files, never pushed
.github/                  ci.yml, workstation.yml, dependabot.yml, CODEOWNERS (maintainer review for shared code and scenarios; binds with a ruleset requiring code-owner review, see CONTRIBUTING.md)
```

## Design rules

- Deep modules: each package exposes a few methods and hides the rest. The
  exported signatures in each package's main file are the contract other
  packages compile against; change one only together with its callers and
  say so in the report.
- Interfaces are declared by the consumer with only the methods it calls
  (`env.Kube`, `env.Grader`). Constructors return concrete types.
- Dependencies arrive through constructors or `Deps` structs, never through
  package-level variables or `init()`.
- `context.Context` is the first parameter of anything that touches the
  cluster, the network or the filesystem for long; every goroutine has an
  owner that waits for it and an exit tied to `ctx.Done()`.
- Errors wrap their cause with the operation and input:
  `fmt.Errorf("load scenario %s: %w", dir, err)`. Sentinels are compared with
  `errors.Is`. Validation reports every problem at once, not the first.
- A grader problem is a `Result` with `StatusError`, never a panic; it
  counts as not passed in the score.
- Only the standard library plus the modules already in `go.mod`. Do not run
  `go get`; report a missing dependency instead.
- No code comments except doc comments on exported identifiers (full
  sentences starting with the name) and the rare invariant. No narration.
- Plain ASCII in every file, hyphens for dashes, no emojis. Exception: the
  vendored addon manifests under `cluster/addons/` are upstream files kept
  byte-identical except image pins, the flags listed in addon.yaml and a
  first-line modification notice, so they keep upstream characters. They
  stay Apache-2.0 (`LICENSES/Apache-2.0.txt`); `cluster/addons/README.md`
  lists each source, version and local change and is updated with them.
- Learner-facing text (instruction.md, hints, solution, CLI output) is en-US,
  concrete, and names the observable fact: "deployment hello: expected 2
  ready replicas, got 0".
- Scripts are bash 3.2 compatible (macOS default).
- External references are pinned: images as `image:tag@sha256:...` (except
  when the tag is the bug), GitHub Actions by commit SHA with the version in a
  comment, binaries with a recorded checksum.

## Scenario rules

- The ticket never names the root cause. It is written in the reporter's
  words with a `## Done when` list that mirrors checks.yaml.
- Checks assert outcomes, never command history. Several valid fixes pass.
- Every scenario has at least two `[[variant]]` entries for L1 and above
  (one is acceptable for L0) and `solution/solve.sh` must pass the checks for
  every variant; `kubedrill smoke` proves it.
- Right after setup, the checks must fail; `kubedrill smoke` asserts this.
- `## Before you start` in instruction.md is at most 8 lines.
- Every break-fix variant has a `root_cause` (20-300 characters) and the
  scenario lists `fix_targets` (`group/resource/name`, core group `core`).
- hints.md is the four-rung ladder (`## Hint 1: where to look`, `## Hint 2:
  what to observe`, `## Hint 3: what it means`, optional `## Hint 4: what to
  change`); no hint contains a mutating kubectl command.
- solution/README.md has exactly `## Symptom`, `## Candidate causes`, `##
  Observation that splits them`, `## Change`, `## Verify`, `## In the real
  world`, `## Go deeper`, in order.
- Every file that names the namespace writes `{{ .Namespace }}`: the episode
  namespace is the scenario namespace plus a random suffix.
- Setup Deployments set `terminationGracePeriodSeconds: 1`.
- Setup manifests stay inside the pod sandbox: no `privileged: true`,
  `hostPath`, `hostNetwork`, `hostPID`, `hostIPC`, `hostPort`, added
  capabilities, `procMount: Unmasked`, `Unconfined` seccomp or AppArmor
  profiles, PersistentVolume `spec.local` or docker.sock path unless
  scenario.toml sets `allow_privileged = true`, which needs maintainer
  review.
- A profile or audit policy change applies only to a new cluster:
  `kubedrill down --yes && kubedrill up`; `up` and `start` tell the learner
  when the running cluster's audit policy differs from the repository file.
- A check whose actual value names the root cause sets `spoiler: true`.
- Scenario scripts (hook.sh, solve.sh, script checks) run on the contributor's
  machine during smoke; review them before running a third-party scenario.

## Commands

```
just check       gofmt, go vet, go test -race, go build, go mod tidy -diff, shellcheck, scenario lint
just build       builds bin/kubedrill
just smoke       runs every scenario end to end against the current cluster
just vuln        govulncheck over the module (needs network)
just check-ci    check, vuln and smoke; the check and smoke jobs of CI
```

CI (`.github/workflows/ci.yml`) runs check and smoke; its shellcheck step
runs the runner's shellcheck over the files `just shellcheck` scans. `.github/workflows/workstation.yml` builds the
workstation image for amd64 and arm64 with a tool smoke on pull requests
that touch the workstation paths, on push to main and weekly; `just check-ci`
does not run it.

Go 1.27.3 (see go.mod). Tests use only the `testing` package, table-driven,
with got/want messages. Integration tests that need a cluster live behind the
`integration` build tag.

## Commit messages

Conventional Commits 1.0.0, en-US, imperative, no trailing period:
`feat(grader): add probe checks`, `docs: rewrite study flow`.
