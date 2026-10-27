#!/usr/bin/env bash
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}" "${VAR_PORT:?VAR_PORT is required}"

kubectl -n "$TASK_NS" apply -f - <<EOF
apiVersion: v1
kind: Service
metadata:
  name: admin
  namespace: $TASK_NS
  labels:
    app.kubernetes.io/name: admin
    app.kubernetes.io/part-of: backoffice
spec:
  type: NodePort
  selector:
    app.kubernetes.io/name: admin
    track: stable
  ports:
    - name: http
      port: 80
      targetPort: $VAR_PORT
      nodePort: 30080
EOF

for _ in $(seq 1 30); do
  ready=$(kubectl -n "$TASK_NS" get endpointslices -l kubernetes.io/service-name=admin -o jsonpath='{.items[*].endpoints[?(@.conditions.ready==true)].addresses[*]}' 2>/dev/null || true)
  if [ -n "$ready" ]; then
    echo "service admin ready endpoints: $ready"
    exit 0
  fi
  sleep 1
done
echo "service admin has no ready endpoint after 30s" >&2
exit 1
