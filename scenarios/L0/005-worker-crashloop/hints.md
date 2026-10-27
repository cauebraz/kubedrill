# Hints

## Hint 1: where to look

`CrashLoopBackOff` is the container's own exit, not an image or scheduling
problem: the process started and stopped. Start with the pods and their
RESTARTS column, `kubectl -n {{ .Namespace }} get pods`, then the
`Containers` section of `kubectl -n {{ .Namespace }} describe pod <pod-name>`:
`State`, `Last State` and its `Exit Code`.

## Hint 2: what to observe

The output of the container that exited is still there:
`kubectl -n {{ .Namespace }} logs <pod-name> --previous` prints it, and it
ends with one `worker:` line that names what the script could not find. Put
that line next to three things in the pod spec: the `Args` and the `Mounts`
(with their `path="..."`) in `describe pod`, and the keys of the two
ConfigMaps, `kubectl -n {{ .Namespace }} get configmap worker-env worker-files -o yaml`.

## Hint 3: what it means

The script gets its inputs from the pod spec, and each input has a name that
must match on both sides. `envFrom` copies every ConfigMap key as an
environment variable with the same name, so a renamed key is a variable the
script never sees. `args` are passed to the script as they are, and a flag it
does not know stops it before it reads anything (exit code 2, a usage error).
A `subPath` on a ConfigMap mount must be a key of that ConfigMap; when it is
not, the kubelet creates an empty directory at the mount path instead of a
file. The `worker:` line says which of the three does not match.

## Hint 4: what to change

{{ if eq .Variant "renamed-key" -}}
The script reads the queue address from a variable named `QUEUE_URL`, and the
variable names come from the keys of ConfigMap `worker-env`. Put the queue
address back under the key the script reads. A ConfigMap change reaches the
pods at their next container start: the kubelet retries after the backoff,
and a new rollout of Deployment `worker` makes it immediate.
{{- else if eq .Variant "unknown-flag" -}}
The script accepts one flag, and the `args` of container `worker` in
Deployment `worker` pass it a different one. Change the argument to the
flag the script prints as accepted; the Deployment rolls out new pods with
the new argument.
{{- else -}}
The mount at `/etc/worker/worker.conf` in Deployment `worker` names a key
of ConfigMap `worker-files` in its `subPath`, and that key does not exist in
the ConfigMap. Make the two names agree: the ConfigMap's keys are `run.sh`
and `worker.conf`. A change to the Deployment rolls out new pods.
{{- end }}
