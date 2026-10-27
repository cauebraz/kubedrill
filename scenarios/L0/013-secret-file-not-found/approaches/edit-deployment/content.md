Open the Deployment in an editor and correct the field that does not line
up with `/etc/secrets/db-password`: `mountPath` under
`spec.template.spec.containers[0].volumeMounts`, or `secretName` under
`spec.template.spec.volumes`. Saving creates a new ReplicaSet, and `rollout
status` waits until the new pods read the file and turn Ready.

Choose it when you want to see the volume and the mount next to each other
before you change one of them: the editor shows both blocks of the pod
template on one screen, which is where a one-letter difference becomes
visible. `kubectl patch` makes the same change in one line once you know
the field.

The trade-off is drift. The cluster now differs from the manifest the team
applied, so make the same change in the repository, or the next deploy
brings the mismatch back.
