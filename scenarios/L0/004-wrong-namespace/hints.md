# Hints

## Hint 1: where to look

`Connection refused` from a name that resolves means the request reached
the Service and found nowhere to go, so the pods are not the first thing to
blame. Start where the Service and the pods meet:
`kubectl -n {{ .Namespace }} describe service catalog` and
`kubectl -n {{ .Namespace }} get pods --show-labels`.

## Hint 2: what to observe

In the `describe service` output read three lines next to each other:
`Selector`, `TargetPort` and `Endpoints`. Then read the LABELS column of
`get pods --show-labels` for the catalog pods, and the port the container
declares: `kubectl -n {{ .Namespace }} get pods -l team=catalog -o
jsonpath='{.items[*].spec.containers[0].ports[*].containerPort}'`. One of
the three Service lines does not match what the pods show, and
`kubectl -n {{ .Namespace }} get deployments` tells you whether the
Deployment itself carries the name the team expects.

## Hint 3: what it means

A Service forwards each connection to a Ready pod whose labels contain every
key and value of its selector, on the targetPort. An empty `Endpoints` line
with Running pods means no pod carries the selector's labels, whether because
the pods were created under another name and label or because the selector
was copied with the old value. Endpoints present and still `Connection refused`
means the pods are reached on a port nothing listens on. The manifests were
edited by hand during the copy, so the mismatch is in whatever was edited:
the Deployment name and its labels, the selector value, or the targetPort.

## Hint 4: what to change

Make this namespace look like every other environment: a Deployment named
`catalog` whose pods carry `app.kubernetes.io/name=catalog`, and Service
`catalog` whose selector names that same label and whose targetPort is the
port nginx listens on. A Deployment's selector cannot be edited after
creation, so a wrongly named Deployment is replaced by a correct one and then
removed, not renamed. Change only the side that differs from the reference
and leave the storefront alone.
