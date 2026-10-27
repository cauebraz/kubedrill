`kubectl edit` opens the live Deployment in `$EDITOR`. Find the block of
the container that was killed under `spec.template.spec.containers`, set
`resources.requests.memory` to the steady-state use (64Mi for `report`,
32Mi for `log-shipper`) and `resources.limits.memory` above the peak (128Mi
and 64Mi), save, and quit. When the container has no `resources` block at
all, which is the case when a LimitRange default filled the limit in, add
the whole block. Saving applies the change; `rollout status` waits for the
new pod to be Ready for `minReadySeconds`.

Choose it when you want to see the whole template while you change it: the
container names, the args that say how much data the report loads, and
whether a `resources` block exists at all. It is also the route that
practices the same edit you will make in the repository's manifest, which
is where the fix has to land before the next deploy.

The trade-off is precision under pressure. An editor session invites a
second change ("while I am here"), and a YAML indentation slip is rejected
with an error you have to read. In a GitOps setup the edit is reverted by
the next sync, so it is a mitigation, not the fix.
