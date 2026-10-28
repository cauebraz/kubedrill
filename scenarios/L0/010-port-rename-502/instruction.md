# Docs portal answers 502 since the web release that changed its port

## Ticket

> **From:** Alex (docs team)\
> **Priority:** high
>
> We shipped release 3.2.0 of the docs portal (`web`) yesterday evening. The
> release notes say the nginx cleanup changed the HTTP port of the web
> container; both web pods show `Running`. Since then
> `https://docs.example` answers `502 Bad Gateway` from the edge on every
> request, not now and then. The page itself is fine: the same release
> serves it on a laptop with `docker run`. Nothing else changed in the
> namespace. Can platform take a look?

## Before you start

A request walks `edge` -> Service `web` -> a Ready `web` pod -> the port
nginx listens on. The Service's `targetPort` names the pod port, as a number
or as the name of a `containerPort`, and the EndpointSlice shows what it
resolved to and which pods are Ready. A `502` is the proxy's own answer: it
asked the next hop and got nothing back, so read the proxy's log for the
upstream error first, then walk the hop it names. Read before you change
anything.

## Environment

- Namespace: `{{ .Namespace }}`
- Resources: Deployment `edge` (1 replica, nginx reverse proxy configured by
  ConfigMap `edge-nginx`, shared with other teams), Service `edge` (port 80,
  the customer entry point), Deployment `web` (2 replicas, container
  `nginx`, release 3.2.0), Service `web` (port 80), ConfigMaps `web-nginx`
  (the server block of release 3.2.0) and `web-html` (the page), Deployment
  `statuscheck` (1 replica, the status page's checker; it requests
  `http://edge/` every 5 seconds and logs the result)
- You have cluster-admin on the local kind cluster
- The port change in release 3.2.0 is intentional; the docs team ships
  `web-nginx` with the release and it stays as it is

## Done when

- deployment web has 2 ready replicas
- service web has at least one ready endpoint
- service web endpoints list a port
- service web serves the ACME Docs Portal 3.2.0 page
- service edge serves the ACME Docs Portal 3.2.0 page
- configmap web-nginx still holds the release 3.2.0 server block
- deployment web still has a readiness probe

## Constraints

- Keep the names of the Deployments, Services and ConfigMaps
- Do not change ConfigMap `web-nginx`; the port of release 3.2.0 stays
- Do not change the edge Deployment, Service or ConfigMap, or the
  `statuscheck` Deployment
- Do not delete the namespace
