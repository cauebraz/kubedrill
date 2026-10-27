# Hints

## Hint 1: where to look

A restart is counted per pod, in the RESTARTS column of
`kubectl -n {{ .Namespace }} get pods`. With 40 rows, let kubectl order
them: `kubectl -n {{ .Namespace }} get pods --sort-by='.status.containerStatuses[0].restartCount'`
prints the pod with the most restarts last. One pod stands out; the
other 39 show 0.

## Hint 2: what to observe

Three places explain a restart, read all three for that pod:
`kubectl -n {{ .Namespace }} describe pod <pod-name>` has a `Last State`
block under `Containers` (Reason, Exit Code) and the `Events` at the end
(look for `Liveness probe failed`, `Killing`, `BackOff`, and the port in
each probe URL); `kubectl -n {{ .Namespace }} logs <pod-name> --previous`
prints what the container wrote before it died. In the same `describe`
output, compare `Limits`, `Mounts` and the probe lines with those of any
healthy pod, for example one of `catalog`.

## Hint 3: what it means

The three readings point at three different causes, and this episode has
one of them. `Exit Code: 1` with a log line that names a file the
container could not find means the process stopped itself; the path in
the log and the path under `Mounts` differ by one letter. `Liveness probe
failed: ... connect: connection refused` followed by `Killing` means the
kubelet killed a working container because it probed a port nothing
listens on; compare the port in the probe URL with the one in the log
line `serving on :8080`. `Reason: OOMKilled` with `Exit Code: 137` means
the kernel killed the container for using more memory than its limit; the
log says how large the cache it builds is, and `Limits` shows a number
below what that needs while every other service runs with 32Mi.

## Hint 4: what to change

The pod copies everything from the pod template of Deployment `reviews`,
so the change goes there, in container `web`: the mount path of the
`config` volume (the entrypoint reads `/etc/reviews/reviews.conf`), the
port of the liveness probe (the one `httpd` listens on), or the memory
limit (the one the other 13 services run with). Change the one field your
observation named, wait for the rollout, then add the `triage=yes` label
to the pod that replaced the old one: the old pod is gone, and its labels
with it.
