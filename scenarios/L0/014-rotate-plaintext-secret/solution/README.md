# Solution - 014 Payments token ended up in a pull request; security wants it rotated today

## Symptom

Nothing is broken yet: the pods are `Running` and `2/2` ready. The problem
is a value, not a state. Secret `payments-token` and the pods of
`payments` and `payments-worker` hold the leaked token
`{{ .Vars.old_token }}`, a second copy lives in Secret `payments-token-old`,
and the provider revokes the old token at 18:00. The reporter also recalls
that the last Secret change "kept the old value for hours": a `kubectl get
pods` that shows everything green does not catch a stale environment
variable.

## Candidate causes

This is a build ticket, so the candidates are the places the old token can
survive after the rotation. Each leaves a trace:

- The Secret itself: `data.token` still decodes to the old value, or to the
  new value with a trailing newline when the file was written with `echo`.
- The running pods: a container reads its environment once, at start.
  `printenv API_TOKEN` inside a pod created before the change still prints
  the old value, while the Secret already holds the new one.
- A Deployment that reads another Secret: `payments-worker` may take
  `API_TOKEN` from `payments-token-old` instead of `payments-token`, so a
  restart alone keeps it on the leaked value and deleting the copy first
  leaves its next pod in `CreateContainerConfigError`.
- The copy: `payments-token-old` holds the same bytes under another name.

## Observation that splits them

```sh
kubectl -n {{ .Namespace }} get secrets,deployments
kubectl -n {{ .Namespace }} get secret payments-token -o jsonpath='{.data.token}' | base64 -d
kubectl -n {{ .Namespace }} exec deploy/payments -- printenv API_TOKEN
kubectl -n {{ .Namespace }} get deployment payments payments-worker -o jsonpath='{range .items[*]}{.metadata.name}{" reads "}{.spec.template.spec.containers[0].env[0].valueFrom.secretKeyRef.name}{"\n"}{end}'
```

The first command lists two Secrets (`payments-token`, `payments-token-old`)
and two Deployments. The second prints `{{ .Vars.old_token }}`: the Secret
holds the leaked value. The third prints the same value from inside a pod.
The fourth tells the variants apart:

- worker-on-new-secret: `payments-worker reads payments-token`. Both
  Deployments read the Secret you rotate; each only needs its pods
  replaced.
- worker-on-old-copy and worker-on-old-copy-audit-label:
  `payments-worker reads payments-token-old`. The worker reads Tom's copy,
  so its `secretKeyRef` has to move to `payments-token` before the copy is
  deleted, or its next pod stops at `CreateContainerConfigError` with
  `secret "payments-token-old" not found`.

`kubectl -n {{ .Namespace }} get secret payments-token --show-labels`
shows no `rotated` label yet.

## Change

Write the token to a file without a trailing newline, build the Secret from
the file, and apply the generated manifest over the existing Secret. The
file is deleted at the end; the manifest never touches disk.

```sh
printf '%s' '{{ .Vars.new_token }}' > token.txt
kubectl -n {{ .Namespace }} create secret generic payments-token --from-file=token=token.txt --dry-run=client -o yaml | kubectl -n {{ .Namespace }} apply -f -
kubectl -n {{ .Namespace }} label secret payments-token rotated={{ .Vars.rotated }} --overwrite
kubectl -n {{ .Namespace }} patch deployment payments-worker -p '{"spec":{"template":{"spec":{"containers":[{"name":"worker","env":[{"name":"API_TOKEN","valueFrom":{"secretKeyRef":{"name":"payments-token","key":"token"}}}]}]}}}}'
kubectl -n {{ .Namespace }} rollout restart deployment/payments deployment/payments-worker
kubectl -n {{ .Namespace }} delete secret payments-token-old
rm token.txt
```

`printf '%s'` writes exactly the 21 characters; `echo` would add a newline
that `--from-file` keeps and the provider rejects. The first `apply` warns
that the `last-applied-configuration` annotation is missing, because the
Secret was not created with `kubectl apply`; kubectl adds it and moves on.
The `patch` sets the worker's `secretKeyRef` to `payments-token`; in the
worker-on-new-secret variant it reports `(no change)`, and `kubectl edit
deployment payments-worker` does the same job by hand. `rollout restart`
changes an annotation in each pod template, which is the only way a
Deployment replaces pods whose spec did not change. The delete comes after
the patch, so no worker pod is created against a Secret that no longer
exists.

A second valid path deletes `payments-token` and creates it again with
`kubectl create secret generic payments-token --from-file=token=token.txt`,
then labels it. Running pods keep working during the gap, because their
environment was injected at start, but a pod replaced during that gap fails
to start; keep the gap short.

## Verify

```sh
kubectl -n {{ .Namespace }} rollout status deployment/payments
kubectl -n {{ .Namespace }} rollout status deployment/payments-worker
kubectl -n {{ .Namespace }} get secret payments-token -o jsonpath='{.data.token}' | base64 -d
kubectl -n {{ .Namespace }} exec deploy/payments -- printenv API_TOKEN
kubectl -n {{ .Namespace }} exec deploy/payments-worker -- printenv API_TOKEN
kubectl -n {{ .Namespace }} get secrets --show-labels
```

Both `rollout status` lines end with `successfully rolled out`. The next
three commands print `{{ .Vars.new_token }}` three times, once from the
Secret and once from each Deployment's new pods; `base64 -d` prints no
newline after it, which is the "exactly" of the ticket. The last command
lists `payments-token` with `rotated={{ .Vars.rotated }}` and no
`payments-token-old`. `kubectl get pods` shows three pods whose `AGE` is
younger than the Secret change.

## In the real world

- A token in a pull request is a rotation, not a revert: git keeps the
  commit, forks keep the clone, and the CI logs may have printed it. Rotate
  first, clean history second.
- `kubectl create secret generic --from-file` and `--from-literal` keep the
  value out of the manifest; the manifest with `data:` is what leaks when
  someone commits it. The `--dry-run=client -o yaml | kubectl apply -f -`
  form is the same command used as a generator, and it is the pattern for
  changing one key without hand-writing base64.
- Env vars from a Secret are read at container start, so every rotation
  ends with a rollout restart and a `printenv` (or the app's own health
  endpoint) in a new pod. Secrets mounted as files are updated in place
  within a kubelet sync period, but only if the app rereads the file.
- Deleting the old Secret before every consumer moved off it is the usual
  self-inflicted outage of a rotation; `kubectl get deployments -o yaml |
  grep -n secretKeyRef` and `grep -n secretName` list the consumers first.
- A trailing newline from `echo` is the second most common rotation bug;
  `printf '%s'` or `echo -n`, then `base64 -d | od -c` to see the bytes.

## Go deeper

- Secrets, how to create them from files and literals, and how env vars and
  volumes consume them:
  https://kubernetes.io/docs/concepts/configuration/secret/
- Managing Secrets with kubectl, including `--from-file` and the dry-run
  pattern: https://kubernetes.io/docs/tasks/configmap-secret/managing-secret-using-kubectl/
- Deployment rollouts and `rollout restart`:
  https://kubernetes.io/docs/concepts/workloads/controllers/deployment/
