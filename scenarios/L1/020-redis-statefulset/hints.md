# Hints

## Hint 1: where to look

The client already tells you what it needs. Read its node list and its log:
`kubectl -n {{ .Namespace }} get configmap session-api-config -o yaml` and
`kubectl -n {{ .Namespace }} logs deploy/session-api --tail=4`. Then read the
fields you will write: `kubectl explain statefulset.spec.serviceName`,
`kubectl explain statefulset.spec.volumeClaimTemplates` and
`kubectl explain service.spec.clusterIP`.

## Hint 2: what to observe

Split each name in the list into its parts:
`redis-2 . redis . {{ .Namespace }} . svc.cluster.local`. Once you have
created objects, `kubectl -n {{ .Namespace }} get statefulset,pods,pvc,svc`
shows how far they got, `kubectl -n {{ .Namespace }} get endpointslices -l
kubernetes.io/service-name=redis -o wide` lists the pods behind the Service,
and `kubectl -n {{ .Namespace }} get events --sort-by=.lastTimestamp` shows a
pod or a claim that waits.

## Hint 3: what it means

The first part is a pod name, which only a StatefulSet makes stable: pod
`<statefulset>-<ordinal>`. The second part is a Service name: DNS publishes
one record per Ready pod for the Service the StatefulSet names in
`spec.serviceName`, and the ticket wants that Service headless
(`clusterIP: None`) so the name `redis` itself never load-balances. A ready
endpoint list with the three pods means the records exist. Each pod gets its own claim
`<template>-<pod>` from `volumeClaimTemplates`; on `standard` the claim stays
Pending until its pod is scheduled (WaitForFirstConsumer), which is normal.
`podManagementPolicy` decides whether the pods start one after the other
(`OrderedReady`, the default) or together (`Parallel`); it cannot change on
an existing StatefulSet.

## Hint 4: what to change

Write two objects. A Service `redis` with `clusterIP: None`, port 6379 and
the selector `app=redis`. A StatefulSet `redis` with `serviceName: redis`, 3
replicas, pod label `app=redis`, the pinned `redis:7.4-alpine` image, a
readiness probe that runs `redis-cli ping`, and a volumeClaimTemplate `data`
of 100Mi on `standard` mounted at `/data`. Then add what the last line of
the ticket asks for{{ if ne .Vars.config "none" }}: a ConfigMap volume and
the server arguments{{ end }}. Apply them and wait for the rollout.
