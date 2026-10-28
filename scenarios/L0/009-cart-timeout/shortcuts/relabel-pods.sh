#!/usr/bin/env bash
# why: pods relabeled by hand match the Service only until the next rollout, whose pods carry tier=web again
# variants: stale-tier-selector
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

kubectl -n "$TASK_NS" rollout status deployment/cart --timeout=120s
kubectl -n "$TASK_NS" label pod -l app=cart,tier=web tier=api --overwrite

for _ in $(seq 1 30); do
  ready=$(kubectl -n "$TASK_NS" get endpointslices -l kubernetes.io/service-name=cart \
    -o jsonpath='{.items[*].endpoints[?(@.conditions.ready==true)].addresses[*]}' 2>/dev/null || true)
  if [ -n "$ready" ]; then
    break
  fi
  sleep 1
done
