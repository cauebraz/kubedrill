#!/usr/bin/env bash
# why: a restart recreates the pods from the same template, which still names an image that does not exist
# variants: first-root-cause
# kubedrill-template: a shortcut is a wrong or symptom-only fix a learner would
# plausibly try; kubedrill smoke runs it on a fresh setup of each variant it
# applies to and fails when every check passes afterwards. The comment lines
# right after the shebang are its header: "# why:" (required, one line, what
# the shortcut gets wrong) and "# variants:" (optional, comma-separated names
# from scenario.toml; every variant when absent). Runs like solution/solve.sh
# with TASK_NS, SEED, VARIANT, VAR_* and KUBECONFIG, bash 3.2 compatible. Wait
# for the state the shortcut produces, so a check fails because the fix is
# wrong, not because it was graded too early. Delete these comment lines in a
# real scenario, or the shortcuts/ directory when the scenario has none.
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

kubectl -n "$TASK_NS" rollout restart deployment/app
kubectl -n "$TASK_NS" rollout status deployment/app --timeout=20s || true
