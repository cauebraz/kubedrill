# The worker that keeps quitting

## What happened

Priya's team runs a worker: a program that takes invoices from a queue and
processes them, one after another, all day. It has no web page; it just runs.
This afternoon the team deployed it again. They did not change the program
itself, only the Kubernetes files that describe how to run it and the config
objects it reads. The deploy reported success. Since then the two copies of
the worker start, stop within a second, start again, stop again. Kubernetes
calls this `CrashLoopBackOff`. The queue is filling up because nobody is
taking invoices out of it.

"Success" meant the cluster accepted the new description. Accepting a
description is not the same as the program running with it. The program is
started, reads its inputs, finds one it cannot use, prints a line that says
which one, and quits. Your job is to read that line and make the input match.

## The pieces

Kubernetes keeps the cluster looking like the YAML you gave it. The worker
is described by one object and fed by three others:

- Deployment `worker`: the team's wish, "keep 2 copies of this pod running".
  Its `spec.template` is the blueprint of every copy: the image, the command
  and its arguments (`args`), where the environment comes from (`envFrom`)
  and which files are mounted where (`volumeMounts`).
- ReplicaSet and Pods: the Deployment creates a ReplicaSet, which creates
  the pods. A pod wraps the container that runs the program. Its name is the
  Deployment name plus two random parts, so you list it, never guess it.
- ConfigMap `worker-env`: a small dictionary of names and values. With
  `envFrom`, every key becomes an environment variable of the same name
  inside the container. The program reads `QUEUE_URL` from there.
- Secret `worker-token`: the same idea for the queue token, `QUEUE_TOKEN`.
- ConfigMap `worker-files`: two keys that are mounted as files, the script
  `run.sh` the container runs and the settings file `worker.conf`. A mount
  with `subPath` picks one key and places it at one path inside the
  container.

```
Deployment worker            "keep 2 pods from this template"
      |
      v
ReplicaSet -> Pod x2         the kubelet on the node:
      |                        1. pulls the image (already present)
      |                        2. builds the environment from worker-env + worker-token
      |                        3. mounts run.sh and worker.conf from worker-files
      |                        4. starts: sh /opt/worker/run.sh <args>
      v
the program                  reads the flag, QUEUE_URL, QUEUE_TOKEN, worker.conf
      |                        all present -> "worker started", polls the queue
      v                        one missing -> prints "worker: ..." and exits
CrashLoopBackOff             the kubelet waits 10s, 20s, 40s ... and starts it again
```

Each input has a name on two sides: the name the program expects and the
name the pod spec provides. The deploy changed the pod spec and the config
objects. One name no longer matches, and the program says which.

## Following the trail

Every command here only reads. In the workstation the namespace is already
set, so plain `kubectl` (or `k`) works; the full form is shown once.

Start at the pods:

```sh
kubectl -n {{ .Namespace }} get pods
```

STATUS is `CrashLoopBackOff` and RESTARTS grows, with the time since the
last restart in parentheses. Both pods do the same, so the problem is in the
shared blueprint, not in one node.

Ask one pod for details. Get its name with TAB (`kubectl describe pod wor`
then TAB), with a label (`kubectl describe pods -l
app.kubernetes.io/name=worker`) or by copying it from the terminal:

```sh
kubectl describe pod <pod-name>
```

Read the `Containers` section. `State: Waiting, Reason: CrashLoopBackOff`
is now; `Last State: Terminated, Reason: Error, Exit Code: N` is what
happened to the previous run. A program chooses its exit code: 1 is the
usual "a configuration is wrong", 2 is the usual "I did not understand my
arguments", and 137 would mean the kernel killed it for memory. Below that,
`Args` lists the arguments the container receives, `Environment Variables
from` lists the ConfigMap and Secret the environment is built from, and
`Mounts` lists each path with the key behind it as `path="..."`.

Now read what the program said before it quit. The container that exited is
gone, but its output is kept until the next restart replaces it:

```sh
kubectl logs <pod-name> --previous
```

Without `--previous` you get the output of the current attempt, which may
be empty while the kubelet waits. The last line starts with `worker:` and
names the input the program could not use: an empty `QUEUE_URL`, an unknown
flag, or a config file it cannot read. That line tells the three episodes
apart.

Finally, print the other side of each name:

```sh
kubectl get configmap worker-env worker-files -o yaml
```

`worker-env` lists the keys that become environment variables; `worker-files`
lists the keys that can be mounted as files. Compare them with the log line
and with the `Args` and `Mounts` from `describe pod`. One of the three pairs
(variable name and key, flag and script, `subPath` and key) does not match.

## The fix and why it works

Change the side that is wrong, and only that:

- The log says `QUEUE_URL is empty` and `worker-env` has the address under
  another key: rename the key back to `QUEUE_URL`, for example with
  `kubectl edit configmap worker-env`. The kubelet rebuilds the environment
  every time it starts the container, so the next restart gets the variable;
  `kubectl rollout restart deployment/worker` replaces the pods now instead
  of after the current backoff.
- The log says `unknown flag` and names the accepted one: change the
  argument in the Deployment, for example with `kubectl edit deployment
  worker`, under `args`. The Deployment sees a new template and rolls out
  new pods.
- The log says it `cannot read config file /etc/worker/worker.conf` and
  `Mounts` shows a `path=` that is not a key of `worker-files`: change the
  `subPath` of that mount in the Deployment to the key that exists. A
  `subPath` that names no key is created as an empty directory, which is
  why the path exists but is not a file.

Why other moves do not work:

- Deleting the pods: the ReplicaSet recreates them from the same blueprint
  with the same mismatch, and the output of the crash is lost with the pod.
- Changing the script to accept the new name: the ticket says the same
  script runs everywhere; the deploy is what changed, so the deploy is what
  gets fixed.
- Scaling up: more copies of the same crash.

## Check your work

```sh
kubectl rollout status deployment/worker
kubectl get pods
kubectl logs -l app.kubernetes.io/name=worker --tail=5
```

`rollout status` ends with `successfully rolled out` once both new pods
stayed Ready for 5 seconds. `get pods` shows `1/1` READY and `0` restarts.
The logs show `worker started: ...` with the queue address, then a `polling`
line per minute. The checks below the terminal turn green, and `kubedrill
check` grades once from the shell.

The habit to keep from this ticket: a crash loop is the program talking.
Read `Last State` and `kubectl logs --previous` before changing anything,
and compare names on both sides instead of trusting that a rename was
complete.
