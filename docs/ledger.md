# The ledger

The ledger records two facts about each action: what the decision point
decided, and what the person answered. From these records, Limen reports
how often each source of decisions is wrong for a given person. This page
tells you what Limen records and what each field means. It also tells you
how Limen calculates the numbers in a reliability report.
[Theory](theory.md#the-ledger-error-rates-of-any-source) gives the
mathematics.

The ledger does not need the predictions of Limen. A decision point that
decides only with rules can record its decisions and the answers. It can
then read the error rate of its rules and of its judge.

## What Limen records

Each record is a case. Three fields identify a case:

| Field | Meaning |
|---|---|
| `org` | The organization that the records belong to. |
| `subject` | The person for whom the action runs. Limen learns the approvals of this person. |
| `action_id` | The identifier that the decision point gives the action. It is unique in the organization and for the person. |

A case has up to four parts. Limen writes each part one time only.

1. **The action.** This is the action as the decision point saw it before
   it ran, without its input. Limen reads the input to calculate the
   features. Limen does not keep the input.
2. **The prediction.** A case has a prediction only if the decision point
   asked Limen for one.
3. **The decision.** This is the applied verdict, the probability that a
   person sees the action, and the source that decided.
4. **The answers.** These are the answers that the person gave. For each
   answer, Limen records the time when it arrived and who gave it.

Record a decision for each action that your decision point decides. Do
not record only the actions that a person sees. The allows of a source
are the denominator of its error rate. If the ledger gets only the `ask`
decisions, it cannot calculate an error rate.

## The review probability

The `review_probability` of a decision is the probability that a person
sees the action. The decision point sets it before the action runs.
`verdict.Decide` in `latere.ai/x/pkg/verdict` calculates it together with
the verdict:

| What occurred | Verdict | `review_probability` |
|---|---|---|
| Your rules deny the action (the ceiling is `block`). | `block` | 0 |
| The action waits for a person. | `ask` | 1 |
| Your rules require that a person examines the action after it runs. | `flag` | 1 |
| Automatic allow, and the random selection selected it for audit. | `flag` | the audit rate |
| Automatic allow, and the random selection did not select it. | `allow` | the audit rate |
| Automatic block, and the random selection selected it for audit. | `ask` | the audit rate |
| Automatic block, and the random selection did not select it. | `block` | the audit rate |
| Automatic verdict, and the audit rate is 0. | `allow` or `block` | 0 |

Two rows give the same probability for a selected action and for an
action that was not selected. This is correct. The probability is the
value before the random selection. It does not tell if the selection
selected the action.

The decision point makes the random selection one time for each action.
Assume that a decision point makes the selection again until the action
is not selected. Then all estimates from the records have a bias. For this
reason, an action can have only one decision.

The ledger makes sure that a verdict and its probability agree. A person
sees a `flag` or an `ask`, so its probability must be more than 0. Nobody
sees an `allow` or a `block`, so its probability must be less than 1.

## How answers count

A decision can show an action to a person (a `flag` or an `ask`). The
first answer to such an action is the review of the action. A review goes
into the error estimates with a weight of 1 divided by the review
probability of the decision. Examples:

- The answer to an `ask` has a weight of 1.
- With an audit rate of 5%, the answer to an audited `allow` has a weight
  of 20. It counts for itself and for the 19 other allows that nobody
  examined.

This weight comes from the review probability only. The weight of an
answer, from how the person gave it, is a different number. Only the
[cover rule](theory.md#the-cover-rule) reads it, and no estimate reads
it.

All other answers teach the model, but they never go into an estimate.
Examples:

- A person disputes an action that ran without a question to the person.
- A person answers after the review.
- A person changes an earlier answer.

Nothing set the probability that such an answer occurs. For this reason,
there is no weight to give it. The model learns from the most recent
answer.

Limen gets the role of each answer from the recorded decision. For this
reason, an answer needs a recorded decision first. If an action has no
recorded decision, Limen refuses its answer with `no_decision`.

## Sources

A reliability report is about one source of verdicts:

| `of` | The verdict of the source for an action |
|---|---|
| `learned` | The suggested verdict of Limen, from the prediction |
| `applied` | The verdict that the decision point applied, from any source |
| The `source` of a decision, for example `rules/1` or `topos/progressive` | The applied verdict, for the actions that this source decided |
| The `source` of a signal, for example `classifier/claude-auto-mode` | The verdict of the signal, for the actions that have this signal |

The allows of a source are the actions for which it gave `allow`. Its
blocks are the actions for which it gave `block`. A signal that has only a
score has no allows and no blocks. Its rates are always `null`.

Give each source a name in the form `<kind>/<name>`, and do not change the
name. A name can have more parts. For example, Topos and a replay of
Topos logs name a decision `topos/confirm/always_allow`: the mode of
Topos and the type of rule that decided. The ledger reports under the
full name, for each person and each period of time. A report for
`topos/confirm` does not include the decisions of
`topos/confirm/always_allow`.

## Reading a report

```json
{
  "org": "org_7",
  "subject": "https://issuer.example|usr_42",
  "of": "classifier/claude-auto-mode",
  "from": "2026-09-01T00:00:00Z",
  "to": "2026-10-01T00:00:00Z",
  "false_allow": {"value": 0.10, "interval": [0.03, 0.25], "covered": 0.96, "reviews": 24, "audits": 9, "unanswered": 1},
  "false_block": {"value": 0.49, "interval": [0.31, 0.67], "covered": 1, "reviews": 31, "audits": 2, "unanswered": 0},
  "certificate": {"alpha": 0.01, "delta": 0.05, "clean": 112, "needed": 300, "holds": false}
}
```

| Field | Meaning |
|---|---|
| `value` | The estimated part of the allows of the source that the person would deny. In `false_block`, it is the part of the blocks that the person would approve. If a person could see none of these actions, the value is `null`. `null` means unknown. It does not mean zero. |
| `interval` | A 95% interval for the value. Read the interval before the value. A period with few reviews gives a wide interval. |
| `covered` | The part of the allows (or blocks) of the source that had a review probability more than 0. The estimate applies only to this part. |
| `reviews`, `audits` | `reviews` is the number of reviews that the estimate uses. `audits` is the number of these reviews that came from the random selection. |
| `unanswered` | The number of actions that a decision showed to the person and that have no review yet. The estimate treats these answers as missing at random: it assumes that the chance of a missing answer does not depend on the answer. If people do not answer the difficult actions, this assumption is false. |
| `certificate` | A test of the claim of the profile: the false allow rate is below `alpha`, with a confidence of `1 - delta`. `clean` is the number of the most recent consecutive audited allows that the person approved, in the period. A denied allow stops the count. `needed` is the number that the claim needs. `holds` is true if `clean` is equal to or more than `needed`. |

The numbers above are only examples. The rates apply only to this person
and to this period. The same judge can be more reliable or less reliable
for a stricter person or for a more permissive person. It can also change
for the same person, after the tolerance of the person changes.

In the simulation of the paper, the tolerance of one person suddenly
decreased (a trust reset). The judge did not change. Its false allow rate
for this person went from 2.9% to 10.0% after the reset. Then it went
back to 3.4%.

## The calibration report

The calibration report uses the same estimate for a different question.
It asks if the approval probabilities of Limen are true for the person.
It puts the predictions of Limen in ten bins of the approval probability.
For each bin, it compares the mean approval probability with the
approval rate that the reviews measured. Each review counts with the
weight 1 divided by its review probability, as above. A bin with an
effective size less than 20 is `not_estimable`, and its rate is `null`.

Audits are the only reviews of the bins above the allow threshold and
below the block threshold. If your audit rate is 0, these bins stay
`not_estimable`. The [API](api.md#calibration) describes the fields, and
[Theory](theory.md#calibration) gives the reasons.

## Mistakes that bias the record

- **Record each decision, not only the actions that a person sees.** If
  you record only these actions, the allows of the source are missing. Its
  error rate then has no denominator.
- **Make the random selection one time only.** A sample that you can
  select again is not random.
- **Record the probability that you used.** Assume that your code shows
  each action with a given property to the person. Then the probability
  of these actions is 1, for all audit rates in the profile. Record what
  your decision point did.
- **Record the decision before the action runs.** Also record it before a
  person sees the action. Then the record cannot depend on the result.
- **Answer the actions in the review queue.** If the `unanswered` count
  increases, each estimate becomes weaker.
