`kubectl edit` opens the live object in your editor, validates the result
when you save and writes it back. For a Deployment, a saved change to the
pod template starts a rollout; for a ConfigMap, the change is read by each
container at its next start, so a `rollout restart` of the Deployment makes
it reach the pods now instead of after the current backoff.

Choose it when the fix is a rename or a one-word change and you want to see
the surrounding fields while you type it: the `args` next to the `command`,
the `subPath` next to its `mountPath`, the key next to its value. The editor
also catches a typo before the object is written.

The trade-off is that the change leaves no record except
`metadata.managedFields` (`kubectl-edit` as the manager) and, for the
Deployment, a new revision. The manifest in the repository still has the old
value, so the next deploy puts the crash back; fix the manifest in the same
change.
