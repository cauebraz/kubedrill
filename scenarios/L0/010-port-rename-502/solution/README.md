# Solution - 010 Docs portal answers 502 since the web release that changed its port

## Symptom

`http://edge.{{ .Namespace }}.svc/` answers `502 Bad Gateway` on every
request since release 3.2.0 of `web` rolled out. The rollout finished and
the pods show `Running`, which is why the deploy looked fine: a rollout
only proves that the pods started and, where the probe still points at a
port that answers, that they passed it. Neither says that the Service in
front of them reaches the port nginx now listens on.

## Candidate causes

A proxy returns 502 when the next hop gives it no answer. Behind Service
`web` that hop can fail in four ways, each with its own trace:

- No pod is Ready: the readiness probe fails, READY shows `0/1`, Events
  repeat `Readiness probe failed`, the EndpointSlice lists the addresses
  with `conditions.ready: false`, and kube-proxy refuses the connection.
- The Service selector matches no pod: the EndpointSlice has no addresses
  at all (`<unset>` in ENDPOINTS) and `kubectl get pods -l <selector>` is
  empty.
- The Service's `targetPort` is a name no containerPort carries: the pods
  are Ready, the EndpointSlice lists them, but its PORTS column is
  `<unset>` and kube-proxy has nothing to forward to.
- The Service's `targetPort` is a number nothing listens on: the pods are
  Ready, the EndpointSlice is complete, and the connection to
  `<pod-ip>:<targetPort>` is refused at the pod.

The edge itself (its config, its DNS lookup of `web`) is a fifth candidate;
its log settles it in one line.

## Observation that splits them

```sh
kubectl -n {{ .Namespace }} logs deploy/edge
kubectl -n {{ .Namespace }} get pods
kubectl -n {{ .Namespace }} get endpointslices -l kubernetes.io/service-name=web
```

The edge log pairs each 502 with the upstream error (measured on kind
v1.37 with nginx 1.27):

```
[error] 31#31: *8 connect() failed (111: Connection refused) while connecting to upstream, client: 127.0.0.1, server: _, request: "GET / HTTP/1.1", upstream: "http://10.96.163.12:80/", host: "127.0.0.1"
```

The upstream is the ClusterIP of Service `web` on port 80, so DNS and the
edge config are fine: the Service refused the connection. The READY column
of the pods and the PORTS and ENDPOINTS columns of the EndpointSlice then
split the remaining causes, and they differ per variant:

- renamed-port: pods `1/1`, slice `web-xxxxx   IPv4   <unset>
  10.244.1.171,10.244.2.129`. Addresses but no port: the Service's
  `targetPort: http` resolved against containerPorts named `web`.
- moved-to-8080: pods `1/1`, slice `web-xxxxx   IPv4   80
  10.244.2.136,10.244.1.174`. A complete slice on port 80 while
  `web-nginx` says `listen 8080;`, so kube-proxy forwards to a port nothing
  listens on.
- probe-left-on-80: pods `0/1`, slice `web-xxxxx   IPv4   8080
  10.244.1.27,10.244.2.229` with every endpoint `ready=false`; `describe
  pod` shows `Readiness probe failed: Get "http://10.244.2.229:80/": dial
  tcp 10.244.2.229:80: connect: connection refused`. The probe still asks
  port 80 while nginx listens on 8080.

```sh
kubectl -n {{ .Namespace }} get svc web -o jsonpath='{.spec.ports[0].targetPort}{"\n"}'
kubectl -n {{ .Namespace }} get deploy web -o jsonpath='{.spec.template.spec.containers[0].ports}{"\n"}{.spec.template.spec.containers[0].readinessProbe.httpGet.port}{"\n"}'
kubectl -n {{ .Namespace }} get cm web-nginx -o jsonpath='{.data.default\.conf}'
kubectl -n {{ .Namespace }} get endpointslices -l kubernetes.io/service-name=web -o jsonpath='{range .items[*].endpoints[*]}{.addresses[0]} ready={.conditions.ready}{"\n"}{end}'
```

The three fields side by side name the one that disagrees with `listen`:
`targetPort` (a name that matches no containerPort, or the old number), or
the probe port. The `ready=` lines show the readiness failure that the
ENDPOINTS column hides, because that column lists not-ready addresses too.

## Change

Change the one field that still describes the old port. For the two
Service variants, point `targetPort` at the port the pod carries:

```sh
kubectl -n {{ .Namespace }} patch service web --type=json -p '[{"op":"replace","path":"/spec/ports/0/targetPort","value":"web"}]'
```

For moved-to-8080 the value is `8080` (a number, or `"http"` to resolve
the containerPort by name). For probe-left-on-80 the Service is already
right and the probe in the pod template is the field to change:

```sh
kubectl -n {{ .Namespace }} patch deployment web --type=json -p '[{"op":"replace","path":"/spec/template/spec/containers/0/readinessProbe/httpGet/port","value":"http"}]'
```

`kubectl edit svc web` or `kubectl edit deploy web` makes the same change
by hand, and a second valid route for renamed-port is to rename the
containerPort back to `http` in the pod template, which the Service then
resolves again. The checks read ready replicas, the EndpointSlice, and the
page through both Services, so every route passes. `web-nginx` stays as
shipped: the port move is the release, and reverting it would be undone by
the next deploy.

## Verify

```sh
kubectl -n {{ .Namespace }} rollout status deployment/web
kubectl -n {{ .Namespace }} get endpointslices -l kubernetes.io/service-name=web
kubectl -n {{ .Namespace }} run curl --rm -it --restart=Never --image=curlimages/curl:8.14.1 -- curl -sS -m 5 http://edge.{{ .Namespace }}.svc/
```

The EndpointSlice shows a number in PORTS (`80` for renamed-port, `8080`
for the others) and two addresses, and `rollout status` ends with
`deployment "web" successfully rolled out` (a Service patch needs no
rollout, so it returns at once). Before the fix the curl pod printed `curl:
(22) The requested URL returned error: 502` with `-f`, or the nginx 502
page without it; after the fix it prints the page with `<h1>ACME Docs
Portal 3.2.0</h1>`. A pod named `kubedrill-probe-...` that appears for a
few seconds is the grader making the same request.

## In the real world

- A port change in an image is a contract change with every object that
  names the port: Service `targetPort`, readiness and liveness probes,
  NetworkPolicy ports, ServiceMonitor endpoints. Naming the containerPort
  and referencing it by name from the Service and the probes means one
  edit in the pod template moves all of them together.
- `kubectl rollout status` reports success when the pods pass their probe;
  it does not request the page through the Service. A deploy pipeline that
  runs one request through the Service or the edge after the rollout turns
  this 502 into a failed deploy.
- A readiness probe left on the old port is the quieter variant: the
  rollout never completes, `rollout status` times out, and the old
  ReplicaSet keeps serving until it is scaled down, so staging can look
  fine while production fails after the surge pods are counted.
- `kubectl get endpointslices` is the split between "the pods are wrong"
  and "the Service is wrong". Read PORTS and the `conditions.ready` of
  every endpoint before touching either object.

## Go deeper

- Services, `targetPort` by name and by number:
  https://kubernetes.io/docs/concepts/services-networking/service/#defining-a-service
- EndpointSlices, `ports` and the endpoint conditions:
  https://kubernetes.io/docs/concepts/services-networking/endpoint-slices/
- Readiness probes and what a failing one does to the Service:
  https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/
- Debugging Services, from DNS to endpoints to kube-proxy:
  https://kubernetes.io/docs/tasks/debug/debug-application/debug-service/
