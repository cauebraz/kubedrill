#!/usr/bin/env bash
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

case "${VARIANT:-}" in
  mount-path-typo)
    patch='[{"op":"replace","path":"/spec/template/spec/containers/0/volumeMounts/0/mountPath","value":"/etc/secrets"}]'
    ;;
  key-name)
    patch='[{"op":"add","path":"/spec/template/spec/volumes/0/secret/items","value":[{"key":"password","path":"db-password"}]}]'
    ;;
  wrong-secret-name)
    patch='[{"op":"replace","path":"/spec/template/spec/volumes/0/secret/secretName","value":"db-credentials"}]'
    ;;
  *)
    echo "solve.sh: unknown variant '${VARIANT:-}'" >&2
    exit 1
    ;;
esac

kubectl -n "$TASK_NS" patch deployment api --type=json -p "$patch"
kubectl -n "$TASK_NS" rollout status deployment/api --timeout=180s
