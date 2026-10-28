# uploads PVC Pending on the new cluster

## Ticket

> **From:** Noor (media team)\
> **Priority:** high
>
> We moved the `uploads` service to the new cluster yesterday with the
> manifests from our repo. The pipeline went green, but `uploads` shows
> `0/1` ready, the pod sits in `Pending`, and `kubectl get pvc` says
> `uploads-data` is `Pending` too. Customers cannot attach images to their
> listings since the move. On the old cluster the same claim bound in
> seconds. Did we lose our storage on the new cluster?

## Before you start

A PersistentVolumeClaim (PVC) asks for storage; a StorageClass names the
provisioner that creates a PersistentVolume to satisfy it, and the claim
turns `Bound` once one exists. A pod that mounts a claim cannot start until
the claim is Bound. A StorageClass with `volumeBindingMode:
WaitForFirstConsumer` creates the volume only after a pod that uses the claim
is scheduled, so a `Pending` claim is normal until then. Read the Events of
the claim and of the pod before changing anything.

## Environment

- Namespace: `{{ .Namespace }}`
- Resources: Deployment `uploads` (1 replica, container `uploads`, writes
  customer files under `/data`), PersistentVolumeClaim `uploads-data` (1Gi)
- You have cluster-admin on the local kind cluster
- The old cluster ran the same manifests; the new cluster's storage is
  whatever `kubectl get storageclass` lists

## Done when

- persistentvolumeclaim uploads-data is Bound
- deployment uploads has 1 ready replica
- the uploads pod writes and reads a file on the volume mounted at /data

## Constraints

- Keep the names of the Deployment `uploads` and the claim `uploads-data`
- One replica is enough; do not scale the Deployment up
- Do not delete the namespace
