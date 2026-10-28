# Ops wants a one-liner: list every image running in shop into a ConfigMap inventory

## Ticket

> **From:** Marco (ops)\
> **Priority:** normal
>
> Security is auditing which container images run in each namespace this
> week. For `shop` I need a ConfigMap named `inventory` with one key,
> `images`, listing every image that is running in the namespace right now,
> one per line, sorted, no duplicates. Copy each image string exactly as the
> pod spec writes it, because the audit job compares strings. "Running" means
> containers that are up right now: an init container that already finished
> does not count, and a Deployment scaled to zero runs nothing. Please do not
> change any Deployment, this is a read-and-record task. If you can leave me
> the one-liner you used, I will run it for the other namespaces.

## Before you start

A pod's `spec.containers` lists the containers that run for the life of the
pod; `spec.initContainers` run once before them and exit. `kubectl get pods
-o jsonpath=...` reads any field of every pod in one call, so the image
list is a read from the pods, not from the Deployments: a Deployment holds
a template, the pods hold what runs. `kubectl create configmap` stores a
value under a key from `--from-literal=key=value` or `--from-file=key=path`.

## Environment

- Namespace: `{{ .Namespace }}`
- Resources: Deployments `storefront`, `catalog`, `cache`, `worker` and
  `checkout`, each with its pods; no ConfigMap yet
- You have cluster-admin on the local kind cluster
- The workstation shell has `kubectl`, `sort`, `tr` and `yq`

## Done when

- configmap inventory exists
- configmap inventory has a key named images with a value
- key images lists every image a running container in the namespace uses, one per line, as the pod spec writes it
- key images lists no other image; an init container that already exited and a Deployment at 0 replicas are not running

## Constraints

- Do not change, scale or delete any Deployment
- Keep the ConfigMap name `inventory` and the key name `images`
- Do not delete the namespace
