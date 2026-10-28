`kubectl rollout undo` copies the pod template of an earlier revision back
into the Deployment. The controller treats it as a new revision, scales the
ReplicaSet of 2.4.0 back to 4 and the broken one to 0, and `rollout status`
blocks until the 4 pods are available again.

Choose it first during an incident: it needs no diagnosis and takes seconds
because the old ReplicaSet still exists, as long as the old template can
still start pods. Revision 1 reads key `payments_url` from ConfigMap
`checkout-config`; when the 2.5.0 deploy removed it, the rollback's new pod
stays in `CreateContainerConfigError`, so verify with `rollout status`. `--to-revision=1` names the target; without it,
undo goes to the previous revision, which is the same one here but not
after a second bad deploy.

The trade-off is drift and a lost release. The repository still says 2.5.0,
so the next pipeline run reapplies the broken template, and the 2.5.0
changes the team wanted are not running. Follow the undo with a fix to the
manifest, then deploy 2.5.0 again.
