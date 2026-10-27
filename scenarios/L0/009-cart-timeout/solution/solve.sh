#!/usr/bin/env bash
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

case "${VARIANT:-}" in
  wrong-app-label)
    kubectl -n "$TASK_NS" patch svc cart --type=merge -p '{"spec":{"selector":{"app":"cart"}}}'
    ;;
  stale-tier-selector)
    kubectl -n "$TASK_NS" patch svc cart --type=merge -p '{"spec":{"selector":{"tier":null}}}'
    ;;
  wrong-target-port)
    kubectl -n "$TASK_NS" patch svc cart --type=json -p '[{"op":"replace","path":"/spec/ports/0/targetPort","value":80}]'
    ;;
  *)
    echo "solve.sh: unknown variant '${VARIANT:-}'" >&2
    exit 1
    ;;
esac

kubectl -n "$TASK_NS" rollout status deployment/cart --timeout=120s
kubectl -n "$TASK_NS" rollout status deployment/frontend --timeout=120s

ready=""
for _ in $(seq 1 30); do
  ready="$(kubectl -n "$TASK_NS" get endpointslices -l kubernetes.io/service-name=cart \
    -o jsonpath='{.items[*].endpoints[?(@.conditions.ready==true)].addresses[*]}' 2>/dev/null || true)"
  [ -n "$ready" ] && break
  sleep 1
done
if [ -z "$ready" ]; then
  echo "solve.sh: service cart has no ready endpoint after 30s" >&2
  exit 1
fi
echo "service cart ready endpoints: $ready"

pods="$(kubectl -n "$TASK_NS" get pods -l 'app in (cart, frontend)' -o jsonpath='{.items[*].metadata.name}')"
for _ in $(seq 1 60); do
  reached=1
  for pod in $pods; do
    kubectl -n "$TASK_NS" exec "$pod" -- wget -q -O /dev/null -T 3 http://cart/ 2>/dev/null || reached=0
  done
  if [ "$reached" = 1 ]; then
    echo "http://cart/ answers from every cart and frontend pod"
    exit 0
  fi
  sleep 1
done

echo "solve.sh: http://cart/ still fails from a pod after 60s" >&2
exit 1
