#!/usr/bin/env bash
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}" "${VARIANT:?VARIANT is required}"

case "$VARIANT" in
  probe-404) kubectl -n "$TASK_NS" rollout undo deployment/checkout --to-revision=1 ;;
  cpu-64) kubectl -n "$TASK_NS" set resources deployment/checkout -c web --requests=cpu=10m ;;
  typo-tag) kubectl -n "$TASK_NS" set image deployment/checkout web=nginx:1.27-alpine@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10 ;;
  *) echo "unknown variant $VARIANT" >&2; exit 1 ;;
esac
kubectl -n "$TASK_NS" rollout status deployment/checkout --timeout=180s

i=0
while [ "$i" -lt 60 ]; do
  ready=$(kubectl -n "$TASK_NS" get endpointslices -l kubernetes.io/service-name=checkout -o jsonpath='{.items[*].endpoints[?(@.conditions.ready==true)].addresses[*]}' | wc -w | tr -d ' ')
  phases=$(kubectl -n "$TASK_NS" get pods -l app.kubernetes.io/name=checkout -o jsonpath='{.items[*].status.phase}')
  case " $phases " in
    *" Pending "*) ;;
    *) if [ "$ready" = "4" ]; then exit 0; fi ;;
  esac
  sleep 1
  i=$((i + 1))
done
echo "service checkout: expected 4 ready endpoints and no Pending pod, got $ready endpoints and phases: $phases" >&2
exit 1
