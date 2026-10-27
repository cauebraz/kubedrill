Correct the Deployment and the HPA in the manifests the team deploys from,
review the change with `kubectl diff`, apply it and record it. The
workstation holds no copy of the team's repository, so the snippet exports
the live objects to `deploy/` and practices the same edit, diff and apply on
them: `resources.requests.cpu` of container `api`, or `spec.scaleTargetRef`
and `spec.metrics` of the HPA, whichever link is broken.

Choose it when the manifests are the source of truth. The reviewer sees the
Deployment and its HPA side by side, which is where a renamed target or a
missing request shows up. Add a CPU request to every container of the pod
template while the file is open: one container without it is enough to make
the HPA blind again. The trade-off is the time of a review and a pipeline
run.
