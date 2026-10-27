# Solution - 002 One of the shop pods keeps restarting

## Symptom

The restart alert for namespace `{{ .Namespace }}` fires: one pod restarted
more than 5 times in the last hour. The namespace runs 40 pods across 14
Deployments and nothing is down for customers, because the restarting pod
belongs to `reviews`, a service with one replica that the storefront calls
without blocking. The alert did not catch the cause because it counts
restarts; the kubelet restarts a container for a crash, a failed liveness
probe and an OOM kill alike, and only the pod's status and logs tell them
apart.

## Candidate causes

A container that is restarted again and again has one of these behind it,
each with its own trace:

- The process exits by itself: `Last State: Terminated, Reason: Error`
  with a non-zero `Exit Code`, and the previous log explains why (a file it
  could not read, a bad argument, a dependency it gave up on).
- The kubelet kills a working process because the liveness probe fails:
  Events repeat `Liveness probe failed` and then `Killing ... failed
  liveness probe, will be restarted`; the process itself wrote nothing
  about an error, and `Exit Code` is 137 (SIGKILL after the 1s grace).
- The kernel kills the process for exceeding its memory limit: `Reason:
  OOMKilled`, `Exit Code: 137`, the log stops in the middle of the work
  and `Limits` shows a number below what the process needs.
- The process exits with code 0 (a command that daemonizes or finishes):
  `Reason: Completed`, then a restart because `restartPolicy` is `Always`.
- The node evicts the pod: no restart count, a new pod name instead, and
  `Evicted` in the status.

## Observation that splits them

```sh
kubectl -n {{ .Namespace }} get pods --sort-by='.status.containerStatuses[0].restartCount'
kubectl -n {{ .Namespace }} describe pod <pod-name>
kubectl -n {{ .Namespace }} logs <pod-name> --previous
kubectl -n {{ .Namespace }} logs deployment/reviews --previous
```

`--sort-by` prints the pod with the most restarts last; the other 39 show
`0`. The pod is one of `reviews`. `describe pod` names the reason in `Last
State` under `Containers` and in `Events`; `logs --previous` prints the
output of the container that died, and `deployment/reviews` in place of
the pod name resolves to the pod of that Deployment (measured on kind
v1.37, the Event lines come from the kubelet and the exit codes from the
container runtime):

- config-mount-path: `Last State: Terminated, Reason: Error, Exit Code:
  1`, and the previous log ends with `reviews: /etc/reviews/reviews.conf
  not found after 10s, giving up`. The `Mounts` block of the same output
  lists `/etc/review from config (ro)`: the ConfigMap is mounted one
  directory away from where the entrypoint reads it.
- liveness-wrong-port: `Last State: Terminated, Reason: Error, Exit Code:
  137`, the previous log ends with `reviews: cache ready (8388608 bytes),
  serving on :8080`, and Events repeat `Liveness probe failed: Get
  "http://<pod-ip>:8081/": dial tcp <pod-ip>:8081: connect: connection
  refused` followed by `Container web failed liveness probe, will be
  restarted`. The process serves on 8080 and the probe asks 8081; the
  readiness probe on 8080 passes, which is why the pod shows `1/1` between
  kills.
- memory-limit-too-low: `Last State: Terminated, Reason: OOMKilled, Exit
  Code: 137`, the previous log stops after `reviews: warming the review
  cache (8 MiB)`, and `Limits` shows `memory: 10Mi`. The shell that
  builds the 8 MiB cache holds a second copy while it reads the pipe, so
  the container needs about 20 MiB at its peak; every other service in the
  namespace runs with `32Mi`.

```sh
kubectl -n {{ .Namespace }} get deployment reviews -o yaml
```

Confirms the wrong field lives in the pod template of Deployment
`reviews`; a pod is a copy of that template, so deleting the pod would
only produce another copy with the same fault.

## Change

Change one field of container `web` in the pod template of Deployment
`reviews`, the one the observation named (`volumeMounts[1]` is the
`config` volume):

```sh
kubectl -n {{ .Namespace }} patch deployment reviews --type json -p '[{"op": "replace", "path": "/spec/template/spec/containers/0/volumeMounts/1/mountPath", "value": "/etc/reviews"}]'
kubectl -n {{ .Namespace }} patch deployment reviews --type json -p '[{"op": "replace", "path": "/spec/template/spec/containers/0/livenessProbe/httpGet/port", "value": 8080}]'
kubectl -n {{ .Namespace }} patch deployment reviews --type json -p '[{"op": "replace", "path": "/spec/template/spec/containers/0/resources/limits/memory", "value": "32Mi"}]'
```

Run only the line that matches your observation. `kubectl edit deployment
reviews` and changing the same field in the editor is the same change, and
so is correcting the manifest in the repository and applying it. Then put
the label on the pod the rollout created:

```sh
kubectl -n {{ .Namespace }} rollout status deployment/reviews --timeout=120s
kubectl -n {{ .Namespace }} label pod -l app=reviews triage=yes
```

Labeling before the fix is a natural order, and it is also why the first
check can turn red again: the rollout replaces the pod, and the new pod
starts with the labels of the template, without `triage=yes`. The
selector `-l app=reviews` labels whichever reviews pod exists now, so you
never type the random pod name.

## Verify

```sh
kubectl -n {{ .Namespace }} get pods -l app=reviews
kubectl -n {{ .Namespace }} get pods -l triage=yes
kubectl -n {{ .Namespace }} get deployments
```

The reviews pod shows `1/1 Running` with `0` restarts, and stays at `0`
after a minute (the liveness kill took 35 seconds to show, so wait at
least that long). `get pods -l triage=yes` lists exactly that pod. `get
deployments` shows `READY` equal to the replica count on all 14 rows,
40 pods in total.

## In the real world

- A restart alert that counts restarts without naming the pod sends the
  on-call to `kubectl get pods --sort-by`; a better alert carries the pod
  name and the `Last State` reason from `kube_pod_container_status_last_terminated_reason`.
- A liveness probe on the wrong port is a copy-paste error that passes
  review because the readiness probe is right; `kubectl rollout status`
  in the pipeline does not catch it, since the pod becomes Ready before
  the first kill. Set the probe `port` to the named container port
  (`port: http`) so both probes change together.
- A memory limit below the real peak passes in staging when the cache is
  empty; set limits from a measured `memory.peak` (or `kubectl top pod`
  over a day), not from the request.
- A wrong mount path reaches production when the entrypoint reads a
  hard-coded path and the manifest another; an init container or a
  startup check that prints the path it reads makes the log name the fix.

## Go deeper

- Container states, restart backoff and `restartPolicy`:
  https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/
- Liveness and readiness probes, with their fields and defaults:
  https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/
- Memory limits and what the kernel does when a container exceeds one:
  https://kubernetes.io/docs/tasks/configure-pod-container/assign-memory-resource/
- Labels, selectors and `kubectl label`:
  https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/
