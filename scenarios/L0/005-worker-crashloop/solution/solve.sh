#!/usr/bin/env bash
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}" "${VARIANT:?VARIANT is required}"

case "$VARIANT" in
  renamed-key)
    url=$(kubectl -n "$TASK_NS" get configmap worker-env -o jsonpath="{.data.${VAR_QUEUE_KEY:?}}")
    kubectl -n "$TASK_NS" patch configmap worker-env --type merge -p "{\"data\":{\"QUEUE_URL\":\"$url\",\"${VAR_QUEUE_KEY}\":null}}"
    kubectl -n "$TASK_NS" rollout restart deployment/worker
    ;;
  unknown-flag)
    kubectl -n "$TASK_NS" patch deployment worker --type json -p '[{"op":"replace","path":"/spec/template/spec/containers/0/args","value":["--workers=4"]}]'
    ;;
  wrong-subpath)
    kubectl -n "$TASK_NS" patch deployment worker --type json -p '[{"op":"replace","path":"/spec/template/spec/containers/0/volumeMounts/1/subPath","value":"worker.conf"}]'
    ;;
  *)
    echo "solve.sh: unknown variant $VARIANT" >&2
    exit 1
    ;;
esac

kubectl -n "$TASK_NS" rollout status deployment/worker --timeout=120s
