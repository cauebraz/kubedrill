# Cart checkout times out but the cart pods are Running

## Ticket

> **From:** Dana (storefront team)\
> **Priority:** high
>
> We shipped cart release 2.3 this morning by re-applying the whole `cart/`
> folder (Deployment and Service) from the release branch. The rollout
> finished, `kubectl get pods` shows both cart pods `Running` and `1/1`,
> and nothing restarts. But the checkout page spins for 30 seconds and then
> shows "cart unavailable", and the frontend log says every call to
> `http://cart/` fails. The image is the same one we run everywhere; the
> release was manifest housekeeping, no code change. We already restarted
> the frontend, same result. Can platform take a look?

## Before you start

A Service is a name, a selector and a port mapping. The EndpointSlice
controller lists every pod whose labels match the selector, kube-proxy
sends traffic to the listed addresses that are Ready, and the Service
`port` is forwarded to `targetPort` on the pod. `Running` is a property of
the pod; reachable through the Service depends on the selector and on the
port. `kubectl get endpointslices -l kubernetes.io/service-name=cart` shows
what the Service resolved to, and `kubectl port-forward` reaches a pod
without the Service, which splits "the pod serves" from "the Service routes".

## Environment

- Namespace: `{{ .Namespace }}`
- Resources: Deployment `cart` (2 replicas, container `cart`, nginx
  listening on port 80), Service `cart` (port 80), Deployment `frontend`
  (1 replica; it calls `http://cart/` every 5 seconds and logs the result)
- You have cluster-admin on the local kind cluster
- Release 2.2 answered `http://cart.{{ .Namespace }}.svc` with the nginx
  welcome page, and the image did not change in 2.3

## Done when

- deployment cart has 2 ready replicas
- service cart lists at least one endpoint
- service cart has at least one ready endpoint
- the cart page is served through service cart from inside the cluster
- deployment cart finished rolling out: every pod runs the current template
- service cart selects the pods that deployment cart creates

## Constraints

- Keep the Deployment and Service names: `frontend` and the other teams
  call `http://cart/`
- Do not delete the namespace
