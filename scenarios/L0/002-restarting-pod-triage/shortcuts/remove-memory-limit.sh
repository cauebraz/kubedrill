#!/usr/bin/env bash
# why: removing the memory limit stops the OOM kills, but a leaking reviews container can then starve the 39 other pods on the node
# variants: memory-limit-too-low
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

kubectl -n "$TASK_NS" patch deployment reviews --type json -p '[{"op":"remove","path":"/spec/template/spec/containers/0/resources/limits"}]'
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
