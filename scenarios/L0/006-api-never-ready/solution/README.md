# Solution - 006 api never becomes Ready; the dev says it starts in 2 seconds

## Symptom

Deployment `api` shows `0/2` ready, both pods are `Running` with READY `0/1`
and RESTARTS 0, and `http://api.{{ .Namespace }}.svc` answers connection
refused. Nothing crashed, so the usual signals (restart count, crash logs,
a pod stuck in `Pending`) stay green. `Running` reports the process;
`Ready` reports the readiness probe, and the Service routes only to Ready
pods.

## Candidate causes

Running pods that never turn Ready, with no restarts, fit a short list.
Each one leaves a different trace:

- Readiness probe asks the wrong question: wrong path, wrong port, a
  command that fails. Events repeat `Readiness probe failed` with the
  answer the probe got.
- The app is slow to start: the same Events, but they stop after the first
  minutes and the pods turn Ready late; the ticket's "2 seconds" and a
  probe that fails forever rule it out.
- The app listens on 127.0.0.1 or on another port than the one declared:
  Events show `connection refused` and `kubectl exec` into the container
  shows the listening socket.
- A readiness gate or a `startupProbe` that never succeeds: the pod's
  conditions name it; with a failing startup probe the container is killed
  and RESTARTS grows.
- Selector mismatch: the pods would be Ready and the EndpointSlice empty;
  READY `0/1` on the pods rules it out before you look at the Service.

## Observation that splits them

```sh
kubectl -n {{ .Namespace }} get pods
kubectl -n {{ .Namespace }} describe pod <pod-name>
kubectl -n {{ .Namespace }} get events --sort-by=.lastTimestamp
```

Take `<pod-name>` from the NAME column of `get pods`. Every pod shows
`Running` and `0/1`, RESTARTS 0, which rules out a crash, an image problem
and scheduling. The newest Event of each pod is `Warning Unhealthy` and
its message names the probe and the answer, and that line differs per
variant (measured on kind v1.37):

- wrong-path: `Readiness probe failed: HTTP probe failed with statuscode:
  404`. The server answered, so the port is right; the path `/healthz` is
  not one this build serves. The ConfigMap `api-html` holds only
  `index.html`, served at `/`.
- wrong-port: `Readiness probe failed: Get "http://10.244.1.76:8080/":
  dial tcp 10.244.1.76:8080: connect: connection refused`. Nothing listens
  on 8080; the container declares `containerPort: 80` and nginx listens
  there.
- missing-ready-file: `Readiness probe failed: cat: can't open
  '/tmp/ready': No such file or directory`. The probe is an `exec` command
  and the file it reads never exists in the image, and nothing in the pod
  creates it; with `failureThreshold: 1` the first attempt is final.

```sh
kubectl -n {{ .Namespace }} get pod <pod-name> -o jsonpath='{.spec.containers[0].readinessProbe}{"\n"}{.spec.containers[0].ports}{"\n"}'
kubectl -n {{ .Namespace }} get endpointslices -l kubernetes.io/service-name=api -o jsonpath='{range .items[*].endpoints[*]}{.addresses[0]} ready={.conditions.ready}{"\n"}{end}'
```

The first line prints the probe next to the declared port, so the mismatch
is visible in one screen. The second shows both pod IPs with
`ready=false`: the EndpointSlice controller lists them, kube-proxy routes
to none of them, which is the refused connection the frontend logs. The
same `0/1` on both pods says the problem is in the shared template, not in
one node.

## Change

Change one thing: the readiness probe in the pod template of Deployment
`api`, container `api`, the field the observation named, so the probe asks
for the path this build serves on the port it listens on:

```sh
kubectl -n {{ .Namespace }} patch deployment api --type=json -p='[{"op":"replace","path":"/spec/template/spec/containers/0/readinessProbe/httpGet/path","value":"/"}]'
kubectl -n {{ .Namespace }} patch deployment api --type=json -p='[{"op":"replace","path":"/spec/template/spec/containers/0/readinessProbe/httpGet/port","value":"http"}]'
kubectl -n {{ .Namespace }} patch deployment api --type=json -p='[{"op":"replace","path":"/spec/template/spec/containers/0/readinessProbe","value":{"httpGet":{"path":"/","port":"http"},"initialDelaySeconds":2,"periodSeconds":5}}]'
```

Run only the line that matches your observation: the path for wrong-path,
the port for wrong-port, and the whole probe for missing-ready-file, whose
`exec` probe has no path or port to correct (a JSON patch `replace` on a
path that does not exist is rejected). The last line fixes every variant,
because it replaces the whole probe. `kubectl edit deployment api` and
changing the same field in the editor is the same change. For wrong-path
a second route keeps the probe and makes the server answer it: add a
`healthz` key to ConfigMap `api-html`; the kubelet syncs the volume within
about a minute and nginx serves `/healthz` with 200. Removing the probe
also makes the pods Ready, but it removes the signal that would catch the
next real outage, so the ticket's constraints forbid it and a check reads
that the probe is still there.

## Verify

```sh
kubectl -n {{ .Namespace }} rollout status deployment/api
kubectl -n {{ .Namespace }} get pods
kubectl -n {{ .Namespace }} get endpointslices -l kubernetes.io/service-name=api
kubectl -n {{ .Namespace }} run curl --rm -it --restart=Never --image=curlimages/curl:8.14.1 -- curl -sS -m 5 http://api.{{ .Namespace }}.svc/
```

`rollout status` ends with `deployment "api" successfully rolled out`,
`get pods` shows two pods `1/1 Running`, the EndpointSlice lists two
addresses, and the curl pod prints
`{"service": "ACME API 3.2.0", "status": "ok"}`. Before the fix the same
curl printed `curl: (7) Failed to connect to api.{{ .Namespace }}.svc port
80 after 0 ms: Could not connect to server`: a Service with no ready
endpoint is refused at once by kube-proxy, which is why the frontend saw a
refusal and not a timeout.

## In the real world

- A probe copied from a checklist or from another service carries that
  service's path and port. The pipeline applies it, the pods start, and
  nothing fails loudly: `kubectl apply` returns before the first probe
  runs. `kubectl rollout status --timeout` in the pipeline turns a probe
  that never passes into a failed deploy.
- The reporter's "it starts in 2 seconds" is a real fact and a trap: a
  probe that is wrong forever looks like a probe that is early, and the
  first reflex is to raise `initialDelaySeconds`. The Event message tells
  the two apart: a slow app answers `connection refused` or a timeout for a
  while and then stops; a wrong probe fails the same way at minute 10.
- A rolling update of a Deployment whose new pods never turn Ready keeps
  the old pods serving, so the outage waits for the first deploy from
  scratch, a scale-up or a node drain. Alert on
  `kube_deployment_status_replicas_ready` against `spec.replicas`, not on
  restarts.
- `exec` probes that read a flag file depend on the app writing it; pin
  that contract in the app's tests, or probe the HTTP endpoint the app
  serves instead.

## Go deeper

- Liveness, readiness and startup probes, with every field and default:
  https://kubernetes.io/docs/concepts/workloads/pods/probes/
- Pod conditions, ContainersReady and Ready:
  https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/#pod-conditions
- EndpointSlice conditions ready, serving and terminating:
  https://kubernetes.io/docs/concepts/services-networking/endpoint-slices/
