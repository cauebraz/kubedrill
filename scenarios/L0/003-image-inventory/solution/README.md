# Solution - 003 Ops wants a one-liner: list every image running in shop into a ConfigMap inventory

## Symptom

Ops needs ConfigMap `inventory` in `{{ .Namespace }}` with one key, `images`,
holding every image a running container uses: one per line, sorted, no
duplicates, each string as the pod spec writes it. Nothing is broken; the
work is reading the right field of the right objects. The obvious read,
`kubectl get deployments -o wide`, prints an IMAGES column and looks
complete, but it lists templates, not running containers: it includes a
Deployment at 0 replicas, and `kubectl describe pod` prints the same image in
a form (`docker.io/library/...`) that does not match the spec string the
audit compares against.

## Candidate causes

Four places hold an image string, and each one misleads in a different way:

- `Deployment.spec.template.spec.containers[*].image`: the template. It
  lists what would run, including a Deployment scaled to 0, whose pods do
  not exist.
- `Pod.spec.initContainers[*].image`: ran once before the main containers
  and exited. `kubectl describe pod` shows it under `Init Containers` with
  `State: Terminated, Reason: Completed`.
- `Pod.status.containerStatuses[*].image`: what the container runtime
  reports, normalized: `docker.io/library/nginx:1.27-alpine` for a spec that
  says `nginx:1.27-alpine`, and `sha256:<config digest>` for a spec pinned
  with `@sha256:` (measured on kind v1.37 with containerd). It does not match
  the spec string.
- `Pod.spec.containers[*].image`: the string the kubelet pulled, one per
  container, on pods that exist. This is the source.

## Observation that splits them

```sh
kubectl -n {{ .Namespace }} get deployments
kubectl -n {{ .Namespace }} get pods
kubectl -n {{ .Namespace }} get pods -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.initContainers[*].image}{"\t"}{.spec.containers[*].image}{"\n"}{end}'
```

`get deployments` shows five Deployments; `get pods` shows the pods behind
them. The jsonpath line prints, per pod, its init image (empty for most) and
its main images, so the three traps show on one screen:

- scaled-to-zero: `checkout` reads `0/0` in `get deployments` and has no
  pod, so `nginx:1.26-alpine` appears in the Deployment template and in no
  pod line. It is not running.
- sidecar: the `checkout` pod reads `2/2` in READY and its last column holds
  two images, `nginx:1.26-alpine busybox:1.37`. Both run, both count.
- digest-pin: the `cache` pod's image reads
  `redis:7.4-alpine@sha256:6ba19c50b56777f5c11c58eff8ddb69d0d5f27ba131cffb63f180ccfd43c66af`.
  The audit compares strings, so the digest stays in.

In every variant the `worker` pod shows `busybox:1.36` in the init column
and `registry.k8s.io/e2e-test-images/agnhost:2.53` in the main column: the
init image ran and exited, the agnhost image runs. `catalog` runs agnhost
too, so the final list holds it once.

## Change

Build the list from the pods' main containers and store it under the key
`images`:

```sh
kubectl -n {{ .Namespace }} get pods --field-selector=status.phase=Running -o jsonpath='{range .items[*]}{range .spec.containers[*]}{.image}{"\n"}{end}{end}' | sort -u > images.txt
kubectl -n {{ .Namespace }} create configmap inventory --from-file=images=images.txt
```

The nested `range` prints one image per line; `sort -u` orders the lines
and drops the duplicate agnhost. `--from-file=images=images.txt` names the
key `images`; without the `images=` prefix the key would be `images.txt`.
Two other reads produce the same list. A flat jsonpath with `tr`:

```sh
kubectl -n {{ .Namespace }} get pods --field-selector=status.phase=Running -o jsonpath='{.items[*].spec.containers[*].image}' | tr ' ' '\n' | sort -u
```

A go-template, which walks the same fields with `range`:

```sh
kubectl -n {{ .Namespace }} get pods --field-selector=status.phase=Running -o go-template='{{ "{{" }}range .items{{ "}}" }}{{ "{{" }}range .spec.containers{{ "}}" }}{{ "{{" }}.image{{ "}}" }}{{ "{{" }}"\n"{{ "}}" }}{{ "{{" }}end{{ "}}" }}{{ "{{" }}end{{ "}}" }}' | sort -u
```

`--from-literal=images="$(...)"` stores the same value without a file. The
checks read the ConfigMap's `data.images` and compare the set of lines with
the running images, so any of these routes passes; the order of the lines
and a trailing newline do not matter to the grade, the ticket asks for
sorted output because the audit diffs it against last week's.

## Verify

```sh
kubectl -n {{ .Namespace }} get configmap inventory -o jsonpath='{.data.images}{"\n"}'
kubectl -n {{ .Namespace }} get pods -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.phase}{"\t"}{.spec.containers[*].image}{"\n"}{end}'
```

The first line prints the stored value, one image per line. Compare it with
the second: every image in the last column appears once in the ConfigMap,
no pod is missing, and nothing else is listed. The checks under the terminal
turn green, and `kubedrill check` grades once from the shell.

## In the real world

- The inventory is a snapshot. A pod in `Terminating` is still in phase
  `Running` for a few seconds during a rollout, so the one-liner run in the
  middle of a deploy lists both the old and the new image. Run it when
  `kubectl rollout status` is quiet, or record the time next to the list.
- `status.containerStatuses[*].imageID` holds the repo digest the node
  resolved (`docker.io/library/nginx@sha256:...`), which is what a
  vulnerability scanner matches. An audit that compares tags with a scanner
  report needs both columns.
- Admission policies (`ImagePolicyWebhook`, Kyverno, Gatekeeper) and
  `kubectl get pods -A -o jsonpath` across every namespace turn this
  one-off into a control: the ticket's one-liner with `-A` and
  `{.metadata.namespace}` in the range is the weekly report.
- The same read answers an incident question: "which pods run the image
  with the CVE" is `kubectl get pods -A -o jsonpath=... | grep <image>`.

## Go deeper

- JSONPath support in kubectl, including `range` and `end`:
  https://kubernetes.io/docs/reference/kubectl/jsonpath/
- Init containers and why their image is not running once the pod starts:
  https://kubernetes.io/docs/concepts/workloads/pods/init-containers/
- `kubectl create configmap` with `--from-literal` and `--from-file`:
  https://kubernetes.io/docs/tasks/configure-pod-container/configure-pod-configmap/
- Image names, tags and digests:
  https://kubernetes.io/docs/concepts/containers/images/
