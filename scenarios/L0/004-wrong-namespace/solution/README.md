# Solution - 004 Catalog copied into the shop namespace, storefront cannot reach it

## Symptom

From the storefront pod, `wget -qO- http://catalog/` answers `Connection
refused` at once, and the storefront's catalog panel is empty. `kubectl
apply` printed `created` for every object because the API server only
validates each manifest on its own; nothing checks that a Service's selector
and targetPort match the pods that exist, and the catalog pods are Running
with READY `1/1`.

## Candidate causes

A name that resolves and refuses the connection at once, with Running pods,
fits several causes. Each one leaves a different trace:

- Service selector matches no pod: `describe service` shows an empty
  `Endpoints` line, `get pods -l <selector>` returns nothing. The reason can
  sit on either side: the pods were created under another name and label,
  or the selector carries a value from somewhere else.
- Service targetPort names a port nothing listens on: endpoints are listed,
  with the wrong port, and every connection is refused by the pod.
- Pods not Ready: READY `0/1`, endpoints present with `ready=false`, Events
  repeat `Readiness probe failed`.
- Pods crashing: STATUS `CrashLoopBackOff`, RESTARTS grows.
- NetworkPolicy blocking the storefront: the connection times out instead of
  being refused.
- DNS: the name would not resolve; `Connection refused` names an IP, so the
  lookup worked.

## Observation that splits them

```sh
kubectl -n {{ .Namespace }} get deployments
kubectl -n {{ .Namespace }} describe service catalog
kubectl -n {{ .Namespace }} get pods --show-labels
```

`get deployments` shows every catalog pod Running and Ready (READY `2/2`),
which rules out the probe and the crash loop. `describe service catalog`
prints `Selector`, `TargetPort` and `Endpoints` next to each other, and the
LABELS column of `get pods --show-labels` is the other half of the
comparison. The line that differs per variant (measured on kind v1.37):

- stray-deployment: `get deployments` lists `catalog-shop-tmp` and
  `storefront`, no `catalog`. The pods carry
  `app.kubernetes.io/name=catalog-tmp`, the Service selects
  `app.kubernetes.io/name=catalog`, and the `Endpoints` line is empty. The
  manifest copied from the old namespace was renamed and relabeled during
  the copy, so this namespace holds a catalog under a name and label nothing
  else expects.
- selector-old-value: `get deployments` lists `catalog` and `storefront`.
  The pods carry `app.kubernetes.io/name=catalog`, the Service selects
  `app.kubernetes.io/name=catalog-api`, and the `Endpoints` line is empty.
  `kubectl -n {{ .Namespace }} get pods -l app.kubernetes.io/name=catalog-api`
  prints `No resources found`.
- stale-target-port: the selector matches, `Endpoints` lists two addresses
  with `:8080`, `TargetPort: 8080/TCP`, and `wget` is still refused. The
  nginx container declares `containerPort: 80` and listens there; 8080 was
  the catalog's port in the old namespace.

```sh
kubectl -n {{ .Namespace }} get pods -l team=catalog -o jsonpath='{.items[*].spec.containers[0].ports[*].containerPort}'
kubectl -n {{ .Namespace }} get endpointslices -l kubernetes.io/service-name=catalog
```

The first prints `80 80`, the port the Service must target. The EndpointSlice
shows the Service side of the match: `<unset>` under ENDPOINTS when no pod
matches the selector, and the PORTS column shows the port the Service
forwards to.

## Change

Make this namespace look like every other environment: a Deployment named
`catalog` whose pods carry `app.kubernetes.io/name=catalog`, behind Service
`catalog` selecting that label on targetPort 80. Change only the side that
differs.

For the stray Deployment, a Deployment's selector is immutable, so create
the right one and remove the stray one:

```sh
kubectl -n {{ .Namespace }} get deployment catalog-shop-tmp -o yaml > catalog.yaml
sed -i.bak -e 's/catalog-shop-tmp/catalog/g' -e 's/app.kubernetes.io\/name: catalog-tmp/app.kubernetes.io\/name: catalog/g' catalog.yaml
kubectl -n {{ .Namespace }} apply -f catalog.yaml
kubectl -n {{ .Namespace }} rollout status deployment/catalog
kubectl -n {{ .Namespace }} delete deployment catalog-shop-tmp
```

For a wrong selector value, patch the Service:

```sh
kubectl -n {{ .Namespace }} patch service catalog --type merge -p '{"spec":{"selector":{"app.kubernetes.io/name":"catalog"}}}'
```

For a wrong targetPort, patch the port:

```sh
kubectl -n {{ .Namespace }} patch service catalog --type json -p '[{"op":"replace","path":"/spec/ports/0/targetPort","value":80}]'
```

Editing the Service with `kubectl edit service catalog` reaches the same end
state. The checks read the Deployment name and ready count, the Service's
EndpointSlices and the page served through the Service, so either route
passes. Changing the Service selector to `catalog-tmp` would also give it
endpoints, but it leaves a Deployment nobody else expects in the namespace
and the dashboards still find no `catalog`.

## Verify

```sh
kubectl -n {{ .Namespace }} get deployments
kubectl -n {{ .Namespace }} get endpointslices -l kubernetes.io/service-name=catalog
kubectl -n {{ .Namespace }} exec deploy/storefront -- wget -qO- -T 5 http://catalog/
```

A Service patch takes effect once the EndpointSlice controller and
kube-proxy on every node have caught up, a few seconds on kind, so a check
run in the same second as the patch can still see a refusal; run it again.
`get deployments` lists `catalog` with READY `2/2` and `storefront`, nothing
else. The EndpointSlice lists two addresses under PORTS `80`. The `wget`
prints the page with `<h1>ACME Catalog 3.2.0</h1>`; before the fix it printed
`wget: can't connect to remote host (10.96.x.x): Connection refused`
(measured on kind v1.37), the refusal coming from kube-proxy when the
Service had no endpoints and from the pod when the targetPort was wrong.

## In the real world

- A move between namespaces copies manifests together with the labels, ports
  and names of the source. `kubectl diff -f` against the target namespace
  before the apply shows what the copy would change, and a `grep` for the old
  namespace, old port and old label values in the copied files catches most
  of it.
- `kubectl apply` reports success per object. Nothing validates a Service
  against its pods, so a pipeline that ends in `rollout status` still misses
  a selector or port mismatch; a smoke request through the Service after the
  deploy is what catches it.
- `kubectl get endpointslices` is the split between "the pods are fine" and
  "the Service does not reach them". Check it before DNS and before
  restarting anything.
- Shared label conventions (`app.kubernetes.io/name`, one value per
  workload across every namespace) make the selector obvious to the next
  reader; a temporary label invented during a migration outlives the
  migration.

## Go deeper

- Services, selectors and targetPort:
  https://kubernetes.io/docs/concepts/services-networking/service/#defining-a-service
- Debugging Services, from the pod labels to the endpoints:
  https://kubernetes.io/docs/tasks/debug/debug-application/debug-service/
- Labels and selectors:
  https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/
