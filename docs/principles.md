# Principles

Fourteen rules every implementation of Limen keeps. Each says what holds,
why, and how it is checked. Most are checked by the conformance suite in
`conformance/`, which runs against any implementation of the contract;
the rest by the tests of the shared verdict package
(`latere.ai/x/pkg/verdict`), of the contract itself, or of the engine. The
reasoning behind the rules is in [Theory](theory.md) and in the paper it
cites.

## 1. Limen suggests; the decision point decides

Limen predicts and suggests. The decision point, the code that lets an
action run, composes that suggestion with its own rules, applies the
verdict, and records what it did.

**Why.** Only the code that applies a verdict knows which verdict was
applied: a rule that narrowed it, a hook, a person's earlier "always allow
this", a rollout in which Limen is only watching. If Limen also decided,
its records would describe what it would have done, and every error rate
built on them would be wrong whenever the decision point did something
else. Keeping the decision with the decision point makes the record true by
construction, and it lets a harness use Limen's ledger with no predictor at
all.

**Checked by.** The contract: a prediction carries only a suggestion
(allow, ask, or block), and a decision is a separate record the decision
point sends.

## 2. Escalate, don't refuse

When the evidence about an action is unclear, the action waits for a
person (`ask`). It is not refused.

**Why.** An agent that is refused does not stop. It reads the refusal as a
result and tries something else, which is often worse than the action it
was denied. A person asked at the right moment costs one interruption; an
agent routing around a block can cost much more. In the paper's simulation
of an LLM judge, turning the judge's blocks into escalations instead of
enforcing them cut cost-weighted regret from 0.296 to 0.088.

**Checked by.** The conformance suite checks that an approval probability
inside the ask band is suggested as `ask`, never `block`.

## 3. Thresholds come from stated costs

You state three costs: running an action the person would deny, refusing
an action the person would approve, and one interruption. The thresholds
follow from them by a closed formula and are never tuned on data.

**Why.** With those three costs, allow, ask, and block is the choice with
the least expected cost exactly when the thresholds are
`allow_above = 1 - ask / false_allow` and `block_below = ask / false_block`
(Chow's reject option). Tuning thresholds instead would need answers about
actions decided automatically, and those actions never get answers. A cost
profile also writes down an organization's appetite for risk where anyone
can read it.

**Checked by.** The contract's tests check the formula against the expected
costs on a grid of probabilities; the conformance suite checks that every
prediction's thresholds are the ones its profile's costs imply.

## 4. An action about someone unknown is an ask

Limen refuses a cost profile under which it would allow or block an action
about a person it knows nothing about. Each kind of error must cost more
than two interruptions.

**Why.** With no answers from a person, Limen's probability that they
approve is one half. The ask band contains one half exactly when
`false_allow > 2 * ask` and `false_block > 2 * ask`. Under any other costs a
new person's first action would be decided on no evidence at all. Saving
such a profile fails with `ask_too_costly`.

**Checked by.** The contract's tests, and the conformance suite, which
checks that a subject's first prediction is `ask` under every accepted
profile.

## 5. A deterministic deny is final

Your decision point's rules set a ceiling: the most permissive verdict
they leave open. Nothing Limen suggests can exceed it, and a ceiling of
`block` is never reviewed or overturned.

**Why.** Rules you wrote down, such as "never push to a protected branch",
are commitments, not evidence. A learned model operates inside the
envelope they leave. `verdict.Decide` composes a suggestion with the
ceiling by taking the less permissive of the two, and treats a `block`
ceiling as final: verdict `block`, review probability zero.

**Checked by.** The tests and fuzzing of `latere.ai/x/pkg/verdict`: no
input yields a verdict more permissive than the ceiling.

## 6. Failure means ask

When Limen fails, is slow, or cannot be reached, the action waits for a
person. It is never allowed by default.

**Why.** A gate that fails open is not a gate. `FailClosed` bounds how long
a decision point waits for a prediction and turns any error or timeout
into a suggestion of `ask` with the source `learned/unavailable`, which the
ceiling can only narrow. The decision point then records the decision with
the action attached, so the person's answer still teaches the model.

**Checked by.** The contract's tests check `FailClosed`; the conformance
suite checks that an implementation's failures reach the caller as errors,
never as suggestions.

## 7. Some asks never go away

Where a person is genuinely undecided about a kind of action, Limen keeps
suggesting to ask, however much it has learned.

**Why.** If a person approves a kind of action about half the time, no
model can predict their answer, and under most costs asking is the
cheapest choice. The share of actions that must be asked about, even with
a perfect model, is set by the person and the costs, not by Limen. In the
paper's simulation it was 10.3% of actions under symmetric costs and 31.3%
under costs that weigh a wrong allow ten times an interruption. A promise
of "95% automation" is a claim about the person, not the product.

**Checked by.** This is a consequence rather than a rule to test: it holds
because thresholds come only from the profile (rule 3).

## 8. Forget the person's tolerance, not the action's risk

Limen's model lets a person's tolerance drift over time, but keeps what it
learned about how risky each kind of action is.

**Why.** People change: trust grows over a project and drops on a new one.
The risk of force-pushing to production does not. A model that forgets
both, as the common time-decaying construction does, drifts every action
back to "unsure" on a fixed clock and asks again about actions it already
knew. In the paper's simulation, keeping the static part cut human answers
per action from 28.4% to 13.5% and cost-weighted regret by 59%, at a
false-allow rate of about 1%. Time is counted in decisions, per person,
and Limen keeps the count itself.

**Checked by.** The engine's tests, against fixtures exported from the
research implementation.

## 9. An automatic verdict is not a label

An action allowed or blocked automatically never gets an answer. To know
the error rate of automatic verdicts, the decision point shows a small
random share of them to the person anyway, fixes the probability of
showing each action before it runs, draws once, and records the
probability with the decision. Each answer then counts with weight one
over that probability.

**Why.** An error rate computed only from the actions a person was shown
is biased: those are the actions that were already in doubt. Answers with
known probabilities give an unbiased estimate (Horvitz-Thompson). When no
action in a window could have been shown, a rate is reported as `null`,
meaning "cannot be estimated", never as zero. An action shown to the
person that nobody answers is counted as `unanswered` rather than dropped,
because an estimate that silently skipped the hard reviews would look
better than it is. Certifying a false-allow rate below 1% with 95%
confidence takes 300 consecutive clean reviews.

**Checked by.** The tests of `verdict.Decide` check that the probability it
returns is the frequency with which an action is shown; the conformance
suite checks that estimates use the recorded probabilities and that a rate
with no reviewable action is `null`.

## 10. No re-rolling

Every record is keyed by the organization, the person, and the decision
point's own id for the action. Predicting the same action again returns the
first prediction; predicting a different action under the same id is
refused; an action takes one decision.

**Why.** If a decision point could predict again until it got the
suggestion it wanted, or record a second decision until a review was
avoided, the sample behind every estimate would no longer be random. The
key makes both visible: `action_conflict` and `already_decided`.

**Checked by.** The conformance suite checks that a repeated prediction is
identical to the first, that a different action under the same id is
refused, and that a second decision is refused.

## 11. A judge is evidence, and its reliability is personal

An LLM judge's verdict, or any rule's score, enters as a `signal`: an
input to Limen's prediction of what this person would say. Its error rate
is reported per source, per person, and per window.

**Why.** A judge's error rate for a person cannot be read off its verdicts;
it depends on the person's decisions too, and it changes when that
person's tolerance changes, even though the judge does not. In the paper's
simulation, the same unchanged judge allowed actions its supervisor would
deny 2.9% of the time before a trust reset, 10.0% after it, and 3.4%
later.

**Checked by.** The conformance suite checks that reliability is reported
per source and per subject, and that a source's allows are only the
actions on which it said allow.

## 12. Later answers train, never estimate

The first answer to an action the decision showed a person is that
action's review. Every other answer, such as a dispute of an action that
ran without anyone being asked or a person changing their mind, teaches
the model and never enters an error estimate.

**Why.** Nothing fixed the probability with which a later answer is given,
so it cannot be weighted the way a review is. Counting it would bias the
estimate toward whatever happens to get noticed. Limen derives each
answer's role from the recorded decision; the caller does not declare it.

**Checked by.** The conformance suite checks that a later answer changes
no estimate and changes what the model learns.

## 13. Text an agent writes never reaches a model, and Limen never keeps it

An action's input is read only by deterministic feature extractors, for
example one that recognizes a recursive delete in a shell command. No
language model inside Limen reads it, and Limen does not store it: it keeps
the features and drops the input.

**Why.** The agent writes the input. If a model inside the gate read it,
the agent could write instructions to the gate. Judges that read the
transcript have been attacked this way; Limen takes a judge's verdict as a
signal and never the text the judge read. Not keeping the input also means
a harness's redaction of a secret in a command is never undone by a copy in
Limen.

**Checked by.** The engine's architecture test (no package that reads an
action's input depends on a model client), and the conformance suite,
which checks that a case read back carries no input.

## 14. A person's data is theirs

Limen learns from a person's answers only for that person, and forgets
everything about them in an organization on request: cases, answers, and
the model learned from them.

**Why.** A person's approve and deny history describes how they work.
Using it is their choice, and deleting it must be complete and idempotent.

**Checked by.** The conformance suite checks that after forgetting a
subject in an organization, no case, pending review, or reliability report
for that subject remains there.
