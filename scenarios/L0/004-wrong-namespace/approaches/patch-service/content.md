`kubectl patch service` changes one field of the Service in place: the
selector value with a merge patch, the targetPort with a JSON patch that
addresses `ports[0]`. The EndpointSlice controller recomputes the endpoints
within a second and kube-proxy reprograms the nodes, so the first line of the
snippet (print what the Service has) and the last (print what it now
matches) are the before and after of one small change. `kubectl edit service
catalog` reaches the same end state through an editor.

Choose it when the Deployment is right and the Service is the side that was
copied with an old value, which `describe service catalog` next to `get pods
--show-labels` tells you. It is one field, it takes effect without a rollout,
and the ClusterIP stays the same, so clients that cached the address keep
working.

The trade-off is the manifest in the repository, which still carries the old
value: the next apply puts it back. Patch to restore the panel, then fix the
file. Patching the selector toward a label the pods happen to carry (a
temporary label invented during a copy) also restores endpoints, but it
cements the wrong name in a namespace every other environment calls
`catalog`.
