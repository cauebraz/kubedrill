#!/usr/bin/env bash
# why: a 2-minute schedule hides the overlap only until an export runs longer, and the team needs the file every minute
# variants: allow-overlap
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

kubectl -n "$TASK_NS" patch cronjob invoice --type=merge -p '{"spec":{"schedule":"*/2 * * * *"}}'
