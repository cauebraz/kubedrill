The strategic merge patch replaces the `configMap.name` of the volume named
`banner` in the pod template with the ConfigMap the team edits. The template
changed, so the Deployment rolls out new pods that mount `banner-config-v2`,
and the next edit of that ConfigMap reaches them without any further action.

Choose it in the wrong-name variant, when `kubectl get configmaps` lists a
`banner-config-v2` that holds the new text and the Deployment still mounts
`banner-config`. It fixes the root cause: the runbook, the Deployment and
the team now agree on one object. In the other two variants there is no
`banner-config-v2`, and the API accepts the patch anyway. With the subPath
mount the new pod stays in `ContainerCreating` with `FailedMount ...
configmap "banner-config-v2" not found` and the rollout stops. In the env
var variant no container mounts a volume named `banner`, so the patch adds
an unused volume and the rollout only replaces the pods, which helps by
accident and leaves a reference to a missing object. Read the Deployment
first.

The trade-off is the orphan. `banner-config` stays in the namespace with the
old text, and the next person to grep for `Summer sale` finds it. Delete it
once nothing references it, and change the manifest in the repository so the
next deploy does not point the volume back.
