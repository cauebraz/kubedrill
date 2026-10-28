# Hints

## Hint 1: where to look

A rollout lives in the Deployment and its ReplicaSets, one per revision.
Start there: `kubectl -n {{ .Namespace }} get deployment,replicasets` and
`kubectl -n {{ .Namespace }} rollout history deployment/checkout`.

## Hint 2: what to observe

The new ReplicaSet has pods, but its READY column stays at 0. List the pods
with `kubectl -n {{ .Namespace }} get pods -l app.kubernetes.io/name=checkout`
and read the STATUS and READY columns of the pods with the new ReplicaSet's
hash in their name. Then read the Events at the bottom of
`kubectl -n {{ .Namespace }} describe pod <pod-name>` for one of them, and
compare the two revisions with
`kubectl -n {{ .Namespace }} rollout history deployment/checkout --revision=2`
(and `--revision=1`).

## Hint 3: what it means

The old pods are healthy and keep serving; only the pods of revision 2 fail,
so the cause is a field revision 2 changed in the pod template. The Event
says which step stops them: a `Readiness probe failed` line means the
container runs but answers the probe with an error, `FailedScheduling` with
`Insufficient cpu` means no node can fit the request, and `Failed to pull
image` means the image reference does not exist. The rollout waits for new
pods to become available before it replaces more old ones, so it stays stuck
until the template changes again. A pod in `CreateContainerConfigError`
names a ConfigMap or key its template reads that is not there: the template
it runs is fine on its own, but the settings around it changed with the
deploy.

## Hint 4: what to change

Mitigate first: put the Deployment back on the last revision that worked
(the `rollout` subcommand has a verb for it) and wait for
`rollout status` to report success. If the rollback stalls too, read the
Events of its new pod: revision 1 depends on a ConfigMap key the 2.5.0
deploy may have removed from `checkout-config`. Then fix forward instead:
change only the one field of container `web` in Deployment `checkout` that
differs between revision 1 and revision 2 and is neither the version label
nor the ConfigMap key. Deleting the stuck pods does not help; the
ReplicaSet recreates them from the same template.
