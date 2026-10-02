---
title: "Ledger and estimation: the records, the derived roles of answers, and the error rate of any decision source"
status: drafted
track: core
depends_on: [001-architecture.md, 002-contract.md]
affects: [estimate/, stub/, engine/, reliability.go, answer.go]
effort: medium
created: 2026-10-02
updated: 2026-10-02
author: changkun
---

# Ledger and estimation

## Overview

An action a decision point lets run on its own never receives an
answer, so its error rate cannot be counted; it can only be estimated,
and only from answers to actions that a person saw with a known
probability (paper, Propositions 5 and 8). The ledger keeps exactly what
that estimate needs: every decision with the probability, fixed before
the action ran, that a person sees it, and every answer. From those it
reports, for any source of decisions and any person and window, how
often the source allows what the person would deny, how much of the
source's behavior the estimate covers, and whether a stated claim holds.
The same arithmetic serves Cribrum's own predictions, the verdicts a
decision point applied, a rule, and an LLM judge. `estimate/` holds it,
shared by the engine and the stub so that both report the same numbers.

## Design

### Records

A case holds one action (without its input), the prediction if one was
made, the decision, and the answers, keyed by (org, subject,
action_id). The decision's `review_probability` is the probability that
a person sees the action: one for an `ask` and for a `flag` the decision
point's rules force, the audit rate for an automatic verdict under random
review whether or not the draw picked it, zero when nothing samples it.
`verdict.Decide` returns exactly this.

### Roles of answers

An answer's role is derived from the case, never sent by a caller:

- the first answer to a case whose decision was shown (`flag` or `ask`)
  is the case's **review**, `RecordedAnswer.review = true`;
- every other answer trains and never estimates: an answer to a case
  whose decision was not shown (a dispute of an action that ran, which
  nothing sampled), and any answer after the review.

The model learns each case's latest answer. A case is **pending** while
its decision is shown and it has no review; the pending cases are the
review queue.

### Opinions of a source

A reliability query names a source:

| `of` | The source's verdict on a case |
|---|---|
| `learned` | the prediction's `verdict` |
| `applied` | the decision's `verdict` |
| a decision source, such as `rules/1` | the decision's `verdict`, for decisions with that `source` |
| a signal source, such as `classifier/claude-auto-mode` | the action's signal verdict from that source |

A source **allowed** a case when its verdict was `allow` or `flag`: the
action ran with no one's prior approval. It **blocked** a case when its
verdict was `block`, or, for a decision verdict, `ask` with a review
probability below one, which is an automatic block the review draw held
for a person. Counting the drawn cases is what puts the reviews inside
the denominator: an audited allow is recorded as `flag` and an audited
block as `ask`, and a source's rates computed over its plain `allow` and
`block` verdicts alone would never contain a review. A source with
scores and no verdicts allows and blocks nothing, so its rates are not
estimable.

### The estimate

For a source `S`, a subject, a window `[from, to)` of receive times, and
the false-allow side (the false-block side is the same with blocked
cases and approvals counted as errors):

- `A` is the set of cases in the window that `S` allowed, and `A+` the
  subset whose decision had a positive review probability. `covered` is
  `|A+| / |A|`, and 0 when `A` is empty.
- When `A+` is empty, or `A+` holds shown cases and none of them has a
  review yet, the value is null: the rate is not estimable, which is not
  zero.
- Each case `i` in `A+` that has a review contributes the weight
  `w_i = 1 / (p_i r)` and the error `e_i`, 1 when the review denied the
  action. `p_i` is the case's review probability; `r` is the share of
  the shown cases in `A+` that have a review, each case weighted by
  `1/p_i`, so that cases shown and not yet answered are treated as
  missing at random rather than as approvals. With every shown case
  answered, `r` is one.
- The value is `sum(w_i e_i) / |A+|`, the Horvitz-Thompson estimate over
  the covered cases, clipped to [0, 1].
- The interval is the Wilson 95% interval for the value at the Kish
  effective sample size `n_eff = (sum w_i)^2 / sum(w_i^2)`; with no
  review it is [0, 1].
- `reviews` counts the reviewed cases in `A+`, `audits` those among them
  with `p_i < 1`, and `unanswered` the shown cases in `A+` with no
  review.

When the decision point's audit rate is constant and every shown case is
reviewed, the value is the paper's estimator exactly: the denied audits
divided by the rate, over the source's allows.

### The certificate

The claim is the effective profile's `certify` for the organization:
a false-allow rate below `alpha` at confidence `1 - delta`. `needed` is
`CertificationSize(alpha, delta)`. `clean` counts the source's most
recent audited allows in the window (reviewed cases in `A+` with
`p_i < 1`), newest first, that the person approved, stopping at the
first denial; `holds` is `clean >= needed`. Forced reviews (`p_i = 1`)
do not count toward a certificate, because the claim is about the
actions nobody was asked about.

### Replay

A replay applies the same arithmetic to recorded history. It orders the
records by `at`, predicts each before learning its answer, counts
Cribrum's would-be verdicts, and estimates the false-allow rate of
Cribrum's would-be allows from the recorded review probabilities, with
`covered` saying how much of that the history can speak for. With
`keep`, the records enter the ledger and the models as if they had
arrived live, each recorded answer becoming its case's review when its
recorded verdict was shown.

## Acceptance criteria

| # | Criterion | Proved by |
|---|---|---|
| 1 | The first answer to a shown case is its review and later answers are not; an answer to an unshown case is never a review | `estimate/` tests and the conformance suite |
| 2 | With a constant audit rate and every shown case answered, the value equals denied audits over the rate over the source's allows | an `estimate/` test against the hand-computed value |
| 3 | The estimate is unbiased: over many simulated draws with a known false-allow rate, the mean estimate is within three standard errors of the truth | a seeded Monte Carlo test in `estimate/` |
| 4 | A window whose allows all had probability zero reports `value: null` and `covered: 0` | `estimate/` and conformance tests |
| 5 | `unanswered` counts shown cases without a review, and they change the value only through `r` | an `estimate/` test |
| 6 | A certificate counts only audited allows, stops at the first denial, and holds at `needed` | an `estimate/` test at the 300-review boundary |
| 7 | The engine and the stub return identical reports for identical ledgers | a test running both on one scripted history |
