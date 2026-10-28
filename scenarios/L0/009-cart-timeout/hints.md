# Hints

## Hint 1: where to look

Both cart pods are `Running` and `1/1`, so the pod layer is not where the
request stops; the frontend fails on the name `cart`, which is the Service.
Look between the two: `kubectl -n {{ .Namespace }} get svc cart -o wide`
prints the Service with its SELECTOR column, and
`kubectl -n {{ .Namespace }} get endpointslices -l kubernetes.io/service-name=cart`
prints the addresses and the port that selector resolved to.
`kubectl -n {{ .Namespace }} logs deploy/frontend --tail=5` has the exact
error the frontend gets.

## Hint 2: what to observe

Put three outputs side by side: the SELECTOR column of
`kubectl -n {{ .Namespace }} get svc cart -o wide`, the LABELS column of
`kubectl -n {{ .Namespace }} get pods --show-labels`, and the PORTS and
ENDPOINTS columns of the EndpointSlice. Then compare `TargetPort` in
`kubectl -n {{ .Namespace }} describe svc cart` with the `containerPort` of
a cart pod:
`kubectl -n {{ .Namespace }} get pods -l app=cart -o jsonpath='{.items[0].spec.containers[0].ports}'`.
`kubectl -n {{ .Namespace }} port-forward deploy/cart 8080:80 &` in the
background, then `curl -sS localhost:8080/` and `kill %1`, shows whether a
pod serves the page when the Service is left out.

## Hint 3: what it means

{{ if eq .Variant "wrong-target-port" -}}
The EndpointSlice lists both pod addresses, so the selector matches, but its
PORTS column is not the port nginx listens on. kube-proxy forwards Service
port 80 to `targetPort` on the pod; when nothing listens there the pod's
kernel answers with a reset and the client sees `Connection refused` at
once. The port-forward proves the pod serves the page on 80; only the
mapping in the Service is wrong.
{{- else -}}
The EndpointSlice shows `<unset>` while two Ready pods exist, so the Service
selector matches none of them. A selector is a map and every key in it must
be a label the pod carries with the same value; one key or value that
differs from the pod labels is enough to select nothing.
`kubectl -n {{ .Namespace }} get pods -l <the selector as printed>`
returning `No resources found` is the proof. With no endpoint, kube-proxy
rejects every connection to the Service, which is why the client sees
`Connection refused` instead of a slow timeout.
{{- end }}

## Hint 4: what to change

{{ if eq .Variant "wrong-target-port" -}}
The Service `cart`, field `spec.ports[0].targetPort`: make it the port the
cart container listens on, which is its `containerPort`. The pods need no
change, and the EndpointSlice updates on its own.
{{- else if eq .Variant "stale-tier-selector" -}}
The Service `cart`, field `spec.selector`: every key and value in it must be
a label the cart pods carry, so correct or drop the entry that does not
match. Changing that label in the Deployment's pod template instead also
works but rolls the pods, and a label added to a pod by hand is undone by
the next rollout.
{{- else -}}
The Service `cart`, field `spec.selector`: every key and value in it must be
a label the cart pods carry, so correct the entry that does not match. The
pod template cannot take the Service's value instead: the Deployment
selector is immutable and the API refuses a template whose `app` label no
longer matches it. A label added to a pod by hand is undone by the next
rollout.
{{- end }}
