#!/usr/bin/env bash
# why: removing the quota caps admits pods without resources, which run BestEffort, and the namespace loses its budget
# variants: quota-2-cpu
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

kubectl -n "$TASK_NS" patch resourcequota shop --type=json -p '[{"op":"remove","path":"/spec/hard"}]'
for d in storefront catalog cart; do
  kubectl -n "$TASK_NS" rollout status deployment/"$d" --timeout=90s || true
done
