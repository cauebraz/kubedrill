A Deployment's `spec.selector` is immutable and its name is part of its
identity, so a Deployment created under the wrong name and label is not
edited into the right one: export it, correct the name and the label value in
the file, apply it as a new Deployment named `catalog`, wait for its rollout,
then delete the stray one. The export carries `resourceVersion`, `uid` and
`status` from the old object; `kubectl apply` ignores them when it creates
the new one. In a team repository you would edit the tracked manifest on a
branch instead of exporting.

Choose it when `get deployments` shows no `catalog` at all, only something
named during the copy. Creating the right object first and deleting the wrong
one second keeps two ready pods available at every moment; deleting first
leaves the catalog with no pods until the new rollout finishes.

The trade-off is a second set of pods for a minute and a file to keep. Check
that the exported file mounts the same ConfigMap and runs the same image
before applying it, so the new Deployment serves the same page the old one
did.
