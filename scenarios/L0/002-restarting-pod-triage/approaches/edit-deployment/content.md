`kubectl edit` opens the live Deployment in `$EDITOR` (`vi` in the
workstation), and saving sends the whole object back. Find the field the
observation named under `spec.template.spec.containers[0]` (the `config`
entry of `volumeMounts`, `livenessProbe.httpGet.port` or
`resources.limits.memory`), change it, save and quit; the Deployment
rolls out a new pod. `rollout status` waits for it, and the label goes on
the pod that exists after the rollout, selected by `-l app=reviews`
rather than typed by name.

Choose it when the change is one field and you want to see it in context:
the editor shows the probe next to the container port, the mount next to
the volume, the limit next to the request, which is how a wrong value is
spotted. A typo in the YAML is rejected on save and the editor reopens
with the error at the top, so nothing half-broken reaches the cluster.

The trade-off is that the change is not recorded anywhere but the
cluster. The next deploy from the repository applies the old field again,
so follow it with the same edit in the tracked manifest. In an interview
or exam, `edit` is also the slowest of the three routes when the field
path is already known; `patch` is one line.
