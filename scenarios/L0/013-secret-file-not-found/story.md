# The password file the api could not find

## What happened

Priya's team runs the billing `api`, a small program that needs a database
password before it can do anything. Until today the password sat in an
environment variable. This afternoon the team moved it into a Secret, the
way the app's README asks: the app reads the file
`/etc/secrets/db-password` when it starts, and stops when that file is not
there.

They created the Secret, told the Deployment to mount it, and applied. The
cluster accepted every object, and `kubectl get secret db-credentials`
lists the Secret. Still, no copy of the api is ready. Accepting the objects
only means the cluster stored them; whether a file appears at the exact
path the app reads is decided later, on the node, when the pod starts.

## The pieces

- Secret `db-credentials`: a small object that holds named values, here the
  database password. Each name is a key, and each key has one value.
- Secret volume: a way to show a Secret to a container as files. Every key
  becomes one file, named after the key, unless an `items` list in the
  volume gives a key another file name. The volume names its Secret in the
  field `secretName`.
- Volume mount: the container's `volumeMounts` entry says in which
  directory (`mountPath`) the volume's files appear inside the container.
- Deployment `api`: the team's wish, "keep 2 copies of this pod running".
  Its pod template holds the container, the volume and the mount.
- Pod: one running copy, named after the Deployment plus two random parts,
  for example `api-6c4bd45bf7-mv2wm`.
- kubelet: the agent on each node that builds the volumes of a pod first,
  then starts its containers, then runs the readiness probe.

```
Secret db-credentials      key -> value
      |
      v  volume: secretName picks the Secret, each key becomes a file
Pod volume "db-credentials"
      |
      v  mount: mountPath picks the directory in the container
/etc/secrets/db-password   the file the app opens at start
      |
      v
api: connected  -> readiness probe passes -> pod Ready
api: cannot read -> container exits -> CrashLoopBackOff
```

Three names must line up for the file to land where the app looks: the
Secret the volume asks for must exist, the mount must point at
`/etc/secrets`, and some key must produce a file named `db-password`. If the
Secret does not exist, the kubelet cannot build the volume and never starts
the container. If the other two are off, the container starts and the app
finds no file.

## Following the trail

Every command here only reads. In the workstation the namespace is already
set, so plain `kubectl` (or `k`) works. The full form is shown once.

Start with the pods:

```sh
kubectl -n {{ .Namespace }} get pods
```

STATUS splits the problem in two. `ContainerCreating` that never moves on
means the kubelet is still building the pod and no container exists yet.
`Running` with `0/1` in READY, later `Error` and `CrashLoopBackOff` with a
growing RESTARTS count, means the container started and quit.

Ask a pod why. Press TAB after `kubectl describe pod api` to complete the
name, or describe them all with `kubectl describe pods -l app=api`:

```sh
kubectl describe pod <pod-name>
```

Read three blocks. `Mounts:` under the container shows the directory and
the volume, for example `/etc/secrets from db-credentials (ro)`. `Volumes:`
shows `SecretName:`, the Secret that volume asks for. `Events:` at the
bottom shows what the kubelet tried. A `FailedMount` line that says
`secret "..." not found` means the volume asks for a Secret that does not
exist; compare its name with `kubectl get secrets`.

When the container started, the app's own words are in the logs, and you
can look inside the container during the 45 seconds it stays up:

```sh
kubectl logs <pod-name>
kubectl exec <pod-name> -- ls /etc/secrets
```

`ls: /etc/secrets: No such file or directory` means the files were mounted
somewhere else; the `Mounts:` block says where. A listing that shows a file
under another name means the Secret's key is not `db-password`. The keys
are visible without decoding the values:

```sh
kubectl get secret db-credentials -o jsonpath='{.data}'
```

## The fix and why it works

Change the one name that does not line up, in the Deployment's pod
template, for example the mount directory when it is off:

```sh
kubectl patch deployment api --type=json -p '[{"op":"replace","path":"/spec/template/spec/containers/0/volumeMounts/0/mountPath","value":"/etc/secrets"}]'
```

The same command with another `path` and `value` fixes `secretName`, and an
`items` entry (`key: <the key the Secret has>`, `path: db-password`) gives a
key the file name the app reads. `kubectl edit deployment api` reaches all
three fields too.

Why this works: the Deployment sees a new template and replaces the pods.
The kubelet builds the volume from a Secret that exists, puts a file named
`db-password` in `/etc/secrets`, the app reads it, prints `api: connected`
and the readiness probe passes.

Why other moves do not work:

- Deleting the pods: new pods come from the same template, with the same
  mismatch.
- Recreating the Secret with the same keys: it already exists and its keys
  are what they were; only a change to a name moves the file.
- Changing the app's script: the path it reads is the contract the README
  promises; the cluster side has to deliver the file there.

## Check your work

```sh
kubectl rollout status deployment/api
kubectl get pods
kubectl logs -l app=api
```

`rollout status` ends with `successfully rolled out`. `get pods` shows 2
pods `Running` with `1/1` READY and no new restarts, and the logs print
`api: connected` once per pod. The checks below the terminal turn green,
and `kubedrill check` grades once from the shell.

The habit to keep: when an app says a file is missing, read `Mounts:`,
`Volumes:` and `Events:` side by side and compare every name with the path
the app reads.
