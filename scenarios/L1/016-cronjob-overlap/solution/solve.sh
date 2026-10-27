#!/usr/bin/env bash
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

kubectl -n "$TASK_NS" patch cronjob invoice --type=merge -p '{"spec":{"concurrencyPolicy":"Forbid"}}'

for cj in $(kubectl -n "$TASK_NS" get cronjobs -l app.kubernetes.io/name=invoice -o jsonpath='{.items[*].metadata.name}'); do
  if [ "$cj" != invoice ]; then
    kubectl -n "$TASK_NS" patch cronjob "$cj" --type=merge -p '{"spec":{"suspend":true}}'
  fi
done

sleep 2

keep=""
surplus=""
while read -r name owner active; do
  [ -n "$name" ] || continue
  [ "${active:-0}" -gt 0 ] || continue
  if [ "$owner" = invoice ] && [ -z "$keep" ]; then
    keep="$name"
  else
    surplus="$surplus $name"
  fi
done <<JOBS
$(kubectl -n "$TASK_NS" get jobs -l app.kubernetes.io/name=invoice --sort-by=.metadata.creationTimestamp \
  -o jsonpath='{range .items[*]}{.metadata.name}{" "}{.metadata.ownerReferences[0].name}{" "}{.status.active}{"\n"}{end}' | awk '{print NR, $0}' | sort -rn | cut -d' ' -f2-)
JOBS

for job in $surplus; do
  kubectl -n "$TASK_NS" delete job "$job" --wait=true
done

kubectl -n "$TASK_NS" get cronjobs,jobs -l app.kubernetes.io/name=invoice
