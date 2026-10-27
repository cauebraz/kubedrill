# Security

kubedrill runs on your machine: a kind cluster in Docker, a workstation
container, and a web server bound to 127.0.0.1 that needs the token printed
at start. Nothing leaves the machine; the journal and the recordings stay
under your home directory.

## Reporting a vulnerability

Use GitHub's private vulnerability reporting on
https://github.com/cauebraz/kubedrill/security/advisories/new. Do not open
a public issue for a security problem. Expect an acknowledgement within 7
days and a fix or a published advisory within 90 days of the report.

In scope: the `kubedrill` binary, the web server and its page, the
workstation image and its entrypoint, the grader and the scripts it runs,
the cluster profiles and audit policy, the release workflow.

## What to know before running third-party scenarios

Scenario scripts (`hook.sh`, `solve.sh`, script checks) run on the host
with a reduced environment, not in the cluster. Read them before running a
scenario you did not write. Setup manifests cannot create cluster-scoped
objects or escape the pod sandbox unless `scenario.toml` sets
`allow_privileged = true`, which needs maintainer review in this
repository.

## Supported versions

The latest tagged release and the `main` branch.
