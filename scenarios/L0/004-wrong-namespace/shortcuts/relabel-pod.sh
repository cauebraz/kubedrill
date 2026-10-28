#!/usr/bin/env bash
# why: a pod relabeled by hand leaves its ReplicaSet and serves until it dies; no Deployment named catalog owns the service
# variants: stray-deployment
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

kubectl -n "$TASK_NS" rollout status deployment/catalog-shop-tmp --timeout=120s
pod=$(kubectl -n "$TASK_NS" get pods -l app.kubernetes.io/name=catalog-tmp -o jsonpath='{.items[0].metadata.name}')
kubectl -n "$TASK_NS" label pod "$pod" app.kubernetes.io/name=catalog --overwrite
kubectl -n "$TASK_NS" wait --for=condition=Ready "pod/$pod" --timeout=60s
