This route applies only when the limit the pod shows is one the Deployment
never wrote: `kubectl -n {{ .Namespace }} get deployment report -o
jsonpath='{.spec.template.spec.containers[*].resources}'` prints `{}` and
`kubectl -n {{ .Namespace }} get limitrange` lists `namespace-defaults`.
The patch raises the namespace's `max`, `default` (limit) and
`defaultRequest` for containers; `max` has to move too, or it keeps
capping every container at 32Mi. A LimitRange is applied at admission, so the running pod
keeps its 32Mi until it is replaced; `rollout restart` creates a new pod,
which is admitted with the new defaults.

Choose it when the namespace default is wrong for every workload in it,
not only for this one, and when the team's policy is "the platform sets
the ceiling, applications do not". It leaves the Deployment manifest
untouched, which is what a team without `resources` in its manifests wants.

The trade-off is reach. The LimitRange belongs to the platform team, the
change touches every future pod in the namespace, and a Deployment that
still writes no `resources` depends on it forever. The explicit route
(`set-resources`) makes the report's need visible in its own manifest, and
a reviewer can see it; most teams do both: explicit numbers on the
workload, a defensive default in the namespace.
