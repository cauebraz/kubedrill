# Changelog

All notable changes to kubedrill. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Badges: a module (hexagon), level (shield) and exam track (rosette)
  badge, each locked, in progress, earned or mastered from the clean-solve
  and mastery rules; a Badges screen with a 1200x627 share card (PNG,
  self-reported, local); the badge state on `kubedrill path`. They replace
  the plan badges of the browser workspace.
- Browser workspace (`kubedrill web`): meeting with the customer, quiz,
  learn page, guided practice, DIY run, exam rehearsal, debrief, catalog
  with filters, exam coverage, authoring preview.
- Welcome flow on the first visit: the five steps of the troubleshooting
  loop on a worked ticket, then the workspace.
- Exam rehearsal in the browser: a time box, one submit, auto-submit at
  the deadline.
- The kubedrill mark in the page, as a favicon and in the README.
- GitHub link in the page, `kubedrill version` and `--help`.
- Image preload on `kubedrill up`, so practice after it needs no registry.
- Scenarios 002-020: L0 complete, the first L1 modules.
- Scenario packs: `kubedrill pack add|list|remove` for scenarios from
  other repositories, pinned to a commit and linted before they are
  enabled.
- Project files: Apache-2.0 license, NOTICE, code of conduct, security
  policy, issue and pull request templates, release workflow, DCO check.
- Mermaid diagrams: every story and concept page draws its diagram as a
  Mermaid flowchart, rendered in the page with the theme tokens and as a
  text chain by `kubedrill story` and `kubedrill learn` (mermaid 12.2.0
  vendored).
- `restart_is_fix` in scenario.toml: a rollout restart of a fix target
  counts as a fix in the debrief verdict.
- Release archives carry the tagged repository with a prebuilt
  `bin/kubedrill`.
- `rollout` check source: a Deployment or StatefulSet finished rolling out
  its current template (generation observed, every replica updated and
  available, no old pod left); scenarios whose fix changes a pod template
  grade it, so a broken edit after the fix no longer passes on old pods.
- Scenario shortcuts: `shortcuts/<name>.sh` scripts apply a wrong fix a
  learner would try; `kubedrill smoke` runs each on a fresh setup and fails
  when it passes every check (`--no-shortcuts` skips them). 18 scenarios
  of 001-020 ship 24 of them, taken from the live validation.

### Changed

- A probe or script check that expects success and exits non-zero runs
  once more after 3 seconds, so a Service whose endpoint became ready just
  before grading is not failed while kube-proxy syncs.
- The Learn diagram lays sources next to their children and routes long
  arrows around boxes; bare URLs in scenario text are links.
- `smoke` without arguments skips pack scenarios.
- The browser workspace is the only way to practice; the terminal
  workstation layout (tmux, `kubedrill shell`, `kubedrill watch`,
  `ticket --follow`, `start --no-shell/--layout/--exam`) is gone.
- License changed from MIT to Apache-2.0 for the code and the scenarios;
  the vendored browser libraries keep their MIT licenses.

### Fixed

- Scenarios: the practice fix no longer solves the DIY variant (006, 008,
  009, 012, 014, 015, 016, 018, 019); ticket constraints are graded (006
  readiness probe, 007 migrate step, 016 schedule); Done when lines state
  outcomes, not the fix (001, 004, 016, 017); 002 grades restarts over a
  45 s window; one reporter per team across the tickets.
- Workspace: Enter on a focused link or button no longer triggers the
  screen's main action (it could relaunch a run); a second check popover
  no longer closes itself; the welcome flow returns to the link that was
  opened; diagrams keep `column: 0` and survive edge cycles.
- `kubedrill pack add` refuses symlinks and id collisions and lists inline
  script checks; a broken pack no longer disables the catalog.
- CI installs metrics-server before smoke (the HPA scenario needs it) and
  pins bind9-dnsutils after a Debian security update withdrew dnsutils.
- Scenario 002 waits for every Deployment before its checks run.
- Scenarios: checks no longer pass wrong fixes (002 deleted probe or
  limit, 005 edited script, 006 exec probe, 007 skipped migration, 009
  relabeled pod, 011 canary scaled to 0, 014 old Secret left in the
  template, 016 relabeled CronJob, 019 emptyDir, 020 flags instead of the
  config file); texts that were false on the cluster are corrected (003,
  005, 007, 010, 012, 015, 016, 018, 020); 016 drops the Replace variant,
  which could not produce the ticket's symptom.
- `kubedrill story` and the Story tab keep "The fix and why it works"
  closed during a DIY or exam run until hint 4 or the end of the run.
- A failing spoiler check hides its expected value as well as its actual
  value, in `kubedrill check` and in the page.
- Guided practice runs no longer count as clean solves or as exam
  coverage.
- `kubedrill up` and `web` skip images the nodes already hold; setup hook
  output is kept for errors instead of printed.
- The debrief count reads "API reads before the first write".
- Every scenario was replayed live, variant by variant, and the text was
  corrected where the cluster disagreed: endpoint tables list not-ready
  addresses (001, 004, 006, 007), `describe pvc` leaves Access Modes empty
  while Pending (019), an OOMKilled container shows `OOMKilled` not
  `CrashLoopBackOff` (008), per-pod DNS records are not exclusive to
  headless Services (020), Forbid runs the missed tick when the active
  Job ends (016), `Endpoints:` prints blank rather than `<none>` (004,
  011), ConfigMap and Secret volumes refresh in 65-85 s (012, 014), the
  stalled rollout after an oversized attempt (018), `rollout status`
  exits with the deadline error on every call (015), and `kubectl logs`
  without `--previous` is not empty during the backoff (005). New
  checks: 010 keeps the readiness probe, 013 the volume names
  `db-credentials`; 010 gains a status checker so the edge log holds the
  502 before anyone requests it; 011's NodePort check retries the
  kube-proxy rule race.
- An episode's namespace is deleted when the episode ends, solved or not,
  instead of at the next start; the start-time sweep removes every other
  namespace of the same journal.
- A no-op write (`patched (no change)`) is no longer the first write or
  the root-cause write in the debrief: the audit policy records response
  bodies for writes to fix targets and the request's field manager entry
  shows whether the object changed. Needs a new cluster.
- The page shows mastery as distinct variants solved clean.
- A wrong `--root` or `KUBEDRILL_ROOT` names which one is wrong.
- Releasing the journal lock no longer removes a lock another process
  took over in the meantime.
- The debrief verdict counts a pod template label change as a fix; only
  annotations under the template are metadata-only.
- Concept pages: Pending with no event points at the claim, OOMKilled
  status, `rollout undo` at revision 1, zero ready endpoints.

### Security

- Episode namespaces enforce the Pod Security `baseline` level, and setup
  rechecks the rendered manifests against the sandbox rules before it
  applies them, unless the scenario sets `allow_privileged`.
- Setup templates charge strings built by `print`, `printf`, `html`, `js`
  and `urlquery` to the render byte budget.
- `kubedrill web` sends a Content-Security-Policy, refuses cross-site
  requests by `Sec-Fetch-Site`, closes terminal containers on shutdown
  and removes ones a previous run left behind.
- A journal lock left by another host is taken over after 2 minutes, not
  30 seconds, and a writer waits up to 150 seconds for it.
