Correct the probe in the manifest the team deploys from, review the change
with `kubectl diff`, apply it and record it in the repository. The
workstation holds no copy of the team's repository, so the snippet exports
the live Deployment to `deploy/api.yaml` and practices the same edit, diff
and apply on it: under `readinessProbe` set `httpGet.path` to `/` and
`httpGet.port` to `80` (or the named port `http`), and replace an `exec`
block with that `httpGet` block. The first apply warns that the
`kubectl.kubernetes.io/last-applied-configuration` annotation is missing,
because the setup did not create the Deployment with `kubectl apply`; the
warning does not come back.

Choose it when the manifest is the source of truth, which is every GitOps
setup: the fix survives the next pipeline run, the history records who
changed what, and the reviewer sees a one-line diff. It is also the moment
to compare the probe with the paths the app serves, since a probe copied
from a checklist carries another service's path and port.

The trade-off is time. A pull request, a review and a pipeline run take
minutes an outage may not have. A common pattern combines both routes:
the patch to restore service, then the manifest change so the next deploy
does not undo it.
