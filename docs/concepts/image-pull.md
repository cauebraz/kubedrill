# Image pull

`ImagePullBackOff` tops every vendor list of the most common Kubernetes errors, yet k8s.af tags only 2 of its 59 production failure stories with the image registry. A container image reference names a registry, a repository and either a tag or a digest; every part that is omitted gets a default, and the defaults are where most pull failures start. The kubelet asks the container runtime to pull, retries with a growing delay, and writes the registry's answer into an Event. The sentence to hold on call: the `Failed` event message tells missing tag, unknown repository, unreachable registry and denied access apart, so read it before changing anything.

## The mechanism

A reference is `[registry/][namespace/]repository[:tag][@sha256:digest]`. Without a registry the runtime uses `docker.io`; without a namespace on Docker Hub it uses `library`; without a tag it uses `latest`. So `nginx` is `docker.io/library/nginx:latest`. A digest names one exact manifest; a tag is a pointer the publisher can move, so `image:tag@sha256:...` is what makes a deploy reproducible.

`imagePullPolicy` is set when the pod is created and not updated later: `Always` when the tag is `:latest` or omitted, `IfNotPresent` when a tag or a digest is given, `Never` only when written. With `IfNotPresent` a node that already has the image never asks the registry again, so two nodes can run different builds of one `:v2` tag. Exception: for an image pulled with a secret, `KubeletEnsureSecretPulledImages` (beta, on by default since v1.35) re-verifies the new pod's credentials before reuse.

```
kubelet                container runtime            registry
  |  PullImage(ref,  --> resolve ref -> manifest --> HEAD/GET manifest
  |  imagePullSecrets)   auth from secret/           (200 | 404 not found |
  |                      node credentials             401/403 denied |
  |                                                   dial/i/o timeout)
  |<-- ok: Pulled, Created, Started
  |<-- error: Event Failed "Failed to pull image ..."
  |      state.waiting.reason = ErrImagePull
  |      next attempt after backoff -> ImagePullBackOff
  |      delay doubles per attempt, capped at 300s
```

`imagePullSecrets` on the pod spec (or on its ServiceAccount, copied into the pod at admission) lists Secrets of type `kubernetes.io/dockerconfigjson` (or the older `kubernetes.io/dockercfg`) in the same namespace; the kubelet hands them to the runtime for that pull. The kubelet pulls serially by default (`serializeImagePulls: true` in the kubelet configuration; with `false`, `maxParallelImagePulls` caps the concurrent pulls), so one slow pull delays the pods behind it on that node.

After a failed pull the container waits with reason `ErrImagePull`; between retries the reason is `ImagePullBackOff`. The delay grows per attempt to a compiled-in cap of 300 seconds, so after a fix the next attempt can be 5 minutes away.

## What you see on call

`kubectl get pods -n <ns>`:

```
NAME                     READY   STATUS             RESTARTS   AGE
hello-7c9f5b6d8-xk2lp    0/1     ImagePullBackOff   0          4m
```

`kubectl describe pod <pod> -n <ns>`, the `Events` section; the `Failed` message is the registry's answer passed through containerd. the wording below is containerd's on kind nodes; other runtimes differ:

```
Warning  Failed   Failed to pull image "nginx:1.27-alpnie": ... docker.io/library/nginx:1.27-alpnie: not found
Warning  Failed   Failed to pull image "docker.io/library/ngnix:1.27-alpine": ... pull access denied, repository does not exist or may require authorization
Warning  Failed   Failed to pull image "registry.internal.example/platform/nginx:1.27-alpine": ... dial tcp: lookup registry.internal.example: no such host
Warning  Failed   Failed to pull image "ghcr.io/acme/api:1.4.0": ... unexpected status from HEAD request ...: 401 Unauthorized
Normal   BackOff  Back-off pulling image "nginx:1.27-alpnie"
```

Missing tag: `not found` on a repository that exists. Unknown repository: Docker Hub answers `pull access denied`, hiding whether a private repository exists. Unreachable registry: `no such host`, `connection refused` or `i/o timeout`. Auth failure on a private registry: `401 Unauthorized` or `403 Forbidden`, which means the reference is right and the credentials are missing or wrong.

`kubectl get deploy <name> -n <ns> -o jsonpath='{.spec.template.spec.containers[*].image}'` shows the reference the controller wants; compare it with the one in the event.

`kubectl set image deployment/<name> <container>=<image>` writes a new reference into the template and starts a rollout; `kubectl rollout status deployment/<name>` waits and prints `deployment "<name>" successfully rolled out` or stays on `1 of 2 updated replicas are available...`.

`kubectl get pod <pod> -n <ns> -o jsonpath='{.spec.imagePullSecrets}'` shows whether the pod carries a pull secret; `kubectl get secret <name> -n <ns> -o jsonpath='{.type}'` must print `kubernetes.io/dockerconfigjson`.

## Common failures

- `ImagePullBackOff`, message `not found` -> the tag does not exist (typo, deleted, never pushed) -> `kubectl get deploy <name> -o jsonpath='{.spec.template.spec.containers[0].image}'`.
- `ImagePullBackOff`, message `pull access denied, repository does not exist` -> the repository name is wrong or private without credentials -> `kubectl describe pod <pod>`, compare the repository against the registry.
- `ImagePullBackOff`, message `no such host` or `i/o timeout` -> the node cannot resolve or reach the registry (DNS, proxy, firewall, internal hostname) -> `kubectl debug node/<node> -it --image=busybox:1.37 -- nslookup <registry>`; delete the `node-debugger-<node>-<suffix>` pod it leaves behind.
- `ImagePullBackOff`, message `401 Unauthorized` -> missing or wrong `imagePullSecrets` -> `kubectl get pod <pod> -o jsonpath='{.spec.imagePullSecrets}'`.
- Pod stays `ImagePullBackOff` minutes after the fix -> the backoff is still counting down -> `kubectl delete pod <pod>` and watch the replacement.
- The fix works on one node and not another -> `IfNotPresent` with a tag whose content moved -> `kubectl get pod <pod> -o jsonpath='{.status.containerStatuses[0].imageID}'` per node.

## Go deeper

- https://kubernetes.io/docs/concepts/containers/images/ : reference syntax, pull policy defaults, the 300 second backoff cap, `serializeImagePulls` and `imagePullSecrets`.
- https://kubernetes.io/docs/tasks/configure-pod-container/pull-image-private-registry/ : creating a `kubernetes.io/dockerconfigjson` Secret and attaching it.
- https://kubernetes.io/docs/concepts/workloads/controllers/deployment/#updating-a-deployment : `kubectl set image`, `rollout status` and how a stuck rollout keeps the old pods.
