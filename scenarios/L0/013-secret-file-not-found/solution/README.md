# Solution - 013 billing api never starts after db-password moved into a Secret

## Symptom

Deployment `api` shows `0/2` ready since the database password moved from an
environment variable into Secret `db-credentials`. `kubectl get secret
db-credentials` lists the Secret, so the change looks applied: the API
server stored every object, but whether a file appears at
`/etc/secrets/db-password` is decided by the kubelet when it builds the pod.

## Candidate causes

A container that needs a mounted file and does not get it fits several
causes. Each one leaves a different trace:

- The volume names a Secret that does not exist: STATUS stays
  `ContainerCreating`, Events show `FailedMount` with `secret "<name>" not
  found`, no container starts and `kubectl logs` refuses.
- The mount points at another directory: the container starts, the app logs
  `cannot read`, `Mounts:` in `describe` shows a path other than
  `/etc/secrets`, and `ls /etc/secrets` in the container fails.
- The Secret's key has another name: the container starts, `Mounts:` shows
  `/etc/secrets`, and `ls /etc/secrets` lists a file whose name is not
  `db-password`.
- An `items` list on the volume that leaves the key out or maps it to
  another path: same trace as the key name, with `Items:` in `describe`.
- A failing readiness probe on an app that did connect: the logs would say
  `api: connected` while READY stays `0/1`.

## Observation that splits them

```sh
kubectl -n {{ .Namespace }} get pods
kubectl -n {{ .Namespace }} describe pod <pod-name>
kubectl -n {{ .Namespace }} logs <pod-name>
kubectl -n {{ .Namespace }} exec <pod-name> -- ls /etc/secrets
kubectl -n {{ .Namespace }} get secret db-credentials -o jsonpath='{.data}'
```

Take `<pod-name>` from the NAME column of `get pods`. In mount-path-typo
and key-name the app logs `api: cannot read /etc/secrets/db-password` and
exits after 45 seconds, which turns into `Error` and `CrashLoopBackOff`;
the `exec` works while the container is up, and the probe is ruled out
because the logs never print `api: connected`. In wrong-secret-name the
container never starts, so `logs` and `exec` are refused and the Events of
`describe` carry the cause. Per variant (measured on kind v1.37):

- mount-path-typo: STATUS `Running` with `0/1` READY, then
  `CrashLoopBackOff`. `describe` shows `Mounts: /etc/secret from
  db-credentials (ro)`, one letter short of `/etc/secrets`. `ls /etc/secrets`
  prints `ls: /etc/secrets: No such file or directory`, and `ls /etc/secret`
  lists `db-password`.
- key-name: STATUS `Running` with `0/1` READY, then `CrashLoopBackOff`.
  `Mounts: /etc/secrets from db-credentials (ro)` is right, but `ls
  /etc/secrets` lists `password`, and the jsonpath prints
  `{"password":"..."}`: the key, and so the file, is `password`, not
  `db-password`.
- wrong-secret-name: STATUS `ContainerCreating` with 0 restarts. Events
  show `Warning FailedMount ... MountVolume.SetUp failed for volume
  "db-credentials" : secret "db-credential" not found`, `Volumes:` shows
  `SecretName: db-credential`, and `kubectl logs` answers `container "api"
  in pod "<pod-name>" is waiting to start: ContainerCreating`.

## Change

Change the one name that does not line up, in the pod template of
Deployment `api`. For mount-path-typo, the mount directory:

```sh
kubectl -n {{ .Namespace }} patch deployment api --type=json -p '[{"op":"replace","path":"/spec/template/spec/containers/0/volumeMounts/0/mountPath","value":"/etc/secrets"}]'
```

For key-name, map the key to the file name the app reads with `items`, or
rename the key in Secret `db-credentials` and restart the rollout:

```sh
kubectl -n {{ .Namespace }} patch deployment api --type=json -p '[{"op":"add","path":"/spec/template/spec/volumes/0/secret/items","value":[{"key":"password","path":"db-password"}]}]'
```

For wrong-secret-name, point the volume at the Secret that exists:

```sh
kubectl -n {{ .Namespace }} patch deployment api --type=json -p '[{"op":"replace","path":"/spec/template/spec/volumes/0/secret/secretName","value":"db-credentials"}]'
```

`kubectl edit deployment api`, or the same change in the team's manifest
followed by `kubectl apply`, passes too: the checks read the ready and
available replica counts, the app's log line and the Secret the volume
names, never the command used. Creating a second Secret under the name the
volume asks for (`db-credential`) starts the pods but fails the last check:
the ticket keeps one Secret, `db-credentials`.

## Verify

```sh
kubectl -n {{ .Namespace }} rollout status deployment/api
kubectl -n {{ .Namespace }} get pods
kubectl -n {{ .Namespace }} logs -l app=api --tail=5
```

`rollout status` ends with `deployment "api" successfully rolled out`, `get
pods` shows 2 pods `Running` with `1/1` READY, and the logs print `api:
connected` once per pod. A pod of the old ReplicaSet can still print `api:
cannot read` for a second while it terminates.

## In the real world

- A Secret volume fails quietly: the cluster accepts a mount to any
  directory and a Secret with any key, so the first error comes from the
  app. A startup log line that names the path it reads, as this app does,
  turns a guess into a comparison.
- A volume that names a missing Secret blocks the pod in
  `ContainerCreating` without a log. Alerts on pods that stay not Ready
  catch it; `optional: true` on the volume trades the block for a missing
  file, so use it only when the app copes without the file.
- Teams that create the Secret by hand and the Deployment from the
  repository drift apart: a key renamed in one place and not the other.
  Keep both in the same change, or generate the Secret from a template
  (External Secrets, Sealed Secrets, kustomize `secretGenerator`).
- `subPath` mounts of a single key do not receive Secret updates; a full
  volume mount does, after the kubelet sync delay.

## Go deeper

- Secrets as files in a pod:
  https://kubernetes.io/docs/concepts/configuration/secret/#using-secrets-as-files-from-a-pod
- Projecting keys to specific paths with `items`:
  https://kubernetes.io/docs/tasks/inject-data-application/distribute-credentials-secure/
- Debugging pods:
  https://kubernetes.io/docs/tasks/debug/debug-application/debug-pods/
