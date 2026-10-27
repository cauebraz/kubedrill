`kubectl patch` changes one field without an editor, so it fits a runbook,
a pipeline step or a shell history you can paste into the incident channel.
A merge patch (`--type merge`) sets or removes keys of a map, which is the
shape of a ConfigMap rename: set the new key, set the old one to `null`. A
JSON patch (`--type json`) addresses one element of a list by index, which
is the shape of an argument or a `volumeMount` inside the pod template.

Choose it when the change is small and you want it recorded verbatim. The
`path` of a JSON patch depends on the order of the lists in the live object
(`containers/0`, `volumeMounts/1`), so print the object first with
`kubectl get deployment worker -o yaml` and count. A patch to the ConfigMap
needs a `rollout restart` of the Deployment afterwards, or the pods wait for
their next backoff retry to read it.

The trade-off is the same as any imperative change: the manifest in the
repository still carries the old value until you fix it there too.
