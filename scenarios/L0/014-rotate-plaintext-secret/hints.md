# Hints

## Hint 1: where to look

Three objects carry the token: the Secret `payments-token`, the pods of
Deployment `payments` and the pods of Deployment `payments-worker`, each
with its own copy. List what exists first:
`kubectl -n {{ .Namespace }} get secrets,deployments`. Then read which
Secret each Deployment reads `API_TOKEN` from:
`kubectl -n {{ .Namespace }} get deployment payments payments-worker -o yaml | grep -n -B1 -A3 secretKeyRef`.

## Hint 2: what to observe

Compare the three copies with the value in the ticket:

- the cluster's copy:
  `kubectl -n {{ .Namespace }} get secret payments-token -o jsonpath='{.data.token}' | base64 -d`
- the API pods' copy:
  `kubectl -n {{ .Namespace }} exec deploy/payments -- printenv API_TOKEN`
- the worker's copy:
  `kubectl -n {{ .Namespace }} exec deploy/payments-worker -- printenv API_TOKEN`

Also note the `name:` under `secretKeyRef` for `payments-worker` and the
`AGE` column of the pods; `kubectl -n {{ .Namespace }} get secret payments-token --show-labels`
shows whether the audit label is there.

## Hint 3: what it means

The Secret and the pods are separate copies. Rewriting the Secret changes
what the cluster stores, but a container reads its environment once, at
start, so pods created before the change keep the old value until they are
replaced; that is why "the app kept using the old value for hours" last
time. Each Deployment replaces its pods only when its pod template changes
or when a rollout is restarted. A Deployment whose `secretKeyRef` names
`payments-token-old` reads Tom's copy, not the Secret you rotate, and once
that copy is gone its new pods stop at `CreateContainerConfigError`. A file
written with `echo` ends with a newline, and `--from-file` keeps every byte.

## Hint 4: what to change

Four changes, in this order. Write the token to a file without a trailing
newline (`printf '%s'`, not `echo`) and rebuild Secret `payments-token`'s
key `token` from that file, then add the `rotated` label the ticket asks
for. If `payments-worker` reads `payments-token-old`, change the `name`
under its `secretKeyRef` to `payments-token`. Restart the rollout of both
Deployments so new pods start with the new value, and confirm with
`printenv` in a new pod. Remove `payments-token-old` last, after the worker
no longer references it.
