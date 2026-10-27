# Hints

## Hint 1: where to look

The Service does not exist yet, so the facts it needs are on the pods: their
labels and the port the container listens on.
`kubectl -n {{ .Namespace }} get pods --show-labels` prints the labels, and
`kubectl -n {{ .Namespace }} get deployment admin -o jsonpath='{.spec.template.spec.containers[0].ports}{"\n"}'`
prints the container port. Once your Service exists,
`kubectl -n {{ .Namespace }} describe service admin` shows Type, TargetPort,
NodePort and Endpoints on one screen.

## Hint 2: what to observe

In the LABELS column: the labels the `admin` pod carries, and whether another
pod in the namespace shares any of them. In the ports output: the
`containerPort` number. In `describe service admin`: the `Endpoints:` line,
which lists the pod IP and port the Service forwards to, or `<none>`.
`kubectl -n {{ .Namespace }} get endpointslices -l kubernetes.io/service-name=admin -o jsonpath='{range .items[*].endpoints[*]}{.targetRef.name} ready={.conditions.ready}{"\n"}{end}'`
names each endpoint pod, so you see which pods the selector matched.

## Hint 3: what it means

`spec.selector` is an AND of label pairs: a pod becomes an endpoint when it
carries every pair. A selector that names a label the pod lacks gives
`Endpoints: <none>` and connection refused; a selector that another pod also
satisfies sends part of the requests to that pod, so add a pair only the
stable admin pod carries. `targetPort` must be the port the container listens
on, or the endpoint exists and the request is refused inside the pod.
`type: NodePort` opens a port on every node; without an explicit `nodePort`
the API server picks a random one in 30000-32767, not 30080.

## Hint 4: what to change

Write a Service named `admin` in `{{ .Namespace }}` with `type: NodePort`, a
selector made of the labels that identify the stable admin pod and no other
pod, `port` 80, `targetPort` equal to the container's port and `nodePort`
30080, then apply it. Nothing in the Deployment or the ConfigMaps needs a
change.
