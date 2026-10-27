Correct the image in the manifest the team deploys from, review the change
with `kubectl -n {{ .Namespace }} diff -f deploy/storefront.yaml`, apply it
and record it in the repository. The workstation holds no copy of the team's
repository, so the snippet starts by exporting the live Deployment to
`deploy/storefront.yaml` and practices the same edit, diff and apply on it.
In the team's repository you would edit the tracked file on a branch and
commit it instead of exporting. The first apply warns that the
`kubectl.kubernetes.io/last-applied-configuration` annotation is missing:
the setup did not create the Deployment with `kubectl apply`. kubectl adds
the annotation, and the warning does not come back.

Choose it when the manifest is the source of truth, which is every GitOps
setup: the fix survives the next pipeline run, the history records who
changed what, and the reviewer sees a one-line diff. After a migration it is
also the moment to search the repository for the old registry host, since
every manifest copied from the old environment may carry it.

The trade-off is time. A pull request, a review and a pipeline run take
minutes that an outage may not have. A common pattern combines both routes:
`set image` to restore service, then the manifest change so the next deploy
does not undo it. `kubectl diff` is the step that catches an apply that would
also change fields nobody meant to touch.
