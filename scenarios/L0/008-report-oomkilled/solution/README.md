# Solution - 008 report pod is OOMKilled every run; dev insists it uses 50 MB

## Symptom

Deployment `report` shows `0/1` ready and `0` available, the pod alternates
between `OOMKilled` and `CrashLoopBackOff`, and its RESTARTS count grows
while you watch. The laptop measurement (about 50 MB) did not catch it
because nothing on the laptop limited the process; in the cluster every
container runs under a memory limit the kernel enforces, and the number
that matters is the limit of the container being killed, not the average
use of the program.

## Candidate causes

A container restarting from the first seconds of its life fits several
causes. Each one leaves a different trace:

- Memory limit below the container's use: `Last State: Terminated, Reason:
  OOMKilled, Exit Code: 137` on that container, `Limits` smaller than what
  `Args` and `kubectl top` say it needs.
- Node short of memory: the pod is `Evicted`, not `OOMKilled`; `kubectl
  describe node` shows `MemoryPressure True`.
- Application crash: `Reason: Error` with an exit code other than 137 and a
  stack trace in `kubectl logs --previous`.
- Failing liveness probe: Events repeat `Liveness probe failed` and the
  kubelet kills the container with `Killing`; the exit code is 137 too, but
  the Reason is `Error` and the probe line is in the Events.
- A different container in the same pod being killed: READY shows `1/2`,
  the `BackOff` event names a container other than the one the team wrote,
  and only that block in `describe pod` shows `OOMKilled`.
- A limit nobody wrote: the pod shows `Limits` while the Deployment's
  template has no `resources`; a LimitRange default in the namespace
  filled it in at admission. When the LimitRange also sets a `max`, a
  higher limit in the Deployment is refused at admission and the
  ReplicaSet records `FailedCreate`.

## Observation that splits them

```sh
kubectl -n {{ .Namespace }} get pods
kubectl -n {{ .Namespace }} describe pod <pod-name>
kubectl -n {{ .Namespace }} get events --sort-by=.lastTimestamp
```

Take `<pod-name>` from the NAME column of `get pods`. `describe pod` has
one block per container under `Containers:`; the block that reads `Last
State: Terminated`, `Reason: OOMKilled`, `Exit Code: 137` names the
container the kernel kills, and its `Limits:` line names the ceiling. That
rules out eviction (no `Evicted`), a crash (Reason would be `Error`) and a
probe (no `Liveness probe failed` event) at once. The `Warning BackOff`
event repeats `Back-off restarting failed container <name>` with the same
container name. What differs per variant (measured on kind v1.37):

- low-limit: the `report` block shows `Limits: memory: 32Mi`, `Requests:
  memory: 32Mi`, `Restart Count` growing; `Args` include
  `--mem-total=48Mi`. The pod is `0/1`.
- limitrange-default: the `report` block shows the same `Limits: memory:
  32Mi`, but the Deployment never wrote it (next command), and `kubectl -n
  {{ .Namespace }} describe limitrange namespace-defaults` prints a
  Container row with `Max 32Mi`, `Default Request 32Mi` and `Default Limit
  32Mi`. The pod is `0/1`.
- sidecar-limit: the pod is `1/2`. The `report` block is `Running`,
  `Restart Count: 0`, `Limits: memory: 128Mi`; the `log-shipper` block is
  the one with `OOMKilled`, `Limits: memory: 16Mi`, and the `BackOff`
  event names `log-shipper`.

```sh
kubectl -n {{ .Namespace }} get deployment report -o jsonpath='{.spec.template.spec.containers[*].resources}{"\n"}'
kubectl -n {{ .Namespace }} get limitrange
```

The first command prints the `resources` of each container as the
Deployment wrote them: `{"limits":{"memory":"32Mi"},...}` in low-limit,
`{}` in limitrange-default (the 32Mi came from the LimitRange, which the
second command lists as `namespace-defaults`; in the other variants it
prints `No resources found`), and two objects in sidecar-limit with the
second one holding `16Mi`.

The real need: the `report` container's `Args` say `--mem-total=48Mi`, and
the Go runtime adds about 10Mi, so `kubectl -n {{ .Namespace }} top pod
--containers` prints about `58Mi` for it once it stays up (the sidecar
holds about `26Mi`). 32Mi and 16Mi are below those numbers, so the kernel
kills the container on every start, a few hundred milliseconds in.

## Change

Change one thing: the memory limit of the container that was killed, with
a request that matches its need.

```sh
kubectl -n {{ .Namespace }} set resources deployment/report -c report --requests=memory=64Mi --limits=memory=128Mi
```

In sidecar-limit the killed container is `log-shipper`, so the same command
takes `-c log-shipper --requests=memory=32Mi --limits=memory=64Mi` and the
`report` container keeps its numbers. In limitrange-default the command
above is not enough: the LimitRange caps every container at `max` 32Mi, so
the new pod is refused at admission (`kubectl -n {{ .Namespace }} get
events` shows `FailedCreate ... maximum memory usage per Container is 32Mi,
but limit is 128Mi`) and AVAILABLE stays `0`. The LimitRange has to change:
raise `max` and the defaults, then restart the rollout so a new pod is
admitted with them (a LimitRange is applied at admission, so the running
pod keeps 32Mi until it is replaced):

```sh
kubectl -n {{ .Namespace }} patch limitrange namespace-defaults --type=json -p '[{"op":"replace","path":"/spec/limits/0/max/memory","value":"128Mi"},{"op":"replace","path":"/spec/limits/0/default/memory","value":"128Mi"},{"op":"replace","path":"/spec/limits/0/defaultRequest/memory","value":"64Mi"}]'
kubectl -n {{ .Namespace }} rollout restart deployment/report
```

Raising only `max` and then running `set resources` on the Deployment
works too; raising only the defaults does not, because `max` still caps
them.

The checks read the Deployment's available replicas, the `lastState` of
every container in the pod and the `report` container's args, so either
route passes. Shrinking `--mem-total` would also stop the kills, and fails
the last check: the team asked for room for the data, not for less data.

## Verify

```sh
kubectl -n {{ .Namespace }} rollout status deployment/report
kubectl -n {{ .Namespace }} get deployment report
kubectl -n {{ .Namespace }} get pods
kubectl -n {{ .Namespace }} top pod --containers
```

`rollout status` ends with `deployment "report" successfully rolled out`
(the Deployment uses `Recreate`, so the old pod is gone before the new one
starts and `get pods` lists exactly one). `get deployment` shows READY
`1/1` and AVAILABLE `1`, which needs 10 seconds of Ready without a restart
(`minReadySeconds: 10`). `get pods` shows `Running` with RESTARTS `0`, and
still `0` a minute later. `top` shows about `58Mi` for `report` under its
new 128Mi limit; metrics-server needs up to a minute after the start to
report a new pod.

## In the real world

- A service moved from a VM into a cluster meets a memory limit for the
  first time. Measure it under load with `kubectl top pod --containers` or
  the `container_memory_working_set_bytes` metric, then set the request to
  the steady state and the limit with headroom for peaks; a limit equal to
  a guessed average is the classic OOMKilled.
- A namespace LimitRange turns "no resources" into a hidden 32Mi ceiling,
  and the Deployment manifest looks innocent. `kubectl describe pod` shows
  the value in force, `kubectl get limitrange -n <ns>` shows where it came
  from; a ResourceQuota that requires limits makes the LimitRange default
  mandatory in practice.
- Sidecars (log shippers, proxies, agents) are injected by templates or
  admission webhooks with limits the application team never saw. A pod at
  `1/2` READY is the signal to read the other container's block.
- An alert on `kube_pod_container_status_last_terminated_reason{reason="OOMKilled"}`
  or on `container_oom_events_total` catches this in minutes; waiting for a
  user to report the page catches it in days.

## Go deeper

- Requests, limits and how the kernel enforces them:
  https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/
- LimitRange defaults, applied at admission:
  https://kubernetes.io/docs/concepts/policy/limit-range/
- Assigning memory to a container, with an OOMKilled example:
  https://kubernetes.io/docs/tasks/configure-pod-container/assign-memory-resource/
