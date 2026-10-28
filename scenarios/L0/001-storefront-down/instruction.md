# Storefront is down after the cluster migration

## Ticket

> **From:** Dana (storefront team)\
> **Priority:** high
>
> We moved `storefront` to the new cluster this morning with the manifests
> from the old repo. The pipeline went green, but the status page shows
> `0/3` ready, `https://shop.example` returns 503 from the edge, and the
> internal URL `http://storefront.{{ .Namespace }}.svc` refuses connections.
> The image and the static build (release 2.14.0) are the ones we run
> everywhere; nothing changed in the app. Can platform take a look?

## Before you start

A pod moves through the Pending phase (scheduling, image pull,
ContainerCreating) to Running, and the readiness probe decides when it counts
as Ready; the Service only routes to Ready pods, so `0/3` means the pods
stalled somewhere on that path. The STATUS column of `kubectl get pods` names
the step that stalled, and `kubectl describe pod` prints the Events the kubelet
wrote about it. Read the Events before changing anything.

## Environment

- Namespace: `{{ .Namespace }}`
- Resources: Deployment `storefront` (3 replicas, container `web`), Service
  `storefront` (port 80), ConfigMap `storefront-html` (the static build)
- You have cluster-admin on the local kind cluster
- Every other environment runs the same storefront image and serves the
  files of `storefront-html`; that image is the reference for this one, and
  the storefront team can name it

## Done when

- deployment storefront exists
- deployment storefront has 3 ready replicas
- deployment storefront runs the image every other environment runs
- service storefront has at least one ready endpoint
- service storefront serves the ACME Storefront 2.14.0 page

## Constraints

- Keep the Deployment, Service and ConfigMap names
- Do not delete the namespace
