# Hints

## Hint 1: where to look

`0/2 ready` with `Running` pods and 0 restarts is a property of the pods'
readiness, not of the Service or the frontend. Start with the pods and both
columns: `kubectl -n {{ .Namespace }} get pods`. READY `0/1` next to STATUS
`Running` says the container started and something after the start keeps
it out of service.

## Hint 2: what to observe

The kubelet records every failed readiness attempt. Read the Events at the
bottom of `kubectl -n {{ .Namespace }} describe pod <pod-name>` (take the
name from `get pods`), or list them with
`kubectl -n {{ .Namespace }} get events --sort-by=.lastTimestamp`. The
`Warning Unhealthy` line starts with `Readiness probe failed:` and quotes
the answer the probe got. Then print the probe the pod runs next to the
port the container exposes:
`kubectl -n {{ .Namespace }} get pod <pod-name> -o jsonpath='{.spec.containers[0].readinessProbe}{"\n"}{.spec.containers[0].ports}{"\n"}'`.

## Hint 3: what it means

The probe is the only thing standing between `Running` and `Ready`, and
its answer names which of its fields is wrong. `HTTP probe failed with
statuscode: 404` means the server answered, but not at that path. `connect:
connection refused` means nothing listens on the port the probe dials.
`cat: can't open '/tmp/ready'` means the command the probe runs looks for a
file nothing creates. In each case the reporter is right: the process is up
in 2 seconds; the probe asks a question the process cannot answer yes to, so
a longer initial delay changes nothing.

## Hint 4: what to change

The pods copy the probe from the Deployment's pod template, so the change
belongs in Deployment `api`, container `api`, field `readinessProbe`: make
the probe ask something this nginx build answers, the path it serves on the
port it listens on. Keep a probe; deleting it hides the next real outage.
With a 404 you may instead make the server serve that path by adding the
file to ConfigMap `api-html` and waiting for the kubelet to sync the volume.
Then watch the rollout and the EndpointSlice until both pods are Ready.
