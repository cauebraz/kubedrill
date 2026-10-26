# Readiness and endpoints

A Service sends traffic only to pods whose `Ready` condition is true, and that condition comes from the readiness probe. `Running` describes the process; `Ready` describes whether traffic may reach it; the two differ in exactly the cases that page you. The sentence to hold on call: a Deployment whose 2 pods are Running with READY 0/2 has zero endpoints, so the Service is down even though nothing crashed.

## The mechanism

Each container has three probes. The startup probe holds the other two off until it succeeds; failing, it kills the container. The liveness probe restarts the container when it fails. The readiness probe never restarts anything: on failure the kubelet sets the container's `ready: false`, the pod condition `ContainersReady` and then `Ready` go false, and the endpoint stops receiving traffic. Probe defaults are `periodSeconds: 10`, `timeoutSeconds: 1`, `failureThreshold: 3`, `successThreshold: 1`, so the third consecutive failure comes 20 seconds after the first, and a pod goes not ready 20 to 30 seconds after the app stops answering.

```
kubelet probes container   EndpointSlice controller    kube-proxy / CNI
  readiness ok ->          watches Pods, Services      watches slices
  pod.status.conditions    selector match + Ready      programs rules for
  [Ready]=True      --->   -> endpoints[].addresses -> addresses where
                           conditions.ready=true       conditions.ready=true
  readiness fail ->
  Ready=False       --->   conditions.ready=false   -> drops the address

Deployment controller counts Ready pods -> status.readyReplicas
```

The EndpointSlice controller in kube-controller-manager watches Services and Pods. For each pod matched by `spec.selector` it writes an entry in an `EndpointSlice` labeled `kubernetes.io/service-name=<svc>` with `addresses[]` and three `conditions`: `serving` mirrors the pod's `Ready` condition, `terminating` is set when the pod has a `deletionTimestamp`, and `ready` is `serving` and not `terminating`. Slices hold at most 100 endpoints by default. kube-proxy on every node (or the CNI replacing it) reads the slices and programs only the addresses with `conditions.ready=true`. `spec.publishNotReadyAddresses: true` on the Service forces `ready=true` for every endpoint; headless Services for StatefulSets use it so peers can find each other before they are ready.

The Deployment controller counts pods with `Ready=True` into `status.readyReplicas`, and those ready for at least `minReadySeconds` into `status.availableReplicas`. So `status.readyReplicas` shows a readiness failure that `status.replicas` hides.

## What you see on call

Check endpoints before DNS, and never restart first: a Service with no ready endpoints looks like a DNS or network fault from the client, and a restart discards the probe Events.

`kubectl get deploy <name> -n <ns>` shows READY as ready/desired; the gap is the alarm:

```
NAME    READY   UP-TO-DATE   AVAILABLE   AGE
hello   0/2     2            0           8m
```

`kubectl get pods -n <ns>` shows Running pods with READY 0/1; nothing restarts, so RESTARTS stays 0:

```
NAME                     READY   STATUS    RESTARTS   AGE
hello-7c9f5b6d8-xk2lp    0/1     Running   0          8m
```

`kubectl describe pod <pod> -n <ns>` names the probe and the answer in Events:

```
Warning  Unhealthy  Readiness probe failed: HTTP probe failed with statuscode: 404
Warning  Unhealthy  Readiness probe failed: Get "http://10.244.1.7:8081/healthz": dial tcp 10.244.1.7:8081: connect: connection refused
```

`kubectl get endpointslices -n <ns> -l kubernetes.io/service-name=<svc>` lists the slices; the ENDPOINTS column lists every address, ready or not, and prints `<unset>` only when no pod matches the selector or none has an IP yet; for readiness use the `conditions.ready` jsonpath below. Two Running, not-ready pods still show their IPs:

```
NAME          ADDRESSTYPE   PORTS   ENDPOINTS               AGE
hello-8kz2n   IPv4          80      10.244.1.7,10.244.2.4   8m
```

`kubectl get endpointslices -n <ns> -l kubernetes.io/service-name=<svc> -o jsonpath='{range .items[*].endpoints[*]}{.addresses[0]} ready={.conditions.ready} serving={.conditions.serving}{"\n"}{end}'` prints each endpoint with its conditions: what the Service routes to.

`kubectl get pod <pod> -n <ns> -o jsonpath='{.spec.containers[0].readinessProbe}'` prints the probe the kubelet runs; compare path, port and scheme with what the container listens on (`kubectl get pod <pod> -o jsonpath='{.spec.containers[0].ports}'`).

## Common failures

- Pods Running, READY 0/1, `Readiness probe failed: ... statuscode: 404` -> the probe path does not exist in this build -> `kubectl exec <pod> -- wget -qO- localhost:<port><path>` (or curl) from inside the container.
- `connection refused` on the probe -> the probe port differs from the listening port, or the app binds `127.0.0.1` -> `kubectl get pod <pod> -o jsonpath='{.spec.containers[0].readinessProbe.httpGet.port}'` against `containerPort`.
- `Readiness probe failed: ... context deadline exceeded` -> the app answers in more than `timeoutSeconds` (default 1s) -> `kubectl get pod <pod> -o jsonpath='{.spec.containers[0].readinessProbe.timeoutSeconds}'`.
- Service answers for some requests and fails for others -> one endpoint is ready and one is not -> `kubectl get endpointslices -l kubernetes.io/service-name=<svc> -o yaml` and read each `conditions`.
- EndpointSlice has no endpoints while pods are Ready -> the Service selector matches no pod (see `labels-and-selectors`) -> `kubectl get pods -l <selector from the service> -n <ns>`.
- Pods flap between Ready and not ready, RESTARTS 0 -> readiness `failureThreshold` reached under load, liveness untouched -> `kubectl get events --sort-by=.lastTimestamp -n <ns>` and count `Unhealthy` per minute.
- Pods restart every few minutes with no error log -> a liveness probe set where a readiness probe was meant -> `kubectl describe pod <pod>`, Events with `Liveness probe failed` followed by `Killing`.
- Rollout never finishes, `0 of 2 updated replicas are available` -> the new pods never reach Ready, the old ReplicaSet keeps serving -> `kubectl rollout status deployment/<name>`, then describe a new pod.

## Go deeper

- https://kubernetes.io/docs/concepts/workloads/pods/probes/ : the three probe kinds and the fields with their defaults.
- https://kubernetes.io/docs/concepts/services-networking/endpoint-slices/ : `conditions.ready`, `serving`, `terminating`, the `kubernetes.io/service-name` label and the 100 endpoint default.
- https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/#pod-conditions : `ContainersReady`, `Ready` and readiness gates.
- https://kubernetes.io/docs/reference/kubernetes-api/core/service-v1/ : `publishNotReadyAddresses` in the Service spec.
