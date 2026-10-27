# Hints

## Hint 1: where to look

`0/1` ready with the pod in `Pending` means the pod never got a node, and the
ticket already names a Pending claim. Look at the pod, the claim and the
storage the cluster offers:
`kubectl -n {{ .Namespace }} get pods,pvc` and `kubectl get storageclass`.

## Hint 2: what to observe

Read the Events of both objects:
`kubectl -n {{ .Namespace }} describe pvc uploads-data` and
`kubectl -n {{ .Namespace }} describe pod -l app.kubernetes.io/name=uploads`,
or all of them with `kubectl -n {{ .Namespace }} get events --sort-by=.lastTimestamp`.
Note the `Warning` lines (`ProvisioningFailed`, `FailedScheduling`) and
compare the claim's `StorageClass` and `Access Modes`, and the claim name in
the pod's `Volumes` section, with what the cluster has.

## Hint 3: what it means

A claim stays Pending for one of three reasons here. It names a StorageClass
that does not exist on this cluster, so no provisioner picks it up. It asks
for an access mode the provisioner cannot create (local-path only creates
volumes one node mounts at a time). Or nothing is wrong with the claim at all:
`standard` uses `WaitForFirstConsumer`, and the pod refers to a claim with
another name, so no pod ever consumes `uploads-data`. The Event that names the
claim tells you which one this is.

## Hint 4: what to change

If the pod points at the wrong claim name, fix the volume in the Deployment
`uploads` pod template. If the claim itself asks for something the cluster
cannot give, the claim must be recreated with `uploads-data` as its name: a
PVC's `storageClassName` and `accessModes` cannot be changed after creation.
Write the corrected claim to a file first, then remove the old claim and
create the new one; the Pending pod picks it up without a restart.
