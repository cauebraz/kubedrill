# Solution - 018 Set requests and limits for shop so every pod is Guaranteed within its quota

## Symptom

Deployments `storefront`, `catalog` and `cart` show `0/2`, `0/2` and
`0/1` READY and `kubectl get pods` lists no pod at all. Restarting the
Deployments changed nothing, because the new pods come from the same
template. Nothing crashes, so no pod status or log points at the cause.

## Candidate causes

Zero pods behind a Deployment, as opposed to pods that fail, fits a short
list, each with its own trace:

- Admission refuses the pods (ResourceQuota, LimitRange, Pod Security):
  the ReplicaSet shows DESIRED above CURRENT and `FailedCreate` Events
  with `forbidden`.
- The Deployment is scaled to 0 or paused: DESIRED is `0`, or
  `describe deploy` shows `Progressing ... DeploymentPaused`.
- The controller manager is down: no ReplicaSet Events at all and no
  progress on any Deployment in the cluster.
- Scheduling: pods would exist and stay `Pending` with
  `FailedScheduling`; here no pod exists, so this is out from the start.

## Observation that splits them

```sh
kubectl -n {{ .Namespace }} get deploy,rs,pods
kubectl -n {{ .Namespace }} describe rs -l app.kubernetes.io/name=catalog
kubectl -n {{ .Namespace }} describe resourcequota shop
```

The ReplicaSets show DESIRED `2` (or `1`) and CURRENT `0`, which rules
out scaling and pausing. Their Events repeat:

```
Warning  FailedCreate  replicaset-controller  Error creating: pods "catalog-5d68f96b54-4sxkn" is forbidden: failed quota: shop: must specify limits.cpu for: {{ if eq .Vars.sidecar "yes" }}heartbeat,web{{ else }}web{{ end }}; limits.memory for: ...; requests.cpu for: ...; requests.memory for: ...
```

which rules out the controller manager and names the quota. The list
after `for:` is every container still missing a value{{ if eq .Vars.sidecar "yes" }}: in this
episode `catalog` has two containers, `web` and the sidecar `heartbeat`,
and both need resources{{ end }}. `describe resourcequota shop` shows `Used` `0`
against `Hard` `{{ .Vars.cpu }}` CPU and `{{ .Vars.memory }}` memory for
requests and limits. `describe deploy catalog` adds the condition
`ReplicaFailure True FailedCreate`.

Then count containers to size them. The variants differ here:

- quota-2-cpu: 5 pods, 5 containers, budget 2 CPU and 2Gi: at most 400m
  and 409Mi per container.
- quota-1-cpu: 5 pods, 5 containers, budget 1 CPU and 1Gi: at most 200m
  and 204Mi per container; 250m admits 4 pods and refuses the fifth.
- catalog-sidecar: 5 pods, 7 containers, budget 1500m and 1536Mi: at most
  214m and 219Mi per container; 250m fits at most 6 of the 7 containers,
  so at least one pod is refused.

`kubectl -n {{ .Namespace }} get deploy -o 'custom-columns=NAME:.metadata.name,CONTAINERS:.spec.template.spec.containers[*].name'`
lists them.

## Change

Set requests equal to limits on every container of the three
Deployments, sized by the division above: `Hard` divided by the container
count, rounded down. `kubectl set resources` without `-c` applies to every
container of the template, sidecar included. 100m and 128Mi fit every
budget of this scenario: 5 containers take 500m and 640Mi, 7 take 700m and
896Mi. The idle nginx uses about 5m and 15Mi, so the size leaves headroom.

```sh
kubectl -n {{ .Namespace }} set resources deployment/storefront deployment/catalog deployment/cart --requests=cpu=100m,memory=128Mi --limits=cpu=100m,memory=128Mi
```

Larger values fit only the larger budget. In quota-2-cpu (2 CPU and 2Gi
over 5 containers) 250m and 256Mi take 1250m and 1280Mi:

```sh
kubectl -n {{ .Namespace }} set resources deployment/storefront --requests=cpu=250m,memory=256Mi --limits=cpu=250m,memory=256Mi
kubectl -n {{ .Namespace }} set resources deployment/catalog --requests=cpu=250m,memory=256Mi --limits=cpu=250m,memory=256Mi
kubectl -n {{ .Namespace }} set resources deployment/cart --requests=cpu=250m,memory=256Mi --limits=cpu=250m,memory=256Mi
```

In quota-1-cpu (1 CPU and 1Gi over 5 containers) and in catalog-sidecar
(1500m and 1536Mi over 7 containers) the same 250m crosses `Hard`, and the
last pods are refused with `exceeded quota`.

The second route is a LimitRange `shop-defaults` with `default` equal to
`defaultRequest` for CPU and memory, sized by the same division: admission fills both into any container that sets
none, so the pods come out Guaranteed without editing the Deployments.
The ReplicaSets retry with a growing backoff, so the pods can take a
minute to appear; `kubectl rollout restart` creates new ReplicaSets that
try at once.

## Verify

```sh
kubectl -n {{ .Namespace }} rollout status deployment/storefront
kubectl -n {{ .Namespace }} rollout status deployment/catalog
kubectl -n {{ .Namespace }} rollout status deployment/cart
kubectl -n {{ .Namespace }} get pods -o custom-columns=NAME:.metadata.name,QOS:.status.qosClass,PHASE:.status.phase
kubectl -n {{ .Namespace }} describe resourcequota shop
```

Each rollout ends with `successfully rolled out`, every pod shows
`Guaranteed` and `Running`, and the quota shows `Used` below `Hard`, for
example `limits.cpu 1250m 2` in quota-2-cpu (measured on kind v1.37).

## In the real world

- A quota on CPU and memory without a LimitRange turns every workload
  that never declared resources into `0` pods, silently: `kubectl apply`
  succeeds and only the ReplicaSet Events say why. Platform teams ship
  the quota and a LimitRange together.
- A quota sized exactly to the steady state blocks the next rollout. The
  default RollingUpdate surges one new pod before an old one stops; at
  `Used` equal to `Hard` it fails with `exceeded quota: shop, requested:
  limits.cpu=250m,... used: limits.cpu=2,...` and the rollout stalls.
  Keep headroom for `maxSurge`, or set `maxSurge: 0`.
- Guaranteed puts a pod last in the node-pressure eviction order while its
  usage stays within its requests, and gives it the lowest OOM score
  adjustment; it is still evicted when system daemons need the memory and
  still OOM-killed above its own limit. The price is no bursting: a CPU
  limit equal to the request throttles the bursts. Some teams keep CPU Burstable and
  memory equal, accepting Burstable to avoid throttling.
- `kubectl describe resourcequota` and `kubectl get events
  --field-selector reason=FailedCreate` belong in the first look at any
  namespace where Deployments show `0` and no pods.

## Go deeper

- Resource quotas, including the requirement to set requests or limits:
  https://kubernetes.io/docs/concepts/policy/resource-quotas/
- Pod QoS classes:
  https://kubernetes.io/docs/concepts/workloads/pods/pod-qos/
- LimitRange defaults:
  https://kubernetes.io/docs/concepts/policy/limit-range/
- Configure quotas for a namespace:
  https://kubernetes.io/docs/tasks/administer-cluster/manage-resources/quota-memory-cpu-namespace/
