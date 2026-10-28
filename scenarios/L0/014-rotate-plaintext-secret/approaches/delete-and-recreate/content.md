Delete `payments-token` and create it again with `kubectl create secret
generic --from-file`, the plain form of the command without the dry-run
pipe. The result is a Secret that holds exactly the keys you passed, which
is the point: nothing from the old object survives, not a stale second key,
not an annotation with the old data in it.

Choose it when the old Secret may carry more than the one key you are
rotating, or when you want the new object to be a clean record of what was
created. The checks read the Secret's `data.token`, its `rotated` label,
the value inside new pods of both Deployments and the Secret the worker's
pod template names, so this path passes the same way as the apply path.

The trade-off is the gap. Between the `delete` and the `create` the Secret
does not exist; running pods are unaffected, because their environment was
injected at start, but a pod created in that gap (a node drain, a crash, a
scale-up, the rollout restart you are about to run) stops at
`CreateContainerConfigError` until the Secret is back. Keep the two
commands adjacent, and never run the rollout restart before the `create`.
The labels of the old object are gone too, so `rotated` and any
`app.kubernetes.io/*` labels the team relies on have to be set again.
