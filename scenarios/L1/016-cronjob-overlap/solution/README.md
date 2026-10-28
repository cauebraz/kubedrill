# Solution - 016 Nightly invoice CronJob ran twice at once and corrupted a file

## Symptom

`invoices.csv` holds rows of two runs mixed together and the export log has
two `start` lines for one night. Every Job ends `Complete` or is replaced
without an error, so no alert fired: from the cluster's point of view each
pod did what it was told. The problem is how many ran at once.

## Candidate causes

- Overlap under `concurrencyPolicy: Allow`: a run lasts longer than the
  schedule interval, so the controller starts a new Job while the previous
  one is `Running`; `get jobs` shows two `Running` Jobs of `invoice`.
- A run killed mid-write under `concurrencyPolicy: Replace`: each tick deletes
  the running Job; events show `Deleted job invoice-<n>` next to `Created
  job`, and the killed run's log ends without `done`.
- A second scheduler: another CronJob that is not suspended runs the same
  export on the same schedule; `get jobs` shows Jobs with two name prefixes
  created in the same minute.
- A manual run: someone ran `kubectl create job --from=cronjob/invoice` during
  the scheduled run; the Job name has no schedule-time suffix and the
  CronJob events do not mention it.
- The script itself appends twice: one pod logs two `start` lines.

## Observation that splits them

```sh
kubectl -n {{ .Namespace }} get cronjobs,jobs,pods -l app.kubernetes.io/name=invoice
kubectl -n {{ .Namespace }} get jobs --watch
kubectl -n {{ .Namespace }} get events --sort-by=.lastTimestamp
kubectl -n {{ .Namespace }} get cronjobs -o custom-columns=NAME:.metadata.name,POLICY:.spec.concurrencyPolicy,SUSPEND:.spec.suspend,SCHEDULE:.spec.schedule
kubectl -n {{ .Namespace }} logs job/<job-name>
```

Take `<job-name>` from the NAME column of `get jobs`. Each pod logs one
`start` line, which rules out the script. The Job names end with the
schedule time in minutes since the epoch (`invoice-29885665`), so every Job
came from a CronJob tick, not from a manual run. Watching two ticks splits
the rest (measured on kind v1.37):

- allow-overlap: at the second tick `invoice-29885665` is still `Running`
  (it completes after `93s`) and `invoice-29885666` starts next to it; the
  POLICY column of `invoice` prints `Allow`.
- replace-kills-run: events show `Killing pod/invoice-29885665-<id>
  Stopping container export` and `cronjob/invoice Deleted job
  invoice-29885665` in the same second as `Created job invoice-29885666`.
  No Job ever reaches `Complete`, and `kubectl logs -f job/<job-name>`
  started on the running Job ends after `batch 13 of 18 appended` with no
  `done`; the killed pod is deleted with its Job, so its log is gone
  afterwards. The POLICY column of `invoice` prints `Replace`.
- legacy-twin: `invoice-29885665` and `invoice-legacy-29885665` are created
  in the same minute and both complete in about `43s`. The POLICY column
  prints `Forbid` for both CronJobs, and `invoice-legacy` prints SUSPEND
  `false` and the same SCHEDULE as `invoice`: `Forbid` only sees the Jobs
  of its own CronJob.

## Change

The end state is one schedule that starts the export, and that schedule
skips a tick while its last run is active, so even a slow run never gets a
second writer next to it.
{{ if eq .Variant "legacy-twin" }}
Here `invoice` already skips ticks (`Forbid`); the second writer is
`invoice-legacy`. Suspend it, then remove the Jobs still running beyond
one:

```sh
kubectl -n {{ .Namespace }} patch cronjob invoice-legacy --type=merge -p '{"spec":{"suspend":true}}'
kubectl -n {{ .Namespace }} get jobs -l app.kubernetes.io/name=invoice
kubectl -n {{ .Namespace }} delete job <surplus-job-name>
```

Deleting it (`kubectl -n {{ .Namespace }} delete cronjob invoice-legacy`)
passes too; suspending keeps its history for whoever owns it.
{{- else }}
Here `invoice-legacy` is already suspended; the policy of `invoice` is the
writer problem. Make `invoice` skip a tick while its last run is active,
then remove the Jobs still running beyond one:

```sh
kubectl -n {{ .Namespace }} patch cronjob invoice --type=merge -p '{"spec":{"concurrencyPolicy":"Forbid"}}'
kubectl -n {{ .Namespace }} get jobs -l app.kubernetes.io/name=invoice
kubectl -n {{ .Namespace }} delete job <surplus-job-name>
```

`Replace` is not a fix: it avoids two runs by killing the one in progress.
{{- end }}
Neither change stops a Job that is already running, so the last two lines
remove the extra active Jobs by hand. In production, waiting for the current run to finish is
the safer way: deleting a running Job is exactly how `Replace` corrupted the
file.

## Verify

```sh
kubectl -n {{ .Namespace }} get cronjobs -l app.kubernetes.io/name=invoice
kubectl -n {{ .Namespace }} get jobs --watch
kubectl -n {{ .Namespace }} create job invoice-manual --from=cronjob/invoice
kubectl -n {{ .Namespace }} logs job/invoice-manual
```

`get cronjobs` shows `invoice` with SUSPEND `False` and every other
`invoice` CronJob with `True`. Over the next ticks the watch shows at most
one `Running` Job of `invoice`: under `Forbid` a tick that finds a run still
active creates nothing, and the first tick after it completes starts the
next run. `create job --from=cronjob/invoice` runs the export once on demand
from the same template; `concurrencyPolicy` does not cover it, so run it
only when no scheduled run is active. Its log shows one `start` and one
`done`. `successfulJobsHistoryLimit: 3` and `failedJobsHistoryLimit: 1` keep
the last finished Jobs and their logs around for exactly this comparison.

## In the real world

- `concurrencyPolicy` defaults to `Allow`. Any job that can run longer than
  its interval, because data grows or a dependency slows down, overlaps
  without an error. Jobs that write shared state set `Forbid` from day one.
- `Replace` reads as "the newest wins" but means "kill the one in progress";
  it fits only jobs that are safe to interrupt.
- Migrations leave twins behind: an old CronJob, a host crontab, a scheduler
  in another cluster. `Forbid` cannot see any of them, so the export itself
  should take a lock (a Lease, a lock row, a conditional write) and write to
  a temporary file that it renames at the end.
- A run skipped by `Forbid` is a missed export. Alert on
  `status.lastSuccessfulTime` older than the interval, and set
  `startingDeadlineSeconds` so a late tick still runs within a window.

## Go deeper

- CronJob concurrency policy, suspend and history limits:
  https://kubernetes.io/docs/concepts/workloads/controllers/cron-jobs/
- Running automated tasks with a CronJob:
  https://kubernetes.io/docs/tasks/job/automated-tasks-with-cron-jobs/
- Jobs, completion and cleanup:
  https://kubernetes.io/docs/concepts/workloads/controllers/job/
