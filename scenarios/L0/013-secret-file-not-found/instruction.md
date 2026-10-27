# db-password is mounted but the api says file not found

## Ticket

> **From:** Priya (billing team)\
> **Priority:** high
>
> We moved the database password of the billing `api` out of an environment
> variable and into a Secret this afternoon, the way the app's README asks:
> the app reads `/etc/secrets/db-password` when it starts and refuses to run
> without it. We created the Secret `db-credentials`, mounted it into the
> `api` Deployment and applied. Since then the Deployment sits at `0/2` ready
> and has not served a request. `kubectl logs` on the pods either prints
> `api: cannot read /etc/secrets/db-password` or refuses because the
> container has not started. The Secret is there, `kubectl get secret
> db-credentials` lists it. The same change worked in the dev namespace. What
> are we missing?

New to Kubernetes? `kubedrill story` tells this ticket from zero.

## Before you start

A Secret is a set of keys with a value each. Mounted as a volume, every key
becomes a file under the volume's `mountPath`, named after the key, unless
`items` renames it. The kubelet mounts volumes before it starts the
container: a volume it cannot build keeps the pod in `ContainerCreating`
with a `FailedMount` event, while a volume that mounts fine but puts the
file somewhere else lets the container start and fail on its own. `kubectl
describe pod` prints both the Mounts and the Events.

## Environment

- Namespace: `{{ .Namespace }}`
- Resources: Deployment `api` (2 replicas, container `api`, busybox image
  running the api startup script), Secret `db-credentials` (the database
  password)
- You have cluster-admin on the local kind cluster
- The app reads the file `/etc/secrets/db-password` at start, logs
  `api: connected` when it could, and exits when it could not

## Done when

- deployment api exists
- deployment api has 2 ready replicas
- the api pods log "api: connected"
- deployment api has 2 available replicas

## Constraints

- Keep the Deployment name `api` and the Secret name `db-credentials`
- Do not change the app's startup script; the path it reads is the contract
- Do not delete the namespace
