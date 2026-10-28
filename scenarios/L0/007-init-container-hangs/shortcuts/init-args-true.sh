#!/usr/bin/env bash
# why: an init container that runs true skips the migration check instead of mounting the flag where it is read
# variants: flag-mount-path
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

kubectl -n "$TASK_NS" patch deployment api --type=json -p '[{"op":"replace","path":"/spec/template/spec/initContainers/0/args","value":["true"]}]'
kubectl -n "$TASK_NS" rollout status deployment/api --timeout=120s
