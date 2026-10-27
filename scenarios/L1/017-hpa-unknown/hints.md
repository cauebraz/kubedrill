# Hints

## Hint 1: where to look

`<unknown>` is the HPA controller saying it has no current value to compare
with the target. The controller writes why on the HPA itself, not on the
pods: start with `kubectl -n {{ .Namespace }} get hpa api` and its
REFERENCE column.

## Hint 2: what to observe

Read the `Conditions` and the `Events` at the bottom of
`kubectl -n {{ .Namespace }} describe hpa api`: which of `AbleToScale` and
`ScalingActive` is False, and the message next to it. Then print what the
HPA points at and what the container requests:
`kubectl -n {{ .Namespace }} get deployment api -o jsonpath='{.spec.template.spec.containers[*].resources}'`.

## Hint 3: what it means

The controller needs three links before it can compute a utilization: a
`scaleTargetRef` that names an existing Deployment, usage numbers from
metrics-server, and a request for the metric's resource on every container,
because utilization is usage divided by request. `deployments.apps ... not
found` breaks the first link; `missing request for <resource>` breaks the
third. metrics-server works here (`kubectl top pods` prints numbers), so the
second link holds.

## Hint 4: what to change

Repair the broken link where it lives: the HPA `scaleTargetRef` when it names
another Deployment, the HPA metric when it follows another resource than the
CPU the team asked for, or the CPU request of container `api` in the
Deployment `api` pod template when it is missing. Keep the 50% target and the
1 to 4 range, then watch `get hpa api` until TARGETS shows a percentage.
