# Resources and QoS

Resource sizing is the leading technical cause in the public Kubernetes failure stories: k8s.af tags 10 of 59 stories with OOM and 8 with CPU throttling, while Datadog reports that more than 65% of workloads use less than half of the CPU and memory they request. Requests decide where a pod is scheduled; limits decide when the kernel throttles or kills it. The sentence to hold on call: `OOMKilled` with exit code 137 is the container's own memory limit, `Evicted` is the node running short, and the fix for each is different.

## The mechanism

`resources.requests` is what the scheduler adds up: a pod fits a node when the sum of requests on that node plus its own stays under `status.allocatable`. Actual usage plays no part in scheduling. `resources.limits` is enforced by the kernel through cgroups on the node.

```
pod spec            scheduler             kubelet + kernel (cgroups)
requests.cpu  ----> sum(requests) <=      cpu limit  -> CFS quota per
requests.memory     node allocatable      100ms period: throttled
limits.cpu    -------------------------> memory limit -> OOM kill,
limits.memory                              exit 137, OOMKilled
                    node short of memory -> kubelet eviction, Evicted
```

A CPU limit becomes a CFS quota (`cpuCFSQuota` defaults to true, `cpuCFSQuotaPeriod` to 100ms). A container with `limits.cpu: 500m` may run 50ms per 100ms period; a request burst that needs 80ms of CPU waits for the next period. That is why a service with 20% average CPU still shows throttling and latency spikes: the average hides the bursts. A memory limit is enforced by the kernel OOM killer; the container is restarted with `Last State: Terminated, Reason: OOMKilled, Exit Code: 137`.

Node-pressure eviction is separate. The kubelet compares signals against hard thresholds (Linux defaults `memory.available<100Mi`, `nodefs.available<10%`, `imagefs.available<15%`, `nodefs.inodesFree<5%`), sets `MemoryPressure` or `DiskPressure`, and evicts first the pods whose usage exceeds their requests, ordered by priority.

The QoS class in `status.qosClass` follows from the spec: `Guaranteed` when every container has CPU and memory requests equal to limits, `BestEffort` when no container sets any request or limit, `Burstable` otherwise. A LimitRange fills `default` (limit) and `defaultRequest` at admission; a ResourceQuota on `requests.cpu` or `limits.memory` rejects pods that set neither, with `403 Forbidden`.

## What you see on call

`kubectl describe pod <pod> -n <ns>` shows the limits and the last termination:

```
    Limits:
      memory:       64Mi
    Last State:     Terminated
      Reason:       OOMKilled
      Exit Code:    137
    Restart Count:  5
```

`kubectl get pod <pod> -n <ns> -o jsonpath='{.status.qosClass}'` prints the class: `Burstable`.

`kubectl top pod -n <ns> --containers` (needs metrics-server) shows usage to compare with requests:

```
POD                     NAME     CPU(cores)   MEMORY(bytes)
report-6d9c7b8f5-2xkqp  report   498m         61Mi
```

`kubectl describe node <node>` ends with `Allocated resources`, the sum of requests and limits against allocatable:

```
Allocated resources:
  (Total limits may be over 100 percent, i.e., overcommitted.)
  Resource           Requests      Limits
  --------           --------      ------
  cpu                1850m (92%)   3 (150%)
  memory             1200Mi (31%)  2Gi (53%)
```

`kubectl get events -n <ns> --field-selector reason=Evicted` lists node-pressure evictions with the signal in the message, `The node was low on resource: memory.` followed by `Container <name> was using <usage>, request is <request>`.

## Common failures

- `OOMKilled`, exit 137, restarts every few minutes -> usage above `limits.memory` -> `kubectl describe pod <pod>` Last State and Limits.
- Pod `Pending`, `FailedScheduling ... Insufficient cpu` while nodes are idle -> requests, not usage, fill the node -> `kubectl describe node <node>` Allocated resources.
- p99 latency spikes at 20% average CPU -> CFS throttling under `limits.cpu` -> `kubectl exec <pod> -- cat /sys/fs/cgroup/cpu.stat` (cgroup v2) shows `nr_throttled` and `throttled_usec` growing between two reads.
- Pods `Evicted` across a node, no OOMKilled -> kubelet hard threshold reached -> `kubectl describe node <node>` Conditions shows `MemoryPressure True` or `DiskPressure True`.
- `forbidden: failed quota: <quota>: must specify limits.memory for: <container>` -> ResourceQuota without a LimitRange default -> `kubectl describe resourcequota -n <ns>`.
- New pods get limits nobody wrote -> a LimitRange `default` applied at admission -> `kubectl get limitrange -n <ns> -o yaml`.

## Go deeper

- https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/ : requests, limits, how the kernel enforces them, and the Allocated resources output.
- https://kubernetes.io/docs/concepts/workloads/pods/pod-qos/ : the exact rules for the three QoS classes.
- https://kubernetes.io/docs/concepts/scheduling-eviction/node-pressure-eviction/ : signals, default thresholds and the eviction order.
- https://k8s.af/ : the public failure stories tagged OOM and CPU throttling.
