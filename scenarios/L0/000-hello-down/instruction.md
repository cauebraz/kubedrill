# Hello app is down after the cluster migration

## Ticket

> **From:** Dana (frontend team)\
> **Priority:** high
>
> We moved `hello` to the new cluster this morning using the manifests from the
> old repo. The deploy "worked" according to the pipeline, but the dashboard
> shows `0/2` ready and the internal URL
> `http://hello.{{ .Namespace }}.svc` refuses connections. Nothing changed in
> the app itself. Can someone from platform take a look?

## Before you start

A pod moves through the Pending phase (scheduling, image pull,
ContainerCreating) to Running, and the readiness probe decides when it counts
as Ready; the Service only routes to Ready pods, so `0/2` means the pods
stalled somewhere on that path. The STATUS column of `kubectl get pods` names
the step that stalled, and `kubectl describe pod` prints the Events the kubelet
wrote about it. Read the Events before changing anything.

## Environment

- Namespace: `{{ .Namespace }}`
- Resources: Deployment `hello` (2 replicas), Service `hello` (port 80)
- You have cluster-admin on the local kind cluster
- The frontend team confirms the app runs the nginx `1.27-alpine` image in
  every other environment

## Done when

- deployment hello exists
- deployment hello has 2 ready replicas
- deployment hello runs the nginx:1.27-alpine image
- service hello has at least one ready endpoint

## Constraints

- Keep the Deployment and Service names
- Do not delete the namespace
