#!/usr/bin/env bash
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

case "${VARIANT:-}" in
  config-mount-path)
    kubectl -n "$TASK_NS" patch deployment reviews --type json -p \
      '[{"op": "replace", "path": "/spec/template/spec/containers/0/volumeMounts/1/mountPath", "value": "/etc/reviews"}]'
    ;;
  liveness-wrong-port)
    kubectl -n "$TASK_NS" patch deployment reviews --type json -p \
      '[{"op": "replace", "path": "/spec/template/spec/containers/0/livenessProbe/httpGet/port", "value": 8080}]'
    ;;
  memory-limit-too-low)
    kubectl -n "$TASK_NS" patch deployment reviews --type json -p \
      '[{"op": "replace", "path": "/spec/template/spec/containers/0/resources/limits/memory", "value": "32Mi"}]'
    ;;
  *)
    kubectl -n "$TASK_NS" patch deployment reviews --type json -p \
      '[{"op": "replace", "path": "/spec/template/spec/containers/0/volumeMounts/1/mountPath", "value": "/etc/reviews"},
        {"op": "replace", "path": "/spec/template/spec/containers/0/livenessProbe/httpGet/port", "value": 8080},
        {"op": "replace", "path": "/spec/template/spec/containers/0/resources/limits/memory", "value": "32Mi"}]'
    ;;
esac

kubectl -n "$TASK_NS" rollout status deployment/reviews --timeout=120s

for _ in $(seq 1 60); do
  terminating=$(kubectl -n "$TASK_NS" get pods -l app=reviews -o jsonpath='{.items[*].metadata.deletionTimestamp}')
  if [ -z "$terminating" ]; then
    break
  fi
  sleep 1
done

kubectl -n "$TASK_NS" label pod -l app=reviews triage=yes --overwrite
kubectl -n "$TASK_NS" get pods -l triage=yes
