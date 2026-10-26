`kubectl set image` patches only the container image in the Deployment's pod
template. The Deployment controller creates a new ReplicaSet, and
`rollout status` blocks until the new pods are Ready or the timeout expires,
so the second line is the verification, not decoration.

Choose it during an incident, when restoring service matters more than
anything else: it is one command, it touches one field, and
`kubectl rollout undo` reverts it.

The trade-off is drift. The cluster now differs from the manifest in the
repository, and the next pipeline run or GitOps sync applies the broken image
again. Follow it with a change to the manifest, or the outage returns on the
next deploy. In a cluster managed by Argo CD or Flux with self-heal enabled,
the controller may revert the imperative change within minutes.
