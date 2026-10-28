# report pod is OOMKilled every run; dev insists it uses 50 MB

## Ticket

> **From:** Lucia (billing team)\
> **Priority:** medium
>
> Our `report` service moved to the shared cluster yesterday and has not
> stayed up since. `kubectl get pods` shows `OOMKilled` or
> `CrashLoopBackOff`, the RESTARTS column keeps climbing, and the month-end
> report page never loads. The service reads the month's dataset into
> memory when it starts and then serves it. Our developer measured it at
> about 50 MB on a laptop and says there is no leak; the code and the image
> are the ones that ran for months on the old VM. Can platform find out why
> the cluster keeps killing it and make it stay up? We are fine with the pod
> using more memory than the laptop number if that is what it takes.

## Before you start

A pod wraps one or more containers, each with its own memory limit,
enforced by the node's kernel: a container that grows past it is killed
with exit code 137, which `kubectl describe pod` records as `Last State:
Terminated, Reason: OOMKilled` on that container. The limit comes from the
Deployment's pod template or from a LimitRange in the namespace, which fills
in defaults and can cap the maximum; `describe pod` shows the one in force.
AVAILABLE in `kubectl get deploy` counts pods that stayed Ready for
`minReadySeconds` (10s here) without a restart.

## Environment

- Namespace: `{{ .Namespace }}`, created yesterday from the platform
  team's namespace template
- Resources: Deployment `report` (1 replica, container `report`{{ if eq .Vars.sidecar "true" }} plus
  container `log-shipper`, which forwards the report log to the central
  store{{ end }})
- The `report` container runs `agnhost stress`, which stands in for the
  service: it loads 48Mi of data on start and holds it for as long as it
  runs
- You have cluster-admin on the local kind cluster; `kubectl top` works

## Done when

- deployment report exists
- deployment report has 1 available replica
- no container of the report pod was last terminated with OOMKilled
- deployment report finished rolling out: every pod runs the current template
- container report still loads its 48Mi dataset

## Constraints

- Keep the Deployment name and the container names
- Do not change the container command or args; the report needs its full
  dataset
- Do not delete the namespace
