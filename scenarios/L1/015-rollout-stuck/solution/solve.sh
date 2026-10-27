#!/usr/bin/env bash
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

kubectl -n "$TASK_NS" rollout undo deployment/checkout --to-revision=1
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
