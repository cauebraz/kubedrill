Writing the new text into `banner-config`, the ConfigMap the pods mount,
makes the kubelet refresh the volume on its next sync: it swaps the `..data`
symlink inside the volume and nginx serves the new file with no rollout and
no restart. The second line reads the file back; repeat it until it prints
`Winter sale 2026`, which takes up to about a minute (the kubelet sync
period plus its ConfigMap cache TTL; measured in seconds on kind v1.37).

Choose it in the wrong-name variant when a rollout is unwelcome, for example
during peak traffic, or when the Deployment is owned by another team and you
may only touch configuration. It keeps the pods, their IPs and their
connections.

The trade-off is that it fixes the symptom, not the cause: the Deployment
still mounts `banner-config` while the runbook says `banner-config-v2`, so
the next edit lands in the wrong object again. It also does nothing for the
env var and subPath variants, where the mounted value is frozen until the
pods are replaced. Follow it with a change to the Deployment or the runbook
so both name the same ConfigMap.
