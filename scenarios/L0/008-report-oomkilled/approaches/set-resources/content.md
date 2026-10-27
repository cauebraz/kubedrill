`kubectl set resources` patches only the `resources` block of one container
in the Deployment's pod template. The Deployment creates a new ReplicaSet,
the old pod is removed (the Deployment uses `Recreate`), and `rollout status`
blocks until the new pod has been Ready for `minReadySeconds`, so the second
line is the verification, not decoration. Name the killed container with
`-c`: `report` when its own limit was too low, `log-shipper` when the helper
next to it was the one being killed. When the limit came from a LimitRange
default, this route still works, because an explicit value in the template
wins over the default.

Choose it during an incident: one command, one field, and `kubectl rollout
undo` reverts it. The numbers come from the observation, not from a guess:
`kubectl top pod --containers` once the pod stays up, with the request at
the steady state and the limit above the peak.

The trade-off is drift. The cluster now differs from the manifest in the
repository, and the next pipeline run or GitOps sync applies the old limit
again. Follow it with the same change in the manifest.
