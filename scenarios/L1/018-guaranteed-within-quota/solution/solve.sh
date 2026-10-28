#!/usr/bin/env bash
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}" "${VARIANT:?VARIANT is required}"

case "$VARIANT" in
  quota-2-cpu)
    for d in storefront catalog cart; do
      kubectl -n "$TASK_NS" set resources deployment/"$d" --requests=cpu=250m,memory=256Mi --limits=cpu=250m,memory=256Mi
    done
    ;;
  quota-1-cpu)
    for d in storefront catalog cart; do
      kubectl -n "$TASK_NS" set resources deployment/"$d" --requests=cpu=100m,memory=128Mi --limits=cpu=100m,memory=128Mi
    done
    ;;
  catalog-sidecar)
    kubectl -n "$TASK_NS" apply -f - <<'YAML'
apiVersion: v1
kind: LimitRange
metadata:
  name: shop-defaults
spec:
  limits:
    - type: Container
      default:
        cpu: 100m
        memory: 128Mi
      defaultRequest:
        cpu: 100m
        memory: 128Mi
YAML
    kubectl -n "$TASK_NS" rollout restart deployment/storefront deployment/catalog deployment/cart
    ;;
  *) echo "unknown variant $VARIANT" >&2; exit 1 ;;
esac
for d in storefront catalog cart; do
  kubectl -n "$TASK_NS" rollout status deployment/"$d" --timeout=120s
done
