# Solution - 001 Storefront is down after the cluster migration

## Symptom

Deployment `storefront` shows `0/3` ready, the edge answers 503 and
`http://storefront.{{ .Namespace }}.svc` answers connection refused. The
pipeline reported success because `kubectl apply` only validates the
manifest; it does not wait for the rollout.

## Candidate causes

Zero ready pods behind a Service whose name resolves but refuses connections
fits several causes. Each one leaves a different trace:

- Image pull failure: the kubelet cannot fetch the image, STATUS is
  `ErrImagePull` or `ImagePullBackOff`, no container starts.
- Missing ConfigMap or volume: STATUS stays `ContainerCreating`, Events show
  `FailedMount` naming the volume.
- Crash loop: the container starts and exits, STATUS is `CrashLoopBackOff`,
  the restart count grows and `kubectl logs --previous` shows the exit.
- Failing readiness probe: STATUS is `Running` with `0/1` in READY, Events
  repeat `Readiness probe failed`.
- Selector mismatch: the pods are Ready but the Service selector matches none
  of them, so its EndpointSlice has no addresses.
- Scheduling: STATUS is `Pending` with `FailedScheduling` in Events
  (insufficient CPU or memory, taints, node selectors).

## Observation that splits them

```sh
kubectl -n {{ .Namespace }} get pods
kubectl -n {{ .Namespace }} get events --sort-by=.lastTimestamp
kubectl -n {{ .Namespace }} describe pod <pod-name>
```

Take `<pod-name>` from the NAME column of `get pods`; `describe pod` prints
the Events at the end of its output only for a named pod. Every pod shows
`ErrImagePull` or `ImagePullBackOff`, which rules out the mount, the crash
loop, the probe, the selector and scheduling at once. The newest Event of a
pod is `Error: ImagePullBackOff`; the `Failed to pull image` Event just above
it names the image and the registry's answer, and that line differs per
variant (measured on kind v1.37 with containerd; the text after the image
reference comes from the container runtime):

- old-registry: `Failed to pull image
  "registry.old-dc.example/mirror/nginx:1.27-alpine": ... failed to do
  request: Head
  "https://registry.old-dc.example/v2/mirror/nginx/manifests/1.27-alpine":
  dial tcp: lookup registry.old-dc.example on 192.168.65.254:53: no such
  host`. Repository and tag are right, but the registry host is the old
  datacenter's mirror, which the new cluster's nodes cannot resolve (the
  resolver address depends on the host running kind).
- typo-tag: `Failed to pull image "nginx:1.27-alpnie": rpc error: code =
  NotFound desc = failed to pull and unpack image
  "docker.io/library/nginx:1.27-alpnie": failed to resolve reference
  "docker.io/library/nginx:1.27-alpnie":
  docker.io/library/nginx:1.27-alpnie: not found`. The repository is right,
  the tag `1.27-alpnie` does not exist.
- wrong-repo: `Failed to pull image "docker.io/library/ngnix:1.27-alpine":
  ... failed to resolve reference "docker.io/library/ngnix:1.27-alpine":
  pull access denied, repository does not exist or may require
  authorization: server message: insufficient_scope: authorization failed`.
  The tag is right, `library/ngnix` is a typo of `library/nginx`.

```sh
kubectl -n {{ .Namespace }} get deployment storefront -o jsonpath='{.spec.template.spec.containers[0].image}'
```

Confirms the wrong reference lives in the Deployment's pod template, so
deleting pods would only recreate the same failure.

## Change

Change one thing: the image of container `web` in Deployment `storefront`.

```sh
kubectl -n {{ .Namespace }} set image deployment/storefront web=nginx:1.27-alpine
```

Or correct the image in the manifest in the repository and apply it again,
which is the route that survives the next deploy in a GitOps setup. The
checks read the Deployment's image, the ready replica count, the Service's
EndpointSlices and the page served through the Service, so either route
passes. The ConfigMap needs no change: it was never the problem, because no
container started to mount it.

## Verify

```sh
kubectl -n {{ .Namespace }} rollout status deployment/storefront
kubectl -n {{ .Namespace }} get endpointslices -l kubernetes.io/service-name=storefront
kubectl -n {{ .Namespace }} run curl --rm -it --restart=Never --image=curlimages/curl:8.14.1 -- curl -sS -m 5 http://storefront.{{ .Namespace }}.svc/
```

`rollout status` ends with `deployment "storefront" successfully rolled
out`, and the EndpointSlice lists three addresses once the old pods are gone
(a terminating pod can show as a fourth for a few seconds). Before the fix
the curl pod printed `curl: (7) Failed to connect to
storefront.{{ .Namespace }}.svc port 80 after 0 ms: Could not connect to
server` (measured on kind v1.37): a Service with zero ready endpoints answers
connection refused at once because kube-proxy rejects the connection, while a
timeout would point at a network or DNS problem. After the fix it prints the
page, with `<h1>ACME Storefront 2.14.0</h1>` from the ConfigMap.

## In the real world

- A migration copies manifests together with the old environment's
  assumptions: registry hosts, storage classes, ingress classes, DNS names.
  `grep -rn old-dc.example` over the repository before the move finds the
  registry ones.
- Image typos and stale registries reach production when the pipeline does
  not wait for the rollout. `kubectl rollout status --timeout` or a GitOps
  health check turns this into a failed deploy instead of a silent outage.
- Pinning images by digest (`image:tag@sha256:...`) fails at manifest review
  time and makes the deployed artifact reproducible.
- A private registry that is unreachable or lacks `imagePullSecrets`
  produces the same pod status as a typo. Read the event message, not the
  status.

## Go deeper

- Images, tags, digests and pull policy:
  https://kubernetes.io/docs/concepts/containers/images/
- Debugging pods, including `ImagePullBackOff`:
  https://kubernetes.io/docs/tasks/debug/debug-application/debug-pods/
