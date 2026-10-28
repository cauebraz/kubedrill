#!/usr/bin/env bash
# why: without a readiness probe a pod that stops answering is never taken out of Service web
# variants: probe-left-on-80
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

kubectl -n "$TASK_NS" patch deployment web --type=json -p '[{"op":"remove","path":"/spec/template/spec/containers/0/readinessProbe"}]'
kubectl -n "$TASK_NS" rollout status deployment/web --timeout=120s

for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
  if kubectl -n "$TASK_NS" exec deploy/edge -- wget -qO- -T 2 http://127.0.0.1/ 2>/dev/null | grep -q "ACME Docs Portal 3.2.0"; then
    break
  fi
  sleep 2
done
