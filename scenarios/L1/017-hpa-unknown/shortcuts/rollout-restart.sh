#!/usr/bin/env bash
# why: new pods from the same template still set no CPU request, so the HPA still cannot compute utilization
# variants: no-cpu-request
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

kubectl -n "$TASK_NS" rollout restart deployment/api
kubectl -n "$TASK_NS" rollout status deployment/api --timeout=120s
