# Concept pages

A concept page explains one mechanism a scenario exercises: what the objects
and fields are, how the kubelet, a controller or kube-proxy acts on them, what
the commands show on call, and which failures follow from it. It is the
reference a learner opens when a ticket uses a word they cannot picture, and
the vocabulary a learner uses after a solve to name what they did. A concept
page is read because a scenario sent you there, never as required reading.

The pages live here, one file per concept, with the file name as the id.
Scenarios reference them from `scenario.toml` and the CLI prints them.

## How scenarios reference concepts

```toml
[scenario]
id = "001-storefront-down"
type = "break-fix"
concepts = ["pod-lifecycle", "image-pull", "readiness-and-endpoints"]
```

`concepts` is required for `break-fix` and `build` scenarios. Each entry is
the id of a file `docs/concepts/<id>.md`; `kubedrill lint` rejects an id
without a file. List the two to four concepts the fix depends on, the most
central first.

## How the CLI shows them

- `kubedrill learn` prints the pages the active scenario lists, in order,
  with the full text. `kubedrill learn <id>` prints one concept by id.
- `kubedrill concepts` prints a table with the id, the title, the number of
  scenarios that list the page and the summary (the intro paragraph), so a
  learner can find the one that names a symptom.

When to read them is in `docs/study-flow.md`, step 0: before the first
`kubectl` only if the vocabulary is new, otherwise after `kubedrill check`
passes. `troubleshooting-loop` is the exception: it describes the method
every walkthrough follows (`## Symptom`, `## Candidate causes`, `##
Observation that splits them`, `## Change`, `## Verify`), so read it before
the first scenario.

## Format

The loader validates the title, one intro paragraph and the four `##`
headings below, in this order, with these exact titles; `kubedrill lint`
reports a page that misses one. The file name is the id and matches
`^[a-z0-9]+(-[a-z0-9]+)*$`. The word, command and link counts below are
writing rules checked in review, not by the loader. Length 400 to 900
words.

```
# <Title>

<Intro paragraph: 2-4 sentences, what this is and the one sentence to hold
in your head on call. Becomes the summary shown by `kubedrill concepts`.>

## The mechanism

<How it works, 150-300 words, with one diagram of the actual mechanism as a
```mermaid flowchart block: one node per object, "<b>name</b><br/>what it
does", arrows with -->. Name the exact fields and objects
(status.readyReplicas, EndpointSlice.endpoints[].conditions.ready).>

## What you see on call

<The 3-6 commands you run and what each output field means, with the exact
kubectl flags. Show one realistic output excerpt per command.>

## Common failures

<A list: symptom -> cause -> the one command that confirms it. 4-8 items.>

## Go deeper

<2-4 links to kubernetes.io docs, a KEP or a source file, each with one line
on what to read there.>
```

Writing rules, the same as the rest of the repository: plain ASCII, hyphens
for dashes, en-US, numbers and field names instead of adjectives, no emojis.
Every field name, flag and default value is checked against the kubernetes.io
page for the cluster version in `cluster/profiles/base.yaml` (v1.37) before
it is written; a claim that could not be checked is left out. Another concept
is referenced by its id in backticks, for example `readiness-and-endpoints`.

## Pages

| Id                        | One line                                                                 |
|---------------------------|--------------------------------------------------------------------------|
| `pod-lifecycle`           | Phases, container states and reasons, how the STATUS column is computed  |
| `image-pull`              | Image reference anatomy, pull policy, backoff, what each pull error says |
| `readiness-and-endpoints` | Probes, the Ready condition, EndpointSlice conditions, kube-proxy        |
| `labels-and-selectors`    | Labels vs annotations, selector forms, the selector mismatch outage      |
| `troubleshooting-loop`    | The five-step loop, the layer model, bottom-up order, mitigate first     |
| `what-changed`            | Revisions as ReplicaSets, rollout history and undo, managedFields, diff  |
| `resources-and-qos`       | Requests vs limits, QoS classes, OOMKilled vs eviction, CFS throttling   |
| `cluster-dns`             | resolv.conf, ndots:5, dnsPolicy, CoreDNS capacity, conntrack, NodeLocal  |
| `control-plane-signals`   | 429 and 5xx, API Priority and Fairness, etcd quota and fsync, SLOs       |
