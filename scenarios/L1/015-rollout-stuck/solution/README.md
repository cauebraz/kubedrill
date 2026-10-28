# Solution - 015 Rollout of checkout stuck at 3/4 for an hour

## Symptom

Release 2.5.0 of Deployment `checkout` started and never finished.
`kubectl rollout status` ends with `deployment "checkout" exceeded its
progress deadline`, `get deployment` shows fewer than 4 up-to-date
replicas, and the Deployment's `Progressing` condition is `False` with
reason `ProgressDeadlineExceeded`. Checkout still answers, because the
rolling update keeps the old ReplicaSet's pods serving until new ones are
available, so no customer-facing alert fired. The capacity is one pod short (3 of 4
ready with `maxUnavailable: 25%`) and the rollout cannot move.

## Candidate causes

A new ReplicaSet whose pods never become available fits several causes,
each with its own trace on the new pods:

- Failing readiness probe: STATUS `Running`, READY `0/1`, Events repeat
  `Readiness probe failed` with the HTTP status or connection error.
- Unschedulable request: STATUS `Pending`, Events show `FailedScheduling`
  with `Insufficient cpu` or `Insufficient memory`.
- Image pull failure: STATUS `ErrImagePull` or `ImagePullBackOff`, Events
  show `Failed to pull image` with the registry's answer.
- Crash loop: STATUS `CrashLoopBackOff` and a growing restart count;
  `kubectl logs --previous` shows the exit.
- A quota or admission refusal: the ReplicaSet has fewer pods than it wants,
  and `describe replicaset` shows `FailedCreate`.
- Missing configuration: STATUS `CreateContainerConfigError`, Events show
  `couldn't find key` or `configmap ... not found`.

All of them sit in what revision 2 changed, because revision 1 runs fine
on the same nodes right now.

## Observation that splits them

```sh
kubectl -n {{ .Namespace }} rollout status deployment/checkout --timeout=10s
kubectl -n {{ .Namespace }} rollout history deployment/checkout
kubectl -n {{ .Namespace }} get replicasets
kubectl -n {{ .Namespace }} get pods -l app.kubernetes.io/name=checkout
kubectl -n {{ .Namespace }} describe pod <pod-name>
```

`rollout history` lists revision 1 (`release 2.4.0`) and revision 2
(`release 2.5.0`). `get replicasets` shows two: the old one with all its
pods ready, the new one with READY 0. Take `<pod-name>` from a pod whose
name carries the new ReplicaSet's hash. Its Events differ per variant
(measured on kind v1.37 with containerd):

- probe-404: STATUS `Running`, READY `0/1`, and `Readiness probe failed:
  HTTP probe failed with statuscode: 404`. The container runs; the probe
  asks for a path nginx does not serve.
- cpu-64: STATUS `Pending`, and `FailedScheduling ... 0/3 nodes are
  available: 1 node(s) had untolerated taint(s), 2 Insufficient cpu`. The
  request is larger than any node.
- typo-tag: STATUS `ErrImagePull` or `ImagePullBackOff`, and `Failed to
  pull image "nginx:1.27-alpnie": ... docker.io/library/nginx:1.27-alpnie:
  not found`. The tag does not exist.

```sh
kubectl -n {{ .Namespace }} rollout history deployment/checkout --revision=1
kubectl -n {{ .Namespace }} rollout history deployment/checkout --revision=2
kubectl -n {{ .Namespace }} get deployment checkout -o jsonpath='{.status.conditions[?(@.type=="Progressing")].reason}'
```

The two revision templates differ in the `app.kubernetes.io/version` label,
in the ConfigMap key `PAYMENTS_URL` comes from (`payments_url` in revision 1,
`payments_endpoint` in revision 2), and in the one field the Events point
at: the readiness probe path, the cpu request or the image tag. The last command prints
`ProgressDeadlineExceeded`: the controller gave up waiting after
`progressDeadlineSeconds` (30 here) without progress. It does not roll back
by itself; it only reports.

## Change

Before a rollback, check that revision 1 can still start a pod in today's
cluster: it reads key `payments_url` from ConfigMap `checkout-config`.

```sh
kubectl -n {{ .Namespace }} get configmap checkout-config -o jsonpath='{.data}'
```

- probe-404: the ConfigMap holds both `payments_url` and
  `payments_endpoint`. Mitigate first: put the Deployment back on revision
  1, the template that ran all week.

  ```sh
  kubectl -n {{ .Namespace }} rollout undo deployment/checkout --to-revision=1
  ```

  `rollout undo` copies the template of revision 1 into the Deployment,
  which becomes revision 3; the old ReplicaSet scales back to 4 and the
  broken one to 0. Then fix 2.5.0 forward in the manifest the team deploys
  from.
- cpu-64 and typo-tag: the 2.5.0 deploy removed `payments_url`. An undo
  scales the old ReplicaSet back to 4, its fourth pod stays in
  `CreateContainerConfigError` with `couldn't find key payments_url in
  ConfigMap`, and the rollout is stuck on revision 3 instead. Fix forward
  from the 2.5.0 template instead of rolling back.

Fix forward by changing only the field revision 2 broke, while the
Deployment still runs the 2.5.0 template:

```sh
kubectl -n {{ .Namespace }} patch deployment checkout --type=json -p '[{"op":"replace","path":"/spec/template/spec/containers/0/readinessProbe/httpGet/path","value":"/"}]'
kubectl -n {{ .Namespace }} set resources deployment/checkout -c web --requests=cpu=10m
kubectl -n {{ .Namespace }} set image deployment/checkout web=nginx:1.27-alpine@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10
```

Run the line that matches the Event you read: the probe path for a 404, the
cpu request for `Insufficient cpu`, the image for `Failed to pull image`.

If a rollback already stalled on revision 3, these lines change nothing
useful: the template is revision 1's, which has the right probe, cpu and
image. Two ways out of the stalled rollback:

```sh
kubectl -n {{ .Namespace }} patch configmap checkout-config --type=merge -p '{"data":{"payments_url":"http://payments.{{ .Namespace }}.svc.cluster.local"}}'
```

puts the key revision 1 reads back with the URL `payments_endpoint` holds;
the waiting pod starts on its next retry, with no new revision. Or
`kubectl -n {{ .Namespace }} rollout undo deployment/checkout` without a
target returns to the previous revision, now 2.5.0 (revision 4), and then
the fix-forward line applies. After the key is restored, `rollout status`
can still print the progress-deadline error once, because the deadline
passed while the pod waited; the `Progressing` reason turns to
`NewReplicaSetAvailable` as soon as the fourth pod is available. Every
route passes the checks, which read the ready and up-to-date replica
counts, the `Progressing` reason, the Service's ready endpoints and the pod
phases.

## Verify

```sh
kubectl -n {{ .Namespace }} rollout status deployment/checkout
kubectl -n {{ .Namespace }} get deployment checkout -o jsonpath='{.status.conditions[?(@.type=="Progressing")].reason}'
kubectl -n {{ .Namespace }} get endpointslices -l kubernetes.io/service-name=checkout
kubectl -n {{ .Namespace }} rollout history deployment/checkout
```

`rollout status` ends with `deployment "checkout" successfully rolled out`,
the condition reason is `NewReplicaSetAvailable`, and the EndpointSlice lists
4 addresses. `rollout history` shows the revision your change created: after
an undo, revision 1 is gone from the list and reappears as revision 3 with
`release 2.4.0`.

## In the real world

- A pipeline that runs `kubectl rollout status --timeout` catches a stuck
  rollout, but only a human or a controller acts on it. Argo Rollouts or
  Flagger can roll back automatically on a failed analysis; a plain
  Deployment never does.
- `progressDeadlineSeconds` defaults to 600. Keep it a little above the
  slowest healthy rollout, so a stuck one fails the pipeline fast.
- `maxUnavailable: 0` with `maxSurge: 1` keeps full capacity during a bad
  rollout, at the cost of one extra pod of headroom on the nodes. With
  `maxUnavailable: 25%` a bad rollout costs a quarter of the capacity until
  someone acts.
- A rollback restores the pod template only. ConfigMaps, Secrets, CRDs and
  database schemas the deploy changed stay changed; rename a configuration
  key by adding the new one and removing the old one a release later, so
  the previous release can still start.
- An undo in the cluster leaves the repository on the broken version; the
  next deploy reapplies it. Revert or fix the manifest right after the
  mitigation, and record `kubernetes.io/change-cause` so the history says
  which release each revision is.

## Go deeper

- Deployments, rolling update, progress deadline and rollback:
  https://kubernetes.io/docs/concepts/workloads/controllers/deployment/
- kubectl rollout:
  https://kubernetes.io/docs/reference/kubectl/generated/kubectl_rollout/
- Readiness probes:
  https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/
