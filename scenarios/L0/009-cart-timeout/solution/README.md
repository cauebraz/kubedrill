# Solution - 009 Cart checkout times out but the cart pods are Running

## Symptom

Deployment `cart` shows `2/2` ready, both pods are `Running` with `1/1`
and zero restarts, and `kubectl -n {{ .Namespace }} logs deploy/frontend`
prints `wget: can't connect to remote host (10.96.x.x): Connection refused`
every 5 seconds. The rollout looked healthy because `rollout status` and
the readiness probe both talk to the pod directly; neither goes through
the Service, and the Service is the object release 2.3 also re-applied.

## Candidate causes

Running, Ready pods behind a Service name that resolves and refuses
connections fits a short list. Each cause leaves a different trace:

- Pods not Ready: READY `0/1`, the EndpointSlice lists the addresses with
  `ready=false`, Events repeat `Readiness probe failed`. Ruled out by `1/1`.
- Container bound to `127.0.0.1`: the kubelet probes the pod IP, so the
  readiness probe would fail too. Ruled out by the same `1/1`.
- DNS: the client prints `bad address` or `Could not resolve host` and never
  gets an IP. Ruled out by the ClusterIP in the frontend's error line.
- A NetworkPolicy dropping the traffic: packets are dropped, so the client
  waits for its timeout instead of a refused connection.
- Selector mismatch: the Service selector names a label the pods do not
  carry, the EndpointSlice ENDPOINTS column is `<unset>`, and kube-proxy
  rejects the connection at once.
- Port mapping: the selector matches, the EndpointSlice lists the pod
  addresses, but its PORTS column is a port nothing in the pod listens on,
  and the pod's kernel refuses the forwarded connection.

## Observation that splits them

```sh
kubectl -n {{ .Namespace }} logs deploy/frontend --tail=5
kubectl -n {{ .Namespace }} get pods -o wide --show-labels
kubectl -n {{ .Namespace }} get svc cart -o wide
kubectl -n {{ .Namespace }} get endpointslices -l kubernetes.io/service-name=cart
```

The frontend log names the Service's ClusterIP and `Connection refused`,
which rules out DNS and a NetworkPolicy (measured on kind v1.37 with
kube-proxy in iptables mode). The pod labels include
`app=cart,pod-template-hash=...,tier=web`. The SELECTOR column of the
Service and the EndpointSlice line then split the last two causes:

{{ if eq .Variant "wrong-target-port" -}}
- wrong-target-port: SELECTOR is `app=cart` and the EndpointSlice reads
  `cart-xxxxx IPv4 8080 10.244.2.132,10.244.1.172`: both pod addresses are
  listed, but on port 8080. `kubectl -n {{ .Namespace }} describe svc cart`
  prints `Port: http 80/TCP`, `TargetPort: 8080/TCP` and `Endpoints:
  10.244.2.132:8080,10.244.1.172:8080`, while the container declares
  `containerPort: 80` and nginx listens there. The selector is fine; the
  port mapping is wrong.
{{- else if eq .Variant "stale-tier-selector" -}}
- stale-tier-selector: SELECTOR is `app=cart,tier=api` and the EndpointSlice
  reads `cart-xxxxx IPv4 <unset> <unset>`. The pods carry `tier=web`. Every
  key of a selector must match, so the extra key selects nothing.
  `kubectl -n {{ .Namespace }} get rs --show-labels` shows where the old
  value came from: ReplicaSet `cart-6c9d8f5b7` with `0` desired and
  `tier=api` in its labels is revision 1, release 2.2; release 2.3 renamed
  the label on the pods and the Service kept the old value.
{{- else -}}
- wrong-app-label: SELECTOR is `app=carts` and the EndpointSlice reads
  `cart-xxxxx IPv4 <unset> <unset>`: no pod matched, so there is no address
  and no port. The pods carry `app=cart`; one character differs.
{{- end }}

```sh
kubectl -n {{ .Namespace }} get pods -l app={{ .Vars.selector_app }}{{ if .Vars.selector_tier }},tier={{ .Vars.selector_tier }}{{ end }}
kubectl -n {{ .Namespace }} describe svc cart
kubectl -n {{ .Namespace }} port-forward deploy/cart 8080:80
```

The first command runs the Service's selector against the pods by hand:
`No resources found in {{ .Namespace }} namespace.` is the selector
diagnosis, two pods means the selector is fine and the port is next.
`describe svc cart` prints `TargetPort` and the `Endpoints` with their
port. The port-forward opens `localhost:8080` on the workstation straight
to a cart pod, skipping the Service: `curl -sS localhost:8080/` in a second
pane prints `<title>Welcome to nginx!</title>` in every variant, which
proves the pods serve and leaves the Service as the only broken layer.

## Change

{{ if eq .Variant "wrong-target-port" -}}
Change one thing: `spec.ports[0].targetPort` of Service `cart`, to the port
the container listens on.

```sh
kubectl -n {{ .Namespace }} patch svc cart --type=json -p '[{"op":"replace","path":"/spec/ports/0/targetPort","value":80}]'
```

Or `kubectl -n {{ .Namespace }} edit svc cart` and set `targetPort: 80`,
or better `targetPort: http`, the name of the container port, so the
number lives in one place. The selector needs no change.
{{- else -}}
Change one thing: `spec.selector` of Service `cart`, so that every key and
value in it is a label the cart pods carry.

```sh
kubectl -n {{ .Namespace }} set selector svc cart app=cart
```

`set selector` replaces the whole map. The same result comes from
`kubectl -n {{ .Namespace }} edit svc cart`, or from correcting the
selector in `cart/service.yaml` on the release branch and applying it,
which is the route that survives the next deploy. Relabeling the pods
through the Deployment template works for an extra selector key but rolls
the pods for a bug that lives in the Service.
{{- end }}

## Verify

```sh
kubectl -n {{ .Namespace }} get endpointslices -l kubernetes.io/service-name=cart
kubectl -n {{ .Namespace }} logs deploy/frontend --tail=3
kubectl -n {{ .Namespace }} run curl --rm -it --restart=Never --image=curlimages/curl:8.14.1 -- curl -sS -m 5 http://cart.{{ .Namespace }}.svc/
```

The EndpointSlice now reads `cart-xxxxx IPv4 80 10.244.2.132,10.244.1.172`:
both addresses on port 80. Within 5 seconds the frontend log switches from
`-> failed, checkout shows a timeout` to `GET http://cart/ -> ok`, and the
curl pod prints the page with `<h1>Welcome to nginx!</h1>`. Before the fix
the same curl printed `curl: (7) Failed to connect to
cart.{{ .Namespace }}.svc port 80 after 0 ms: Could not connect to server`
(measured on kind v1.37).

## In the real world

- The API server accepts any Service selector and any targetPort; nothing
  checks that a pod matches or listens. A Service with zero endpoints is
  silent until a client calls it. A post-deploy smoke test that calls the
  Service name, not the pod, turns this into a failed deploy.
- Alert on it: kube-state-metrics exposes
  `kube_endpointslice_endpoints`; a Service whose count stays at 0 for
  2 minutes is an alarm with the Service name in it.
- Keep selectors on stable labels (`app`, `app.kubernetes.io/name`,
  `app.kubernetes.io/instance`) and keep `tier`, `version` and release
  labels out of them; a label rename on the pod template then needs no
  Service change. When a selector label must change, the Service and the
  Deployment change in the same pull request.
- Name the container port (`ports: [{name: http, containerPort: 80}]`) and
  write `targetPort: http` in the Service: the number lives in the
  Deployment, and a Service manifest copied between apps cannot carry a
  stale number.

## Go deeper

- Debugging a Service, selector and port checks in order:
  https://kubernetes.io/docs/tasks/debug/debug-application/debug-service/
- Service `spec.selector`, `port` and `targetPort`:
  https://kubernetes.io/docs/concepts/services-networking/service/#defining-a-service
- EndpointSlices, the `kubernetes.io/service-name` label and `conditions`:
  https://kubernetes.io/docs/concepts/services-networking/endpoint-slices/
