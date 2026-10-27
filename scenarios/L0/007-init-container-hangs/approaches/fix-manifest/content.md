Correct the field in the manifest the team deploys from, review the change
with `kubectl diff`, apply it and record it in the repository. The
workstation holds no copy of the team's repository, so the snippet exports
the live Deployment to `deploy/api.yaml` and practices the same edit, diff
and apply on it; in the team's repository you would edit the tracked file on
a branch instead. In the editor, find the `migrate` entry under
`initContainers`: the `env` list holds `DB_HOST` and `DB_PORT`, the
`volumeMounts` list holds the `mountPath` of `migrate-config`. Change the
one that does not match the cluster. The first apply warns that the
`kubectl.kubernetes.io/last-applied-configuration` annotation is missing;
kubectl adds it and the warning does not come back.

Choose it when the manifest is the source of truth, which is every GitOps
setup: the fix survives the next pipeline run and the reviewer sees a
one-line diff. It is also the only route of the three that fixes the mount
path with the same tool the team already uses. The trade-off is time: a pull
request, a review and a pipeline run take minutes an outage may not have, so
teams often restore service first with `set env` or `patch` and then make
the manifest change.
