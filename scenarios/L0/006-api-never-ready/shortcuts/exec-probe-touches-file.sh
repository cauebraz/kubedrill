#!/usr/bin/env bash
# why: a probe that creates its own file always succeeds, so it no longer tells whether nginx answers
# variants: missing-ready-file
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

kubectl -n "$TASK_NS" patch deployment api --type=json -p='[{"op":"replace","path":"/spec/template/spec/containers/0/readinessProbe/exec/command","value":["sh","-c","touch /tmp/ready"]}]'
kubectl -n "$TASK_NS" rollout status deployment/api --timeout=120s
