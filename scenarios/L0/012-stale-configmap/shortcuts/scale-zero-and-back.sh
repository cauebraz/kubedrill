#!/usr/bin/env bash
# why: new pods still mount banner-config, which holds the old text; the edit went to banner-config-v2
# variants: wrong-name
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

kubectl -n "$TASK_NS" scale deployment/banner --replicas=0
for _ in $(seq 1 30); do
  if [ -z "$(kubectl -n "$TASK_NS" get pods -l app.kubernetes.io/name=banner -o name)" ]; then
    break
  fi
  sleep 1
done
kubectl -n "$TASK_NS" scale deployment/banner --replicas=2
kubectl -n "$TASK_NS" rollout status deployment/banner --timeout=120s
