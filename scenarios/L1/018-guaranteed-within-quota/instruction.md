# Set requests and limits for shop so every pod is Guaranteed within its quota

## Ticket

> **From:** Priya (platform team, capacity)\
> **Priority:** medium
>
> Finance gave the `shop` namespace a fixed budget this quarter, so we put a
> ResourceQuota `shop` on it: `{{ .Vars.cpu }}` CPU and `{{ .Vars.memory }}`
> of memory, for requests and for limits. Since then `storefront`,
> `catalog` and `cart` show `0` ready and no pod comes up, even after the
> storefront team restarted the Deployments. Please give every container of
> the three Deployments requests and limits so all 5 pods run inside the
> budget, and make them Guaranteed: last month the node evicted the cart
> during a memory spike and we do not want that again. Do not raise the
> quota.

## Before you start

A ResourceQuota is checked when a pod is created, not when it runs. A
refused pod never exists, so `kubectl get pods` shows nothing and the
refusal is written as an Event on the ReplicaSet that tried to create it.
The QoS class in `status.qosClass` follows from the pod spec: `Guaranteed`
when every container sets CPU and memory requests equal to its limits.

## Environment

- Namespace: `{{ .Namespace }}`
- ResourceQuota `shop`: requests and limits of `{{ .Vars.cpu }}` CPU and
  `{{ .Vars.memory }}` memory
- Deployments `storefront` (2 replicas), `catalog` (2 replicas) and `cart`
  (1 replica), nginx `1.27-alpine`, container `web`{{ if eq .Vars.sidecar "yes" }};
  `catalog` also runs a sidecar container `heartbeat` (busybox){{ end }}
- An nginx container of this app uses about 5m CPU and 15Mi of memory at
  idle; size for some headroom
- You have cluster-admin on the local kind cluster

## Done when

- deployment storefront has 2 ready replicas
- deployment catalog has 2 ready replicas
- deployment cart has 1 ready replica
- every shop pod has QoS class Guaranteed
- no shop pod is Pending
- resourcequota shop counts the CPU limits of the running pods
- resourcequota shop still caps requests and limits at {{ .Vars.cpu }} CPU and {{ .Vars.memory }} memory

## Constraints

- Keep the ResourceQuota `shop` as it is
- Keep the Deployment names, replica counts and containers
- Do not delete the namespace
