# Hints

<!--
# kubedrill-template: delete this comment block in a real scenario.
The hint ladder: three or four sections with exactly these headings, in this
order; the fourth is optional. Each body is shown with the heading removed
and whitespace trimmed, and renders as a Go template with the same data as
setup files (write the namespace with the .Namespace action).
- where to look: the subsystem or object, and a read command to list it
- what to observe: the field, column or Event line that explains the
  symptom, and the read command that shows it; no interpretation
- what it means: the interpretation of that observation
- what to change: the object and field to change, without the exact value
  or a copy-paste command
No hint contains kubectl followed by a mutating verb (set, edit, patch,
apply, create, replace, delete, scale, label, annotate, expose, run,
autoscale, cordon, uncordon, drain, taint, rollout undo, rollout restart,
rollout pause, rollout resume); the linter rejects one. Read commands (get, describe, logs,
events) are fine.
-->

## Hint 1: where to look

Where to look first: the object named in the ticket and the read command that
lists its state, for example `kubectl -n {{ .Namespace }} get pods`.

## Hint 2: what to observe

Which detail of that output explains the symptom, and the read command that
shows the reason.

## Hint 3: what it means

What that detail means for the system, in one or two sentences.

## Hint 4: what to change

Which object and field to change, without the exact value.
