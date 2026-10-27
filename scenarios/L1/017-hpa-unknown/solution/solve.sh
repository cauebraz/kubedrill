#!/usr/bin/env bash
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"
: "${VARIANT:?VARIANT is required}"

case "$VARIANT" in
  no-cpu-request)
    kubectl -n "$TASK_NS" set resources deployment/api -c api --requests=cpu=50m
    ;;
  wrong-target)
    kubectl -n "$TASK_NS" patch hpa api --type=merge \
      -p '{"spec":{"scaleTargetRef":{"apiVersion":"apps/v1","kind":"Deployment","name":"api"}}}'
    ;;
  memory-metric)
    kubectl -n "$TASK_NS" patch hpa api --type=json \
      -p '[{"op":"replace","path":"/spec/metrics/0/resource/name","value":"cpu"}]'
    ;;
  *)
    echo "solve.sh: unknown variant $VARIANT" >&2
    exit 1
    ;;
esac

kubectl -n "$TASK_NS" rollout status deployment/api --timeout=120s

deadline=$((SECONDS + 150))
while :; do
  active=$(kubectl -n "$TASK_NS" get hpa api -o jsonpath='{.status.conditions[?(@.type=="ScalingActive")].status}')
  value=$(kubectl -n "$TASK_NS" get hpa api -o jsonpath='{.status.currentMetrics[*].resource.current.averageValue}')
  if [ "$active" = "True" ] && [ -n "$value" ]; then
    echo "hpa api: ScalingActive True, current cpu $value"
    break
  fi
  if [ "$SECONDS" -ge "$deadline" ]; then
    echo "hpa api: ScalingActive '$active', current value '$value' after 150s" >&2
    exit 1
  fi
  sleep 5
done
