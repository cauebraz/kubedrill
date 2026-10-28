#!/usr/bin/env bash
# why: the previous revision passes the same unknown flag, so an undo brings back the same crash
# variants: unknown-flag
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

kubectl -n "$TASK_NS" rollout restart deployment/worker
kubectl -n "$TASK_NS" rollout status deployment/worker --timeout=5s || true
kubectl -n "$TASK_NS" rollout undo deployment/worker
kubectl -n "$TASK_NS" rollout status deployment/worker --timeout=30s || true
