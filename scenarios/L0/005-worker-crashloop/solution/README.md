# Solution - 005 worker is CrashLoopBackOff since the last deploy

## Symptom

Deployment `worker` shows `0/2` ready, both pods are `CrashLoopBackOff` and
their RESTARTS column grows. The deploy reported success because
`kubectl apply` only stores the desired state; the kubelet started the
container, the process exited within a second, and nothing in the pipeline
waited for the rollout.

## Candidate causes

A container that starts and exits at once fits several causes, and each one
leaves a different trace:

- The process rejects its arguments: `Last State` shows `Exit Code: 2`
  (the usage-error convention) and `kubectl logs --previous` prints the
  flag it did not accept.
- The process misses a configuration value: `Exit Code: 1` and the log names
  the empty variable; the variable names come from the keys of the ConfigMap
  or Secret listed under `envFrom`.
- The process cannot read a mounted file: `Exit Code: 1` and the log names
  the path; the `Mounts` section of `describe pod` shows which ConfigMap key
  (`path="..."`) is bound to that path.
- The process is killed: `Reason: OOMKilled`, `Exit Code: 137`, no log line
  from the process itself.
- The image changed: the `Image` in `describe pod` differs from the one the
  team runs; the ticket says it did not.

The `Readiness probe errored ... container is in CONTAINER_EXITED state`
Warning in the Events is a consequence, not a cause: the probe ran after the
process had already exited.

## Observation that splits them

```sh
kubectl -n {{ .Namespace }} get pods
kubectl -n {{ .Namespace }} describe pod <pod-name>
kubectl -n {{ .Namespace }} logs <pod-name> --previous
```

Take `<pod-name>` from the NAME column of `get pods`. In `describe pod`,
the `Containers` section shows `State: Waiting, Reason: CrashLoopBackOff`,
`Last State: Terminated, Reason: Error`, an `Exit Code` and a `Restart
Count`, the `Image` the ticket expects, the `Args`, the `Environment
Variables from` list (`worker-env` ConfigMap, `worker-token` Secret) and
the `Mounts`. `Exit Code: 1` or `2` with `Reason: Error` rules out OOMKilled
and the image. The log of the exited container names the input the script
could not use, and that line differs per variant (measured on kind v1.37):

- renamed-key: `worker: QUEUE_URL is empty; the queue address must be set
  in the environment`, exit code 1. `kubectl -n {{ .Namespace }} get
  configmap worker-env -o yaml` lists the keys `BROKER_URL`, `LOG_LEVEL`
  and `WORKER_NAME`: the queue address is there, under a name the script
  does not read. `envFrom` copies keys as variable names, so `QUEUE_URL`
  is unset.
- unknown-flag: `worker: unknown flag --concurrency=4 (accepted:
  --workers=N)`, exit code 2. `Args:` in `describe pod` shows
  `--concurrency=4`; the script stops at the first flag it does not know,
  before it reads any configuration.
- wrong-subpath: `worker: cannot read config file /etc/worker/worker.conf
  (missing or not a regular file)`, exit code 1. `Mounts:` shows
  `/etc/worker/worker.conf from files (ro,path="worker.cfg")` while
  `kubectl -n {{ .Namespace }} get configmap worker-files -o yaml` has the
  keys `run.sh` and `worker.conf`. A `subPath` that names no key of the
  ConfigMap is created as an empty directory, so the path exists but is not
  a file.

```sh
kubectl -n {{ .Namespace }} get configmap worker-env worker-files -o yaml
```

Confirms which names exist on the ConfigMap side, so the comparison with the
pod spec is between two printed lists, not from memory.

## Change

Change one thing: the name that does not match on the side that is wrong.

{{ if eq .Variant "renamed-key" -}}
The script is right and the ConfigMap key is wrong, so the fix is the key.
`kubectl patch` with a merge patch renames it (a `null` value removes a key):

```sh
kubectl -n {{ .Namespace }} patch configmap worker-env --type merge -p '{"data":{"QUEUE_URL":"amqp://queue.billing.svc:5672/invoices","BROKER_URL":null}}'
kubectl -n {{ .Namespace }} rollout restart deployment/worker
```

`kubectl edit configmap worker-env` and renaming the key in the editor is
the same change. A ConfigMap is read when a container starts, so the crashing
pods would pick it up on their next retry anyway; the `rollout restart`
replaces them now instead of after the current backoff (up to 5 minutes).
The other route, an `env` entry in the Deployment that reads `BROKER_URL`
into `QUEUE_URL`, also passes the checks but leaves the address under two
names.
{{- else if eq .Variant "unknown-flag" -}}
The script is right and the argument is wrong, so the fix is `args` of
container `worker` in Deployment `worker`:

```sh
kubectl -n {{ .Namespace }} patch deployment worker --type json -p '[{"op":"replace","path":"/spec/template/spec/containers/0/args","value":["--workers=4"]}]'
```

`kubectl edit deployment worker` and changing the argument in the editor is
the same change. Removing the argument also passes (the script defaults to
one worker), but the team runs four.
{{- else -}}
The ConfigMap has the file under `worker.conf` and the mount asks for
`worker.cfg`, so the fix is the `subPath` of the `/etc/worker/worker.conf`
mount in Deployment `worker`:

```sh
kubectl -n {{ .Namespace }} patch deployment worker --type json -p '[{"op":"replace","path":"/spec/template/spec/containers/0/volumeMounts/1/subPath","value":"worker.conf"}]'
```

`kubectl edit deployment worker` and changing the `subPath` in the editor is
the same change. Renaming the ConfigMap key to `worker.cfg` also passes,
because the mount path inside the container stays `worker.conf`; the
Deployment then needs a `rollout restart`, since a `subPath` mount is not
updated while the pod lives.
{{- end }}

The checks read the ready and available replica counts and the pod logs, so
any route that makes the script start passes. The script `run.sh` needs no
change: the same script runs in staging with the same inputs.

## Verify

```sh
kubectl -n {{ .Namespace }} rollout status deployment/worker
kubectl -n {{ .Namespace }} get pods
kubectl -n {{ .Namespace }} logs -l app.kubernetes.io/name=worker --tail=5
```

`rollout status` ends with `deployment "worker" successfully rolled out`;
with `minReadySeconds: 5` it waits until the new pods stayed Ready for 5
seconds, which a crash-looping pod never does. `get pods` shows two pods
`Running` with `1/1` READY and `0` restarts. The logs show `worker started:
name=billing-worker queue=amqp://queue.billing.svc:5672/invoices workers=4
config=/etc/worker/worker.conf`, then one `worker: polling` line per minute.
Verify against the ticket (both pods Ready and polling), not against the
change (the key or the field looks right).

## In the real world

- A deploy that "did not touch the code" still changes the contract between
  the code and its configuration: ConfigMap keys, flags, mount paths. A
  rename on one side without the other is the most common shape of this
  ticket, and it ships because `kubectl apply` validates YAML, not names.
- `kubectl rollout status --timeout` in the pipeline, or a GitOps health
  check, turns a crash loop into a failed deploy instead of a silent
  outage. With `minReadySeconds` set, a pod that crashes after a few seconds
  counts as not available and the rollout stops at the first replica.
- When the Deployment has a previous revision, `kubectl rollout undo` is the
  mitigation for a wrong argument or mount; it does nothing for a ConfigMap
  rename, because the ConfigMap is outside the pod template. Checking what
  changed (`what-changed`) tells which case you are in.
- Exit codes are a free signal: 1 for a configuration error, 2 for a usage
  error, 137 for a kill, so `Last State` sorts the candidates before the
  first log line is read.
- `kubectl logs --previous` only exists while the pod exists: deleting the
  pod to "see if it comes back" destroys the only output of the crash.

## Go deeper

- Container states, restart backoff and `Last State`:
  https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/
- Debugging a crash loop, including `kubectl logs --previous`:
  https://kubernetes.io/docs/tasks/debug/debug-application/debug-running-pod/
- ConfigMaps as environment variables and as files, including `subPath`:
  https://kubernetes.io/docs/concepts/configuration/configmap/
