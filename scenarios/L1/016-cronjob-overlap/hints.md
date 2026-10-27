# Hints

## Hint 1: where to look

Two `start` lines for one night means two pods ran the export. Pods come
from Jobs and Jobs come from CronJobs, so list all three layers with their
owners: `kubectl -n {{ .Namespace }} get cronjobs,jobs,pods -l
app.kubernetes.io/name=invoice`. Then watch two schedule ticks live with
`kubectl -n {{ .Namespace }} get jobs --watch`.

## Hint 2: what to observe

Read three things. The Job names: a Job created by a CronJob is named after
it, so the prefix says which CronJob started it. The STATUS and DURATION
columns at the moment the next tick fires: is the previous Job still
`Running`, did it disappear, or is a second Job running next to it? And the
CronJob events in `kubectl -n {{ .Namespace }} get events
--sort-by=.lastTimestamp`, where `Deleted job` and `Created job` lines appear
next to the CronJob name. `kubectl -n {{ .Namespace }} get cronjobs -o yaml`
shows `concurrencyPolicy` and `suspend` under each `spec`.

## Hint 3: what it means

`concurrencyPolicy` decides what a tick does while a Job of the same CronJob
is still active: `Allow` starts another one next to it, `Replace` deletes the
running one and starts a new one, `Forbid` skips the tick. A run longer than
the interval under `Allow` gives two writers; `Replace` kills a writer
halfway. The policy only covers the Jobs of its own CronJob: a second
CronJob that is not suspended and runs the same command on the same schedule
starts its own writer every tick, whatever the first one says.

## Hint 4: what to change

The fix belongs in the CronJob specs, not in the Jobs or pods, which are
recreated every minute. Make CronJob `invoice` skip a tick while its last run
is active, and make sure no other CronJob with the `invoice` label is
allowed to schedule. Jobs already running are not stopped by either change:
remove the surplus ones so at most one export is active.
