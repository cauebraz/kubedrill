# Hints

## Hint 1: where to look

The value in the API and the value a container holds are two different
things, and the ticket only checked the first. List both sides: the
ConfigMaps with `kubectl -n {{ .Namespace }} get configmaps`, and the pods
with `kubectl -n {{ .Namespace }} get pods`. Then read how the pods get the
value: `kubectl -n {{ .Namespace }} get deployment banner -o yaml` shows it
under `env`, `volumeMounts` and `volumes` in the pod template.

## Hint 2: what to observe

Three readings, side by side. First, what the API holds in every ConfigMap:
`kubectl -n {{ .Namespace }} get configmaps -o yaml`. Second, what one
container holds, with a pod name from the NAME column of `get pods`:
`kubectl -n {{ .Namespace }} exec <pod-name> -- env` and
`kubectl -n {{ .Namespace }} exec <pod-name> -- cat /usr/share/nginx/html/index.html`.
Third, in the Deployment's pod template: the ConfigMap name that `env` or
`volumes` references, and whether the `volumeMounts` entry carries a
`subPath`. Note the AGE of the pods too.

## Hint 3: what it means

The container still holds `Summer sale 2025` while the edited ConfigMap says
`Winter sale 2026`: the edit saved, but it never reached the running
containers. The pod template tells you why. An env var from
`configMapKeyRef` is resolved once, when the container starts, and the pods
are older than the edit. A `subPath` mount is a bind mount of one file at
pod creation; the kubelet refreshes whole-ConfigMap volumes, never subPath
mounts. A volume that names a ConfigMap other than the one the team edited
reads an object nobody changed, so no amount of waiting helps.

## Hint 4: what to change

When the pod template reads the ConfigMap through an env var or a `subPath`
mount, the running pods cannot pick the value up: replace them. A restart of
the Deployment's rollout creates new pods, and each one reads the ConfigMap
when it starts. When the Deployment's volume names a different ConfigMap
than the one the team edited, change the volume's `configMap.name` in the
Deployment `banner` to the edited one, or put the new text in the ConfigMap
the Deployment names. The Service and the Deployment's image need no change.
