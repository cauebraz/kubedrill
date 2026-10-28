#!/usr/bin/env bash
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"
: "${VAR_IMAGE:?VAR_IMAGE is required}" "${VAR_PROBE_PATH:?VAR_PROBE_PATH is required}" "${VAR_CPU:?VAR_CPU is required}" "${VAR_KEEP_OLD_KEY:?VAR_KEEP_OLD_KEY is required}"

kubectl -n "$TASK_NS" rollout status deployment/checkout --timeout=180s

url="http://payments.$TASK_NS.svc.cluster.local"
if [ "$VAR_KEEP_OLD_KEY" = "true" ]; then
  config="{\"data\":{\"payments_endpoint\":\"$url\"}}"
else
  config="{\"data\":{\"payments_endpoint\":\"$url\",\"payments_url\":null}}"
fi
kubectl -n "$TASK_NS" patch configmap checkout-config --type=merge -p "$config" >/dev/null

patch=$(cat <<JSON
{"metadata":{"annotations":{"kubernetes.io/change-cause":"release 2.5.0"}},
 "spec":{"template":{"metadata":{"labels":{"app.kubernetes.io/version":"2.5.0"}},
  "spec":{"containers":[{"name":"web","image":"$VAR_IMAGE",
   "env":[{"name":"PAYMENTS_URL","valueFrom":{"configMapKeyRef":{"name":"checkout-config","key":"payments_endpoint"}}}],
   "readinessProbe":{"httpGet":{"path":"$VAR_PROBE_PATH","port":"http"}},
   "resources":{"requests":{"cpu":"$VAR_CPU","memory":"32Mi"}}}]}}}}
JSON
)
kubectl -n "$TASK_NS" patch deployment checkout --type=strategic -p "$patch" >/dev/null

gen=$(kubectl -n "$TASK_NS" get deployment checkout -o jsonpath='{.metadata.generation}')
i=0
while [ "$i" -lt 60 ]; do
  observed=$(kubectl -n "$TASK_NS" get deployment checkout -o jsonpath='{.status.observedGeneration}')
  updated=$(kubectl -n "$TASK_NS" get deployment checkout -o jsonpath='{.status.updatedReplicas}')
  if [ "$observed" = "$gen" ] && [ "${updated:-0}" != "4" ]; then
    exit 0
  fi
  sleep 1
  i=$((i + 1))
done
echo "deployment checkout did not start revision 2 within 60s" >&2
exit 1
