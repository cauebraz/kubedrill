When the Secret's key has another name than the file the app reads, rename
the key: rebuild the Secret with the key `db-password` and the same value,
and replace it. The value passes through a local file, so it never shows
up in the shell history; remove the file afterwards.

The kubelet refreshes a Secret volume in a running pod after its sync
delay, and this app reads the file only at start, so the snippet restarts
the rollout to get pods that read it now.

Choose it when the Deployment is the contract other environments share and
the Secret was created by hand for this one: the key then matches what the
app's README and every other environment use. The trade-off is that every
other consumer of the key `password` breaks; check with `kubectl get pods
-o yaml` for other volumes or `secretKeyRef` entries that name it first.
