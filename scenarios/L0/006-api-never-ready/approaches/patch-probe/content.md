A JSON patch with `op: replace` on `readinessProbe` swaps the whole probe
for one that asks for `/` on the named port `http`, so it works whatever
field was wrong: path, port, or an `exec` command. The Deployment
controller rolls out a new ReplicaSet, and `rollout status` blocks until
both pods are Ready or the timeout expires, so the second line is the
verification.

Choose it during an incident: one command, one field, and `kubectl rollout
undo` reverts it. `kubectl edit deployment api` reaches the same result by
hand when you prefer to change only the wrong value and keep the other
probe settings.

The trade-off is drift. The cluster now differs from the manifest in the
repository, and the next pipeline run or GitOps sync applies the broken
probe again. Follow it with the manifest change, or the ticket comes back
on the next deploy.
