# Solution - 019 uploads PVC Pending on the new cluster

## Symptom

Deployment `uploads` shows `0/1` ready, its pod stays `Pending` and PVC
`uploads-data` stays `Pending`. The pipeline reported success because
`kubectl apply` stores the objects; nothing waits for a volume to be
provisioned or for the pod to be scheduled.

## Candidate causes

A Pending pod next to a Pending claim fits several causes. Each one leaves a
different trace:

- The claim names a StorageClass the cluster does not have: the claim's
  Events show `ProvisioningFailed ... storageclass.storage.k8s.io "<name>"
  not found`, and the pod's `FailedScheduling` mentions unbound immediate
  PersistentVolumeClaims.
- The claim asks for something the provisioner cannot create (an access mode,
  a size, a parameter): the provisioner tries, and `ProvisioningFailed`
  quotes its error.
- The claim is fine and waits for a consumer: with `volumeBindingMode:
  WaitForFirstConsumer` the claim shows `WaitForFirstConsumer` and stays
  Pending until a pod that mounts it is scheduled. If the pod refers to
  another claim, the pod's `FailedScheduling` says that claim is not found.
- No provisioner is running: the claim shows `ExternalProvisioning` and
  nothing else, for minutes.
- The pod is Pending for a reason unrelated to storage (CPU, memory, taints,
  node selectors): `FailedScheduling` names that resource instead.

## Observation that splits them

```sh
kubectl get storageclass
kubectl -n {{ .Namespace }} get pvc uploads-data
kubectl -n {{ .Namespace }} describe pvc uploads-data
kubectl -n {{ .Namespace }} describe pod -l app.kubernetes.io/name=uploads
kubectl -n {{ .Namespace }} get events --sort-by=.lastTimestamp
```

`get storageclass` lists one class, `standard (default)`, provisioner
`rancher.io/local-path`, `VOLUMEBINDINGMODE` `WaitForFirstConsumer`. The
Events differ per variant (measured on kind v1.37 with local-path-provisioner):

- missing-class: `get pvc` shows `STORAGECLASS` `fast-ssd`. The claim's
  Event is `Warning ProvisioningFailed storageclass.storage.k8s.io
  "fast-ssd" not found`, and the pod's is `FailedScheduling 0/3 nodes are
  available: pod has unbound immediate PersistentVolumeClaims`. The class
  does not exist, so the scheduler cannot even apply WaitForFirstConsumer.
- rwx-access: `describe pvc` shows `Access Modes: RWX` and `Warning
  ProvisioningFailed failed to provision volume with StorageClass "standard":
  NodePath only supports ReadWriteOnce and ReadWriteOncePod (1.22+) access
  modes`. The class exists and the provisioner ran, but local-path creates a
  directory on one node, which only that node can mount.
- claim-name-typo: the claim shows only `Normal WaitForFirstConsumer waiting
  for first consumer to be created before binding`, and the pod shows
  `FailedScheduling 0/3 nodes are available: persistentvolumeclaim
  "upload-data" not found`. The claim is healthy; the pod's `Volumes` section
  names `ClaimName: upload-data`, one letter short of `uploads-data`.

```sh
kubectl -n {{ .Namespace }} get deployment uploads -o jsonpath='{.spec.template.spec.volumes[0].persistentVolumeClaim.claimName}'
```

Confirms which claim the pod template mounts, so you know whether the fix
belongs in the claim or in the Deployment.

## Change

For missing-class and rwx-access, the claim is wrong. A PVC's
`storageClassName` and `accessModes` are immutable (`kubectl edit` answers
`spec is immutable after creation except resources.requests and
volumeAttributesClassName for bound claims`), so recreate it with the same
name, `storageClassName: standard` and `ReadWriteOnce`. A Pending claim has
no volume, so nothing is lost; the Pending pod is not scheduled, so the
`kubernetes.io/pvc-protection` finalizer does not hold the delete.

```sh
kubectl -n {{ .Namespace }} get pvc uploads-data -o yaml > uploads-data.yaml
kubectl -n {{ .Namespace }} delete pvc uploads-data
kubectl -n {{ .Namespace }} apply -f - <<'YAML'
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: uploads-data
spec:
  storageClassName: standard
  accessModes: [ReadWriteOnce]
  resources:
    requests:
      storage: 1Gi
YAML
```

The first line keeps a copy of the old claim for the record. When only one
field is wrong, recreate the claim from that copy with just that field
changed. For missing-class:

```sh
yq '.spec.storageClassName = "standard" | del(.status) | .metadata = {"name": .metadata.name, "labels": .metadata.labels}' uploads-data.yaml | kubectl -n {{ .Namespace }} apply -f -
```

The same command leaves rwx-access Pending, because the copy still asks for
`ReadWriteMany`; there, set `.spec.accessModes = ["ReadWriteOnce"]`
instead. Changing
`ReadWriteOnce` to `ReadWriteOncePod` also works on local-path. For
missing-class, creating a StorageClass `fast-ssd` with the local-path
provisioner also binds the claim, but it adds a cluster-wide object to hide a
manifest that does not match the cluster; fix the claim instead.

For claim-name-typo, the claim is right and the Deployment is wrong. Point
the volume at `uploads-data`:

```sh
kubectl -n {{ .Namespace }} patch deployment uploads --type=json -p '[{"op":"replace","path":"/spec/template/spec/volumes/0/persistentVolumeClaim/claimName","value":"uploads-data"}]'
```

Creating a second claim named `upload-data` would start the pod, but
`uploads-data` stays Pending and the data lands in a claim nobody else knows
about.

## Verify

```sh
kubectl -n {{ .Namespace }} rollout status deployment/uploads
kubectl -n {{ .Namespace }} get pvc uploads-data
kubectl -n {{ .Namespace }} exec deploy/uploads -- sh -c 'echo test > /data/probe.txt && cat /data/probe.txt && df -h /data'
```

`rollout status` ends with `deployment "uploads" successfully rolled out`.
`get pvc` shows `STATUS` `Bound` with a `pvc-...` volume, `RWO` and
`standard`. The exec prints `test`, and `df` shows `/data` mounted from the
node's disk (`/dev/vda1` under Docker Desktop), not from the container's
`overlay` root: the file lives in the local-path directory on the node.

## In the real world

- StorageClass names are per cluster: `gp3` on EKS, `standard-rwo` on GKE,
  `managed-csi` on AKS, `standard` on kind. Manifests copied between clusters
  carry the old name. Leave `storageClassName` out to use the default class,
  or template it per environment.
- ReadWriteMany needs a shared filesystem (NFS, EFS, Azure Files, CephFS).
  Block storage (EBS, Persistent Disk, local-path) is ReadWriteOnce. A single
  replica rarely needs RWX.
- With WaitForFirstConsumer a Pending claim is not an alarm by itself; a
  Pending claim with a Pending pod for more than a few minutes is. Alert on
  `kube_persistentvolumeclaim_status_phase{phase="Pending"}` together with
  pods in Pending, and make the pipeline run `rollout status`.
- Deleting a Bound claim with the default `reclaimPolicy: Delete` deletes the
  data. Recreating a claim is safe here only because it never bound.

## Go deeper

- Persistent volumes, claims, access modes and binding:
  https://kubernetes.io/docs/concepts/storage/persistent-volumes/
- StorageClasses and volumeBindingMode:
  https://kubernetes.io/docs/concepts/storage/storage-classes/
- local-path-provisioner:
  https://github.com/rancher/local-path-provisioner
