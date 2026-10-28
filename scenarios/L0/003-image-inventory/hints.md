# Hints

## Hint 1: where to look

The ticket asks for images that are running, and what runs is a container in
a pod, not a line in a Deployment. Start with the pods and their READY
column: `kubectl -n {{ .Namespace }} get pods`. Compare the pod list with
`kubectl -n {{ .Namespace }} get deployments`: count the pods behind each
Deployment, and read the READY column of the Deployments too.

## Hint 2: what to observe

Each pod carries its images in `spec.containers[*].image`, one entry per
container; `spec.initContainers[*].image` is a separate list. Print both for
every pod in one call:
`kubectl -n {{ .Namespace }} get pods -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.initContainers[*].image}{"\t"}{.spec.containers[*].image}{"\n"}{end}'`.
Note which pod has two entries in the last column, which Deployment has no
pod at all, and whether any image string ends in `@sha256:...`.

## Hint 3: what it means

A pod with two containers runs two images, and both count. A Deployment
with `0/0` READY has no pod, so its image runs nowhere and stays out of the
list. An init container's image ran once and exited (`kubectl describe pod`
shows it under `Init Containers` with `State: Terminated`), so it is not
running either. An image written with `@sha256:` must be copied with the
digest: the audit compares strings. `status.containerStatuses` can show
a different form of the same image (`docker.io/library/...`, often without
the digest), and the `Image ID:` line of `kubectl describe` is the digest
the node resolved; the `Image:` line of `describe` prints the spec string.
Read `spec.containers`, not the status.

## Hint 4: what to change

Nothing in the Deployments. Build the list from the pods'
`spec.containers[*].image` (pods in phase Running, one image per line,
`sort -u` for the order and the duplicates), then store it in a new
ConfigMap `inventory` under the key `images`. Both `--from-literal` and
`--from-file` of `create configmap` take the key name before the `=`;
without it, `--from-file` names the key after the file.
