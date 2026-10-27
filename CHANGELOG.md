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

### Changed

- A probe check that exits non-zero runs once more after 3 seconds, so a
  Service whose endpoint became ready just before grading is not failed
  while kube-proxy syncs.

- The browser workspace is the only way to practice; the terminal
  workstation layout (tmux, `kubedrill shell`, `kubedrill watch`,
  `ticket --follow`, `start --no-shell/--layout/--exam`) is gone.
- License changed from MIT to Apache-2.0 for the whole repository.

## [0.1.0] - unreleased

The CLI, the grader, the kind lifecycle, the journal, the first scenario.
