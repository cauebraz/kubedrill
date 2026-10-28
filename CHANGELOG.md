# Changelog

All notable changes to kubedrill. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

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

### Changed

- A probe check that expects success and exits non-zero runs once more
  after 3 seconds, so a Service whose endpoint became ready just before
  grading is not failed while kube-proxy syncs.
- The Learn diagram lays sources next to their children and routes long
  arrows around boxes; bare URLs in scenario text are links.
- `kubedrill pack add` refuses symlinks, module and scenario id
  collisions with the ladder, and lists inline script checks; `smoke`
  without arguments skips pack scenarios.
- The browser workspace is the only way to practice; the terminal
  workstation layout (tmux, `kubedrill shell`, `kubedrill watch`,
  `ticket --follow`, `start --no-shell/--layout/--exam`) is gone.
- License changed from MIT to Apache-2.0 for the whole repository.

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
