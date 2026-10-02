# The ledger

Limen's ledger records what decision points decided and what people
answered, and from those two facts it reports how often any source of
decisions is wrong for a given person. This page says what is recorded,
what each field means, and how the numbers in a reliability report come
out of the records. The mathematics is in [Theory](theory.md#the-ledger-error-rates-of-any-source).

The ledger does not need Limen's predictor. A decision point that decides
by rules alone can record its decisions and the answers, and read its
rules' error rate and its judge's.

## What is recorded

Every record is a case, keyed by three fields:

| Key field | |
|---|---|
| `org` | the organization the records belong to |
| `subject` | the person on whose behalf the action runs and whose approvals are learned |
| `action_id` | the decision point's own id for the action, unique within the organization and subject |

A case holds up to four things, each written once:

1. **The action**, as the decision point observed it before it ran, without
   its input: Limen reads the input to extract features and never keeps it.
2. **The prediction**, when the decision point asked Limen for one.
3. **The decision**: the verdict applied, the probability that a person sees
   the action, and the policy that decided.
4. **The answers** the person gave, each stamped by Limen when it arrived,
   with who gave it.

A decision point records a decision for every action it decides, not only
for the actions a person sees. The actions a source allowed are the
denominator of that source's error rate; a ledger that only hears about
asks cannot compute one.

## The review probability

A decision's `review_probability` is the probability, fixed before the
action ran, that a person sees it. `verdict.Decide` in
`latere.ai/x/pkg/verdict` computes it together with the verdict:

| What happened | Verdict | `review_probability` |
|---|---|---|
| your rules deny the action outright | `block` | 0 |
| the action is held for a person | `ask` | 1 |
| your rules require a review after it runs | `flag` | 1 |
| automatic allow, picked by the random review | `flag` | the review rate |
| automatic allow, not picked | `allow` | the review rate |
| automatic block, picked by the random review | `ask` | the review rate |
| automatic block, not picked | `block` | the review rate |
| automatic verdict, no random review | `allow` or `block` | 0 |

Two rows say the same probability for a picked and an unpicked action, and
that is the point: the probability is what the action had before the draw,
not whether the draw picked it. The draw is made once per action. Drawing
again until the review misses would make every estimate built on the
records biased, which is why an action takes one decision.

The ledger checks that a verdict and its probability agree: a person sees a
`flag` or an `ask`, so its probability is positive; nobody sees an `allow`
or a `block`, so its probability is below one.

## How answers count

The first answer to an action whose decision showed a person (a `flag` or
an `ask`) is that action's review. It enters error estimates with weight
one over the decision's review probability: an ask's answer counts once, an
answer to a randomly reviewed allow at a 5% rate counts twenty times,
standing in for the nineteen allows nobody looked at.

Every other answer teaches the model and never enters an estimate: a
dispute of an action that ran without anyone being asked, an answer after
the review, a person changing their mind. Nothing fixed the probability
with which such an answer is given, so there is no weight to give it. The
model learns the latest answer.

Limen derives each answer's role from the recorded decision, so an answer
needs a recorded decision first: an answer for an action with none is
refused with `no_decision`.

## Sources

A reliability report is about one source of verdicts:

| `of` | The source's verdict on an action is |
|---|---|
| `learned` | Limen's suggestion, from the prediction |
| `applied` | the verdict the decision point applied, whatever decided it |
| a decision's `source`, such as `rules/1` or `topos/progressive` | the verdict applied, on the actions that policy decided |
| a signal's `source`, such as `classifier/claude-auto-mode` | the signal's verdict, on the actions that carried one |

A source's allows are the actions on which it said `allow`, and its blocks
the actions on which it said `block`. A signal that only ever carries a
score has no allows and no blocks, so its rates are always `null`.

Name each source as `<kind>/<name>` and keep the name stable: the ledger
reports under that name, per person, per window.

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

| Field | |
|---|---|
| `value` | the estimated share of the source's allows (or blocks) the person would have denied (or approved); `null` when no such action could have been seen, which means unknown, not zero |
| `interval` | a 95% interval; read it before the value, since a window with few reviews gives a wide one |
| `covered` | the share of the source's allows (or blocks) that had any chance of being seen; the estimate speaks for that share only |
| `reviews`, `audits` | the reviews the estimate rests on, and how many of them came from random review rather than an ask |
| `unanswered` | actions shown to the person with no review yet; the estimate assumes they would have gone the way the answered ones did, which is doubtful if people skip the hard ones |
| `certificate` | the profile's claim, a false-allow rate below `alpha` at confidence `1 - delta`: `clean` consecutive clean random reviews of the source's allows in the window against the `needed` ones, and whether the claim `holds` |

The numbers above are illustrative. The rates hold for this person and
this window only: the same judge is more or less reliable for a stricter or
a more permissive person, and for the same person after their tolerance
changes. In the paper's simulation, one unchanged judge's false-allow rate
for one person went from 2.9% to 10.0% after the person's trust reset, and
back to 3.4%.

## Mistakes that bias the record

- **Recording only what a person sees.** The source's allows are then
  missing, and its error rate has no denominator.
- **Drawing the review more than once.** A sample that can be re-drawn is
  not random.
- **Recording a probability you did not use.** If your code shows every
  action with a certain flag to the person, its probability is one, whatever
  the profile's review rate says. Record what your decision point actually
  did.
- **Recording after the fact.** Record the decision before the action runs
  or anyone sees it, so the record cannot depend on the outcome.
- **Leaving the review queue unanswered.** A growing `unanswered` count
  weakens every estimate.
