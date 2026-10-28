#!/usr/bin/env bash
# why: the pods read API_TOKEN once at start, so without a restart they keep the leaked token, and the old copy stays readable
# variants: worker-on-new-secret
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

printf '%s' "${VAR_NEW_TOKEN:?VAR_NEW_TOKEN is required}" > "$work/token.txt"
kubectl -n "$TASK_NS" create secret generic payments-token --from-file=token="$work/token.txt" --dry-run=client -o yaml \
  | kubectl -n "$TASK_NS" apply -f -
kubectl -n "$TASK_NS" label secret payments-token "rotated=${VAR_ROTATED:?VAR_ROTATED is required}" --overwrite
