#!/usr/bin/env bash
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

kubectl -n "$TASK_NS" set image deployment/hello app=nginx:1.27-alpine@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10
kubectl -n "$TASK_NS" rollout status deployment/hello --timeout=120s
