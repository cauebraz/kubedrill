#!/usr/bin/env bash
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

case "${VARIANT:-}" in
  stray-deployment)
    kubectl -n "$TASK_NS" apply -f - <<'YAML'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: catalog
  labels:
    app.kubernetes.io/name: catalog
    app.kubernetes.io/part-of: shop
    team: catalog
spec:
  replicas: 2
  selector:
    matchLabels:
      app.kubernetes.io/name: catalog
  template:
    metadata:
      labels:
        app.kubernetes.io/name: catalog
        app.kubernetes.io/part-of: shop
        team: catalog
    spec:
      terminationGracePeriodSeconds: 1
      containers:
        - name: web
          image: nginx:1.27-alpine@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10
          ports:
            - name: http
              containerPort: 80
          readinessProbe:
            httpGet:
              path: /
              port: http
          volumeMounts:
            - name: html
              mountPath: /usr/share/nginx/html
              readOnly: true
          resources:
            requests:
              cpu: 10m
              memory: 32Mi
            limits:
              memory: 64Mi
      volumes:
        - name: html
          configMap:
            name: catalog-html
YAML
    kubectl -n "$TASK_NS" rollout status deployment/catalog --timeout=120s
    kubectl -n "$TASK_NS" delete deployment catalog-shop-tmp --wait=false
    ;;
  selector-old-value)
    kubectl -n "$TASK_NS" patch service catalog --type merge -p '{"spec":{"selector":{"app.kubernetes.io/name":"catalog"}}}'
    ;;
  stale-target-port)
    kubectl -n "$TASK_NS" patch service catalog --type json -p '[{"op":"replace","path":"/spec/ports/0/targetPort","value":80}]'
    ;;
  *)
    echo "unknown variant: ${VARIANT:-}" >&2
    exit 1
    ;;
esac

kubectl -n "$TASK_NS" wait --for=jsonpath='{.status.readyReplicas}'=2 deployment/catalog --timeout=120s
kubectl -n "$TASK_NS" run kd-solve-curl --restart=Never \
  --image=curlimages/curl:8.14.1@sha256:9a1ed35addb45476afa911696297f8e115993df459278ed036182dd2cd22b67b \
  -- sh -c 'until curl -fsS -m 3 http://catalog/; do sleep 2; done'
kubectl -n "$TASK_NS" wait --for=jsonpath='{.status.phase}'=Succeeded pod/kd-solve-curl --timeout=180s
kubectl -n "$TASK_NS" delete pod kd-solve-curl --wait=false
