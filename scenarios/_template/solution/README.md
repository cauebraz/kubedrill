# Solution - <id> <title>

<!--
kubedrill-template: delete this comment block in a real scenario.
Shown when the learner surrenders or after a solve. It renders as a Go
template with the same data as setup files; write the namespace with the
.Namespace action. The level-2 headings are exactly these seven, in this
order (the linter rejects others); use ### inside a section when needed.
- Symptom: what the reporter and the first command show.
- Candidate causes: the four or five causes that fit the symptom, each with
  the trace it would leave.
- Observation that splits them: the command and the output line that rules
  all but one out, per variant when the line differs.
- Change: one change, two valid routes when they exist.
- Verify: the commands that prove the end state, with the expected output.
- In the real world: why this reaches production and what catches it earlier.
- Go deeper: upstream documentation links.
-->

## Symptom

What the reporter sees and what the first read command shows.

## Candidate causes

- First cause that fits the symptom, and the trace it would leave.
- Second cause, and its trace.
- Third cause, and its trace.

## Observation that splits them

```sh
kubectl -n {{ .Namespace }} get events --sort-by=.lastTimestamp
kubectl -n {{ .Namespace }} describe pod <pod-name>
```

The Event line that rules out the other causes, quoted from a real run.
`describe pod` prints the Events only for a named pod, not with -l.

- first-root-cause: how the line reads for this variant.
- second-root-cause: how the line reads for this variant.

## Change

```sh
kubectl -n {{ .Namespace }} set image deployment/app app=nginx:1.27-alpine
```

## Verify

```sh
kubectl -n {{ .Namespace }} rollout status deployment/app
kubectl -n {{ .Namespace }} get endpointslices -l kubernetes.io/service-name=app
```

The expected output of each command.

## In the real world

- Why this reaches production and what catches it earlier.

## Go deeper

- https://kubernetes.io/docs/tasks/debug/debug-application/debug-pods/
