# Contributing

New scenarios are welcome, especially ones you lived through. This page is
the path from idea to pull request. Every file and field is described in
[docs/scenario-format.md](docs/scenario-format.md).

## What makes a good scenario

- **It happened.** The best scenarios come from an incident, a ticket or a
  code review you remember.
- **The ticket is the reporter's.** Write the symptom and the expectation in
  their words. Never name the root cause.
- **One root cause below L4.** L4 and above may chain two or three.
  `incident` scenarios are the place for several simultaneous faults.
- **Checks grade outcomes.** Ready replicas, endpoints, a response code, an
  object field. Never the commands used, so several valid fixes pass.
- **Variants make repeats new.** A variant is a different root cause behind
  the same ticket, or different parameters of the same cause. L1 and above
  need at least two; L0 may ship one.
- **Local only.** The learner reaches `## Done when` with `kubectl` and the
  objects in the namespace. No external accounts, no cloud credentials, no
  internet beyond image pulls.

## Add a scenario in 8 steps

1. **Scaffold.** Pick the module in `curriculum.yaml` and the next free id
   in its level, then run:

   ```sh
   just new-scenario L1 011 rollout-stuck    # or: ./start.sh new L1 011-rollout-stuck
   ```

2. **`scenario.toml`.** Fill the metadata, `concepts`, `fix_targets` and one
   `[[variant]]` table per root cause, each with a `root_cause` sentence.
   Reuse a concept page when one names the mechanism the fix depends on
   (`kubedrill concepts` lists them).
   [Fields](docs/scenario-format.md#scenariotoml).
3. **`setup/`.** The broken state as templates. Write `metadata.namespace:
   {{ .Namespace }}`, take the fault from `{{ .Vars.<key> }}` and set
   `terminationGracePeriodSeconds: 1` on every pod template. Add
   `setup/hook.sh` only for what a manifest cannot express.
   [Rules](docs/scenario-format.md#setup-and-hooksh).
4. **`instruction.md`.** The ticket. `## Done when` lists the check
   descriptions in order. [Sections](docs/scenario-format.md#instructionmd).
5. **`checks.yaml`.** One check per `## Done when` line, each with a `why`
   written for the learner who is stuck on it. Mark the check that makes the
   rest meaningless `critical`, chain the others with `depends_on`, and set
   `spoiler: true` where the actual value names the root cause.
   [Fields](docs/scenario-format.md#checksyaml).
6. **`hints.md`.** The four-rung ladder: where to look, what to observe,
   what it means, what to change. No mutating `kubectl` command.
   [Ladder](docs/scenario-format.md#hintsmd).
7. **`solution/` and `approaches/`.** The walkthrough under the seven
   loop headings, a `solve.sh` that passes every variant, and optionally
   one directory per valid fix.
   [Format](docs/scenario-format.md#solution).
8. **`story.md`** (optional, recommended for L0 and for a first object). The
   ticket told from zero for a newcomer, under five fixed headings, true for
   every variant. [Sections](docs/scenario-format.md#storymd).
9. **`simulation.yaml`** (optional, recommended for L0 and L1; delete the
   scaffolded file for a lab-only scenario). The browser workspace's
   customer meeting, object diagram, quiz and guided steps;
   `scenarios/L0/001-storefront-down/simulation.yaml` is the example.
   [Format](docs/scenario-format.md#simulationyaml).
   - Write it after `solution/README.md`: `kubedrill draft-guide <id>`
     turns the walkthrough's commands into guide steps to edit, and
     `kubedrill draft-guide <id> --check` flags guide commands the
     walkthrough never runs.
   - Write the meeting from the ticket: one turn per fact the learner needs
     (what changed, the exact symptom, the reference that works, what done
     means). A wrong choice is a move a real engineer would try, its reply
     is still a real fact, and its feedback says what a better question
     learns. No joke options: a distractor that nobody would pick teaches
     nothing.
   - Keep it with the page open: `kubedrill web --preview <id>` needs no
     cluster and shows each edit on reload.
10. **Finish.** Delete the template's comments and marker lines, add the id
   to its module in `curriculum.yaml` (lint refuses a scenario the
   curriculum does not list), add the row to the scenario table in
   `README.md`, then run the checks below.

## Run the checks

```sh
just check          # gofmt, vet, race tests, build, go mod tidy -diff, shellcheck, kubedrill lint
./start.sh up       # once: creates the kind cluster
just smoke 011      # per variant: checks fail after setup, solve.sh runs, checks pass
just check-ci       # check, govulncheck and smoke for every scenario, as CI does
```

- Contributors need Go (`just check`) and `kubectl` on the host: `hook.sh`,
  `solve.sh` and script checks run there. Learners need neither.
- Smoke uses a throwaway journal and its own namespaces. It refuses a
  scenario you have an active episode of, unless `--force`.
- When the change touches shared code, run `./start.sh smoke` with no
  argument before opening the pull request.
- `just vuln` runs govulncheck alone and needs network.
- Changes to the browser workspace (`internal/web/`): `just web-dev` serves
  it on an in-memory backend with the files read from disk, no cluster
  needed; `just ui-test` drives every stage in headless Chrome, and `just
  ui-test /tmp/shots` also saves a screenshot of each screen in both
  themes for the pull request. A deliberate change to the API's JSON
  updates the snapshots with `go test ./internal/web -run TestSnapshots
  -update`.

What runs in CI:

- `.github/workflows/ci.yml` runs the `check` steps plus govulncheck, then
  `kubedrill smoke` on a kind cluster. Its shellcheck step runs the
  runner's `shellcheck -x` over the files `just shellcheck` scans:
  `start.sh`, `workstation/entrypoint.sh`, `workstation/bashrc` and the
  scenario scripts.
- `.github/workflows/workstation.yml` builds the workstation image for amd64
  and arm64 and runs a tool smoke (`kubectl`, `helm`, `k9s`, `stern`, `yq`,
  `kubectx`, `kubens`, `asciinema`) and checks that the `shell`
  entrypoint runs a command. It runs on pull requests that
  change the workstation paths, on push to main and weekly. `just check-ci`
  does not run it.
- Dependabot (`.github/dependabot.yml`) opens weekly pull requests for Go
  modules, GitHub Actions and the workstation Dockerfile.

## Scenario scripts run on your machine

`kubedrill smoke` and `kubedrill start` run `setup/hook.sh`,
`solution/solve.sh` and `script` checks with your user. They get a pinned
copy of your kubeconfig that holds only the `kind-kubedrill` context
(`<cluster state dir>/kubeconfig-scripts`, mode 0600). Read those files in a
third-party scenario before the first `smoke` or `start`, as you would any
script from a pull request.

Setup manifests stay in the episode namespace and inside the pod sandbox.
A scenario that needs privileged fields sets `allow_privileged = true` and
needs a maintainer review
([rule](docs/scenario-format.md#setup-and-hooksh)).

## Writing a concept page

A page under `docs/concepts/<id>.md` explains one mechanism for the learner
a scenario sends there. The format and the writing rules are in
[docs/concepts/README.md](docs/concepts/README.md). The loader checks the
id, title, intro and the four sections. The reviewer also checks:

- The id names the mechanism, not the scenario (`image-pull`, not
  `hello-down-images`).
- The counts in the format: 400 to 900 words (`wc -w`), 150 to 300 words of
  mechanism with one ASCII diagram, 3 to 6 commands, 4 to 8 failures, 2 to
  4 links.
- Plain ASCII (`grep -nP '[^\x00-\x7F]'` prints nothing), en-US.
- Every number, field, flag and default is checked against the
  kubernetes.io page for v1.37 cited in `Go deeper`. A claim from another
  source cites it; a claim you could not check is left out.
- The page is in the table of `docs/concepts/README.md` and at least one
  scenario lists it in `concepts`.

## Conventions

- **Go:** design rules, layout and tests are in [AGENTS.md](AGENTS.md). Go
  1.27.3, standard library plus the modules in `go.mod`. Do not run `go
  get`; report a missing dependency instead.
- **Scripts:** bash 3.2 compatible, the macOS default: no `mapfile`, no
  associative arrays, no `${var,,}`.
- **Pins:** images as `image:tag@sha256:<digest>` in setup, probes and
  `solve.sh`, except when the image is the bug, set from a variant
  variable; lint enforces it for setup and probes. List the image the fix
  needs in `scenario.toml` `images`, so `kubedrill up` preloads it. GitHub
  Actions by commit SHA with the version in a comment.
- **Text:** plain ASCII, hyphens for dashes, no emojis. Learner-facing text
  is en-US and names the observable fact. The vendored manifests under
  `cluster/addons/` keep upstream characters and their Apache-2.0 license;
  [cluster/addons/README.md](cluster/addons/README.md) says how to change
  one.
- **Comments:** none, except doc comments on exported Go identifiers.

## Pull requests

- One scenario per pull request. A change to shared code travels on its own.
- Commit messages follow
  [Conventional Commits 1.0.0](https://www.conventionalcommits.org/en/v1.0.0/):
  `feat(scenarios): add 011 rollout stuck at 3/4`, `fix(grader): report
  probe timeout as error`. Imperative, lower case after the colon, no
  trailing period.

### Review and code owners

`.github/CODEOWNERS` names the maintainer for `scenarios/**`,
`cluster/addons/**`, `workstation/**`, `.github/**` and `internal/faults/**`,
and `*` for the rest. GitHub enforces it only through repository settings a
maintainer sets by hand:

- a ruleset on `main` with "Require a pull request before merging" and
  "Require review from Code Owners";
- "Require approval for all external contributors" for workflows on fork
  pull requests (Settings -> Actions -> Fork pull request workflows);
- `sha_pinning_required`, so every action is pinned by commit SHA
  (Settings -> Actions -> General).
