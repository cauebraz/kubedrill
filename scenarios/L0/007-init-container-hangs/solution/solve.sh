#!/usr/bin/env bash
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}" "${VARIANT:?VARIANT is required}"

case "$VARIANT" in
  missing-service-name)
    kubectl -n "$TASK_NS" set env deployment/api -c migrate DB_HOST=db
    ;;
  wrong-port)
    kubectl -n "$TASK_NS" set env deployment/api -c migrate DB_PORT=5432
    ;;
  flag-mount-path)
    kubectl -n "$TASK_NS" patch deployment api --type=json \
      -p '[{"op":"replace","path":"/spec/template/spec/initContainers/0/volumeMounts/0/mountPath","value":"/etc/migrate"}]'
    ;;
  *)
    echo "solve.sh: unknown variant $VARIANT" >&2
    exit 1
    ;;
esac

kubectl -n "$TASK_NS" rollout status deployment/api --timeout=120s
