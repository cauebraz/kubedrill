#!/usr/bin/env bash
# why: a longer delay and more retries still request /healthz, which nginx answers with 404 every time
# variants: wrong-path
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

kubectl -n "$TASK_NS" patch deployment api --type=json -p='[{"op":"add","path":"/spec/template/spec/containers/0/readinessProbe/initialDelaySeconds","value":15},{"op":"add","path":"/spec/template/spec/containers/0/readinessProbe/failureThreshold","value":10}]'
kubectl -n "$TASK_NS" rollout status deployment/api --timeout=40s || true
