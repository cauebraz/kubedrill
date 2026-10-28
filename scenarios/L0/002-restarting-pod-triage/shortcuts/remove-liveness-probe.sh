#!/usr/bin/env bash
# why: removing the liveness probe stops the kills, but a hung reviews container then keeps its pod forever
# variants: liveness-wrong-port
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

kubectl -n "$TASK_NS" patch deployment reviews --type json -p '[{"op":"remove","path":"/spec/template/spec/containers/0/livenessProbe"}]'
kubectl -n "$TASK_NS" rollout status deployment/reviews --timeout=120s
for d in $(kubectl -n "$TASK_NS" get deployments -o name); do
  kubectl -n "$TASK_NS" rollout status "$d" --timeout=180s
done

for _ in $(seq 1 60); do
  terminating=$(kubectl -n "$TASK_NS" get pods -l app=reviews -o jsonpath='{.items[*].metadata.deletionTimestamp}')
  if [ -z "$terminating" ]; then
    break
  fi
  sleep 1
done

sleep 46

kubectl -n "$TASK_NS" label pod -l app=reviews triage=yes --overwrite
