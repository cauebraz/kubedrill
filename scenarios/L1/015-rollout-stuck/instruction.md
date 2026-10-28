# Rollout of checkout stuck at 3/4 for an hour

## Ticket

> **From:** Dana (storefront team)\
> **Priority:** high
>
> We shipped checkout release 2.5.0 an hour ago. The pipeline's
> `kubectl rollout status` step failed with `deployment "checkout" exceeded
> its progress deadline`, and the deploy dashboard has shown the rollout as
> unfinished ever since: not all 4 replicas run 2.5.0. Checkout still takes
> orders, so customers have not noticed yet, but the evening peak starts
> soon and the next deploy is blocked behind this one. Release 2.4.0 ran
> fine all week. Can platform get checkout back to 4 healthy replicas?

## Before you start

A Deployment rolls out a new pod template by creating a new ReplicaSet and
moving replicas from the old one to it, a few at a time: `maxSurge` says how
many extra pods may exist, `maxUnavailable` how many may be missing. When the
new pods never become available, the rollout stops where it is, and after
`progressDeadlineSeconds` the Deployment's `Progressing` condition turns
False with reason `ProgressDeadlineExceeded`. Each template change is a
revision you can list with `kubectl rollout history`.

## Environment

- Namespace: `{{ .Namespace }}`
- Resources: Deployment `checkout` (4 replicas, container `web`, the official
  nginx `1.27-alpine` image), Service `checkout` (port 80), ConfigMap
  `checkout-config` (settings both releases read)
- Revision 1 of the Deployment is release 2.4.0, revision 2 is release 2.5.0
- You have cluster-admin on the local kind cluster

## Done when

- deployment checkout has 4 ready replicas
- deployment checkout has 4 up-to-date replicas
- deployment checkout reports Progressing with reason NewReplicaSetAvailable
- service checkout has 4 ready endpoints
- no checkout pod is stuck in Pending

## Constraints

- Keep the Deployment and Service names and the 4 replicas
- Do not delete the namespace
- Rolling back to 2.4.0 and fixing 2.5.0 forward are both acceptable
