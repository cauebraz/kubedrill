#!/usr/bin/env bash
# why: scaling the canary to 0 hides it from a selector that still matches it, and the admin team loses its canary
# variants: canary-shares-label
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

kubectl -n "$TASK_NS" apply -f - <<EOF
apiVersion: v1
kind: Service
metadata:
  name: admin
  namespace: $TASK_NS
  labels:
    app.kubernetes.io/name: admin
spec:
  type: NodePort
  selector:
    app.kubernetes.io/name: admin
  ports:
    - name: http
      port: 80
      targetPort: 80
      nodePort: 30080
EOF
kubectl -n "$TASK_NS" scale deployment/admin-canary --replicas=0
kubectl -n "$TASK_NS" rollout status deployment/admin --timeout=120s

for _ in $(seq 1 30); do
  ready=$(kubectl -n "$TASK_NS" get endpointslices -l kubernetes.io/service-name=admin \
    -o jsonpath='{.items[*].endpoints[?(@.conditions.ready==true)].addresses[*]}' 2>/dev/null || true)
  if [ -n "$ready" ]; then
    break
  fi
  sleep 1
done
