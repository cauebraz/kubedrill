Patch the live CronJobs: `concurrencyPolicy: Forbid` on `invoice` when it
is not set yet, and `suspend: true` on every other CronJob that runs the
export. Each patch
touches one field, takes effect at the next tick and is reverted with the
same command and the old value. Suspending instead of removing the legacy
CronJob keeps its finished Jobs and its definition for the team that owns
it, and `kubectl get cronjobs` shows SUSPEND `True` as a visible record.

Choose it while the export is still scheduled tonight. The trade-off is
drift: the manifests in the repository still carry the old policy and the
unsuspended twin, and the next deploy or GitOps sync restores them. Neither
patch stops a Job already running; list the Jobs afterwards and let the
active run finish, or remove the surplus one if it would collide.
