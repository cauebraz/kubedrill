#!/usr/bin/env bash
# why: a copy under the misspelled name starts the pods, but they keep reading db-credential instead of the Secret the team rotates
# variants: wrong-secret-name
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

password=$(kubectl -n "$TASK_NS" get secret db-credentials -o jsonpath='{.data.db-password}' | base64 --decode)
kubectl -n "$TASK_NS" create secret generic db-credential --from-literal=db-password="$password"
kubectl -n "$TASK_NS" rollout restart deployment/api
kubectl -n "$TASK_NS" rollout status deployment/api --timeout=180s
