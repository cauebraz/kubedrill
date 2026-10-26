# Solution - 000 Hello app is down after the cluster migration

## Symptom

Deployment `hello` shows `0/2` ready, and `http://hello.{{ .Namespace }}.svc`
answers connection refused. The pipeline reported success because
`kubectl apply` only validates the manifest; it does not wait for the
rollout.

## Candidate causes

Zero ready pods behind a Service whose name resolves but refuses connections
fits several causes. Each one leaves a different trace:

- Image pull failure: the kubelet cannot fetch the image, STATUS is
  `ErrImagePull` or `ImagePullBackOff`, no container starts.
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
the Events at the end of its output only for a named pod. Both pods show
`ErrImagePull` or `ImagePullBackOff`, which rules out the crash loop, the
probe, the selector and scheduling at once. The newest Event of a pod is
`Error: ImagePullBackOff`; the `Failed to pull image` Event just above it
names the image and the registry's answer, and that line differs per variant
(measured on kind v1.37 with containerd; the text after the image reference
comes from the container runtime):

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
- private-registry: `Failed to pull image
  "registry.internal.example/platform/nginx:1.27-alpine": ... failed to do
  request: Head
  "https://registry.internal.example/v2/platform/nginx/manifests/1.27-alpine":
  dial tcp: lookup registry.internal.example on 192.168.65.254:53: no such
  host`. The path and tag are plausible, the registry host does not resolve
  from the nodes (the resolver address depends on the host running kind).

```sh
kubectl -n {{ .Namespace }} get deployment hello -o jsonpath='{.spec.template.spec.containers[0].image}'
```

Confirms the wrong reference lives in the Deployment's pod template, so
deleting pods would only recreate the same failure.

## Change

Change one thing: the image of container `app` in Deployment `hello`.

```sh
kubectl -n {{ .Namespace }} set image deployment/hello app=nginx:1.27-alpine
```

Or correct the image in the manifest in the repository and apply it again,
which is the route that survives the next deploy in a GitOps setup. The checks
read the Deployment's image, the ready replica count and the Service's
EndpointSlices, so either route passes.

## Verify

```sh
kubectl -n {{ .Namespace }} rollout status deployment/hello
kubectl -n {{ .Namespace }} get endpointslices -l kubernetes.io/service-name=hello
kubectl -n {{ .Namespace }} run curl --rm -it --restart=Never --image=curlimages/curl:8.14.1 -- curl -sS -m 5 -o /dev/null -w '%{http_code}\n' http://hello.{{ .Namespace }}.svc/
```

`rollout status` ends with `deployment "hello" successfully rolled out`, and
the EndpointSlice lists two addresses once the old pods are gone (a
terminating pod can show as a third for a few seconds). Before the fix the
curl pod printed `000` and `curl: (7) Failed to connect to
hello.{{ .Namespace }}.svc port 80 after 0 ms: Could not connect to server`
(measured on kind v1.37): a Service with zero ready endpoints answers
connection refused at once because kube-proxy rejects the connection, while a
timeout would point at a network or DNS problem. After the fix it prints
`200`.

## In the real world

- Image typos reach production when the pipeline does not wait for the rollout.
  `kubectl rollout status --timeout` or a GitOps health check turns this into a
  failed deploy instead of a silent outage.
- Pinning images by digest (`image:tag@sha256:...`) fails at manifest review
  time and makes the deployed artifact reproducible.
- `kubectl get events -n <ns> --sort-by=.lastTimestamp` is the fastest first
  look at any namespace that "deployed fine but does not work".
- A private registry that is unreachable or lacks `imagePullSecrets` produces
  the same pod status as a typo. Read the event message, not the status.

## Go deeper

- Images, tags, digests and pull policy:
  https://kubernetes.io/docs/concepts/containers/images/
- Debugging pods, including `ImagePullBackOff`:
  https://kubernetes.io/docs/tasks/debug/debug-application/debug-pods/
