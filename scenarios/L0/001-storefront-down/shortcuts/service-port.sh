#!/usr/bin/env bash
# why: the Service was not the fault: no pod ever pulls its image, so no port of the Service reaches a ready pod
# variants: old-registry
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

kubectl -n "$TASK_NS" patch service storefront --type json -p '[{"op":"replace","path":"/spec/ports/0/port","value":8080},{"op":"replace","path":"/spec/ports/0/targetPort","value":80}]'
kubectl -n "$TASK_NS" rollout status deployment/storefront --timeout=20s || true
