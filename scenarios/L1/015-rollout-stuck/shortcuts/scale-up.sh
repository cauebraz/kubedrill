#!/usr/bin/env bash
# why: more replicas of a revision that never turns Ready leave the rollout stuck at its progress deadline
# variants: probe-404
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

kubectl -n "$TASK_NS" scale deployment/checkout --replicas=8
kubectl -n "$TASK_NS" rollout status deployment/checkout --timeout=20s || true
