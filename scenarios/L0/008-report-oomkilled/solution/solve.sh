#!/usr/bin/env bash
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

case "${VARIANT:-}" in
  sidecar-limit)
    kubectl -n "$TASK_NS" set resources deployment/report -c log-shipper --requests=memory=32Mi --limits=memory=64Mi
    ;;
  limitrange-default)
    kubectl -n "$TASK_NS" patch limitrange namespace-defaults --type=json \
      -p '[{"op":"replace","path":"/spec/limits/0/max/memory","value":"128Mi"},{"op":"replace","path":"/spec/limits/0/default/memory","value":"128Mi"},{"op":"replace","path":"/spec/limits/0/defaultRequest/memory","value":"64Mi"}]'
    kubectl -n "$TASK_NS" rollout restart deployment/report
    ;;
  *)
    kubectl -n "$TASK_NS" set resources deployment/report -c report --requests=memory=64Mi --limits=memory=128Mi
    ;;
esac

kubectl -n "$TASK_NS" rollout status deployment/report --timeout=120s

for _ in $(seq 1 30); do
  killed=$(kubectl -n "$TASK_NS" get pods -l app.kubernetes.io/name=report \
    -o jsonpath='{.items[*].status.containerStatuses[?(@.lastState.terminated.reason=="OOMKilled")].name}')
  [ -z "$killed" ] && break
  sleep 1
done
