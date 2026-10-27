`kubectl rollout undo` copies the pod template of an earlier revision back
into the Deployment. The controller treats it as a new revision, scales the
ReplicaSet of 2.4.0 back to 4 and the broken one to 0, and `rollout status`
blocks until the 4 pods are available again.

Choose it first during an incident: it needs no diagnosis, it works for
every cause behind a bad revision, and it takes seconds because the old
ReplicaSet still exists. `--to-revision=1` names the target; without it,
undo goes to the previous revision, which is the same one here but not
after a second bad deploy.

The trade-off is drift and a lost release. The repository still says 2.5.0,
so the next pipeline run reapplies the broken template, and the 2.5.0
changes the team wanted are not running. Follow the undo with a fix to the
manifest, then deploy 2.5.0 again.
