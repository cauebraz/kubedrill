Correct the image in the manifest the team deploys from, review the change
with `kubectl -n {{ .Namespace }} diff -f deploy/hello.yaml`, apply it and
record it in the repository. The workstation holds no copy of the team's
repository;
`mkdir -p deploy && kubectl -n {{ .Namespace }} get deployment hello -o yaml > deploy/hello.yaml`
gives you a file to practice the same edit, diff and apply.

Choose it when the manifest is the source of truth, which is every GitOps
setup: the fix survives the next pipeline run, the history records who
changed what, and the reviewer sees a one-line diff.

The trade-off is time. A pull request, a review and a pipeline run take
minutes that an outage may not have. A common pattern combines both routes:
`set image` to restore service, then the manifest change so the next deploy
does not undo it. `kubectl diff` is the step that catches an apply that would
also change fields nobody meant to touch.
