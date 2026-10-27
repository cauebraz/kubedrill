# The api that was running but never ready

## What happened

Priya's team runs the api, the service the checkout page calls. This
morning they deployed it to a new namespace with the health checks the
platform checklist asks for. The pods started and stayed `Running`, the
restart count stayed at 0, and still the frontend got `connection refused`
on every call. The Deployment said `0/2` ready and never moved.

"Running" sounded like success. It is not. Kubernetes keeps two separate
facts about a pod: whether its process is running, and whether it is ready
to take traffic. The second one comes from a small test the node repeats
every few seconds, the readiness probe. Until that test passes, the Service
in front of the pods sends them nothing. The developer is right that the
process starts in 2 seconds; the test the node runs can never pass. Your
job is to read what the test asked, see why the answer is always no, and
change the question.

## The pieces

Kubernetes keeps the cluster looking like the YAML you gave it. The
objects in this ticket, from the top down:

- Deployment `api`: the team's wish, "keep 2 copies of this pod running".
  Its `spec.template` is the blueprint every copy is built from, including
  the readiness probe.
- ReplicaSet: created by the Deployment for each version of the blueprint.
  It creates the pods.
- Pod: one running copy, named like `api-84d64974f9-5ndx2`. You list it,
  you never guess it. The pod wraps the container that runs nginx, a web
  server, listening on port 80.
- Readiness probe: a test the node (its kubelet) runs against the container
  every `periodSeconds`. It is an HTTP request to a path and a port, or a
  command run inside the container. Pass means the pod is Ready; fail
  means it is not, and nothing restarts.
- ConfigMap `api-html`: the files nginx serves, mounted into each pod.
- Service `api`: one stable name and address in front of the pods. It only
  sends traffic to Ready pods.

```
Deployment api          "keep 2 pods from this template, probe included"
      |
      v
ReplicaSet              creates the pods
      |
      v
Pod x2                  1. scheduled on a node
      |                 2. image pulled, container started  -> STATUS Running
      |                 3. kubelet runs the readiness probe
      |                    pass -> READY 1/1, fail -> READY 0/1
      v
Service api             sends traffic only to Ready pods
      |
      v
frontend                no Ready pod -> connection refused
```

The ticket says `Running` and `0/2`. So every pod reached step 2 and none
passed step 3. Nothing is broken in the process; the probe is.

## Following the trail

Every command here only reads. In the workstation the namespace is already
set, so plain `kubectl` (or `k`) works; the full form is shown once.

Start at the pods and read two columns, not one:

```sh
kubectl -n {{ .Namespace }} get pods
```

STATUS `Running` with READY `0/1` and RESTARTS `0` is the signature of this
ticket. A crash would show restarts; an image problem would show
`ImagePullBackOff`; a scheduling problem would show `Pending`. Both pods
look the same, so the problem is in the shared blueprint.

Next, ask a pod why. You do not have to type its name: `kubectl describe
pod api` then TAB completes it, or `kubectl describe pods -l
app.kubernetes.io/name=api` describes both without a name, or select the
name in the terminal and copy it.

```sh
kubectl describe pod <pod-name>
```

Scroll to `Events` at the bottom. The `Warning Unhealthy` line starts with
`Readiness probe failed:` and quotes the answer the node got. This line is
what tells you which of the three possible mistakes this episode has:

- `HTTP probe failed with statuscode: 404`: the server answered, so the
  port is right, but it has no page at the path the probe asked for.
- `dial tcp ...: connect: connection refused`: nothing listens on the port
  the probe dialed; look at the port number in the quoted URL.
- `cat: can't open '/tmp/ready': No such file or directory`: the probe is
  a command, and the file it reads never exists.

Then print the probe next to the port the container declares, so the
mismatch sits in one line:

```sh
kubectl get pod <pod-name> -o jsonpath='{.spec.containers[0].readinessProbe}{"\n"}{.spec.containers[0].ports}{"\n"}'
```

Compare with the ticket: the process listens on port 80 and serves `/`
from the ConfigMap. Whatever the probe asks that differs from that is the
bug. Last, see what the Service sees:

```sh
kubectl get endpointslices -l kubernetes.io/service-name=api -o jsonpath='{range .items[*].endpoints[*]}{.addresses[0]} ready={.conditions.ready}{"\n"}{end}'
```

Both pod addresses are listed with `ready=false`. The Service knows the
pods and refuses to send them anything, which is the refused connection
the frontend logs.

## The fix and why it works

The pods copy the probe from the Deployment's template, so change it there.
One patch replaces the whole probe with one that asks for `/` on the port
named `http`, which fixes every version of this ticket:

```sh
kubectl patch deployment api --type=json -p='[{"op":"replace","path":"/spec/template/spec/containers/0/readinessProbe","value":{"httpGet":{"path":"/","port":"http"},"initialDelaySeconds":2,"periodSeconds":5}}]'
```

You can also `kubectl edit deployment api` and change only the wrong
field. Why it works: the Deployment sees a new template, creates a new
ReplicaSet, and its pods get a probe nginx can answer. The first attempt
passes, the kubelet marks the pod Ready, the EndpointSlice flips the
address to `ready=true`, and the Service starts routing.

Why the tempting moves do not work:

- Raising `initialDelaySeconds` or `failureThreshold`: the probe is not
  early, it is wrong; it fails the same way at minute 10.
- Deleting the pods: the ReplicaSet recreates them from the same template
  with the same probe.
- Removing the probe: the pods turn Ready, but the next real outage (a
  server that hangs) stays in the Service. The ticket forbids it.
- Touching `/tmp/ready` inside the pod with `kubectl exec` makes that one
  pod Ready until it is replaced; the template still creates broken pods.

## Check your work

```sh
kubectl rollout status deployment/api
kubectl get pods
kubectl get endpointslices -l kubernetes.io/service-name=api
```

`rollout status` ends with `successfully rolled out`, `get pods` shows two
pods `1/1 Running`, and the EndpointSlice lists two addresses. A pod named
`kubedrill-probe-...` may appear for a few seconds: it is the grader
sending one request through the Service, the way the frontend would. The
checks below the terminal turn green, and `kubedrill check` grades once
from the shell.

The habit to keep: when a pod is `Running` and not Ready, the probe Event
names the question the node asked. Read it before you raise a timeout.
