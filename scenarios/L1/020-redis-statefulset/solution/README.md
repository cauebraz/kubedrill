# Solution - 020 Build a 3-replica redis StatefulSet with stable DNS and a headless Service

## Symptom

`session-api` logs `redis node redis-0.redis.{{ .Namespace }}.svc.cluster.local:6379:
name does not resolve` for all three nodes and `0 of 3 redis nodes
reachable, logins fail with 503`. The client is Running and Ready, so its
own status says nothing: the outage is in what it calls, which does not
exist yet.

## Candidate causes

A per-pod name such as `redis-2.redis.<ns>.svc.cluster.local` stays
unresolved for any of these reasons, each with its own trace:

- No Service `redis`: `kubectl get svc` lists nothing, every lookup answers
  NXDOMAIN.
- Service `redis` has a cluster IP: `redis.<ns>.svc` resolves to one virtual
  address that load-balances. CoreDNS still publishes `redis-2.redis...` for
  the pods of a StatefulSet that names the Service in `serviceName`
  (measured on kind v1.37 with CoreDNS 1.14), so this is not a cause of
  NXDOMAIN; it fails the headless check and the client's contract instead.
- Pods without stable names: a Deployment names pods
  `redis-<hash>-<suffix>`, so no pod is ever called `redis-2`.
- StatefulSet `spec.serviceName` names another Service: the pods get the
  hostname but no record under `redis`.
- Service selector that misses the pods: the EndpointSlice of `redis` lists
  no addresses, so DNS has nothing to publish.
- Pod not Ready (a failing probe, a claim that never binds): the endpoint is
  `ready=false` and its record is left out until it passes.

## Observation that splits them

```sh
kubectl -n {{ .Namespace }} get configmap session-api-config -o yaml
kubectl -n {{ .Namespace }} logs deploy/session-api --tail=4
kubectl -n {{ .Namespace }} get statefulset,pods,pvc,svc
kubectl -n {{ .Namespace }} get endpointslices -l kubernetes.io/service-name=redis -o wide
```

Before the build, `get statefulset,pods,pvc,svc` shows only the
`session-api` pod, which rules everything but "nothing exists" out and
fixes the shape of the answer from the names alone: `redis-2` is pod
`<statefulset>-<ordinal>`, so a StatefulSet named `redis`; `.redis.` is the
Service in `serviceName`, headless; port 6379.

After the build, the same commands split the remaining causes (measured on
kind v1.37, columns trimmed):

```
NAME                     READY   AGE
statefulset.apps/redis   3/3     76s
NAME                                 STATUS   CAPACITY   STORAGECLASS
persistentvolumeclaim/data-redis-0   Bound    100Mi      standard
NAME            TYPE        CLUSTER-IP   PORT(S)
service/redis   ClusterIP   None         6379/TCP
NAME          ADDRESSTYPE   PORTS   ENDPOINTS
redis-lcvjn   IPv4          6379    10.244.2.65,10.244.1.125,10.244.1.127
```

`CLUSTER-IP None` is the headless Service, three ENDPOINTS mean the selector
matched three Ready pods, and three Bound claims named
`data-redis-<ordinal>` come from the volumeClaimTemplate `data`. A claim
that shows `Pending` with the Event `waiting for first consumer to be
created before binding` is the `standard` class (WaitForFirstConsumer)
waiting for its pod to be scheduled, not a failure.

## Change

Create the headless Service and the StatefulSet that names it, in one file.

```sh
cat > redis.yaml <<'YAML'
apiVersion: v1
kind: Service
metadata: {name: redis, labels: {app: redis}}
spec:
  clusterIP: None
  selector: {app: redis}
  ports: [{name: redis, port: 6379, targetPort: 6379}]
---
apiVersion: apps/v1
kind: StatefulSet
metadata: {name: redis, labels: {app: redis}}
spec:
  serviceName: redis
  replicas: 3
  podManagementPolicy: {{ .Vars.policy }}
  selector: {matchLabels: {app: redis}}
  template:
    metadata: {labels: {app: redis}}
    spec:
      terminationGracePeriodSeconds: 1
      containers:
        - name: redis
          image: redis:7.4-alpine@sha256:6ba19c50b56777f5c11c58eff8ddb69d0d5f27ba131cffb63f180ccfd43c66af
{{- if ne .Vars.config "none" }}
          args: ["redis-server", "/usr/local/etc/redis/redis.conf"]
{{- end }}
          ports: [{name: redis, containerPort: 6379}]
          readinessProbe:
            exec: {command: ["redis-cli", "ping"]}
            periodSeconds: 2
          volumeMounts:
            - {name: data, mountPath: /data}
{{- if ne .Vars.config "none" }}
            - {name: config, mountPath: /usr/local/etc/redis/redis.conf, subPath: redis.conf, readOnly: true}
      volumes:
        - {name: config, configMap: {name: redis-config}}
{{- end }}
  volumeClaimTemplates:
    - metadata: {name: data}
      spec:
        accessModes: [ReadWriteOnce]
        storageClassName: standard
        resources: {requests: {storage: 100Mi}}
YAML
kubectl -n {{ .Namespace }} apply -f redis.yaml
```

The variants ask for different settings:

- ordered-startup: `podManagementPolicy: OrderedReady` (the default; writing
  it makes the intent visible). `redis-1` is created only after `redis-0` is
  Ready.
- parallel-startup: `podManagementPolicy: Parallel`. All three pods are
  created at once. The field is immutable: a StatefulSet created without it
  has to be recreated, and the new one reuses the claims by name.
- tuned-config: a ConfigMap volume for `redis-config` mounted with `subPath`
  at the file path, and `redis-server /usr/local/etc/redis/redis.conf` as
  the container arguments. The file sets `maxmemory-policy allkeys-lru`
  and `appendonly yes`; passing the same settings as flags leaves
  `config_file` empty in `redis-cli info server`, and the check reads it.

The Service can also come from `kubectl create service clusterip redis
--clusterip=None --tcp=6379:6379`, which sets the selector `app=redis`.

## Verify

```sh
kubectl -n {{ .Namespace }} rollout status statefulset/redis --timeout=240s
kubectl -n {{ .Namespace }} get endpointslices -l kubernetes.io/service-name=redis -o wide
kubectl -n {{ .Namespace }} exec deploy/session-api -- nslookup redis-2.redis.{{ .Namespace }}.svc.cluster.local
kubectl -n {{ .Namespace }} logs deploy/session-api --tail=4
kubectl -n {{ .Namespace }} exec redis-0 -- redis-cli config get maxmemory-policy
kubectl -n {{ .Namespace }} exec redis-0 -- redis-cli info server | grep config_file
```

`rollout status` ends with `partitioned roll out complete: 3 new pods have
been updated...`. `nslookup` answers one address, the IP of pod `redis-2`;
before the build it answered `NXDOMAIN`. Within 10 seconds the client logs
`3 of 3 redis nodes reachable, sessions stored`. The last command prints
`allkeys-lru` on tuned-config and `noeviction`, the redis default, on the
other variants; `config_file` is `/usr/local/etc/redis/redis.conf` on
tuned-config and empty on the others. `nslookup redis.{{ .Namespace }}.svc.cluster.local` lists all
three pod IPs: a headless Service answers with the pods, not a virtual IP.

## In the real world

- `volumeClaimTemplates`, `serviceName`, `selector` and
  `podManagementPolicy` are immutable. Changing them means removing the
  StatefulSet with `--cascade=orphan` and creating a new one that adopts the
  running pods, a step to rehearse before a production database needs it.
- Claims outlive the StatefulSet by default. Scaling down or removing it
  keeps `data-redis-*`, which protects the data and also keeps paying for
  the disks; `persistentVolumeClaimRetentionPolicy` changes that.
- Per-pod records exist only for Ready pods unless the Service sets
  `publishNotReadyAddresses: true`, which clustered databases (redis
  cluster, etcd, Cassandra) need so peers can find each other while they
  start.
- Sharding in the client, as session-api does, loses a third of the keys
  when one node is gone; redis Sentinel or redis Cluster add failover, and
  their Helm charts wire the headless Service the same way.

## Go deeper

- StatefulSets: https://kubernetes.io/docs/concepts/workloads/controllers/statefulset/
- StatefulSet basics tutorial: https://kubernetes.io/docs/tutorials/stateful-application/basic-stateful-set/
- DNS for Services and Pods: https://kubernetes.io/docs/concepts/services-networking/dns-pod-service/
- Headless Services: https://kubernetes.io/docs/concepts/services-networking/service/#headless-services
