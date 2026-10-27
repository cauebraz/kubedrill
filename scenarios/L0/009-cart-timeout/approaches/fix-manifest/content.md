Correct the Service in the manifest the team deploys from, review the
change with `kubectl diff`, apply it and record it on the release branch.
The workstation holds no copy of the team's repository, so the snippet
exports the live Service to `cart/service.yaml` and practices the same
edit, diff and apply on it; in the team's repository you would edit the
tracked file on a branch instead of exporting. The first apply warns that
the `kubectl.kubernetes.io/last-applied-configuration` annotation is
missing, because the setup created the Service with server-side apply;
kubectl adds the annotation and the warning does not come back. Drop the
`status`, `resourceVersion` and `uid` fields of the export before you
apply it, or keep only `metadata.name`, `metadata.namespace` and `spec`.

Choose it when the manifest is the source of truth, which is every GitOps
setup: the fix survives the next pipeline run and the reviewer sees a
one-line diff of the selector or the port. It costs a review and a
pipeline run, so during an outage teams often restore service with
`set selector` or `patch` first and open the pull request right after.
