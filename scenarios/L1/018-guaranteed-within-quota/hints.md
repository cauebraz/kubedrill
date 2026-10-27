# Hints

## Hint 1: where to look

No pod exists, so there is no pod to describe. Go one layer up, to the
objects that create pods, and to the quota they answer to:
`kubectl -n {{ .Namespace }} get deploy,rs,pods` and
`kubectl -n {{ .Namespace }} describe resourcequota shop`.

## Hint 2: what to observe

The DESIRED and CURRENT columns of the ReplicaSets differ. Read the
`FailedCreate` Events of one of them with
`kubectl -n {{ .Namespace }} describe rs -l app.kubernetes.io/name=catalog`,
and the `Used` and `Hard` columns of the quota. Print what each container
asks for today with
`kubectl -n {{ .Namespace }} get deploy -o 'custom-columns=NAME:.metadata.name,CONTAINERS:.spec.template.spec.containers[*].name,RESOURCES:.spec.template.spec.containers[*].resources'`.

## Hint 3: what it means

A quota on `requests.cpu`, `limits.cpu`, `requests.memory` and
`limits.memory` refuses any pod with a container that does not set all
four, so `must specify ... for: <containers>` lists every container still
missing them. Once they are set, the quota adds the values of every pod
and refuses the pod that would cross `Hard`. Guaranteed needs requests
equal to limits for both CPU and memory in every container, so the sum of
the limits is what has to fit.

## Hint 4: what to change

Edit the pod template of each Deployment, `storefront`, `catalog` and
`cart`: give every container listed in the `must specify` Event the same
CPU value for request and limit, and the same memory value for request and
limit. Divide the quota by the number of containers across all replicas
and stay under it; the replica counts and the quota stay as they are. A
LimitRange whose `default` equals its `defaultRequest` is the other route:
it fills the missing values at admission.
