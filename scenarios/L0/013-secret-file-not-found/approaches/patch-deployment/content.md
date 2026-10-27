A JSON patch replaces one field of the pod template and nothing else: the
first line sets the mount directory to `/etc/secrets`, the second points
the volume at the Secret that exists. Run only the line for the name that
does not line up; a patch that sets a field to the value it already has
changes nothing and starts no rollout.

Choose it when `describe` already told you which field is off: it is one
command, it touches one field, and `kubectl rollout undo deployment/api`
reverts it. `kubectl edit` makes the same change with the whole template in
front of you.

The trade-off is drift. The cluster now differs from the manifest the team
applied, so make the same change in the repository, or the next deploy
brings the mismatch back.
