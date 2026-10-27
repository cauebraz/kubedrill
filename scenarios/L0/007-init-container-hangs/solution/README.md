# Solution - 007 api never starts: the migrate init container hangs forever on the new cluster

## Symptom

Deployment `api` shows `0/2` ready and `http://api.{{ .Namespace }}.svc`
answers connection refused. Every api pod sits in STATUS `Init:0/1` with
zero restarts. The pipeline reported success because `kubectl apply` only
validates the manifest, and the pods never crashed, so no alert on restarts
fired either: an init container that waits forever looks healthy to
everything that counts failures.

## Candidate causes

Two pods stuck at `Init:0/1` for forty minutes fit several causes. Each one
leaves a different trace:

- The init container waits for a dependency that never answers: STATUS
  stays `Init:0/1`, `State: Running` in describe, the init logs repeat a
  "waiting for" line.
- The init container crashes: STATUS is `Init:CrashLoopBackOff` or
  `Init:Error`, the restart count grows, `kubectl logs -c migrate
  --previous` shows the exit.
- The init image cannot be pulled: STATUS is `Init:ImagePullBackOff`, Events
  show `Failed to pull image`.
- The database is down: `kubectl get pods` shows the db pod not Ready, and
  the init logs say the database is unreachable.
- The app container fails its readiness probe: STATUS would be `Running`
  with `0/1`, not `Init:0/1`.

## Observation that splits them

```sh
kubectl -n {{ .Namespace }} get pods
kubectl -n {{ .Namespace }} logs <pod-name> -c migrate
kubectl -n {{ .Namespace }} describe pod <pod-name>
```

Take `<pod-name>` from the NAME column of `get pods`. Both api pods show
`Init:0/1` with `RESTARTS 0`, and the db pod shows `1/1 Running`: that
rules out the crash, the image pull and a database that is down. `logs -c
migrate` prints the loop's own words, and the last lines differ per
variant (measured on kind v1.37 with busybox 1.36):

- missing-service-name: `nc: bad address 'db-primary'` followed by
  `waiting for database at db-primary:5432`. The name does not resolve:
  cluster DNS answers for Services of this namespace, and
  `kubectl -n {{ .Namespace }} get svc` lists `db`, not `db-primary`.
- wrong-port: `waiting for database at db:5433` with no error line. The
  name resolves, nothing listens on 5433; `get svc` shows `db` on
  `5432/TCP`.
- flag-mount-path: `database reachable at db:5432` then `waiting for
  migration flag /etc/migrate/schema-ready`. The database answered; the
  file is missing at that path.

In `describe pod`, the `Init Containers` section explains each value. `Args`
is the loop: `nc -z` against `$DB_HOST:$DB_PORT`, then a test for the file
`$MIGRATE_FLAG`. `Environment` lists `DB_HOST`, `DB_PORT` and
`MIGRATE_FLAG`; `Mounts` lists where `migrate-config` lands. In
flag-mount-path the mount is `/etc/migrations from migrate-config (ro)`
while `MIGRATE_FLAG` is `/etc/migrate/schema-ready`, one letter apart.
`State: Running` with `Ready: False` confirms the loop is alive and simply
never ends.

```sh
kubectl -n {{ .Namespace }} get svc
kubectl -n {{ .Namespace }} get configmap migrate-config -o yaml
```

Confirms the reference: Service `db` on port 5432, and the ConfigMap holds
the key `schema-ready`, so the file exists wherever the volume is mounted.
The wrong value lives in the Deployment's pod template, so deleting pods
only recreates the same wait.

## Change

Change one thing in Deployment `api`, init container `migrate`: the value
that does not match the cluster.

```sh
kubectl -n {{ .Namespace }} set env deployment/api -c migrate DB_HOST=db
```

For wrong-port the variable is `DB_PORT=5432` instead. For flag-mount-path
the field is the mount path, which `set env` cannot touch:

```sh
kubectl -n {{ .Namespace }} patch deployment api --type=json -p '[{"op":"replace","path":"/spec/template/spec/initContainers/0/volumeMounts/0/mountPath","value":"/etc/migrate"}]'
```

Editing the manifest and applying it again is the route that survives the
next deploy in a GitOps setup. The checks read ready replicas, the
Service's EndpointSlices and the response through the Service, so any
change that lets the loop end passes, including removing the init container,
which restores service but skips the migration guard. The Service `db` and
the ConfigMap need no change: they match what every other environment has.

## Verify

```sh
kubectl -n {{ .Namespace }} rollout status deployment/api
kubectl -n {{ .Namespace }} get pods
kubectl -n {{ .Namespace }} logs <pod-name> -c migrate
kubectl -n {{ .Namespace }} get endpointslices -l kubernetes.io/service-name=api
kubectl -n {{ .Namespace }} run curl --rm -it --restart=Never --image=curlimages/curl:8.14.1 -- curl -sS -m 5 -o /dev/null -w '%{http_code}\n' http://api.{{ .Namespace }}.svc/
```

`rollout status` ends with `deployment "api" successfully rolled out`. The
new pods show `1/1 Running`, and their `migrate` logs end with `database
reachable at db:5432` and `schema 12 ready, migrations done`: the loop
reached its last line and exited 0, which is what let the app container
start. The EndpointSlice lists two addresses once the old pods are gone,
and the curl pod prints `200`. Before the fix it printed `curl: (7) Failed
to connect to api.{{ .Namespace }}.svc port 80`: a Service whose pods are
not Ready has no endpoint, and kube-proxy refuses the connection at once.

## In the real world

- A wait-for-dependency init container is the standard pattern, and its
  failure mode is silence: no crash, no restart, no Event after `Started`.
  Alerts on `kube_pod_init_container_status_running` older than a few
  minutes, or on `kube_deployment_status_replicas_available` below the
  spec, catch it; alerts on restarts never do.
- Give the loop a bound. `nc -z -w 2` retried 30 times, then `exit 1`,
  turns a forever wait into `Init:CrashLoopBackOff`, which pages and keeps
  the reason in `logs --previous`.
- Migration names, ports and mount paths copied between environments drift
  in exactly this way; a `kubectl diff` of the manifests against a working
  environment finds them before the apply.
- A pipeline that runs `kubectl rollout status --timeout` turns this into a
  failed deploy instead of a forty-minute outage.

## Go deeper

- Init containers, their order and what `Init:` statuses mean:
  https://kubernetes.io/docs/concepts/workloads/pods/init-containers/
- Debugging init containers, including `logs -c` and the status table:
  https://kubernetes.io/docs/tasks/debug/debug-application/debug-init-containers/
- DNS for Services and Pods, the names a pod can resolve:
  https://kubernetes.io/docs/concepts/services-networking/dns-pod-service/
