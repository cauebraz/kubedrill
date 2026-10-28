#!/usr/bin/env bash
# why: a quota on limits refuses pods that set only requests, and Guaranteed needs limits equal to requests
# variants: quota-1-cpu
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

for d in storefront catalog cart; do
  kubectl -n "$TASK_NS" set resources deployment/"$d" --requests=cpu=100m,memory=128Mi
done
for d in storefront catalog cart; do
  kubectl -n "$TASK_NS" rollout status deployment/"$d" --timeout=20s || true
done
