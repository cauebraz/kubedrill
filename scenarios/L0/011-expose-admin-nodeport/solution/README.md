# Solution - 011 Expose admin on NodePort 30080 for the QA laptop

## Symptom

`http://<node-ip>:30080/` answers connection refused on every node and
`kubectl -n {{ .Namespace }} get services` lists nothing. The pod of
Deployment `admin` is `1/1` Running and serves the page through a
port-forward, so the workload is fine; the object that publishes a node port
and forwards it to the pod, a Service, was never created.

## Candidate causes

This is a build ticket: nothing is broken, one object is missing. The
candidates are the ways the new Service can still fail to deliver the page,
and each leaves a different trace once the Service exists:

- Selector matches no pod: `kubectl describe service admin` shows
  `Endpoints: <none>`, and a request is refused by kube-proxy at once.
- Selector matches more than one pod: `Endpoints` lists two addresses and
  the EndpointSlice names a pod of another Deployment, so part of the
  requests reach the wrong build.
- Wrong `targetPort`: one endpoint exists, but the request is refused inside
  the pod because nothing listens on that port; the container port in the
  Deployment differs from the Service's `TargetPort`.
- Type `ClusterIP`: `describe` shows `Type: ClusterIP` and no `NodePort`
  line; the page answers on the cluster IP and on nothing else.
- `nodePort` left unset: `Type: NodePort` with a random port in
  30000-32767 on the `NodePort` line, and 30080 stays closed.

## Observation that splits them

Before writing the Service, read the two facts it depends on:

```sh
kubectl -n {{ .Namespace }} get deployments,pods,services
kubectl -n {{ .Namespace }} get pods --show-labels
kubectl -n {{ .Namespace }} get deployment admin -o jsonpath='{.spec.template.spec.containers[0].ports}{"\n"}'
```

The first command confirms one Deployment `admin` with READY `1/1` and no
Service. The other two differ per variant (measured on kind v1.37):

- plain-port-80: one pod, labels
  `app.kubernetes.io/name=admin,app.kubernetes.io/part-of=backoffice,pod-template-hash=...,track=stable`;
  ports `[{"containerPort":80,"name":"http","protocol":"TCP"}]`. Selector
  `app.kubernetes.io/name: admin` alone is enough, `targetPort` 80.
- listens-on-8080: the same labels; ports
  `[{"containerPort":8080,"name":"http","protocol":"TCP"}]`, because the
  `admin-nginx` ConfigMap says `listen 8080;`. `targetPort` must be 8080
  (or the port name `http`); 80 gives one endpoint and connection refused.
- canary-shares-label: two pods, `admin-...` with `track=stable` and
  `admin-canary-...` with `track=canary`, both with
  `app.kubernetes.io/name=admin`; ports show `containerPort` 80. The
  selector needs `track: stable` as well, or the Service gets two endpoints
  and the canary page answers half of the requests.

After the Service exists, these two commands show which of the candidates
above is left:

```sh
kubectl -n {{ .Namespace }} describe service admin
kubectl -n {{ .Namespace }} get endpointslices -l kubernetes.io/service-name=admin -o jsonpath='{range .items[*].endpoints[*]}{.targetRef.name} ready={.conditions.ready}{"\n"}{end}'
```

## Change

Create one Service named `admin`: type NodePort, a selector that matches the
stable admin pod only, port 80, `targetPort` equal to the container port read
above, `nodePort` 30080.

```sh
kubectl -n {{ .Namespace }} apply -f - <<'EOF'
apiVersion: v1
kind: Service
metadata:
  name: admin
spec:
  type: NodePort
  selector:
    app.kubernetes.io/name: admin
    track: stable
  ports:
    - name: http
      port: 80
      targetPort: {{ .Vars.port }}
      nodePort: 30080
EOF
```

A second valid route uses `kubectl expose`, which copies the Deployment's
selector (`app.kubernetes.io/name: admin` and `track: stable`) into the
Service, then pins the node port, which `expose` cannot set:

```sh
kubectl -n {{ .Namespace }} expose deployment admin --type=NodePort --port=80 --target-port={{ .Vars.port }}
kubectl -n {{ .Namespace }} patch service admin --type=json -p '[{"op":"replace","path":"/spec/ports/0/nodePort","value":30080}]'
```

The checks read the Service's type, `nodePort`, the names of its ready
endpoints, the page served through a node and the replica count of every
admin Deployment, so any route that produces that end state passes.
Scaling `admin-canary` to 0 or removing it to keep it out of the selector
fails the last check: the admin team owns it. The Deployments and the
ConfigMaps need no change.

## Verify

```sh
kubectl -n {{ .Namespace }} describe service admin
kubectl -n {{ .Namespace }} get endpointslices -l kubernetes.io/service-name=admin
kubectl -n {{ .Namespace }} run qa-laptop --rm -i --restart=Never --image=curlimages/curl:8.14.1 -- curl -sS -m 5 http://$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}'):30080/
```

`describe` shows `Type: NodePort`, `TargetPort: {{ .Vars.port }}/TCP`,
`NodePort: <unset> 30080/TCP` and one address under `Endpoints`. The
EndpointSlice lists that one address. The curl pod plays the QA laptop: it
asks the first node's InternalIP for port 30080 from the cluster network and
prints the page with `<h1>ACME Admin 3.2.0</h1>`. Before the Service existed
the same command printed `curl: (7) Failed to connect to <node-ip> port
30080 after 0 ms: Could not connect to server`, because nothing listened on
the node port.

## In the real world

- NodePort is the simplest way to reach a pod from outside the cluster and
  the one used in labs, kind and on-prem clusters without a load balancer.
  In production an Ingress or a Gateway in front of a ClusterIP Service
  takes its place, but the selector, port and targetPort rules are the same.
- `kubectl expose` copies the Deployment's selector, so it is safe against
  a sibling workload; a hand-written Service with a one-label selector is
  the classic way to route QA, or customers, to a canary nobody meant to
  expose. Name the Service's endpoints before announcing a URL.
- A fixed `nodePort` is a shared resource: two Services asking for 30080
  conflict, and the API server refuses the second one with `provided port
  is already allocated`. Teams keep a short list of reserved node ports.
- `targetPort` can be the port name (`http`) instead of the number; the
  Service then follows the container when the number changes in a later
  release.

## Go deeper

- Service types, `nodePort` and the 30000-32767 range:
  https://kubernetes.io/docs/concepts/services-networking/service/#type-nodeport
- Defining a Service, selectors and targetPort:
  https://kubernetes.io/docs/concepts/services-networking/service/#defining-a-service
- Debugging Services, from the selector to the endpoints:
  https://kubernetes.io/docs/tasks/debug/debug-application/debug-service/
