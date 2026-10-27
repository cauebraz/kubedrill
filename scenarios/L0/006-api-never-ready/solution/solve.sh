#!/usr/bin/env bash
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

kubectl -n "$TASK_NS" patch deployment api --type=json -p='[{"op":"replace","path":"/spec/template/spec/containers/0/readinessProbe","value":{"httpGet":{"path":"/","port":"http"},"initialDelaySeconds":2,"periodSeconds":5}}]'
kubectl -n "$TASK_NS" rollout status deployment/api --timeout=120s
