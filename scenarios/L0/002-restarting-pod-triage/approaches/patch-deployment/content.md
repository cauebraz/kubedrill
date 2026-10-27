A JSON patch replaces one field by its path and leaves the rest of the
object untouched. The field paths for this ticket are
`volumeMounts/1/mountPath` (value `"/etc/reviews"`),
`livenessProbe/httpGet/port` (value `8080`) and
`resources/limits/memory` (value `"32Mi"`), all under
`/spec/template/spec/containers/0`. The first line prints the container
spec so the index of the `config` mount and the current values are read,
not guessed; a `replace` on a path that does not exist is rejected.

Choose it when the field is known and the change must be repeatable: the
same line goes into a runbook, a ticket comment or a script, and the
rollout it triggers is the same as from `edit`. It is the route
`solution/solve.sh` takes.

The trade-off is that the path is positional: `containers/0` and
`volumeMounts/1` depend on the order in the template, so a template with
the volumes in another order needs another index. A strategic merge patch
(`kubectl patch ... -p '{"spec": ...}'`) merges lists by key instead, which
works for the probe port and the memory limit but not for renaming a mount
path, since `mountPath` is the key. And like `edit`, it leaves the
repository behind: carry the change into the tracked manifest.
