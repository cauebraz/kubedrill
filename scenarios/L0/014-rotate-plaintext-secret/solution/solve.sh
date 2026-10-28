#!/usr/bin/env bash
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"
: "${VAR_NEW_TOKEN:?VAR_NEW_TOKEN is required}" "${VAR_ROTATED:?VAR_ROTATED is required}"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

printf '%s' "$VAR_NEW_TOKEN" > "$work/token.txt"
kubectl -n "$TASK_NS" create secret generic payments-token --from-file=token="$work/token.txt" --dry-run=client -o yaml \
  | kubectl -n "$TASK_NS" apply -f -
kubectl -n "$TASK_NS" label secret payments-token "rotated=$VAR_ROTATED" --overwrite

case "${VAR_WORKER_SOURCE:-env}" in
  file)
    kubectl -n "$TASK_NS" patch deployment payments-worker -p '{"spec":{"template":{"spec":{"volumes":[{"name":"token","secret":{"secretName":"payments-token"}}]}}}}'
    ;;
  *)
    kubectl -n "$TASK_NS" patch deployment payments-worker -p '{"spec":{"template":{"spec":{"containers":[{"name":"worker","env":[{"name":"API_TOKEN","valueFrom":{"secretKeyRef":{"name":"payments-token","key":"token"}}}]}]}}}}'
    ;;
esac

kubectl -n "$TASK_NS" rollout restart deployment/payments deployment/payments-worker
kubectl -n "$TASK_NS" rollout status deployment/payments --timeout=120s
kubectl -n "$TASK_NS" rollout status deployment/payments-worker --timeout=120s

kubectl -n "$TASK_NS" delete secret payments-token-old
