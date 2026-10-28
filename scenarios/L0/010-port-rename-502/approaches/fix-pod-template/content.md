Change the pod template of Deployment `web` so every port reference in it
agrees with `listen` in `web-nginx`: the readiness probe port, or the
containerPort name the Service resolves. A template change starts a
rollout, and `rollout status` waits until the new pods pass the probe, so
the last line is the verification. The patch reads the containerPort's
name first, so the probe follows whatever name the release gave it.

Choose it when the pod template is the object left behind: the probe still
asks the old port (pods `0/1`, `Readiness probe failed ... connection
refused` in Events). Pointing the probe at the containerPort by name
(`port: http`) instead of a number ties it to the port declaration, so the
next port move needs one edit. When the release renamed the containerPort
and the team prefers to restore the name the Service and the dashboards
expect, the snippet's probe patch changes nothing; the patch then targets
`/spec/template/spec/containers/0/ports/0/name` with the value `http`, and
the Service resolves it again after the rollout.

The trade-off is a rollout: new pods, a surge pod per replica, and a
minute of waiting. When the Service is the field left behind, patching the
Service (`service-target-port`) restores traffic without restarting
anything.
