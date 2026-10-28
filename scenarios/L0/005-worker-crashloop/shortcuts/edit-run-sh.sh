#!/usr/bin/env bash
# why: teaching run.sh the wrong flag hides the bad Deployment argument and forks the script staging and production share
# variants: unknown-flag
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

kubectl -n "$TASK_NS" get configmap worker-files -o jsonpath='{.data.run\.sh}' \
  | awk '{ print } /--workers=\*\)/ { print "    --concurrency=*) workers=\"${arg#--concurrency=}\" ;;" }' > "$work/run.sh"
grep -q -- '--concurrency=\*)' "$work/run.sh"
kubectl -n "$TASK_NS" get configmap worker-files -o jsonpath='{.data.worker\.conf}' > "$work/worker.conf"

kubectl -n "$TASK_NS" create configmap worker-files --from-file=run.sh="$work/run.sh" --from-file=worker.conf="$work/worker.conf" --dry-run=client -o yaml \
  | kubectl -n "$TASK_NS" apply -f -
kubectl -n "$TASK_NS" rollout restart deployment/worker
kubectl -n "$TASK_NS" rollout status deployment/worker --timeout=120s
