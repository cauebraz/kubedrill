Recreate the claim with the same name and a spec the cluster can satisfy:
`storageClassName: standard` (or no `storageClassName` at all, which picks
the default class) and `ReadWriteOnce`. A PVC's spec is immutable after
creation, so `edit` and `patch` are refused; delete and create is the only
route. The Pending pod refers to the claim by name, so the scheduler picks up
the new claim on its next attempt and no pod restart is needed.

Choose it when the claim asks for something the cluster cannot give: a
StorageClass from another cluster, or an access mode the provisioner does not
support. It is safe only because the claim never bound: a Pending claim has
no volume and no data. On a Bound claim with `reclaimPolicy: Delete`, the
same delete removes the volume and everything on it.

The trade-off is drift, as with any live change: also correct the claim in
the repository's manifest, or the next pipeline run fails to apply it (the
API server refuses the immutable change) or a fresh cluster repeats the
outage. Leaving `storageClassName` out of the manifest makes it portable
across clusters whose default class differs.
