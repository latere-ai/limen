# Principles

This page gives eighteen rules that each implementation of Limen obeys.
For each rule, the page gives what is true, the reason, and the check.
The conformance suite in `conformance/` checks most rules. This suite runs
against each implementation of the contract. The tests of the shared
verdict package (`latere.ai/x/pkg/verdict`), of the contract, or of the
engine check the other rules. [Theory](theory.md) and the paper that it
cites give the reasons for the rules.

The [Terms](terms.md) page gives the meaning of each term on this page.

## 1. Limen suggests, and the decision point decides

Limen predicts and suggests a verdict. The decision point is the code that
lets an action run. It combines the suggested verdict with its own rules,
applies the verdict, and records what it did.

**Why.** Only the code that applies a verdict knows which verdict it
applied. Many things can change the verdict. Examples are a rule that made
the verdict stricter, a hook, and an earlier "always allow this" from the
person. Another example is a trial period in which the decision point only
records the suggestions of Limen.

If Limen also decided, its records would describe what Limen would do.
Each error rate from these records would then be wrong when the decision
point did a different thing. Because the decision point keeps the
decision, the record always shows what occurred. This also lets an agent
harness use the ledger of Limen with no predictions at all.

**Checked by.** The contract. A prediction has only a suggested verdict
(`allow`, `ask`, or `block`). A decision is a separate record that the
decision point sends.

## 2. An unclear action waits for a person

If the evidence about an action is not clear, the action waits for a
person. The verdict is `ask`. Limen does not refuse the action.

**Why.** An agent does not stop when it gets a refusal. It reads the
refusal as a result and tries a different action. Frequently, that action
is worse than the denied action. A question to a person at the correct
time costs one `ask`. An agent that goes around a block can cost much
more.

The paper simulated an LLM judge. In one test, the blocks of the judge
became `ask` verdicts, and the decision point did not apply them as
blocks. This decreased the cost-weighted regret from 0.296 to 0.088.
Cost-weighted regret is the mean cost of the decisions, minus the mean
cost of an oracle that knows the true approval probability. Its unit is
the cost of one `ask`.

**Checked by.** The conformance suite checks that Limen suggests `ask`,
never `block`, for an approval probability in the ask range.

## 3. The costs set the thresholds

You state three costs: the cost of a false allow, the cost of a false
block, and the cost of one `ask`. A closed formula calculates the
thresholds from these costs. Nothing adjusts the thresholds to fit data.

**Why.** Limen suggests `allow` above the upper threshold, `ask` between
the thresholds, and `block` below the lower threshold. With the three
costs, this rule has the lowest expected cost if, and only if, the
thresholds are `allow_above = 1 - ask / false_allow` and
`block_below = ask / false_block`. This is the reject option of Chow.

To adjust the thresholds to data, Limen would need answers for the
actions that got an automatic verdict. These actions get answers only
from audits, and audits are few: with the default cost profile, one
action in twenty. A cost profile also records how much risk an
organization accepts, in a form that all people can read.

**Checked by.** The tests of the contract compare the formula with the
expected costs on a grid of probabilities. The conformance suite checks
that the thresholds of each prediction are the thresholds that the costs
of its profile give.

## 4. A person without answers gets `ask`

Limen refuses a cost profile that would let it allow or block an action
for a person who has no answers. The cost of a false allow and the cost of
a false block must each be more than two times the cost of one `ask`.

**Why.** If Limen has no answers from a person, the approval probability
is one half. The ask range contains one half if, and only if,
`false_allow > 2 * ask` and `false_block > 2 * ask`. With other costs,
Limen would decide the first action of a new person with no evidence at
all. If you try to save such a profile, Limen returns the error
`ask_too_costly`.

**Checked by.** The tests of the contract, and the conformance suite. The
suite checks that the first prediction for a person is `ask` with each
profile that Limen accepts.

## 5. A `block` from your rules is final

The rules of your decision point set a ceiling. The ceiling is the verdict
that permits the most and that the rules accept. No suggested verdict can
go above the ceiling. If the ceiling is `block`, the verdict is `block`.
No person examines the action, and nothing changes the verdict.

**Why.** Rules that you wrote, for example "never push to a protected
branch", are commitments, not evidence. A learned model works only inside
the limits that these rules leave open. `verdict.Decide` combines a
suggested verdict with the ceiling and takes the one of the two that
permits less. It treats a ceiling of `block` as final: the verdict is
`block`, and the review probability is zero.

**Checked by.** The tests and the fuzz tests of
`latere.ai/x/pkg/verdict`. No input gives a verdict that permits more than
the ceiling.

## 6. If Limen fails, Limen never allows the action

If Limen fails, is slow, or is not available, the suggested verdict is
`ask`. Nothing allows the action because Limen failed. With the usual
composition, the action waits for a person.

**Why.** A check that permits all actions when it fails is not a check.
`FailClosed` sets a limit on the time that a decision point waits for a
prediction. It changes each error or timeout into a suggested verdict of
`ask`, with the source `learned/unavailable`. The ceiling can only make
this verdict stricter. Then the decision point records the decision with
the action attached, and the answer of the person still teaches the model.

The fallback is an `ask` with the kind `unsure`. A decision point that
lets its rules decide an `unsure` ask also lets them decide when Limen
fails ([Integration](integrating.md#kinds-of-ask)). Then the action
gets the verdict that the rules gave before Limen, never more than the
ceiling.

**Checked by.** The tests of the contract check `FailClosed`. The
conformance suite checks that an implementation reports each failure to
the caller as an error, never as a suggested verdict.

## 7. Some actions always need a person

If a person is truly undecided about a kind of action, Limen continues to
suggest `ask`. This does not change, however much Limen learns.

**Why.** If a person approves a kind of action about half of the time, no
model can predict the answer. With most costs, `ask` is then the choice
with the lowest cost. Because of this, some actions must go to the
person, even with a perfect model that takes the verdict of least
expected cost. The person and the costs set the size of this part, not
Limen. A policy can ask less only if it decides some of these actions at
a higher expected cost.

In the simulation of the paper, this part was 10.3% of the actions with
symmetric costs. It was 31.3% with the default costs, which make a false
allow ten times and a false block four times the cost of one `ask`. A promise of "95% automation" is a claim about the
person, not about the product.

**Checked by.** This rule is a result, not a rule to test. It is true
because only the cost profile sets the thresholds (rule 3).

## 8. Tolerance changes, risk does not

The model of Limen lets the tolerance of a person change with time. It
does not let the risk of an action change with time. Because of this, it
keeps what it learned about the risk of each kind of action.

**Why.** People change. Trust increases during a project and decreases on
a new project. The risk of a force push to production does not change.
The usual model with a decay in time forgets both. As time passes, that
model moves each action back to "not sure", and it asks again about
actions that it knew.

In the simulation of the paper, the model with the static part was
compared with the model that forgets both. With symmetric costs, the part
of the actions that a person had to answer decreased from 28.4% to 13.5%. The cost-weighted
regret decreased by 59%, at a false allow rate of about 1%. Limen counts
time in decisions, for each person, and keeps the count itself. Each
action that Limen predicts or that the decision point records adds one,
also without an answer.

**Checked by.** The tests of the engine. They compare the engine with
fixtures from the research implementation.

## 9. An automatic verdict is not an answer

An action with an automatic verdict never gets an answer. To know the
error rate of automatic verdicts, the decision point shows a small random
part of them to the person. It sets the probability that a person sees
each action before the action runs. It makes the random selection one
time and records the probability with the decision. Each answer then
counts with a weight of one divided by that probability.

**Why.** An error rate from only the actions that a person saw has a bias,
because these actions were already unclear. Answers with known
probabilities give an estimate without bias (the Horvitz-Thompson
estimator). If no action in a period of time could go to a person, the
report gives the rate as `null`. `null` means that Limen cannot estimate
the rate. It never means zero.

If a person sees an action and nobody answers it, the report counts the
action as `unanswered`. The report does not drop the action. An estimate
that silently skipped the difficult reviews would look better than it is.
A certificate that the false allow rate is below 1% with 95% confidence
needs 300 consecutive audits without a denial.

**Checked by.** The tests of `verdict.Decide` check that the probability
that it returns is the frequency with which a person sees an action. The
conformance suite checks that the estimates use the recorded
probabilities. It also checks that the rate is `null` if no action could
go to a person.

## 10. One prediction and one decision for each action

The key of each record has three parts: the organization, the person, and
the identifier that the decision point gives the action. A second
prediction for the same action returns the first prediction. Limen refuses
a prediction for a different action with the same identifier. Each action
has one decision.

**Why.** Without this rule, a decision point could predict again until it
got the suggested verdict that it wanted. It could also record a second
decision to avoid a review. In both cases, the sample behind each estimate
would not be random. The key makes both attempts visible, as the errors
`action_conflict` and `already_decided`.

**Checked by.** The conformance suite checks that a second prediction is
the same as the first. It also checks that Limen refuses a different
action with the same identifier, and that Limen refuses a second decision.

## 11. A judge gives evidence, and its error rate is different for each person

The verdict of an LLM judge, or the score of any rule, goes into Limen as
a `signal`. A signal is an input to the prediction of what this person
would answer. Limen reports the error rate of each source for each person
and each period of time.

**Why.** The verdicts of a judge alone do not give its error rate for a
person. The error rate also depends on the decisions of the person. It
changes when the tolerance of the person changes, even if the judge does
not change.

In the simulation of the paper, the same judge did not change. Before a
sudden decrease in the trust of the person, it allowed actions that the
person would deny in 2.9% of the cases. After the decrease, this rate was
10.0%. Later, it was 3.4%.

**Checked by.** The conformance suite checks that Limen reports the error
rates for each source and for each person. It also checks that the allows
of a source are only the actions for which the source said `allow`.

## 12. Later answers teach the model and do not go into error rates

The first answer to an action that the decision showed to a person is the
review of that action. Each other answer teaches the model and never goes
into an error estimate. Examples are a dispute about an action that ran
without a question to anyone, and a person who changes their mind.

**Why.** Nothing set the probability that a later answer occurs. Because
of this, Limen cannot give a later answer a weight in the way that it
weights a review. If Limen counted later answers, the estimate would move
toward the actions that people happen to notice. Limen gets the role of
each answer from the recorded decision. The caller does not state the
role.

**Checked by.** The conformance suite checks that a later answer changes
no estimate, and that it changes what the model learns.

## 13. No language model reads the input, and Limen does not keep it

The agent writes the input of an action. Only feature extractors read the
input. A feature extractor is a fixed program that calculates features
from the input. An example is an extractor that finds a recursive delete
in a shell command. No language model in Limen reads the input. Limen does
not store the input: it keeps the features and the action without its
input.

**Why.** If a model in Limen read the input, the agent could write
instructions to Limen. Attackers have used this method against judges
that read the transcript. Limen takes the verdict of a judge as a signal. It
never takes the text that the judge read.

But the judge reads the input that the agent wrote, so an agent can
change a signal through the judge. The ledger gives the error rate of
each judge for each person, as for each source.

Because Limen does not keep the input, an agent harness can remove a
secret from a command, and no copy in Limen keeps the secret. Limen keeps
the other fields of the action: the kind, the name, the effect, the
target, the environment, the attributes, and the signals. Do not put a
secret in these fields.

To find a first contact, Limen compares the `id` of the target with the
targets of the approved actions of the person. Because of this, Limen
keeps that `id`. If the ledger must not keep a path or a host, send an
opaque `id`. Use the same `id` for each action on the same target.

**Checked by.** The `depcheck` gate in `.lateregate.yaml`. The engine has
the feature extractors, and it gets only the standard library, the
numerical library, and the shared verdict package. No model client is
among them. The conformance suite also checks that a case that Limen
returns has no input.

## 14. The data of a person belongs to that person

Limen uses the answers of a person only for that person. On request, it
forgets all data about the person in an organization. The data are the
cases, the answers, the model that it learned from them, and the cost
profile of the person. The report of an organization never shows one person. Each of its
cells shows numbers only if 5 or more persons took part in it. A number
that Limen calculates from reviews also needs reviews from 5 or more
persons, and no person gave more than one half of them. The counts of
persons and of decisions in a cell obey the first rule only.

**Why.** The history of approvals and denials of a person shows how that
person works. The person chooses if Limen uses it. The deletion must be
complete, and a repeated deletion must give the same result. A kept week
of a report stays after a forget. It contains no record of a person, and
a change would show the numbers of the person who left.

**Checked by.** The conformance suite checks the state after Limen forgets
a person in an organization. No case, no action that waits for a review,
no reliability report, and no cost profile for that person remains in
that organization. It also checks the group rule and the answer rule of
the report, and that a forget does not change a kept week.

## 15. No allow beyond what the person allowed

Limen suggests `allow` for an action only if the approvals of the person
cover it. An approval covers an action of the same family whose scope is
within the scope of the approval. The scope tells if the action can be
undone, the boundaries that it crosses, and the class of its target. In
each family of the action, the covering approvals must have a weight of 3
or more, from 2 sessions or more. These are the default numbers. If the
approval probability is above the upper threshold and the action is not
covered, Limen suggests `ask`. This rule applies after the model and the
thresholds. It never changes a `block` or an `ask`.

**Why.** With few answers, the approval probability can already be above
the upper threshold. One approval of `publish` gives 0.73. With the
upper threshold 0.65, the next `publish` would run without a person
after one answer. Also, an approval of an update is not an approval to
make an app public. The model does not know this difference, because it
compares actions by their features. Approvals from one session can come
from one task only.

**Checked by.** The conformance suite checks that an `allow` becomes
`ask` until the approvals cover the action, and that the prediction gives
the cover. It also checks that a denial, a replaced approval, an approval
of a different family, and a quick approval never count. It checks that
an action without a session counts in the session of its day, and that a
`block` does not change. The tests of the engine check the same for its
predictions, explanations, entries, and replays.

## 16. A quick approval is not consent

An answer carries how the person gave it: the time, where the person
answered, if the person opened the details, and if the client showed the
suggestion of Limen first. An approval that is quick, that came from a
rule or from one gesture for many actions, that followed a suggestion
that the client showed, or that came late in a long session, has weight 0
if the person did not open the details. It does not build the cover. It
still teaches the model. No error rate reads the weight.

**Why.** Most answers are habit. A model that learns from the habit
learns the habit, not the consent. An error rate that read the weight
would not be a Horvitz-Thompson estimate over the reviews, and its value
would move toward the answers that the person gave slowly.

**Checked by.** The conformance suite checks each rule of the weight
with the clock of the service. The tests of `estimate` check each rule,
and that no rate and no certificate changes when the weights change.

## 17. An ask has no lean

The reason of an `ask` states no probability and no direction. A client
does not show the approval probability, the suggested verdict, the
reason, or an explanation before the person answers. The one exception
is a recommendation where rule 18 permits it.

**Why.** People follow an opinion that they see first, and they repeat
its reasons. A person who sees the lean of Limen first can follow Limen.
Then the answer teaches Limen its own opinion.

**Checked by.** The conformance suite checks that no reason of an `ask`
has a probability, in a prediction and in an explanation.

## 18. A number only where the reviews support it

For each person, Limen compares its approval probabilities with the
approval rates that the reviews measured. It uses ten bins of the
approval probability. Each review counts with the weight 1 divided by
its review probability. A bin with an effective size less than 20 is
`not_estimable`, and its rate is `null`.

Each explanation gives the state of the bin of its prediction in the
last 90 days. A client shows the approval probability, or a
recommendation before the answer, only if that state is `calibrated`.
Above the allow threshold, the certificate of the suggestions of Limen
must also hold.

**Why.** A person trusts a number that a client shows. A model can be
wrong for one person, for example a person who approves from habit. Only
the reviews of that person show if the numbers are true for that person.

**Checked by.** The conformance suite checks each bin against its
definition while the reviews arrive. It also checks the weight of each
review and the state on each explanation. The tests of `estimate` check
the report against values calculated by hand. A simulation checks that a
calibrated model is `calibrated` in about 95% of its bins.
