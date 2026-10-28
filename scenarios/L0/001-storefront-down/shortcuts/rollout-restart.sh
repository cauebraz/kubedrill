#!/usr/bin/env bash
# why: a restart recreates the pods from the same template, which still names an image no node can pull
# variants: typo-tag
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

kubectl -n "$TASK_NS" rollout restart deployment/storefront
kubectl -n "$TASK_NS" rollout status deployment/storefront --timeout=20s || true
