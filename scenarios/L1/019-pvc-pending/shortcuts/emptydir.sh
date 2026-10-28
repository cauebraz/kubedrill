#!/usr/bin/env bash
# why: an emptyDir starts the pod but loses every upload when the pod is replaced, and claim uploads-data stays Pending
# variants: missing-class
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

kubectl -n "$TASK_NS" patch deployment uploads --type=json -p '[{"op":"replace","path":"/spec/template/spec/volumes/0","value":{"name":"data","emptyDir":{}}}]'
kubectl -n "$TASK_NS" rollout status deployment/uploads --timeout=120s
