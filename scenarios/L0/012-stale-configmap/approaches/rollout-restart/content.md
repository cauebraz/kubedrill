`kubectl rollout restart` writes the annotation
`kubectl.kubernetes.io/restartedAt` into the Deployment's pod template. The
template changed, so the Deployment creates a new ReplicaSet and replaces the
pods one by one; each new container resolves its env vars and mounts its
volumes now, after the edit, and reads `Winter sale 2026`. `rollout status`
blocks until the new pods are Ready and the old ones are gone, which is the
verification, not decoration.

Choose it when the value reaches the container through an env var or a
`subPath` mount: nothing refreshes those in a running pod, so replacing the
pods is the only route. It also works for the wrong-name variant once the
ConfigMap the pods mount holds the new text, and it is the generic answer an
on-call engineer reaches for when the pod template is correct and the pods
are merely older than the change.

The trade-off is that the next edit needs the same restart again. Teams that
keep env vars or subPath mounts automate it: a checksum of the ConfigMap in
a pod template annotation (Helm), a hashed ConfigMap name
(Kustomize `configMapGenerator`), or a controller that restarts the
Deployment when the ConfigMap changes.
