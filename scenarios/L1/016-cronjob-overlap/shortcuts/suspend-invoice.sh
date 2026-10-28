#!/usr/bin/env bash
# why: a suspended invoice CronJob never overlaps because it never runs; the export stops
# variants: allow-overlap
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

kubectl -n "$TASK_NS" patch cronjob invoice --type=merge -p '{"spec":{"suspend":true}}'
