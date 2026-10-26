# Labels and selectors

Labels are key/value pairs on `metadata.labels` that selectors match; they are the only link between a Deployment and its pods, and between a Service and its endpoints. Annotations live next to them in `metadata.annotations` and are never selected on. The sentence to hold on call: when a Service has no endpoints and the pods are Ready, print the Service selector and the pod labels side by side.

## The mechanism

A label key is an optional DNS-subdomain prefix, a slash, and a name of at most 63 characters; the value is at most 63 characters and may be empty. `kubernetes.io/` and `k8s.io/` prefixes are reserved. Annotations have the same key syntax, values of any size up to a total of 256 KiB of annotations per object, and no API selects on them; they hold tool data such as `kubectl.kubernetes.io/last-applied-configuration`.

Selectors come in two forms. Equality-based: `app=hello`, `tier!=frontend`, comma-separated requirements ANDed. Set-based: `environment in (production, qa)`, `tier notin (frontend)`, `partition` (key exists), `!partition` (key absent). There is no OR. In manifests a Service uses only the equality form (`spec.selector` is a map); Deployments, ReplicaSets, Jobs and DaemonSets use `spec.selector.matchLabels` plus `matchExpressions` with `operator: In | NotIn | Exists | DoesNotExist`.

```
Deployment hello                    Service hello
  spec.selector.matchLabels:          spec.selector:
    app: hello                          app: hello
    |                                   |
    v matches                           v matches (Ready pods only)
  ReplicaSet hello-7c9f5b6d8           EndpointSlice hello-8kz2n
    template.metadata.labels:            endpoints[].addresses: 10.244.1.7
      app: hello                           conditions.ready: true
      pod-template-hash: 7c9f5b6d8
    |
    v creates
  Pod hello-7c9f5b6d8-xk2lp  labels: app=hello, pod-template-hash=7c9f5b6d8
```

`Deployment.spec.selector` must match `spec.template.metadata.labels` or the API rejects the object, and in `apps/v1` the selector is immutable after creation: a change to the pod template labels that breaks the match is refused, and renaming the selector means a new Deployment. The controller adds `pod-template-hash` to each ReplicaSet and its pods so two revisions of one Deployment never overlap. Two controllers whose selectors overlap in one namespace fight over the same pods.

The Service controller side is looser: any map is accepted, nothing checks that a pod matches, and the EndpointSlice controller silently produces a slice with zero endpoints when nothing does. That gap is the classic outage.

## What you see on call

`kubectl get pods -n <ns> --show-labels` prints every label in a last column:

```
NAME                     READY   STATUS    RESTARTS   AGE   LABELS
hello-7c9f5b6d8-xk2lp    1/1     Running   0          3m    app=hello,pod-template-hash=7c9f5b6d8
```

`kubectl get pods -n <ns> -L app -L tier` adds one column per label key, easier to scan than `--show-labels` with many labels:

```
NAME                     READY   STATUS    RESTARTS   AGE   APP     TIER
hello-7c9f5b6d8-xk2lp    1/1     Running   0          3m    hello
```

`kubectl get svc <svc> -n <ns> -o jsonpath='{.spec.selector}'` prints the Service selector as a map, for example `{"app":"hello-web"}`.

`kubectl get pods -n <ns> -l app=hello-web` runs the same selector against the pods; `No resources found in <ns> namespace.` is the diagnosis when the Service has no endpoints. `kubectl get pods -n <ns> -l 'app in (hello, hello-web)'` and `-l '!app'` are the set forms (quote them for the shell).

`kubectl get endpointslices -n <ns> -l kubernetes.io/service-name=<svc>` confirms the result from the Service side; an `ENDPOINTS` column of `<unset>` with Ready pods points at the selector.

`kubectl get deploy <name> -n <ns> -o jsonpath='{.spec.selector.matchLabels}{"\n"}{.spec.template.metadata.labels}'` shows the two maps the API requires to match.

## Common failures

- Service has no endpoints, pods Ready -> `spec.selector` names a label the pods do not carry (`app: hello-web` vs `app: hello`) -> `kubectl get pods -l <selector> -n <ns>` returns nothing.
- Service has no endpoints, pods Ready, selector matches by eye -> a value differs by case or a trailing character, or the selector carries an extra key -> `kubectl get svc <svc> -o jsonpath='{.spec.selector}'` next to `kubectl get pods --show-labels`.
- `kubectl apply` of a Deployment fails with `field is immutable` on `spec.selector` -> the selector was edited on an existing Deployment -> `kubectl get deploy <name> -o jsonpath='{.spec.selector}'` and revert, or create a new Deployment.
- Deployment creation fails with `selector does not match template labels` -> `matchLabels` and `template.metadata.labels` differ -> compare the two maps with the jsonpath above.
- Service routes to pods of another app -> two workloads share `app=web` in one namespace -> `kubectl get pods -l app=web -L app.kubernetes.io/instance`.
- Pod count keeps changing, two ReplicaSets scale against each other -> two Deployments with overlapping selectors -> `kubectl get deploy -n <ns> -o custom-columns=NAME:.metadata.name,SELECTOR:.spec.selector.matchLabels`.
- A manifest with `app.kubernetes.io/version` in the Deployment selector cannot be upgraded -> a label that changes per release was put in the immutable selector -> `kubectl get deploy <name> -o jsonpath='{.spec.selector.matchLabels}'`.

Recommended labels are `app.kubernetes.io/name`, `instance`, `version`, `component`, `part-of` and `managed-by`. Select on `name` and `instance`, which are stable; keep `version` out of selectors because it changes on every release.

## Go deeper

- https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/ : key and value syntax, equality and set-based selector grammar, the overlap note.
- https://kubernetes.io/docs/concepts/overview/working-with-objects/common-labels/ : the `app.kubernetes.io/*` table with examples.
- https://kubernetes.io/docs/concepts/workloads/controllers/deployment/#selector : the selector must match the template and is immutable in `apps/v1`.
- https://kubernetes.io/docs/concepts/services-networking/service/#defining-a-service : how `spec.selector` picks endpoints and what a Service without a selector does.
