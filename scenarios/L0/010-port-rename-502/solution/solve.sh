#!/usr/bin/env bash
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

case "${VARIANT:-}" in
  renamed-port)
    kubectl -n "$TASK_NS" patch service web --type=json \
      -p '[{"op":"replace","path":"/spec/ports/0/targetPort","value":"web"}]'
    ;;
  moved-to-8080)
    kubectl -n "$TASK_NS" patch service web --type=json \
      -p '[{"op":"replace","path":"/spec/ports/0/targetPort","value":8080}]'
    ;;
  probe-left-on-80)
    kubectl -n "$TASK_NS" patch deployment web --type=json \
      -p '[{"op":"replace","path":"/spec/template/spec/containers/0/readinessProbe/httpGet/port","value":"http"}]'
    ;;
  *)
    echo "unknown variant: ${VARIANT:-}" >&2
    exit 1
    ;;
esac

kubectl -n "$TASK_NS" rollout status deployment/web --timeout=120s

for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
  if kubectl -n "$TASK_NS" exec deploy/edge -- wget -qO- -T 2 http://127.0.0.1/ 2>/dev/null | grep -q "ACME Docs Portal 3.2.0"; then
    exit 0
  fi
  sleep 2
done

echo "the edge still does not serve the page" >&2
exit 1
