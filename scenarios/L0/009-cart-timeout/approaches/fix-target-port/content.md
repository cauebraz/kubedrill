A JSON patch replaces one element of `spec.ports`, the `targetPort`, and
leaves the port name, the Service port and the selector untouched. Read
`describe svc cart` first: it prints `TargetPort` and the `Endpoints` with
the port kube-proxy forwards to, so you see the wrong number before and the
right one after.

Choose it when the EndpointSlice lists the pod addresses and only its PORTS
column is wrong. Writing the port's name instead of the number
(`"value":"http"`, the `name` of the container port in the Deployment)
makes the Service follow the container, so a later port change in the
Deployment needs no Service change; `targetPort` accepts either form.

The trade-off is the same as every imperative fix: the manifest on the
release branch still carries the wrong number until someone changes it.
