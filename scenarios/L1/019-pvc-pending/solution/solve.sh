#!/usr/bin/env bash
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

case "${VARIANT:?VARIANT is required}" in
  claim-name-typo)
    kubectl -n "$TASK_NS" patch deployment uploads --type=json \
      -p '[{"op":"replace","path":"/spec/template/spec/volumes/0/persistentVolumeClaim/claimName","value":"uploads-data"}]'
    ;;
  *)
    kubectl -n "$TASK_NS" delete pvc uploads-data --wait=true --timeout=60s
    kubectl -n "$TASK_NS" apply -f - <<YAML
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: uploads-data
  labels:
    app.kubernetes.io/name: uploads
    app.kubernetes.io/part-of: shop
spec:
  storageClassName: standard
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
YAML
    ;;
esac

kubectl -n "$TASK_NS" rollout status deployment/uploads --timeout=120s
kubectl -n "$TASK_NS" wait pvc/uploads-data --for=jsonpath='{.status.phase}'=Bound --timeout=60s
