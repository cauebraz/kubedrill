`kubectl set selector` replaces the whole `spec.selector` map of the Service
with the entries on the command line, so the result is exactly what you
typed, with no leftover key from the old map. The EndpointSlice controller
rewrites the slice within a second and kube-proxy picks it up; no pod
restarts, because the pods were never wrong.

Choose it during an incident when the Service is the object that drifted:
one command, one field, and `kubectl get endpointslices` verifies it at
once. Pick the selector from the labels the pods carry
(`kubectl get pods --show-labels`), and keep it to the stable labels
(`app`, `app.kubernetes.io/name`); a `tier` or `version` entry breaks again
at the next rename.

The trade-off is drift from the manifest: the next apply of the release
branch brings the old selector back. Follow it with the change in
`cart/service.yaml`.
