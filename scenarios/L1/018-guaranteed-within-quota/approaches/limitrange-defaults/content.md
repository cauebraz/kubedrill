A LimitRange with `default` (the limit) equal to `defaultRequest` fills
both into every container that declares none, at admission. The quota then
sees the values and the QoS class comes out Guaranteed, without touching
the Deployments. The ReplicaSets retry the refused pods with a growing
backoff, so `rollout restart` makes them try at once.

Choose it as the namespace-wide safety net a platform team ships next to
a quota: a new Deployment without resources still starts. The trade-off is
that one size fits every container, the sidecar as much as the web
server, and the values are invisible in the Deployment, so a reader of the
manifest does not know what the pod gets. A container that sets only a
limit or only a request is filled differently, so check
`status.qosClass` afterwards.
