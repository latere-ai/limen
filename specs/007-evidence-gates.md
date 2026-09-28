---
title: "Evidence gates: replay, shadow, learned thresholds, judge signals, and what stops a rollout"
status: drafted
track: core
depends_on:
  - specs/004-engine.md
  - specs/005-adapters.md
affects: [docs/integrating.md]
effort: small
created: 2026-09-28
updated: 2026-09-28
author: changkun
---

# Evidence gates

## Overview

Every result behind Limen's design comes from a controlled simulation
with a known oracle ([arXiv:2605.19151](https://arxiv.org/abs/2605.19151)).
Recovering the oracle shows the inference is right under the model; it
does not show that real people approve and deny the way the model
assumes, that their tolerance drifts the way the simulation's does, or
that they stay reliable as ground truth when they are asked often. The
paper names a longitudinal study of real supervisors as its main gap.

This spec is the rule every deployment follows so that Limen earns each
step with evidence from the step before: four gates, each with the
evidence it must produce, the criteria that pass it, and the conditions
that stop a rollout at any step. Nothing here is built; it is the order
the other specs are switched on in, and the criteria an operator writes
down before switching.

## Design

### Pre-registration

Before a gate's data is read, the operator writes down the cohort (the
subjects, who opt in, since their answers are theirs), the cost profile,
the audit rate, the window, and the numbers that pass the gate, and
commits them. The research study that preceded Limen was run this way,
and it is what keeps a gate from being passed by choosing its criteria
after seeing the result. A gate evaluated against criteria written after
its data does not pass.

### Gate 1: replay

**Runs.** `Admin.Replay` over a recorded history from the harness: every
call with the verdict the harness applied and the person's answer where
there was one ([[003-http-api]]). Nothing is applied.

**Produces.** The report of [[002-contract]]: calls, answered calls,
what Limen would have decided, the answered calls it would have allowed
and the person denied, and the false-allow estimate with its coverage.

**Passes when**, for the cohort: the share Limen would hold for a person
is below the harness's own ask share by the pre-registered margin; the
answered calls Limen would have allowed and the person denied are at or
below the pre-registered count; and the coverage is stated. A replay
cannot measure the false-allow rate of calls the harness allowed without
asking, so a low coverage passes this gate but moves the question to the
next one rather than answering it.

### Gate 2: shadow

**Runs.** Limen assesses every live call of the cohort beside the
harness's current rule, and its verdict is recorded and not applied. The
one change a person sees is the audit: a sampled share of the current
rule's automatic allows is shown to them, so that both the current
rule's false-allow rate and Limen's would-be false-allow rate are
estimable from the same answers.

**Produces.** Per subject, over the window: Limen's would-ask share
against the current rule's ask share; both false-allow estimates with
their intervals; the certificate state; and how both behave across a
change the subject makes (a new repository, a new kind of work), which
is the drift the model is built for. This is the longitudinal study of
real supervisors the research lacks, and its data is the first that can
confirm or refute the simulation.

**Passes when**, for the cohort: the upper end of Limen's false-allow
interval is at or below the current rule's estimate plus the
pre-registered non-inferiority margin; Limen's would-ask share is below
the current rule's by the pre-registered margin; the certificate holds
for the pre-registered share of subjects; and no assessment exceeded its
ceiling.

### Gate 3: learned thresholds

**Runs.** For the cohort, the harness applies Limen's verdict inside its
deterministic rules (in Topos, `progressive` mode with a `learned/*`
source, [[005-adapters]]). Audits continue at the pre-registered rate.

**Produces.** The same per-subject reports, now for verdicts that are
applied, and the burden the cohort actually carries.

**Passes when** the gate 2 criteria hold on applied verdicts over the
window, and no stop condition fired.

### Gate 4: judge signals

**Runs.** The harness passes its LLM judge's verdict as a signal
([[004-engine]], the `judge` featurizer), and the `patterns` featurizer
runs beside it.

**Produces.** The judge's own false-allow and false-block rates per
subject and per window (`of=judge/...`), and Limen's reports with and
without the signal.

**Passes when** Limen with the signal is non-inferior on false-allow and
lower on burden than Limen without it, by the pre-registered margins,
and a pre-registered set of commands the judge is known to misjudge is
never allowed without a person. The second criterion exists because
calibrating on a judge's verdict cannot repair a blind spot the features
do not expose; the paper's simulated blind spot let 23 of 29 dangerous
calls through.

### What stops a rollout

Any of these returns the affected subjects, or the whole cohort, to the
harness's current rule at once, at any gate:

| Condition | Scope | Why |
|---|---|---|
| an assessment more permissive than its ceiling | the cohort | a broken invariant, not a statistical event |
| an assessment returned `allow` after a failure | the cohort | fail-open, the one failure the design exists to prevent |
| a flagged or audited record without an audit probability | the cohort | every later estimate from it is biased |
| the lower end of a subject's false-allow interval above the profile's `certify.alpha` | the subject | Limen is allowing, for that person, more than the organization stated it tolerates |
| a subject approving asks at a rate above the pre-registered bound over the window | the subject | a person who approves almost everything is no longer ground truth, and a model calibrated to rubber-stamping learns to rubber-stamp |
| a subject asking to be forgotten | the subject | `Forget`, and their answers leave every estimate |

A stop is recorded with the condition, the time and the subjects, and a
rollout resumes only through the gate it stopped at, with its criteria
unchanged.

## Acceptance criteria

| # | Criterion | Proved by |
|---|---|---|
| 1 | Each gate's pre-registration exists and is committed before the gate's first assessment | the deployment's record, reviewed at each gate |
| 2 | A replay report states calls, answered calls, the would-be verdict counts, the allowed-but-denied count, and coverage | the `ReplayReport` of [[002-contract]] and a `limend` e2e test |
| 3 | In shadow, no Limen verdict changes what the harness does, and the audits are the only calls shown to a person that the current rule would not have shown | an adapter test in [[005-adapters]] with shadow enabled |
| 4 | Every stop condition is detectable from the store and the reports alone | a test in `engine/` per condition over a scripted history |
| 5 | A subject returned to the current rule by a stop receives no Limen verdict until the gate is passed again | an adapter test |
