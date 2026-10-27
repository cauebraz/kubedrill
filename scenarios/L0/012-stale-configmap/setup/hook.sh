#!/usr/bin/env bash
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}" "${VAR_EDITED:?VAR_EDITED is required}"

kubectl -n "$TASK_NS" rollout status deployment/banner --timeout=180s
kubectl -n "$TASK_NS" patch configmap "$VAR_EDITED" --type merge -p '{"data":{"banner":"Winter sale 2026"}}'
