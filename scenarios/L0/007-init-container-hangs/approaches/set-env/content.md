`kubectl set env` rewrites the named environment variables of one container
in the Deployment's pod template; `-c migrate` points it at the init
container, which it edits like any other. The Deployment creates a new
ReplicaSet and `rollout status` blocks until the new pods are Ready or the
timeout expires, so the second line is the verification.

Choose it when the loop waits for a host or a port that does not match the
cluster: it is one command, it touches one or two fields, and
`kubectl rollout undo` reverts it. It does not help when the mount path is
the mismatch; that field needs `patch`, `edit` or the manifest route.

The trade-off is drift. The cluster now differs from the manifest in the
repository, and the next pipeline run or GitOps sync plants the old value
again. Follow it with the same change in the manifest.
