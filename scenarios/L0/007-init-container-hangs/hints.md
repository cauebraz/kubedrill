# Hints

## Hint 1: where to look

`0/2 ready` is a property of the api pods, not of the Service. List them
with `kubectl -n {{ .Namespace }} get pods` and read the STATUS column:
`Init:0/1` means the pod is still inside its first init container, so the
app container has not started and the readiness probe is not the question
yet. The object to study is the init container `migrate`.

## Hint 2: what to observe

An init container has its own logs and its own section in describe. Print
the last lines of
`kubectl -n {{ .Namespace }} logs <pod-name> -c migrate` (the name from
the NAME column of `get pods`), then read the `Init Containers` section of
`kubectl -n {{ .Namespace }} describe pod <pod-name>`: `Args` is the loop
it runs, `Environment` and `Mounts` are the values the loop uses. Put next
to them `kubectl -n {{ .Namespace }} get svc` and
`kubectl -n {{ .Namespace }} get configmap migrate-config -o yaml`.

## Hint 3: what it means

The loop never fails; it retries until a condition holds, so the pod stays
`Init:0/1` with `State: Running` for as long as the condition is false. The
last log line names the condition: `nc: bad address` means the host name
has no Service in this namespace; a silent `waiting for database at
host:port` means the name resolves and nothing listens on that port; and
`waiting for migration flag <path>` means the database answered and the
file is not at that path, which happens when the ConfigMap is mounted
somewhere else. Compare the value in the log with the Service list and the
`Mounts` line; one of them does not match.

## Hint 4: what to change

The values the loop uses live in the Deployment `api`, init container
`migrate`: the `DB_HOST` and `DB_PORT` environment variables and the
`mountPath` of the `migrate-config` volume. Change the one that does not
match the cluster so it agrees with what `get svc db` prints, or so the
mount lands where `MIGRATE_FLAG` points, and watch the rollout replace the
pods. Restarting or deleting the pods changes nothing, because new pods
get the same template. The Service `db` and the ConfigMap are fine as they
are.
