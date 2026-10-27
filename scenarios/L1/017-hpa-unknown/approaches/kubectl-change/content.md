One imperative command on the object that holds the broken link: `set
resources` on the Deployment when the CPU request is missing, `patch` on the
HPA when its target or its metric is wrong. `get hpa --watch` is the
verification: TARGETS turns from `<unknown>` into a percentage once
metrics-server has scraped the pod, which takes up to a minute.

Choose it before a known load peak, when the autoscaler has to work now. The
trade-off is drift: the repository still holds the broken manifest, and the
next pipeline run or GitOps sync applies it again. Follow it with the
manifest change.
