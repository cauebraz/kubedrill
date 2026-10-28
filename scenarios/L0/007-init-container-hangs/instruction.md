# api never starts: the migrate init container hangs forever on the new cluster

## Ticket

> **From:** Lucia (billing team)\
> **Priority:** high
>
> We moved the billing `api` to the new cluster with the same manifests we
> run on the old one. The pipeline went green, but the dashboard has shown
> `0/2` ready for forty minutes and `http://api.{{ .Namespace }}.svc`
> refuses connections. Each api pod runs a small `migrate` step before the
> app starts: it waits for the database and for the schema flag our DBA
> team ships in a ConfigMap, then applies the migrations. On the old cluster
> that step took two seconds. Here it never finishes. The database pod is
> up, nobody touched the migration script, and restarting the pods changed
> nothing. Can platform take a look?

## Before you start

A pod runs its init containers one after another, to completion, before it
starts the app containers. While one is still running, `kubectl get pods`
shows STATUS `Init:0/1`: the app container does not exist yet, so nothing
can become Ready and the Service has nowhere to route. The init container
has its own logs (`kubectl logs <pod> -c <init-name>`) and its own section
in `kubectl describe pod`, with the command, the environment and the
mounts it runs with. Read what it is waiting for before changing anything.

## Environment

- Namespace: `{{ .Namespace }}`
- Resources: Deployment `api` (2 replicas, init container `migrate`, app
  container `api`), Service `api` (port 80), Deployment `db` (1 replica),
  Service `db`, ConfigMap `migrate-config` (the schema flag)
- You have cluster-admin on the local kind cluster
- `migrate` is a shell loop; it prints what it waits for

## Done when

- deployment db has 1 ready replica
- deployment api has 2 ready replicas
- every api pod runs the migrate init container to completion
- service api has at least one ready endpoint
- service api answers 200 to an in-cluster request

## Constraints

- Keep the Deployment, Service and ConfigMap names
- Keep Service `db` on port 5432: other teams connect to it
- Keep the `migrate` step: the app needs the schema it applies
- Do not delete the namespace
