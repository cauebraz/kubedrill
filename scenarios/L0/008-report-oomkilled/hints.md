# Hints

## Hint 1: where to look

`OOMKilled` and a climbing RESTARTS count are properties of one container
inside the pod, not of the Deployment or the node. Start with the pod:
`kubectl -n {{ .Namespace }} get pods` shows READY as `ready/total`
containers, and `kubectl -n {{ .Namespace }} describe pod <pod-name>` has one
block per container under `Containers:`.

## Hint 2: what to observe

In `kubectl -n {{ .Namespace }} describe pod <pod-name>`, read each container
block: `Args`, `State`, `Last State` with its `Reason` and `Exit Code`,
`Restart Count`, `Limits` and `Requests`. Then compare the `Limits` the pod
shows with what the Deployment wrote:
`kubectl -n {{ .Namespace }} get deployment report -o jsonpath='{.spec.template.spec.containers[*].resources}'`,
and list the namespace defaults with
`kubectl -n {{ .Namespace }} get limitrange`. The `BackOff` line of
`kubectl -n {{ .Namespace }} get events --sort-by=.lastTimestamp` names the
container being restarted.

## Hint 3: what it means

`Last State: Terminated, Reason: OOMKilled, Exit Code: 137` means the
kernel killed that container for growing past its own memory limit; the
node was not short of memory (that would be `Evicted`). Only the container
whose block shows it is the problem, the others are fine. Its `Args` say
how much data it loads, and the process needs its runtime on top of that
(`kubectl -n {{ .Namespace }} top pod --containers` shows the real number
once it stays up), so compare that need with the `Limits` line. A limit
that appears in the pod but not in the Deployment was filled in at
admission by the LimitRange default of the namespace
(`kubectl -n {{ .Namespace }} get limitrange -o yaml`); a `max` in the same
LimitRange refuses any pod whose container asks for more.

## Hint 4: what to change

Raise the memory limit of the container that was killed to more than it
uses, and set its request to match what it needs. The place depends on
where the limit came from: the container's `resources` in the Deployment's
pod template when the Deployment wrote it, or, when a LimitRange default
filled it in, the LimitRange itself: its `max` refuses a higher limit
written in the Deployment, so raise the `max` (and the defaults, or give
the Deployment explicit numbers) and restart the rollout, because a
LimitRange only applies to pods created after the change. Deleting the pod
gives you a new pod with the same limit, and shrinking the dataset is not
what the team asked for.
