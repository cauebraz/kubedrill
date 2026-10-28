# HPA on api shows unknown and never scales

## Ticket

> **From:** Sam (api team)\
> **Priority:** medium
>
> Last sprint we added a HorizontalPodAutoscaler to `api` so it adds pods
> when CPU goes above 50% of what each pod requests, up to 4 pods. In
> yesterday's load test the latency climbed for twenty minutes and `api`
> stayed at 1 pod the whole time. `kubectl get hpa` shows `<unknown>/50%`
> under TARGETS. metrics-server is installed and `kubectl top pods` prints
> numbers for our pods, so we do not understand where the unknown comes
> from. We want `api` to scale on CPU, nothing else. Can platform take a look?

## Before you start

An HPA reads the target's pods from `scaleTargetRef`, asks metrics-server
for their usage and divides it by the container requests: CPU
utilization = CPU usage / CPU request. Every link must hold, or the current
value is `<unknown>` and no replica count is computed. `kubectl describe hpa`
prints the Conditions (`AbleToScale`, `ScalingActive`) and the Events with
the controller's own reason.

## Environment

- Namespace: `{{ .Namespace }}`
- Resources: Deployment `api` (1 replica, container `api`, nginx
  `1.27-alpine`), HorizontalPodAutoscaler `api` (`autoscaling/v2`, added last
  sprint)
- metrics-server runs in `kube-system`; `kubectl top` works
- You have cluster-admin on the local kind cluster

## Done when

- deployment api has at least 1 ready replica
- hpa api has condition ScalingActive True
- hpa api reports a current metric value
- hpa api resizes deployment api by the rule the ticket asks for, between 1 and 4 pods
- deployment api finished rolling out: every pod runs the current template

## Constraints

- Keep the Deployment and HorizontalPodAutoscaler names
- Keep the scaling rule the ticket describes and the 1 to 4 pod range
- Do not delete the namespace
