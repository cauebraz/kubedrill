# Solution - 017 HPA on api shows unknown and never scales

## Symptom

`kubectl get hpa api` shows `<unknown>/50%` under TARGETS and REPLICAS never
moves from its start, so `api` stays at 1 pod under load. Nothing is red:
the Deployment is `1/1` ready, the HPA object exists, and `kubectl top pods`
prints usage, which is why the team assumed the autoscaler was wired.

## Candidate causes

The HPA controller turns pod usage into a replica count through a chain of
links. Each broken link leaves a different trace on the HPA:

- `scaleTargetRef` names a workload that does not exist: REFERENCE shows a
  name other than `Deployment/api`, `AbleToScale` is False with
  `FailedGetScale` and `deployments.apps "<name>" not found`.
- A container of the target pods has no request for the metric's resource:
  `ScalingActive` is False with `FailedGetResourceMetric` and `missing request
  for <resource> in container <name>`.
- The metric names another resource than the one the team sized for:
  TARGETS reads `memory: <unknown>/50%` instead of `cpu: ...`.
- metrics-server is missing or not ready: `ScalingActive` False with `unable
  to get metrics for resource cpu: unable to fetch metrics from resource
  metrics API`, and `kubectl top pods` fails too.
- A pod started less than a minute ago: `no metrics returned from resource
  metrics API` until metrics-server scrapes it once; it clears on its own.

## Observation that splits them

```sh
kubectl -n {{ .Namespace }} get hpa api
kubectl -n {{ .Namespace }} describe hpa api
kubectl -n {{ .Namespace }} get deployment api -o jsonpath='{.spec.template.spec.containers[*].resources}'
kubectl -n {{ .Namespace }} top pods
```

`top pods` prints CPU and memory for the `api` pod, which rules out
metrics-server. The `Conditions` block of `describe hpa` names the link that
broke, and it differs per variant (measured on kind v1.37 with metrics-server):

- no-cpu-request: `ScalingActive False FailedGetResourceMetric the HPA was
  unable to compute the replica count: failed to get cpu utilization: missing
  request for cpu in container api of Pod api-...`. The resources print
  `{"requests":{"memory":"32Mi"}}`: there is no CPU request to divide by.
- wrong-target: REFERENCE reads `Deployment/api-v2`, REPLICAS `0`, and the
  only condition is `AbleToScale False FailedGetScale the HPA controller was
  unable to get the target's current scale: deployments.apps "api-v2" not
  found`. No `ScalingActive` line appears, because the controller stops
  before it reads any metric.
- memory-metric: TARGETS reads `memory: <unknown>/50%` and `ScalingActive
  False FailedGetResourceMetric ... failed to get memory utilization: missing
  request for memory in container api of Pod api-...`. The resources print
  `{"requests":{"cpu":"50m"}}`: CPU is requested, memory is not, and the team
  asked for CPU scaling.

The first Events after the start can read `no metrics returned from resource
metrics API`; that is the scrape delay of a new pod, and the newer Events
carry the reason that stays.

## Change

Change one thing, the broken link of this episode.
{{ if eq .Variant "no-cpu-request" }}
Give container `api` a CPU request. The Deployment rolls out a new pod, and
utilization becomes usage divided by 50m:

```sh
kubectl -n {{ .Namespace }} set resources deployment/api -c api --requests=cpu=50m
kubectl -n {{ .Namespace }} rollout status deployment/api
```
{{ else if eq .Variant "wrong-target" }}
Point the HPA at the Deployment that exists:

```sh
kubectl -n {{ .Namespace }} patch hpa api --type=merge -p '{"spec":{"scaleTargetRef":{"apiVersion":"apps/v1","kind":"Deployment","name":"api"}}}'
```

Creating a Deployment `api-v2` would also make the condition True, but it
would scale a copy nobody routes traffic to, and `api` would still stay at
one pod.
{{ else }}
Make the HPA follow CPU, the resource the team sized and requested:

```sh
kubectl -n {{ .Namespace }} patch hpa api --type=json -p '[{"op":"replace","path":"/spec/metrics/0/resource/name","value":"cpu"}]'
```

Adding a memory request would also clear the condition, but the HPA would
then add pods on memory growth and ignore the CPU saturation of the load test.
{{ end }}
Or edit the manifests in the repository and apply them, the route that
survives the next deploy (see the `fix-manifest` approach). The checks read
the HPA target, its metric, its `ScalingActive` condition and its current
value, so any route that repairs the link passes.

## Verify

```sh
kubectl -n {{ .Namespace }} get hpa api --watch
kubectl -n {{ .Namespace }} describe hpa api
```

Within about a minute TARGETS changes from `<unknown>/50%` to a percentage
such as `cpu: 0%/50%` (an idle nginx uses under 1m of CPU), and the
Conditions read `ScalingActive True ValidMetricFound the HPA was able to
successfully calculate a replica count from cpu resource utilization
(percentage of request)`. Stop the watch with Ctrl-C. The value appears only
after metrics-server has scraped the pod once, so a fresh pod shows
`<unknown>` for up to a minute even when everything is right.

## In the real world

- A missing request is the common one: charts and templates that leave
  `resources` empty produce pods the HPA cannot measure. A policy (Kyverno,
  Gatekeeper, a LimitRange `defaultRequest`) that requires CPU requests on
  every container closes it, and sidecars count too: the utilization of a pod
  is computed over all its containers, so one sidecar without a request
  breaks the whole HPA.
- A renamed Deployment leaves its HPA behind pointing at the old name; review
  the HPA in the same change as the rename.
- Alert on `kube_horizontalpodautoscaler_status_condition{condition="ScalingActive",status="false"}`
  from kube-state-metrics, so a blind autoscaler pages before the next load
  peak instead of during it.
- Utilization is relative to the request. A request set far below real usage
  makes the HPA scale out at idle; one set far above it never scales. Size
  the request from `kubectl top` under normal load.

## Go deeper

- Horizontal Pod Autoscaling, the algorithm and the conditions:
  https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/
- HPA walkthrough with metrics-server:
  https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale-walkthrough/
- Requests and limits:
  https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/
