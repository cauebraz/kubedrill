# App reads old config after we updated the ConfigMap

## Ticket

> **From:** Eva (marketing team)\
> **Priority:** medium
>
> The promo banner at `http://banner.{{ .Namespace }}.svc` still says
> `Summer sale 2025`. This morning I changed the key `banner` of ConfigMap
> `{{ .Vars.edited }}` to `Winter sale 2026`, the ConfigMap our runbook names,
> and `kubectl -n {{ .Namespace }} get configmap {{ .Vars.edited }} -o yaml`
> shows the new text. Twenty minutes later the page is unchanged: I reloaded
> it from two browsers and curled the Service from another pod. Both `banner`
> pods are Running and Ready, nothing restarted, nothing in the logs. Did the
> edit not save, or does the page come from somewhere else?

## Before you start

A container gets a ConfigMap value by one of three routes, and each one
reacts to an edit differently: an env var (`valueFrom.configMapKeyRef`) is
read once, when the container starts; a volume that mounts the ConfigMap is
refreshed by the kubelet within one to two minutes; a `subPath` mount of one
key is never refreshed. The API holds what `kubectl get configmap -o yaml`
prints; the container holds what `kubectl exec <pod> -- env` and
`kubectl exec <pod> -- cat <file>` print. Compare the two before you change
anything.

## Environment

- Namespace: `{{ .Namespace }}`
- Resources: Deployment `banner` (2 replicas, container `web`, nginx
  `1.27-alpine`), Service `banner` (port 80), the ConfigMaps that
  `kubectl -n {{ .Namespace }} get configmaps` lists
- The page is the value of the key `banner`, served by nginx as
  `/usr/share/nginx/html/index.html`
- You have cluster-admin on the local kind cluster

## Done when

- deployment banner has 2 ready replicas
- service banner serves the Winter sale 2026 banner
- no running banner pod still serves the Summer sale 2025 banner

## Constraints

- Keep the Deployment, Service and ConfigMap names
- The banner text the team wants is exactly `Winter sale 2026`
- Do not delete the namespace
