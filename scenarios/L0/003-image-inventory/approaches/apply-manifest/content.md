`--dry-run=client -o yaml` turns the same `create configmap` call into a
manifest without touching the cluster, and `kubectl apply` writes it. The
file `inventory.yaml` is the artifact: it goes into the repository next to
the other manifests of the namespace, a pull request reviews the list, and
the next audit diffs the file instead of the cluster.

Choose it when the inventory has to be versioned or reproduced, which is
every audit that runs more than once. `apply` also updates an existing
ConfigMap, so rerunning the three lines refreshes the list where `create`
would fail with `already exists`. The trade-off is one more file to keep in
sync with the cluster: a manifest written by hand drifts as soon as a
Deployment changes its image, so regenerate it, never edit it.
