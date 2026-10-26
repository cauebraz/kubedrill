# Design choices

Why kubedrill works the way it does, with the source behind each choice.

## Grade the end state, not the commands

The iximiuz labs challenge format checks the resulting system state and
leaves the method to the learner
(https://labs.iximiuz.com/docs/learning-materials/challenges). tau-bench
grades the final database state of an agent run the same way. Here every
fix that reaches the state in `checks.yaml` passes.

## Fast reset

Reset speed decides whether people repeat. The iximiuz labs 2025 wrap-up
lists playgrounds booting "nearly twice as fast" among the year's milestones
(https://newsletter.iximiuz.com/posts/iximiuz-labs-year-wrap-up-major-milestones-usage-and-revenue-numbers-and-plans-for-2026).
`kubedrill start` creates a fresh namespace per episode in a long-lived
cluster. It deletes the previous namespace without waiting, instead of
recreating the cluster.

## Seeded episodes

Gymnasium's `reset(seed)` and its `terminated` versus `truncated` end states
(https://gymnasium.farama.org/api/env/) map to `--seed` and to the journal
outcomes. `solved` is terminated. `abandoned`, `failed` and `timed_out` are
truncated.

## No hidden tests

SadServers prints the test next to the task (https://sadservers.com/faq).
Here `kubedrill check --explain` lists every check, and the ticket says how
many there are. A correct fix is never rejected by a check you could not
see.

## One directory per task

The Harbor task layout pairs an instruction file with a task config
(https://docs.harborframework.com/tasks/overview). A scenario follows the
same shape: `instruction.md` plus `scenario.toml`.

## Commit to a cause before the answer

High-confidence errors are corrected best when the learner committed to an
answer first (Metcalfe 2017, hypercorrection). So the last hint and the
solution wait for `kubedrill diagnose`, and the debrief compares your line
with the reference root cause.

## Spaced repetition

Cepeda et al. 2006 (https://pubmed.ncbi.nlm.nih.gov/16719566/) found that
spacing practice sessions improves retention of declarative material.
Whether it applies to kubectl troubleshooting is not established. The
recommendation follows it anyway, and the journal records the evidence to
check it: seeds, hints, times and trajectories per episode.

## Mastery is a heuristic

A scenario or concept counts as mastered after 3 clean solves among its last
4 attempts (rules in [study-flow.md](study-flow.md)). The rule is adapted
from N-correct mastery detectors for short items. It is not validated for
troubleshooting; the journal records what is needed to test it.
