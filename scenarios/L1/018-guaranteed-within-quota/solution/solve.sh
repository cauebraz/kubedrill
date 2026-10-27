#!/usr/bin/env bash
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

for d in storefront catalog cart; do
  kubectl -n "$TASK_NS" set resources deployment/"$d" --requests=cpu=250m,memory=256Mi --limits=cpu=250m,memory=256Mi
done
for d in storefront catalog cart; do
  kubectl -n "$TASK_NS" rollout status deployment/"$d" --timeout=120s
done
