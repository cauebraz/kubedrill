# Hints

## Hint 1: where to look

A `502` is the proxy's own answer: it asked the next hop and got no reply.
The edge's log names that hop and the error it got:
`kubectl -n {{ .Namespace }} logs deploy/edge`. The upstream in that line is
the Service `web`, so the next objects to read are the Service and the
EndpointSlice the cluster builds for it:
`kubectl -n {{ .Namespace }} get svc,endpointslices`.

## Hint 2: what to observe

Put four values side by side: `targetPort` in
`kubectl -n {{ .Namespace }} get svc web -o yaml`; `ports` and
`readinessProbe` of the container in
`kubectl -n {{ .Namespace }} get deploy web -o yaml`; and `listen` in
`kubectl -n {{ .Namespace }} get cm web-nginx -o yaml`. Then read the PORTS
and ENDPOINTS columns of
`kubectl -n {{ .Namespace }} get endpointslices -l kubernetes.io/service-name=web`,
the READY column of `kubectl -n {{ .Namespace }} get pods`, and the Events
of `kubectl -n {{ .Namespace }} describe pod <web-pod>`.

## Hint 3: what it means

Release 3.2.0 changed the port of the web container, and one object still
describes the old port. `targetPort` is resolved against the pod's
containerPorts: a name no containerPort carries leaves the EndpointSlice
without PORTS; a number nothing listens on gives `connection refused` at the
pod; and a readiness probe aimed at the old port keeps every pod not Ready,
which empties the ready endpoints. Your cluster shows one of these three.
The field that disagrees with `listen` in `web-nginx` is the one to change.

## Hint 4: what to change

Change the one field that still names the old port so it matches what
release 3.2.0 listens on: either `targetPort` of Service `web` (a number, or
the current name of the containerPort), or the readiness probe port in the
pod template of Deployment `web`. Leave `web-nginx` and the edge as they
are. Deleting pods changes nothing: they are built from the same template.
