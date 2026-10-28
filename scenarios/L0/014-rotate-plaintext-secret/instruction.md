# Payments token ended up in a pull request; security wants it rotated today

## Ticket

> **From:** Lucia (billing team)\
> **Priority:** high
>
> Yesterday a pull request in our repo included the full YAML of Secret
> `payments-token`, token and all. The repo is internal, but security treats
> the token as leaked and already issued a new one:
>
> `{{ .Vars.new_token }}`
>
> Please put the new value in Secret `payments-token` (key `token`) and
> build it from a file this time, not from a manifest that could end up in
> git again. Security's audit lists rotated Secrets by a label, so the
> Secret needs `rotated={{ .Vars.rotated }}`. The `payments` API and the
> `payments-worker` both need the new token; last time we changed a Secret
> the app kept using the old value for hours, so please make sure the pods
> actually have it. Also, Tom saved a copy named `payments-token-old` "just
> in case" before anyone knew about the leak; it has to go. The old token is
> revoked at 18:00.

## Before you start

A Secret stores key-value pairs. A container receives one as an environment
variable through `env[].valueFrom.secretKeyRef`, read once when the container
starts, so a changed Secret reaches a pod only when the pod is replaced.
`kubectl create secret generic <name> --from-file=<key>=<file>` builds a
Secret from the bytes of a file, newline included when the file ends with
one; `--dry-run=client -o yaml` prints the manifest instead of creating it.
`kubectl get secret <name> -o jsonpath='{.data.<key>}' | base64 -d` shows
what the cluster stores.

## Environment

- Namespace: `{{ .Namespace }}`
- Resources: Secret `payments-token` (key `token`), Secret
  `payments-token-old` (Tom's copy), Deployment `payments` (2 replicas,
  container `api`), Deployment `payments-worker` (1 replica, container
  `worker`); both containers get the token from a Secret, as the env var
  `API_TOKEN` or as the file `/etc/payments/token`
- You have cluster-admin on the local kind cluster
- The new token is the string in the ticket, 21 characters, no trailing
  newline

## Done when

- secret payments-token exists
- secret payments-token key token holds the new value exactly
- secret payments-token carries the label rotated={{ .Vars.rotated }}
- deployment payments has 2 ready replicas
- every running payments pod sees the new token in API_TOKEN
- deployment payments finished rolling out: every pod runs the current template
- every running payments-worker pod sees the new token
- deployment payments-worker finished rolling out: every pod runs the current template
- a new payments-worker pod would also start with the new token
- secret payments-token-old does not exist

## Constraints

- Keep the names `payments-token`, `payments`, `payments-worker`, the env
  var name `API_TOKEN` and the file path `/etc/payments/token`
- Do not delete the namespace
- Do not paste the token into a manifest you keep; write it to a file you
  delete afterwards
