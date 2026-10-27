`kubectl create secret generic --from-file` is used as a generator: with
`--dry-run=client -o yaml` it prints the Secret manifest without creating
anything, and `kubectl apply -f -` writes that manifest over the existing
`payments-token`. The Secret never disappears, so a pod that starts during
the rotation still finds it, and the labels and annotations it already had
survive. The token exists in `token.txt` for a few seconds and in no
manifest you keep.

Choose it whenever the Secret has consumers that may restart at any time,
which is every production Secret. The first `apply` warns that the
`kubectl.kubernetes.io/last-applied-configuration` annotation is missing,
because the Secret was not created with `kubectl apply`; kubectl adds the
annotation and the warning does not come back.

The trade-off is that `apply` merges: a key you want gone has to be removed
on purpose, and a Secret with several keys is rebuilt with every
`--from-file` it needs, or the keys missing from the generated manifest are
left as they were. The `patch` on `payments-worker` is a no-op when the
worker already reads `payments-token`; `kubectl edit` is the interactive
form of the same change.
