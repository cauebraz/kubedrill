# The storefront that never started

## What happened

Dana's team runs a small website, the storefront. It is a set of static
files served by nginx, a web server. For years it ran on the old cluster.
This morning the team copied its Kubernetes manifests (the YAML files that
describe the app) to a new cluster and applied them. The pipeline said
"green". Soon after, customers got a 503 error page.

"Green" only meant that the cluster accepted the YAML. Accepting a
description is not the same as running it. Between "the cluster knows what
you want" and "customers see the page" there is a chain of steps, and one of
them broke. Your job is to walk that chain, find the step that stalled, and
repair it.

## The pieces

Kubernetes is a program that keeps the cluster looking like the YAML you
gave it. You describe the result ("3 copies of nginx serving these files"),
and controllers inside the cluster work until reality matches. The objects in
this ticket, from the top down:

- Deployment `storefront`: the team's wish, "keep 3 copies of this pod
  running". Its `spec.template` is the blueprint every copy is built from,
  including the container image.
- ReplicaSet: created by the Deployment for each version of the blueprint.
  It creates the pods. You rarely touch it.
- Pod: one running copy. A pod wraps one or more containers. Its name is the
  Deployment name plus two random parts, for example
  `storefront-6d4cf56db6-x7k2p`, so you never guess it, you list it.
- Container image: the packaged program the container runs, for example
  `nginx:1.27-alpine`. Before a container can start, the node must download
  (pull) the image from a registry, a server that stores images. Docker Hub
  is the default registry.
- ConfigMap `storefront-html`: the website files, mounted into each pod.
- Service `storefront`: one stable name and address in front of the pods.
  It only sends traffic to pods that are Ready, meaning they passed their
  readiness probe, a small HTTP request the node makes to check the
  container answers.

```
Deployment storefront   "keep 3 pods from this template"
      |
      v
ReplicaSet              creates the pods
      |
      v
Pod x3                  1. scheduled on a node
      |                 2. node pulls the image from the registry
      |                 3. container starts, files mounted from the ConfigMap
      |                 4. readiness probe passes -> pod is Ready
      v
Service storefront      sends traffic only to Ready pods
      |
      v
customers               no Ready pod -> connection refused -> 503
```

The ticket says `0/3` ready. So none of the 3 pods reached step 4. Something
earlier in the chain stopped them, and each step leaves a different trace.

## Following the trail

Every command here only reads; nothing changes until you decide what to
change. In the workstation the namespace is already set, so plain `kubectl`
(or `k`) works. The full form is shown once.

Start where the ticket points, at the pods:

```sh
kubectl -n {{ .Namespace }} get pods
```

Read the STATUS column. It names the step where each pod is stuck:
`Pending` (no node yet), `ContainerCreating` (pulling or mounting),
`ErrImagePull` or `ImagePullBackOff` (the image download failed),
`CrashLoopBackOff` (the container starts and dies), or `Running` with
`0/1` in READY (started, but the probe fails). All 3 pods show the same
status, which already tells you the problem is in the shared blueprint, not
in one unlucky pod.

Next, ask a pod why. You need its name; you do not have to type it:

- Press TAB: `kubectl describe pod stor` then TAB completes the name.
- Or let a label pick the pods: `kubectl describe pods -l
  app.kubernetes.io/name=storefront` describes all 3 without any name.
- Or copy it: select the name in the terminal of the page with the mouse
  and copy it with `Cmd-c` (`Ctrl-Shift-c` on Linux).

```sh
kubectl describe pod <pod-name>
```

Scroll to `Events` at the bottom. Events are short notes the cluster writes
about what it tried and what answered. Read the `Warning` lines. The one that
starts with `Failed to pull image` quotes the exact image the node asked for
and the registry's answer: `no such host` means the registry name does not
exist from this cluster, `not found` means the repository exists but that
tag does not, and `pull access denied` means the repository does not exist
(or is private and needs credentials).

An image reference has three parts, and a manifest copied from another
environment can carry a mistake in any of them:

```
docker.io / library/nginx : 1.27-alpine
registry    repository      tag
```

When the registry is left out, Docker Hub (`docker.io`) is used. Compare each
part of the image in the event with the ticket's `## Environment` section:
"the official nginx `1.27-alpine` image from Docker Hub".

Last, confirm where the wrong reference lives:

```sh
kubectl get deployment storefront -o jsonpath='{.spec.template.spec.containers[0].image}'
```

The pods copy the image from the Deployment's template. That is the one place
to change.

## The fix and why it works

Change the image of container `web` in Deployment `storefront` to the one
every other environment runs:

```sh
kubectl set image deployment/storefront web=nginx:1.27-alpine
```

Why this works: the Deployment sees a new template, creates a new ReplicaSet,
and that ReplicaSet creates new pods. Those pods pull an image that exists,
the container starts, the readiness probe gets the page, the pods turn Ready,
and the Service starts sending traffic to them. Each step of the chain above
now completes.

Why other moves do not work:

- Deleting the pods: the ReplicaSet recreates them from the same template,
  with the same wrong image.
- Editing the ConfigMap or the Service: they were never reached. No container
  started, so nothing read the files and nothing was Ready to route to.

In a real team, also fix the image in the repository's manifest. Otherwise
the next deploy applies the broken image again.

## Check your work

```sh
kubectl rollout status deployment/storefront
kubectl get pods
kubectl get endpointslices -l kubernetes.io/service-name=storefront
```

`rollout status` waits and ends with `successfully rolled out`. `get pods`
shows 3 pods `Running` with `1/1` READY. A pod named `kubedrill-probe-...`
may appear for a few seconds: it is the grader sending one request through
the Service, the way a customer would, and the grader deletes it. The EndpointSlice lists the pod
addresses the Service sends traffic to; an empty list was the reason for the
503. The checks below the terminal turn green, and `kubedrill check`
grades once from the shell.

The habit to keep from this ticket: read the STATUS, then read the Events,
before changing anything. The cluster usually tells you which step failed and
why.
