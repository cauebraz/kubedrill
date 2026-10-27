#!/usr/bin/env bash
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

for d in $(kubectl -n "$TASK_NS" get deployments -o name); do
  kubectl -n "$TASK_NS" rollout status "$d" --timeout=120s
done

images=$(kubectl -n "$TASK_NS" get pods --field-selector=status.phase=Running \
  -o jsonpath='{range .items[*]}{range .spec.containers[*]}{.image}{"\n"}{end}{end}' | sort -u)

kubectl -n "$TASK_NS" create configmap inventory --from-literal=images="$images"
kubectl -n "$TASK_NS" get configmap inventory -o jsonpath='{.data.images}{"\n"}'
