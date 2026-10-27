#!/usr/bin/env bash
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

case "${VAR_SOURCE:-}" in
  env|subpath)
    kubectl -n "$TASK_NS" rollout restart deployment/banner
    ;;
  volume)
    kubectl -n "$TASK_NS" patch deployment banner -p '{"spec":{"template":{"spec":{"volumes":[{"name":"banner","configMap":{"name":"banner-config-v2"}}]}}}}'
    ;;
  *)
    echo "unknown VAR_SOURCE: ${VAR_SOURCE:-}" >&2
    exit 1
    ;;
esac
kubectl -n "$TASK_NS" rollout status deployment/banner --timeout=120s
