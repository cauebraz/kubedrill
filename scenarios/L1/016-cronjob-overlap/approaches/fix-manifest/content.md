Set `concurrencyPolicy: Forbid` in the CronJob manifest the team deploys
from (when it is not set yet), review it with `kubectl diff`, apply it, and
in the same change remove the manifest of the legacy CronJob (or set
`suspend: true` in it). The
workstation holds no copy of the team's repository, so the snippet exports
the live CronJob to `deploy/invoice.yaml` and practices the edit, diff and
apply on it. The first apply warns that the
`kubectl.kubernetes.io/last-applied-configuration` annotation is missing,
because the setup did not use `kubectl apply`; kubectl adds it.

Choose it when the repository is the source of truth: the fix survives the
next deploy and the review shows the one-line diff. Removing a CronJob from
a repository does not remove it from the cluster unless the deploy tool
prunes; check `kubectl get cronjobs` after the deploy. Running Jobs are not
affected by either edit.
