# Title in the reporter's words

<!-- kubedrill-template: delete this block -->
<!--
The ticket the learner sees. Rules (AGENTS.md, Scenario rules):
- Written in the reporter's words, with symptoms and expectations only.
- Never names the root cause.
- "## Done when" is required: one bullet per check in checks.yaml, in the
  same order, with the same text as the check's description (case does not
  matter). The linter rejects a mismatch.
- "## Before you start" is optional and at most 8 lines: one mental model
  the learner needs, not a walkthrough, and never the root cause class.
- The file is a Go template with the same data as setup files. Write the
  namespace with the .Namespace action, as the Ticket and Environment
  sections below do, including in URLs; the episode namespace carries a
  random suffix, so a literal name is wrong for the learner.
Delete this comment block and the marker line above it in a real scenario.
-->

## Ticket

> **From:** Reporter (team)\
> **Priority:** high
>
> What the reporter observed, in their words. Which URL refuses connections
> or answers an error (for example `http://app.{{ .Namespace }}.svc`), which
> dashboard shows what, what changed recently as far as they know.

## Before you start

One mental model in plain sentences. At most eight lines. Say which object
owns the symptom and which command reveals the reason.

## Environment

- Namespace: `{{ .Namespace }}`
- Resources: Deployment `app` (2 replicas), Service `app` (port 80)
- You have cluster-admin on the local kind cluster

## Done when

- deployment app exists
- deployment app has 2 ready replicas
- service app has at least one ready endpoint
- the service answers HTTP 200 from inside the cluster

## Constraints

- Keep the Deployment and Service names
- Do not delete the namespace
