# api never becomes Ready; the dev says it starts in 2 seconds

## Ticket

> **From:** Sam (api team)\
> **Priority:** high
>
> We deployed `api` to the new namespace this morning with the health checks
> the platform checklist asks for. The pods come up and stay `Running`, but
> the Deployment shows `0/2` ready and never moves. The frontend logs
> `connection refused` for `http://api.{{ .Namespace }}.svc` on every
> request, so checkout is down. The dev swears the process answers on port
> 80 within 2 seconds of starting; it is the same nginx build (release
> 3.2.0) we run everywhere, and the files come from our ConfigMap. Nobody
> has seen a crash, the restart count is 0. Can platform take a look?

## Before you start

`Running` and `Ready` are two different facts about a pod. `Running` means
the container process started. `Ready` means the kubelet's readiness probe
succeeded, and a Service routes only to Ready pods. A pod can run for hours
and never be Ready; nothing crashes, nothing restarts, and the Service has
no endpoint. `kubectl describe pod` prints every failed probe attempt in its
Events, with the reason the probe got. Read that line before changing
anything.

## Environment

- Namespace: `{{ .Namespace }}`
- Resources: Deployment `api` (2 replicas, container `api`), Service `api`
  (port 80), ConfigMap `api-html` (the release files)
- You have cluster-admin on the local kind cluster
- Every other environment runs this release on the official nginx
  `1.27-alpine` image, listening on port 80 and serving `/` from
  `api-html`

## Done when

- deployment api exists
- deployment api has 2 ready replicas
- deployment api finished rolling out: every pod runs the current template
- deployment api keeps a readiness probe that asks nginx on port 80 (HTTP or TCP)
- service api has at least one ready endpoint
- service api answers a request with the ACME API 3.2.0 body

## Constraints

- Keep the Deployment, Service and ConfigMap names
- Keep a readiness probe on the container that asks nginx itself on port
  80 (an HTTP or TCP probe); removing it, or a probe that only tests a
  file, is not a fix
- Do not delete the namespace
