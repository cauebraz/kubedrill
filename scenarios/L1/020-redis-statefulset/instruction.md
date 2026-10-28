# Build a 3-replica redis StatefulSet with stable DNS and a headless Service

## Ticket

> **From:** Yuki (session team)\
> **Priority:** medium
>
> We are moving login sessions off the shared cache onto our own redis in
> `{{ .Namespace }}`. `session-api` is already deployed and its ConfigMap
> lists the three nodes it shards across:
> `redis-0.redis.{{ .Namespace }}.svc.cluster.local:6379`, `redis-1...` and
> `redis-2...`. Its logs say none of them resolve, because nothing called
> `redis` exists yet. Can platform build it? Three redis 7.4 nodes on the
> `redis:7.4-alpine` image, each with its own 100Mi volume on the `standard`
> storage class so a restart keeps the data, and each answering at the exact
> name in the list. A plain load-balanced address does not work for us: the
> client picks the node, not the network.
{{- if eq .Vars.policy "Parallel" }}
> After a restore we bring all three back from backups at once, so please
> start the pods all together, not one by one.
{{- else if ne .Vars.config "none" }}
> We tuned redis for sessions: the file is in ConfigMap `redis-config`. Mount
> it at `/usr/local/etc/redis/redis.conf` and start `redis-server` with it.
{{- else }}
> Our failover tooling expects `redis-0` up before `redis-1`, and `redis-1`
> before `redis-2`, so start the pods in order.
{{- end }}

New to Kubernetes? `kubedrill story` tells this ticket from zero.

## Before you start

A StatefulSet gives each pod a fixed name (`redis-0`, `redis-1`, ...) and its
own PersistentVolumeClaim from `volumeClaimTemplates`. The DNS name
`<pod>.<service>.<ns>.svc.cluster.local` exists when `spec.serviceName` names
a Service whose selector matches the pods, and only while the pod is Ready.
A StatefulSet's Service is headless (`clusterIP: None`), so `redis.<ns>.svc`
answers with the pods instead of one load-balanced address. `kubectl explain
statefulset.spec` lists the fields.

## Environment

- Namespace: `{{ .Namespace }}`
- Resources: Deployment `session-api` (the client, logs every 10 seconds
  which redis nodes it reaches), ConfigMap `session-api-config` (the node
  list){{ if ne .Vars.config "none" }}, ConfigMap `redis-config` (the redis
  configuration file){{ end }}; no Service or StatefulSet `redis` yet
- Image: `redis:7.4-alpine` (preloaded on the nodes); give the pods the label
  `app=redis` and a readiness probe that runs `redis-cli ping`
- Storage: StorageClass `standard` (the kind default, local-path provisioner)
- You have cluster-admin on the local kind cluster

## Done when

- statefulset redis has 3 ready replicas
- service redis is headless (clusterIP None)
- service redis has 3 ready endpoints, redis-0, redis-1 and redis-2
- redis-2.redis.{{ .Namespace }}.svc.cluster.local resolves and answers PING on port 6379
- persistentvolumeclaims data-redis-0, data-redis-1 and data-redis-2 are Bound
- {{ .Vars.goal }}

## Constraints

- Name the Service and the StatefulSet `redis` and the volumeClaimTemplate
  `data`; session-api's node list depends on these names
- Do not change Deployment `session-api` or its ConfigMap
- Do not delete the namespace
