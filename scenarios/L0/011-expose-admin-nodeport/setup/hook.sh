#!/usr/bin/env bash
set -euo pipefail

: "${TASK_NS:?TASK_NS is required}" "${KUBECONFIG:?KUBECONFIG is required}"

scenario_id="011-expose-admin-nodeport"
port=30080

holder() {
  kubectl get services -A -o jsonpath='{range .items[*]}{.metadata.namespace} {.metadata.name} {.spec.ports[*].nodePort}{"\n"}{end}' 2>/dev/null \
    | awk -v p="$port" '{ for (i = 3; i <= NF; i++) if ($i == p) { print $1, $2; exit } }'
}

release() {
  ns=$1
  name=$2
  owner=$(kubectl get namespace "$ns" -o jsonpath='{.metadata.labels.kubedrill\.io/scenario}' 2>/dev/null || true)
  if [ "$ns" = "$TASK_NS" ] || [ "$owner" != "$scenario_id" ]; then
    return 1
  fi
  ops='[{"op":"replace","path":"/spec/type","value":"ClusterIP"}'
  i=0
  for np in $(kubectl -n "$ns" get service "$name" -o jsonpath='{range .spec.ports[*]}{.nodePort}{"\n"}{end}' | sed 's/^$/-/'); do
    if [ "$np" != "-" ]; then
      ops="$ops,{\"op\":\"remove\",\"path\":\"/spec/ports/$i/nodePort\"}"
    fi
    i=$((i + 1))
  done
  ops="$ops]"
  kubectl -n "$ns" patch service "$name" --type=json -p "$ops" >/dev/null
}

h=$(holder)
if [ -z "$h" ]; then
  exit 0
fi
ns=${h%% *}
name=${h#* }
if release "$ns" "$name"; then
  echo "nodePort $port released from Service $ns/$name of a previous episode"
  exit 0
fi
echo "nodePort $port is held by Service $ns/$name, which this scenario does not own; the Service of this episode will be refused until it is released" >&2
exit 0
