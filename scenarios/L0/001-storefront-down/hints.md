# Hints

## Hint 1: where to look

`0/3 ready` is a property of the pods behind the Deployment, not of the
Service or the edge. Start with the pods and their STATUS column:
`kubectl -n {{ .Namespace }} get pods`.

## Hint 2: what to observe

The STATUS column names the step that stalled. Read the Events at the bottom
of `kubectl -n {{ .Namespace }} describe pod <pod-name>`, with the name from
the NAME column of `get pods`, or list them all with
`kubectl -n {{ .Namespace }} get events --sort-by=.lastTimestamp`; the
`Warning` lines say what the kubelet tried to do and what answered.

## Hint 3: what it means

The Events say the kubelet cannot pull the image, so no container ever starts
and nothing becomes Ready. An image reference has three parts: registry host,
repository and tag. Compare each part of the reference in the event with what
the team runs everywhere else: the `1.27-alpine` tag of the official `nginx`
image on Docker Hub. A manifest copied from another environment can carry
that environment's registry with it.

## Hint 4: what to change

The pods only copy the image from the Deployment's pod template, so the fix
belongs in the Deployment `storefront`, container `web`: change its image
reference to the one the team runs everywhere else, then watch the rollout
finish. Deleting the pods changes nothing, the new ones get the same
reference. The ConfigMap is fine; leave it as it is.
