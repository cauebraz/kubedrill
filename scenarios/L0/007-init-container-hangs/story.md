# The api that never got past the first step

## What happened

Priya's team runs the billing api, a small web service. Before the api
starts, each copy runs a short step called `migrate`: it waits until the
database answers and until the DBA team's "schema ready" flag is in place,
then applies the migrations and hands over to the api. On the old cluster
that took two seconds. The team copied the manifests (the YAML files that
describe the app) to the new cluster and applied them. The pipeline said
"green". Forty minutes later the dashboard still showed `0/2` ready and
nobody could reach the api.

"Green" only meant that the cluster accepted the YAML. Running the pods is a
chain of steps that happens later, on the nodes. Nothing crashed, nothing
restarted, so no alarm fired: the first step simply never finished, and the
api never got its turn. Your job is to find out what that step is waiting
for, and why the wait never ends on this cluster.

## The pieces

Kubernetes keeps the cluster looking like the YAML you gave it. The objects
in this ticket, from the top down:

- Deployment `api`: the wish "keep 2 copies of this pod running". Its
  `spec.template` is the blueprint every copy is built from.
- Pod: one running copy. A pod can hold more than one container. This one
  holds an init container `migrate` and an app container `api`. Init
  containers run first, one after another, each to completion; the app
  container starts only after the last init container exits with code 0.
  While an init container is running, `kubectl get pods` shows
  `Init:0/1`: zero of one init containers done.
- Init container `migrate`: a `busybox` shell loop. It reads three settings
  from its environment: `DB_HOST` and `DB_PORT` (where the database is) and
  `MIGRATE_FLAG` (a file it expects to find). It retries until both the
  database answers and the file exists.
- Deployment `db` and Service `db`: the database and its stable name inside
  the cluster. A Service name resolves through cluster DNS only for pods in
  the same namespace, and only with the port the Service declares.
- ConfigMap `migrate-config`: a small set of files. Mounting it into the
  pod at a path (`mountPath`) makes each key a file under that path, so the
  key `schema-ready` becomes `<mountPath>/schema-ready`.
- Service `api`: the stable name in front of the api pods. It sends traffic
  only to pods that are Ready, and a pod stuck in an init container has no
  app container to be Ready.

```
Deployment api        "keep 2 pods from this template"
      |
      v
Pod x2                1. scheduled on a node
      |               2. init container migrate runs:
      |                    waits for DB_HOST:DB_PORT   <-- Service db, port 5432
      |                    waits for MIGRATE_FLAG      <-- ConfigMap mounted at mountPath
      |                    exits 0
      |               3. app container api starts
      |               4. readiness probe passes -> pod is Ready
      v
Service api           sends traffic only to Ready pods
      |
      v
other teams           no Ready pod -> connection refused
```

The ticket says `0/2` ready and the pods show `Init:0/1`. So both pods are
inside step 2, and one of the three settings the loop uses does not match
the cluster it now runs on.

## Following the trail

Every command here only reads; nothing changes until you decide what to
change. In the workstation the namespace is already set, so plain `kubectl`
(or `k`) works. The full form is shown once.

Start at the pods:

```sh
kubectl -n {{ .Namespace }} get pods
```

Read STATUS and RESTARTS. `Init:0/1` with `0` restarts means the init
container is alive and has never exited: it is waiting, not crashing (a
crash would show `Init:CrashLoopBackOff` or `Init:Error` and a growing
restart count). The db pod shows `1/1 Running`, so the database itself is
up.

Ask the init container what it is waiting for. It has its own logs, chosen
with `-c`. You need a pod name; you do not have to type it:

- Press TAB: `kubectl logs api-` then TAB completes the name.
- Or copy it: select the name in the terminal and copy it with `Cmd-c`
  (`Ctrl-Shift-c` on Linux).

```sh
kubectl logs <pod-name> -c migrate
```

The last line is the condition that never holds, and it reads differently
depending on what is wrong:

- `nc: bad address 'db-primary'`: the host name has no Service in this
  namespace, so DNS cannot resolve it.
- `waiting for database at db:5433` with no error: the name resolved, but
  nothing listens on that port.
- `waiting for migration flag /etc/migrate/schema-ready` after `database
  reachable`: the database is fine; the file is not at that path.

Then read where those values come from:

```sh
kubectl describe pod <pod-name>
```

Scroll to `Init Containers`. `Args` prints the loop, so you can read the
command without opening any repository. `Environment` lists `DB_HOST`,
`DB_PORT` and `MIGRATE_FLAG`. `Mounts` lists the path where
`migrate-config` is mounted. `State: Running` and `Ready: False` confirm
what the STATUS column said.

Last, compare with what the cluster has:

```sh
kubectl get svc
kubectl get configmap migrate-config -o yaml
```

`get svc` lists the Service names and ports a pod in this namespace can
reach; `db` on `5432/TCP` is the one the loop should use. The ConfigMap
holds the key `schema-ready`, so the file exists under whatever path the
volume is mounted at. Put the three values from `describe` next to these
two outputs: exactly one does not match.

## The fix and why it works

Change the value that does not match, in Deployment `api`, init container
`migrate`. For a host or port, `set env` edits the variable in place:

```sh
kubectl set env deployment/api -c migrate DB_HOST=db
```

(or `DB_PORT=5432`). For the mount path, change the `mountPath` of the
`migrate-config` volume in the Deployment so it matches the directory of
`MIGRATE_FLAG`:

```sh
kubectl patch deployment api --type=json -p '[{"op":"replace","path":"/spec/template/spec/initContainers/0/volumeMounts/0/mountPath","value":"/etc/migrate"}]'
```

Why this works: the Deployment sees a new template, creates new pods, and in
those pods the loop's condition holds on the first try. `nc` connects,
the file is found, the loop prints `migrations done` and exits 0. That exit
is the signal the kubelet waits for: it starts the app container, the
readiness probe passes, the pod turns Ready and the Service starts routing
to it.

Why other moves do not work:

- Restarting or deleting the pods: new pods are built from the same template
  and wait for the same wrong value.
- Changing the Service `db` or the ConfigMap: both match what every other
  environment has, and other teams use the Service on port 5432.
- Removing the init container: the api starts, but the migrations never run
  and the mismatch stays in the manifest for the next environment.

In a real team, also fix the value in the repository's manifest, or the next
deploy puts the wrong one back.

## Check your work

```sh
kubectl rollout status deployment/api
kubectl get pods
kubectl logs <pod-name> -c migrate
kubectl get endpointslices -l kubernetes.io/service-name=api
```

`rollout status` ends with `successfully rolled out`. `get pods` shows two
api pods `Running` with `1/1` READY. The init logs now end with `schema 12
ready, migrations done`, the loop's last line. The EndpointSlice lists two
addresses the Service sends traffic to; an empty list was the reason for the
refused connections. A pod named `kubedrill-probe-...` may appear for a few
seconds: it is the grader sending one request through the Service, and the
grader deletes it. The checks below the terminal turn green, and `kubedrill
check` grades once from the shell.

The habit to keep from this ticket: a pod that is not crashing can still be
stuck. Read the STATUS, then read the logs of the container the STATUS
names, before changing anything.
