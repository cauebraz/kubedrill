# The release that stopped halfway

## What happened

Priya's team runs checkout, the part of the shop that takes orders. It runs
as 4 identical copies so that one slow or failing copy does not stop
orders. An hour ago the team shipped a new release, 2.5.0. Kubernetes does
not swap all 4 copies at once: it replaces them a few at a time, so
customers are never left without a working copy. That gradual replacement
is called a rollout.

This rollout started and never finished. The pipeline waited, gave up with
`exceeded its progress deadline`, and the dashboard still shows the new
release on only part of the copies. Orders still go through, because the
old copies keep working. That is why nobody outside the team noticed, and
also why it is easy to leave it: the old copies are carrying the load until
the next deploy or the evening peak.

## The pieces

Kubernetes keeps the cluster looking like the description (YAML) you gave
it. The objects in this ticket, from the top down:

- Deployment `checkout`: "keep 4 copies of this pod running". Its
  `spec.template` is the blueprint of a copy: image, probe, CPU and memory
  it asks for. Every change to the template is a new revision.
- ReplicaSet: the Deployment makes one per revision. Revision 1 (release
  2.4.0) has one ReplicaSet, revision 2 (release 2.5.0) another. During a
  rollout the Deployment grows the new one and shrinks the old one.
- `maxSurge` and `maxUnavailable`: the speed limits of a rollout. The first
  says how many extra copies may exist above 4, the second how many of the
  4 may be missing. The rollout only moves on when new copies are available.
- Pod: one copy. Its name is the ReplicaSet name plus a random part, so the
  pod names tell you which revision each copy belongs to.
- Readiness probe: a small HTTP request the node sends to a copy; the copy
  counts as Ready only when it answers with success.
- Service `checkout`: one stable address in front of the Ready copies.
- Progress deadline: if the rollout makes no progress for
  `progressDeadlineSeconds`, the Deployment marks its `Progressing` condition
  False with reason `ProgressDeadlineExceeded`. It reports; it does not undo.

```
Deployment checkout (4 replicas)
  |-- revision 1 -> ReplicaSet (2.4.0) -> old pods, Ready -----+
  |                                                            |
  `-- revision 2 -> ReplicaSet (2.5.0) -> new pods, never Ready|
                                                               v
                                   Service checkout -> Ready pods only
                                          |
                                          v
                                       customers
```

The new pods must become Ready before the rollout replaces more old ones.
They never do, so the rollout waits forever with the old pods still serving.

## Following the trail

Every command here only reads. In the workstation the namespace is already
set, so plain `kubectl` (or `k`) works. The full form is shown once.

Start with the rollout itself:

```sh
kubectl -n {{ .Namespace }} rollout status deployment/checkout --timeout=10s
kubectl rollout history deployment/checkout
```

`rollout status` repeats the pipeline's error. `rollout history` lists the
revisions with their change cause: 1 is `release 2.4.0`, 2 is
`release 2.5.0`.

Next, the two ReplicaSets and their pods:

```sh
kubectl get replicasets
kubectl get pods -l app.kubernetes.io/name=checkout
```

One ReplicaSet has all its pods ready, the other has 0 ready. The pods whose
name starts with the new ReplicaSet's name are the ones to read. Their
STATUS column says which step stops them: `Running` with `0/1` in READY (the
probe fails), `Pending` (no node can take the pod) or `ErrImagePull` (the
image cannot be downloaded).

Ask one new pod why. Press TAB to complete its name, or copy it from the
NAME column:

```sh
kubectl describe pod <pod-name>
```

Read the `Warning` lines under Events. `Readiness probe failed: HTTP probe
failed with statuscode: 404` means the probe asks for a page the app does
not have. `FailedScheduling` with `Insufficient cpu` means the pod asks for
more CPU than any node has. `Failed to pull image ... not found` means the
image tag does not exist. Your episode shows one of the three.

Last, compare what revision 2 changed:

```sh
kubectl rollout history deployment/checkout --revision=1
kubectl rollout history deployment/checkout --revision=2
```

Apart from the version label, one field differs, and it is the one the Event
named.

## The fix and why it works

Stop the impact first. Put the Deployment back on the revision that worked:

```sh
kubectl rollout undo deployment/checkout --to-revision=1
```

Why this works: undo copies the blueprint of revision 1 back into the
Deployment. Its ReplicaSet still exists, so the Deployment scales it back to
4 and the broken one to 0. The old pods are already Ready, the rollout
completes in seconds, and `Progressing` turns back to
`NewReplicaSetAvailable`.

The other valid route is to fix forward: change only the field revision 2
broke (the probe path, the CPU request or the image tag) and let a new
rollout run. It keeps release 2.5.0, but it needs you to be sure of the
cause.

Why other moves do not work:

- Deleting the stuck pods: the new ReplicaSet recreates them from the same
  blueprint, and they fail the same way.
- Scaling the Deployment up: more copies of a broken blueprint fail too, and
  the rollout still waits for them.
- Waiting: the Deployment never rolls back by itself. The deadline only
  turns the failure into a status.

In a real team, also fix the manifest in the repository, or the next deploy
brings revision 2's mistake back.

## Check your work

```sh
kubectl rollout status deployment/checkout
kubectl get deployment checkout
kubectl get endpointslices -l kubernetes.io/service-name=checkout
```

`rollout status` ends with `successfully rolled out`. `get deployment` shows
`4/4` READY and 4 UP-TO-DATE. The EndpointSlice lists 4 pod addresses, the
copies the Service sends orders to. The checks below the terminal turn
green, and `kubedrill check` grades once from the shell.

The habit to keep from this ticket: when something breaks right after a
deploy, look at what the deploy changed, and restore the last good revision
before you dig into why.
