# The one pod out of forty

## What happened

Priya is on call for the shop. Since 07:40 an alert keeps paging her:
"a pod restarted more than 5 times in the last hour" in the namespace of
the shop. Customers are fine, nothing is down. But the alert does not say
which pod, and the shop runs about 40 of them across 14 small services.
Priya wants two things: find the pod and tag it, so the on-call dashboard
shows it and a teammate can pick up the deeper analysis, and make it stop
restarting.

"Restarting" is a precise thing in Kubernetes. A container that stops is
started again, in place, by the node that runs it, and a counter goes up
by one. The counter is the symptom. Why the container stopped is the
question, and there are three usual answers: the program quit on its own,
the node killed it because a health check failed, or the kernel killed it
because it used more memory than it was allowed.

## The pieces

Kubernetes keeps the cluster looking like the YAML you gave it. The objects
in this ticket, from the top down:

- Namespace `{{ .Namespace }}`: a folder that holds everything the shop
  runs. All the commands below name it with `-n`.
- Deployment: the wish "keep N copies of this pod running". There are 14
  of them (`storefront`, `catalog`, `cart`, ... `reviews`). Its
  `spec.template` is the blueprint of every copy: the container image,
  the command, the health checks, the memory limit, the files to mount.
- Pod: one running copy, named after its Deployment plus two random parts
  (`catalog-7f68f4d7db-tbfvj`). You never guess the name; you list it or
  select it by label.
- Container `web`: the program inside the pod, a small web server. The
  kubelet, the agent on each node, starts it, checks it and restarts it
  when it stops.
- Liveness probe: a request the kubelet makes to the container every few
  seconds; three failures in a row and the kubelet kills the container
  and starts it again. A readiness probe is the same request with a
  gentler verdict: fail it and the pod stops receiving traffic, nothing
  is killed.
- Memory limit: the most memory the container may use; one byte over and
  the kernel kills it (`OOMKilled`, exit code 137).
- ConfigMap: a bag of files (`reviews-config` holds `reviews.conf`)
  mounted into the container at a path the Deployment chooses.
- Label: a `key=value` tag on any object. `app=reviews` is on every
  reviews pod; `triage=yes` is the one you add. Dashboards and `kubectl
  -l` select objects by label.

```
alert "a pod restarted > 5 times"      which of the 40?
          |
          v
kubectl get pods --sort-by=restarts     one pod stands out
          |
          v
Deployment reviews   "keep 1 pod from this template"
          |              template: command, probes, memory limit, mounts
          v
Pod reviews-xxxx     container web
          |            1. starts, reads /etc/reviews/reviews.conf
          |            2. builds an 8 MiB cache
          |            3. serves on port 8080
          |            4. the kubelet probes it, the kernel watches its memory
          v
kubelet              container stopped -> start it again, RESTARTS + 1
```

Step 1 can fail (the file is not where the program looks), step 4 can
fail twice over (the probe asks a port nothing listens on, or the memory
limit is below what step 2 needs). This episode has one of the three.

## Following the trail

Every command here only reads. In the workstation the namespace is
already set, so plain `kubectl` (or `k`) works; the full form is shown
once.

Start with the list, sorted so the pod with the most restarts comes last:

```sh
kubectl -n {{ .Namespace }} get pods --sort-by='.status.containerStatuses[0].restartCount'
```

Read the RESTARTS column. 39 pods show `0`; the last row shows a number
that grows, with the time of the last restart in parentheses, for example
`4 (31s ago)`. Its STATUS may say `Running`, `Error` or
`CrashLoopBackOff` depending on when you look: after each failure the
kubelet waits longer before the next start (10s, 20s, 40s, up to 5
minutes), and `CrashLoopBackOff` is that waiting. Note the pod's name;
you do not have to type it:

- Press TAB: `kubectl describe pod rev` then TAB completes the name.
- Or select by label: `kubectl describe pod -l app=reviews`.
- Or copy it from the terminal with the mouse.

```sh
kubectl describe pod <pod-name>
```

Three parts of this output answer "why":

- Under `Containers`, the `Last State` block: `Reason` and `Exit Code`
  of the run that died. `Error` with `Exit Code: 1` means the program
  quit on purpose. `Error` with `Exit Code: 137` means something sent it
  a kill signal. `OOMKilled` with `Exit Code: 137` means the kernel did,
  for memory.
- Still under `Containers`: `Limits` (the memory ceiling), `Liveness` and
  `Readiness` (the probe URLs with their ports) and `Mounts` (where each
  file bag is mounted). Compare them with a healthy pod: every other
  service runs with `32Mi` and a readiness probe on port 8080.
- `Events` at the bottom: `Liveness probe failed: ... connection refused`
  followed by `Killing ... failed liveness probe, will be restarted` is
  the kubelet explaining a kill. `Back-off restarting failed container`
  is only the consequence.

The third source is the program itself:

```sh
kubectl logs <pod-name> --previous
```

`--previous` prints what the container wrote before it died; without it
you get the current run, which may be too young to have failed. The
reviews entrypoint prints one line per step: `/etc/reviews/reviews.conf
not found after 10s, giving up` names a file it could not find (compare
with `Mounts`); `warming the review cache (8 MiB)` as the last line means
it died while building the cache (compare with `Limits`); `cache ready,
serving on :8080` means the program was fine and something outside killed
it (compare the port with the probe's).

Last, confirm that the wrong value sits in the Deployment, not only in
this pod:

```sh
kubectl get deployment reviews -o yaml
```

The pod is a copy of `spec.template`. Deleting the pod would produce
another copy with the same fault.

## The fix and why it works

Two changes, in this order.

First, change the one field the trail pointed at, in the pod template of
Deployment `reviews`, container `web`. `kubectl edit deployment reviews`
opens the YAML in an editor; find the field, change it, save:

- the `mountPath` of the `config` volume: `/etc/reviews`, the directory
  the entrypoint reads;
- or the liveness probe `port`: `8080`, the port the log says it serves
  on;
- or `resources.limits.memory`: `32Mi`, what the other services run with.

The same change as one command, for each case (run the one you need):

```sh
kubectl patch deployment reviews --type json -p '[{"op": "replace", "path": "/spec/template/spec/containers/0/volumeMounts/1/mountPath", "value": "/etc/reviews"}]'
kubectl patch deployment reviews --type json -p '[{"op": "replace", "path": "/spec/template/spec/containers/0/livenessProbe/httpGet/port", "value": 8080}]'
kubectl patch deployment reviews --type json -p '[{"op": "replace", "path": "/spec/template/spec/containers/0/resources/limits/memory", "value": "32Mi"}]'
```

Why it works: the Deployment notices its template changed, creates a new
pod from the corrected template and removes the old one. The new
container finds its file, or is probed on the port it listens on, or has
room for its cache, so it never stops, and the counter stays at 0.

Second, label the pod. The pod you found is gone now, replaced by the new
one, and labels live on the pod object, so label the pod that exists:

```sh
kubectl rollout status deployment/reviews
kubectl label pod -l app=reviews triage=yes
```

`-l app=reviews` picks the reviews pod by its label, whatever its random
name. If you labeled the old pod before the fix, that label left with it;
run the command again.

Why the tempting moves do not work:

- Deleting the pod: the Deployment creates another one from the same
  template, with the same fault, and the counter starts again.
- Removing the liveness probe altogether: the restarts stop, but a
  container that hangs would never be restarted again; fix the port.
- Raising the memory limit when the cause is the probe (or the other way
  around): a change that does not match the observation changes nothing,
  and the counter keeps growing.

## Check your work

```sh
kubectl get pods -l app=reviews
kubectl get pods -l triage=yes
kubectl get deployments
```

The reviews pod shows `1/1 Running` with `0` in RESTARTS, and still `0`
a minute later. The second command lists exactly that pod. The third
shows 14 rows with `READY` equal to the replica count (`4/4`, `3/3`,
`2/2`, `1/1`), 40 pods in all. The checks below the terminal turn green,
and `kubedrill check` grades once from the shell.

The habit to keep: sort before you scroll, read `Last State`, Events and
`logs --previous` before you change anything, and remember that a label
on a pod lives as long as the pod.
