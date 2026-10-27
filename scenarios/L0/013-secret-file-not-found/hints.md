# Hints

## Hint 1: where to look

`0/2 ready` is a property of the pods behind the Deployment. Start with
`kubectl -n {{ .Namespace }} get pods` and read STATUS and RESTARTS. Two
different pictures are possible here, and each names a different step: a
pod that is `Running` with `0/1` ready, turns `Error` and then
`CrashLoopBackOff` with RESTARTS growing started its container and the
container quit; a pod that stays `ContainerCreating` with no restarts never
got a container at all.

## Hint 2: what to observe

`kubectl -n {{ .Namespace }} describe pod <pod-name>` has three blocks worth
reading side by side: `Mounts:` under the container (which directory, from
which volume), `Volumes:` (which Secret the volume takes its files from),
and `Events:` at the bottom. Then compare with the ticket: the app reads
`/etc/secrets/db-password`. For a pod that started, `kubectl -n
{{ .Namespace }} logs <pod-name>` prints the app's own line and
`kubectl -n {{ .Namespace }} exec <pod-name> -- ls /etc/secrets` shows what
is really there during the 45 seconds the container stays up. Finish with
`kubectl -n {{ .Namespace }} get secret db-credentials -o jsonpath='{.data}'`,
which lists the keys of the Secret without printing the values decoded.

## Hint 3: what it means

A Secret volume builds files from three inputs, and all three must line up
with the path the app reads: the volume's `secretName` must be a Secret that
exists in the namespace, the container's `mountPath` is the directory the
files land in, and each key of the Secret becomes a file named after the key
(an `items` list can give a key a different file name). A `FailedMount`
event saying the Secret was not found points at the first input; a container
that starts, prints `cannot read`, and whose `ls` shows the file in another
directory or under another name points at the second or the third. The app
did nothing wrong: it looks exactly where its README says.

## Hint 4: what to change

The pods copy the volume and the mount from the pod template of Deployment
`api`, so that is where the fix goes, in the part that does not line up:
the `mountPath` of the mount in container `api`, the `secretName` of the
volume named `db-credentials`, or an `items` entry on that volume that maps
the key the Secret has to the file name the app reads. Renaming the key
inside the Secret `db-credentials` is the other valid route when the key is
what is off; the kubelet refreshes a Secret volume in a running pod after a
delay, and a rollout restart of the Deployment makes it immediate. Deleting
the pods changes nothing: new ones get the same template.
