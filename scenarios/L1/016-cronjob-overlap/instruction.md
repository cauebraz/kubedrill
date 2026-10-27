# Nightly invoice CronJob ran twice at once and corrupted a file

## Ticket

> **From:** Priya (billing team)\
> **Priority:** high
>
> Last night's invoice export left `invoices.csv` with rows from two runs
> mixed together, and finance could not close the day. The export log shows
> two `start` lines for the same night. We reproduced it
> in `{{ .Namespace }}`: the copy there runs every minute instead of nightly,
> so it misbehaves within two minutes. The export takes longer some nights,
> and we moved it to this CronJob from an older setup last month. The export
> script itself has not changed in a year. Please make sure only one export
> can write the file at a time.

## Before you start

A CronJob creates a Job on each schedule tick, and the Job creates a pod
that runs the export. Several Jobs can exist at once; whether a new tick
waits, runs alongside or replaces the running Job is a field of the CronJob.
`kubectl get jobs --watch` shows Jobs appear and finish live, and the pod
logs show which run wrote which line. Watch two ticks before changing
anything.

## Environment

- Namespace: `{{ .Namespace }}`
- Resources: CronJobs labeled `app.kubernetes.io/name=invoice`, the Jobs
  and pods they create (container `export`)
- Schedule `*/1 * * * *` here; production runs the same CronJob nightly
- You have cluster-admin on the local kind cluster

## Done when

- cronjob invoice skips a run while the previous one is active (concurrencyPolicy Forbid)
- at most one invoice Job is active
- cronjob invoice is the only unsuspended CronJob that runs the invoice export

## Constraints

- Keep CronJob `invoice` and its schedule; it must keep running every minute
- Do not delete the namespace
