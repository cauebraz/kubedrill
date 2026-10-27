When the Secret's key has another name than the file the app reads, an
`items` list on the Secret volume maps a key to a file path: `key` is the
name in the Secret, `path` the file name under the `mountPath`. The Secret
stays as it is, and the Deployment rolls out pods that find
`/etc/secrets/db-password`.

Choose it when the Secret is shared with other consumers that expect the
key as it is, or when another team or a controller (External Secrets,
Sealed Secrets) owns the Secret and you own only the Deployment.

The trade-off: with `items`, only the listed keys become files. A key added
to the Secret later stays invisible to the app until someone adds it to the
list, and a listed key that disappears from the Secret blocks the pod in
`ContainerCreating` unless the volume sets `optional: true`.
