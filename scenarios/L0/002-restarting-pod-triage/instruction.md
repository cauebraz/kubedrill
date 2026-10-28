# One of the shop pods keeps restarting

## Ticket

> **From:** Dana (shop on-call)\
> **Priority:** medium
>
> The restart alert for namespace `{{ .Namespace }}` has been firing since
> 07:40: "a pod restarted more than 5 times in the last hour". It does not
> say which one, and the namespace runs about 40 pods across 14 small
> services. Customers have not complained, so this is triage, not an
> outage. Can you find the pod, label it `triage=yes` so it shows up on the
> on-call dashboard (it lists pods with that label), and make it stop
> restarting? Please leave the other services alone, they are fine.

## Before you start

A Deployment keeps a number of pods running from one template; a pod wraps
a container. When a container exits or fails its liveness probe, the
kubelet starts it again inside the same pod and adds one to the RESTARTS
column of `kubectl get pods`. A restart count that keeps growing is the
symptom; the reason is in the pod's `Last State`, its Events and the logs
of the previous run. A label is a key=value you attach to any object;
dashboards and selectors filter by it.

## Environment

- Namespace: `{{ .Namespace }}`
- Resources: 14 Deployments of the shop (storefront, catalog, search,
  cart, checkout, pricing, inventory, recommendations, accounts, images,
  payments, shipping, notifications, reviews), 40 pods in total, each pod
  one container named `web`
- Every service runs the same busybox `httpd` on port 8080 with a 32Mi
  memory limit; the ConfigMaps `shop-pages` and `reviews-config` hold
  their files
- You have cluster-admin on the local kind cluster

## Done when

- exactly one pod in the namespace carries the label triage=yes
- the pod labeled triage=yes is a pod of the service that was restarting
- the service that was restarting runs one pod with 0 restarts that has stayed up at least 45 seconds
- the service that was restarting has its pod Running and Ready
- every deployment in the namespace has all its replicas available (14 deployments, 40 pods)

## Constraints

- Keep the 14 Deployment names and their replica counts
- Do not delete the namespace or the ConfigMaps
- The label goes on the pod that is running when you hand the ticket back;
  a rollout replaces pods, and their labels with them
