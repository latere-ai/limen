# Integration

This page is for the authors of a decision point. A decision point is the
software that decides if an action runs. These are examples:

- An agent harness that decides about a tool call.
- A proxy that decides about a network request.
- A service that decides about a push or a payment.

This page tells you how a decision point gets a prediction from Limen,
decides, records the decision, and sends the answer of the person.
[Terms](terms.md) gives the meaning of each term.

## The three calls

| Call | Go | HTTP | When |
|---|---|---|---|
| Predict | `Predictor.Predict` | `POST /v1/predictions` | Before you decide, while the action waits |
| Record | `Ledger.Record` | `POST /v1/decisions` | One time for each action, after you decide. Record before the action runs and before a person sees it. |
| Answer | `Ledger.Answer` | `POST /v1/answers` | Each time a person gives an answer about an action |

A decision point in Go that runs as one process can include the engine.
Then the call does not go through a network. Other decision points call
the server over HTTP. This includes a decision point in a different
language, and a decision point that runs on many computers.

The engine and the HTTP client are both a `limen.Service`. You choose one
of them at start-up, and the remaining code does not change. For tests,
the stub in `stub/` supplies the same interfaces.

Wrap the predictor with `FailClosed`. Give it a timeout that agrees with
the time limit of your decision. For example, use 200 milliseconds in the
same process or 2 seconds over HTTP:

```go
predictor := limen.FailClosed(svc, 200*time.Millisecond, func(err error) {
	log.Printf("limen: %v", err) // The action waits for a person.
})
```

If Limen returns an error or does not answer before the timeout,
`FailClosed` returns a prediction and no error. The suggested verdict of
this prediction is `ask`, and its source is `learned/unavailable`. Limen
has no record of this action. Put the action in your decision
(`Decision.Action`). Then the answer of the person still teaches the
model.

## The action

An `Action` describes the action as your decision point sees it before it
runs. The [API](api.md) page and the
[OpenAPI document](../api/openapi.yaml) give all the fields.

| Field | What to put in it |
|---|---|
| `org` | The organization. Limen uses the cost profile of this organization. |
| `subject` | The person for whom the action runs. Write the issuer of the token and its `sub`, with a pipe between them: `https://issuer.example\|usr_42`. Limen keeps one model for each person. If an agent acts for a person, write the person, not the agent. |
| `action_id` | Your identifier for the action. It must be unique for the person in the organization. An example is a session identifier and a tool call identifier together. |
| `actor` | The proposer of the action: `kind` (`agent`, `service`, or `person`), `id`, and `session`. |
| `kind`, `name` | The domain and the operation. Examples: `tool` and `bash`, `egress` and `https`, `repo` and `ref.update`, `spend` and `charge`. A cost profile can apply to one kind. |
| `effect` | `none`, `read`, `write`, or `external`. Use `write` for a change inside the system that runs the action. Use `external` for an effect outside that system: the network, a sent message, a transfer of money. If you are not sure, use `external`. |
| `reversible` | Whether somebody can undo the action. Set it only if you know. |
| `input` | The input of the action as JSON, for example the arguments of a tool call. Only the feature extractors of Limen read it. These are fixed programs, not language models. Limen never stores the input. |
| `target` | The object of the action: `kind` (`path`, `ref`, `host`, `account`) and `id`. Optionally, `sensitivity`, from 0 (scratch data) to 1 (production). |
| `environment` | The place where the action runs: `kind` (`host`, `sandbox`, `service`) and `credentialed`. Set `credentialed` if a credential in that place gives access outside it. |
| `attributes` | Other facts from your domain, as strings, numbers, or booleans. Examples: an amount, whether a branch is protected, a count of files. The maximum is 64 attributes. |
| `signals` | Verdicts or scores from other sources. Examples: the score of your rules, `{"source": "rules/1", "score": 0.4}`. The verdict of a judge, `{"source": "classifier/claude-auto-mode", "verdict": "allow"}`. |

Each signal has a verdict, a score from 0 to 1, or both. A higher score
means more risk.

If you request a prediction for the same action again, Limen returns the
first prediction. If you request a prediction for a different action with
an `action_id` that you used before, Limen refuses it with
`action_conflict`. Give each action its own identifier.

### Examples from four domains

A tool call in an agent harness:

```json
{
  "org": "org_7", "subject": "https://issuer.example|usr_42", "action_id": "ses_9f/toolu_01",
  "actor": {"kind": "agent", "id": "agt_ci", "session": "ses_9f"},
  "kind": "tool", "name": "bash", "effect": "external",
  "input": {"command": "git push origin main"},
  "target": {"kind": "ref", "id": "refs/heads/main"},
  "environment": {"kind": "sandbox", "credentialed": true},
  "signals": [{"source": "rules/1", "score": 0.4}]
}
```

A network request through the egress proxy of a sandbox:

```json
{
  "org": "org_7", "subject": "https://issuer.example|usr_42", "action_id": "egr_5c1",
  "actor": {"kind": "agent", "id": "agt_ci", "session": "ses_9f"},
  "kind": "egress", "name": "https", "effect": "external",
  "target": {"kind": "host", "id": "api.github.com"},
  "environment": {"kind": "sandbox", "credentialed": true},
  "attributes": {"method": "POST", "bytes": 18234}
}
```

A push to a repository host:

```json
{
  "org": "org_7", "subject": "https://issuer.example|usr_42", "action_id": "push_77a",
  "actor": {"kind": "agent", "id": "agt_ci"},
  "kind": "repo", "name": "ref.update", "effect": "write", "reversible": false,
  "target": {"kind": "ref", "id": "refs/heads/main", "sensitivity": 0.9},
  "attributes": {"protected": true, "force": false, "commits": 3}
}
```

A payment:

```json
{
  "org": "org_7", "subject": "https://issuer.example|usr_42", "action_id": "chg_19",
  "actor": {"kind": "agent", "id": "agt_buyer"},
  "kind": "spend", "name": "charge", "effect": "external", "reversible": false,
  "target": {"kind": "account", "id": "acct_vendor_3"},
  "attributes": {"amount": 120.5, "currency": "EUR"}
}
```

## The decision

Limen suggests a verdict. Your decision point decides. Combine the
suggested verdict with your rules and with the random selection for
audit. Do this in one call to the shared verdict package:

```go
v, prob := verdict.Decide(p.Verdict, ceiling, p.AuditRate, rand.Float64())
```

- **`ceiling`** is the verdict that permits the most and that your rules
  accept for this action:

  | Your rules say | `ceiling` |
  |---|---|
  | Nothing about this action | `allow` |
  | Run the action, and examine it after it runs | `flag` |
  | Always ask a person about this type of action | `ask` |
  | Never run this action | `block` |

  The result never permits more than the ceiling. If the ceiling is
  `block`, the result is `block`. No person reviews this result, and
  nothing can change it.
- **`p.AuditRate`** is the audit rate from the cost profile of the
  organization. You can use a different rate if your policy is different.
  Record the review probability that `verdict.Decide` returns for the rate
  that you used.
- **The random selection** occurs one time for each action, in this call.
  Do not make a second random selection for the same action.

`verdict.Decide` returns the verdict to apply and the review probability.
The review probability is the probability that a person sees the action.
If the random selection selects an automatic `allow`, the verdict becomes
`flag`. If the random selection selects an automatic `block`, the verdict
becomes `ask`.

| Verdict | What the decision point does |
|---|---|
| `allow` | Run the action. |
| `flag` | Run the action. Then put it in the review queue of the person, the list of actions that wait for an answer. The answer comes later. |
| `ask` | Hold the action until the person answers. Do not change an `ask` to a refusal after a timeout. An agent that gets a refusal tries a different way to do the same thing. |
| `block` | Do not run the action. Tell the agent the reason in your own words. The `reason` of a prediction is for the record of the person. Never show it to an agent. |

### Two kinds of `ask`

A suggested `ask` has an `ask_kind`. The kind tells why Limen is not
sure:

| `ask_kind` | Evidence ratio | Meaning |
|---|---|---|
| `unsure` | Less than 0.5 | The person has few answers about this type of action. More answers can change the suggestion. |
| `split` | 0.5 or more | The answers of the person tell much about this type of action, and the approval probability is still in the ask range. The answers can disagree, or they can agree but not enough yet to pass a threshold. |

The evidence ratio is the `evidence_ratio` of the prediction.
[Theory](theory.md#two-kinds-of-ask) gives its formula. The `FailClosed`
fallback has the kind `unsure`.

A `split` ask can change. More answers that agree can move the approval
probability out of the ask range. Answers that disagree keep it in the
ask range. For example, a person approves `publish` two times and does
not deny it. If the upper threshold is 0.90, Limen suggests `ask` with
the approval probability 0.80 and the kind `split`. More approvals move
the approval probability above 0.90, and then Limen suggests `allow`.

The kind `split` does not tell that the answers disagree. To tell the
person that their answers disagree, examine the answers:

- In an explanation, `evidence` has an approval and a denial with the
  same `label`.
- In `GET /v1/learned`, the entry has `approved` and `denied` both more
  than 0.

Your decision point can use the kind. The person decides a `split` ask,
because the ask comes from the answers of that person, not from too few
answers. Your rules can decide an `unsure` ask, as they did before you
connected Limen:

```go
suggested := p.Verdict
if p.Verdict == limen.Ask && p.AskKind != limen.AskSplit {
	suggested = rulesVerdict // Your rules decide. Limen learns from the answers.
}
v, prob := verdict.Decide(suggested, ceiling, p.AuditRate, rand.Float64())
```

- Use the verdict of your rules only for an `ask` that is not `split`.
  Use the suggested `allow` and `block` of Limen as usual.
- Treat an `ask` without `ask_kind` as `unsure`. A server before v0.3.0
  does not send the field.
- Keep the audit rate of the prediction. Then a part of the automatic
  verdicts of your rules also goes to the person, and the ledger can
  measure your rules.
- Record the decision as usual, with the review probability that
  `verdict.Decide` returns.
- If Limen fails, the `FailClosed` fallback is an `unsure` ask. Then
  your rules decide, as they did before you connected Limen. The
  fallback has an audit rate of zero, so no random selection occurs. The
  verdict never permits more than the ceiling.

With this policy, the suggested verdict of Limen decides only where Limen
has evidence about the person. Hosted Topos uses this policy in its
`learned` mode ([Topos](#topos)). Limen still suggests `ask`, and its
reliability report (`of=learned`) still measures that suggestion.

## Record each decision

Record each decision before the action runs and before a person sees it:

```go
d := limen.Decision{Key: a.Key, Verdict: v, ReviewProbability: prob, Source: "myharness/learned"}
if p.Source == limen.SourceUnavailable {
	d.Action = &a
}
err := svc.Record(ctx, d)
```

The `source` field names the policy that decided, for example `rules/1`
or `topos/progressive`. With this field, the ledger can report the error
rate of each policy. An action has one decision only. Limen refuses a
second decision with `already_decided`.

You can send the record and not wait for the response. But the content of
the record must not depend on the result of the action.
[The ledger](ledger.md) tells you what the review probability means. It
also gives the mistakes that make the record biased.

## Answers

Send the answer of the person each time the person gives one:

```json
{"org": "org_7", "subject": "https://issuer.example|usr_42", "action_id": "ses_9f/toolu_01", "approve": true}
```

- **At an `ask`:** send whether the person approved the action.
- **After the review of a `flag`:** send whether the person would approve
  the action. The action ran before the review.
- **Later:** a person can dispute an action that ran, for example because
  a reviewer rejected its result. Then send `"approve": false`. This
  answer teaches the model. It never goes into an error rate.

Limen finds which answer is a review from the recorded decision. For this
reason, an answer needs a recorded decision first. If there is no recorded
decision, Limen refuses the answer with `no_decision`.

Limen records the caller as the person who answered. An agent harness can
relay the answer of a person, for example a confirmation in its user
interface. To do this, the agent harness names the person in `by`. Limen
accepts `by` only if your authorization endpoint permits the agent harness
to answer for that person. If the caller is the person, Limen ignores `by`.

`GET /v1/cases?org=...&subject=...&pending=true` gives the review queue:
the actions that the person saw and that have no answer yet. The oldest
action is first.

## The ledger without predictions

The ledger does not need the predictions of Limen. A decision point that
decides with rules can measure its rules and its judge:

1. For each action, use the verdict of your rules, not the suggested
   verdict: `verdict.Decide(rulesVerdict, ceiling, rate, rand.Float64())`.
2. Record the decision. Set `source` to the name of your rules.
3. Put the action in the decision, because Limen has no prediction to
   take it from.
4. Attach the verdict of your judge to the action as a signal.
5. Send the answers.

Then `GET /v1/reliability?...&of=rules/1` gives the error rate of your
rules for that person. `of=classifier/<name>` gives the error rate of your
judge. A rate of `null` means that a person could not see any action that
your source allowed. Set an audit rate above zero. Then Limen can measure
the allows.

## The cost profile

A cost profile states three costs. Use any unit, because only the ratios
of the costs are important:

| Cost | Meaning |
|---|---|
| `false_allow` | The cost of an action that runs and that the person would deny |
| `false_block` | The cost of an action that the decision point refuses and that the person would approve |
| `ask` | The cost of one `ask` |

The thresholds come from the costs: `allow_above = 1 - ask / false_allow`
and `block_below = ask / false_block`. Each type of error must cost more
than two times the cost of `ask`. Then Limen always suggests `ask` for a
person that it has no answers from, with an approval probability of 0.5.
If a cost is too small, Limen does not save the profile and returns
`ask_too_costly`.

| Profile | `false_allow` | `false_block` | `ask` | Ask range | `allow_above` | `block_below` |
|---|---|---|---|---|---|---|
| Symmetric | 2.857 | 2.857 | 1 | 0.35 to 0.65 | 0.65 | 0.35 |
| Safety-weighted (the default) | 10 | 4 | 1 | 0.25 to 0.90 | 0.90 | 0.25 |

```http
PUT /v1/profiles
Content-Type: application/json

{
  "org": "org_7",
  "kind": "spend",
  "costs": {"false_allow": 50, "false_block": 4, "ask": 1},
  "audit_rate": 0.05,
  "certify": {"alpha": 0.01, "delta": 0.05}
}
```

- **Scope.** A cost profile applies to an organization. You can make it
  apply only to one kind of action, to one agent, or to both. For each
  action, the most specific saved profile applies. Limen uses this order:
  kind and agent, agent, kind, organization. If there is no saved profile,
  the built-in default applies, and `GET /v1/profiles` returns
  `"default": true`.
- **Removal.** `DELETE /v1/profiles` (`Admin.DeleteProfile` in Go)
  removes the cost profile of one scope. Then the next scope in the order
  applies. For example, an agent goes back to the cost profile of its
  organization.
- **A person.** A person can save a cost profile for their own actions in
  the organization, with `subject` and no `kind` or `agent`. It can only
  make the cost profile of the organization stricter for that person.
  Limen applies the meet of the two cost profiles
  ([API](api.md#the-cost-profile-of-a-person)). A decision point does not
  change: the thresholds and the audit rate of each prediction are
  already the meet. `Forget` removes the cost profile of the person.
- **`audit_rate`** is the audit rate. The simulation of the paper used 5%.
  If the audit rate is zero, Limen cannot estimate the false allow rate of
  automatic verdicts. The reports tell you this.
- **`certify`** is the claim that the reliability reports test: a false
  allow rate below `alpha`, with a confidence of `1 - delta`. It is
  optional. Without it, the claim is a rate below 1% with 95% confidence.

The default claim needs 300 audits of automatic allows in a row with no
denial. At an audit rate of 5%, this is approximately 6,000 automatic
allows. A denial in an audit starts the count again. A claim of 25% with
95% confidence needs 12 audits.

## The reliability report

`GET /v1/reliability?org=...&subject=...&of=...&from=...&to=...` gives
the error rates of one source, for one person, in one period of time. The
value of `of` is one of these sources:

- `learned`: the suggested verdicts of Limen.
- `applied`: the verdicts that you applied.
- The `source` of a policy.
- The `source` of a signal.

[The ledger](ledger.md#reading-a-report) explains each field. In summary:

- Read the `interval` before the `value`.
- Read `covered` to know which part of the actions the `value` applies to.
- Keep `unanswered` small.

## The report of an organization

`GET /v1/reports?org=...&from=...&to=...` gives the counts and the error
rates of the actions of an organization, for all its persons together, by
week. In Go, it is the `Reporter` interface of each `limen.Service`. The
[API](api.md#reports-of-an-organization) describes the fields and the
rules.

A week shows its numbers only after it closed, and only if 5 or more
persons took part. A number that comes from reviews has a second rule. It
shows only if 5 or more persons gave the reviews, and no person gave more
than one half of them. A week that a report showed never changes.

A decision point does not read the report. Give `report.read` only to the
persons who manage the organization.

## Explanations for the person

Three routes show a person what Limen learned from their answers:

- `GET /v1/explanations`, for one action.
- `GET /v1/learned`, for each type of action.
- `POST /v1/previews`, for a different cost profile.

In Go, they are the `Explainer` interface of each `limen.Service`. The
[API](api.md#explanations) describes the fields.

> **Warning:** Show an explanation only to the person. Never put an
> explanation, or a part of it, in what an agent reads: a tool result, a
> message, or a hint to try again. An agent can use it to change an
> action until Limen allows it.

## A safe start

Before Limen decides anything, show that it is useful on your own
history. There are four steps. Each step needs the evidence from the step
before it.

1. **Replay.** Send your recorded history to `POST /v1/replays`.

   Use newline-delimited JSON, with one record on each line. Each record
   has these fields:

   - `at`: the time when the agent proposed the action.
   - `action`: the action.
   - `verdict`, `review_probability`, and `source`: the decision that you
     applied.
   - `approve`: the answer of the person, if there was one.

   If you had no explicit audit policy, use these review probabilities: 1
   for an `ask`, and 1 for a `flag` that your rules forced. Use 0 for an
   `allow` or a `block`.

   Limen predicts the records in time order. It predicts each record
   before it learns the answer of that record. The report gives:

   - `would`: the suggested verdicts of Limen.
   - `allowed_denied`: how often Limen would allow an action that the
     person denied.
   - `blocked_approved`: how often Limen would block an action that the
     person approved.
   - An estimate of the false allow rate. Its `covered` value tells you
     how much of the estimate your history can support.

   With `keep=true`, the records also go into the live models and the
   ledger.

2. **Shadow mode.** Your rules continue to decide. For each action, get a
   prediction from Limen. Use `verdict.Decide` with the verdict of your
   rules, not the suggested verdict, and an audit rate above zero. Record
   the decision with the source of your rules.

   Limen learns from the answers. Its reliability report (`of=learned`)
   measures its suggestions on the actions that your decisions showed to
   a person. `covered` tells you how many of the possible allows of Limen
   your reviews reach.

3. **Learn under the ceiling.** Use the suggested verdict of Limen with
   `verdict.Decide`, under the ceiling of your rules. Start where an
   automatic `allow` can do the least damage, for example in a sandbox.

   You can also start with the policy in
   [Two kinds of `ask`](#two-kinds-of-ask). Then Limen decides only where
   it has evidence about the person, and your rules decide the other
   actions as before. Hosted Topos uses this policy.

4. **Signals from a judge.** Attach the verdicts of your judge to the
   actions as signals. Monitor the reliability report of the judge for
   each person.

## Notes for each agent harness

### Topos

[Topos](https://github.com/latere-ai/topos) is the first decision point
that uses Limen. Topos can use any decision service that has this API.

The rules of Topos give the ceiling, and `verdict.Decide` combines the
verdicts. Topos records each decision with the tool call of the session.
Topos sends the confirmations of the person as answers.

From Topos v0.68.0, the source of a decision is `topos/<mode>/<rule>`.
`<mode>` is `confirm` or `progressive`. `<rule>` tells what decided the
call. For example, `always_allow` is a pattern of the permitted list,
`score` is the risk score, and `learned` is the suggested verdict of
Limen. A decision without a `<rule>` has the source `topos/<mode>`.
Before v0.68.0, the source was `topos/confirm` or `topos/progressive`.

#### Topos on your computer

`topos run` uses Limen from Topos v0.11.0. It has two modes:

- **Manual mode** (`topos run --mode manual`, the `confirm` mode of
  Topos). Topos asks you about each call that is not read-only, as
  before. Each answer teaches Limen. Topos records each prediction with
  the call. You can see what Limen would do.
- **Auto mode** (`topos run --mode auto`, the `progressive` mode of
  Topos). The suggested verdict of Limen decides each call that the rules
  of Topos do not decide. A familiar safe call runs, and Topos blocks a
  call that you would deny. If Limen is not sure, Topos asks you. Topos
  still shows a part of the automatic verdicts to you for audit. With
  these audits, the ledger can measure how often auto mode is wrong.

`topos run` does not read `TOPOS_DECISIONS_MODE`. Its auto mode is the
`decide` mode of [hosted Topos](#hosted-topos).

Start the server of Limen. Then give Topos its address and the token:

```sh
export LIMEN_TOKEN=$(openssl rand -hex 16)
limend &                                        # http://127.0.0.1:7720

export TOPOS_DECISIONS_URL=http://127.0.0.1:7720
export TOPOS_DECISIONS_TOKEN=$LIMEN_TOKEN
topos run --mode manual "Fix the failing test."  # First, for some time: Limen learns.
topos run --mode auto   "Fix the failing test."  # Then: Limen decides under the rules.
```

For a new person, Limen suggests `ask`. It suggests `allow` or `block`
only after the answers of the person support it. Usually, auto mode asks
about a type of call until it has approximately 30 or more answers for
that type.

#### Hosted Topos

The Topos server, `toposd`, sends each tool call of each hosted session
to Limen if `TOPOS_DECISIONS_URL` is set. `TOPOS_DECISIONS_MODE` sets how
much the suggested verdict of Limen decides in the `progressive` mode of
Topos. Topos v0.66.0 added this variable.

| `TOPOS_DECISIONS_MODE` | What decides a `progressive` call |
|---|---|
| `shadow` | The rules and the risk score of Topos decide each call. Topos records the suggested verdict of Limen with the call. |
| `learned` | The suggested verdict of Limen decides if it is `allow`, `block`, or an `ask` with `ask_kind` `split`. For an `unsure` ask, an `ask` without `ask_kind`, or if Limen does not answer, the rules and the risk score decide. If Limen answered, a part of these automatic verdicts goes to the person, at the audit rate of the prediction. |
| `decide` (the default) | The suggested verdict of Limen decides each call that the rules do not decide. If Limen fails, the call waits for a person. |

In each mode, these rules apply:

- In the `confirm` mode, the rules of Topos decide. Topos records each
  decision in Limen, and Limen learns from the answers.
- In the `plan` mode, Topos does not send the call to Limen.
- A hosted session that runs on the computer of the person decides a
  `progressive` call as in the `confirm` mode.
- The lists of the organization, a tool that holds its own calls, and the
  network rules of Topos decide before Limen.

From v0.66.0, Topos also sends the answer of a person to a network
approval. The answer goes to Limen as an answer to the call whose
connection Topos refused.

`TOPOS_DECISIONS_ORG` sets the organization of each call in Limen. Topos
v0.68.0 added this variable.

| `TOPOS_DECISIONS_ORG` | The `org` of a call |
|---|---|
| `initiator` (the default) | The context of the person who started the session. |
| `agent` | The ID of the organization that owns the agent of the session. If no organization owns the agent, the context of the person who started the session. |

The organization of a call sets which cost profiles apply to the call,
and which report of an organization counts it. The person of a call is
always the person who started the session. Each `agent.tool_use` event
of Topos records the organization of its call as `decision_org`. To read
the records of a call in Limen, for example its explanation, use
`decision_org` as `org`. An event without `decision_org` is in the
context of the person, or Topos did not send its call to Limen.

The answers of a person in an organization and in the context of the
person are separate. An answer in one does not change a suggestion in
the other. Set `agent` only after your authorization endpoint lets Topos
send the calls of an organization, and your clients read `decision_org`.

> **Warning:** If you change `TOPOS_DECISIONS_ORG` while a call waits for
> a person, Limen does not get the answer to that call. Use the same value
> in each `toposd` process.

The Topos
[configuration](https://github.com/latere-ai/topos/blob/main/docs/configuration.md)
page gives the token settings, `TOPOS_DECISIONS_TOKEN` or
`TOPOS_DECISIONS_AUDIENCE`. Use a hosted `limend` for hosted Topos
([Running limend](running.md#hosted)).

#### Replay the history of Topos

To see what Limen would do on sessions that you ran before, replay the
data directory of Topos. A replay does not need the server:

```sh
limen replay topos
```

The data directory is `--data`, then `TOPOS_DATA_DIR`, then
`$XDG_STATE_HOME/topos`, then `~/.local/state/topos`. A replay opens the
directory with the session store of Topos, at the version in the `go.mod`
file of Limen. If the directory has
logs from a Topos version before v0.60.0, the store updates them to the
current format one time. This is the same update that Topos does when it
opens the directory.

Each tool call that has a verdict becomes one record:

- The action is a `tool` action with the name of the tool. Its effect is
  the declared effect of a built-in tool of Topos, or `write` for a
  different tool. From Topos v0.56.0, it has the attribute `attended`,
  which Topos also sends with each call. The attribute is `false` if a
  trigger, not a person, started the turn of the call.
- The verdict is the recorded verdict of Topos, with the source
  `topos/<mode>/<rule>` ([Topos](#topos)). Topos v0.65.0 and later record
  `<rule>` in the log. For a log from an earlier version, the source is
  `topos/<mode>`.
- The review probability is the recorded probability. A log from before
  Topos v0.11.0 has no probability. Then the probability is 1 for an `ask`
  or a `flag`, and 0 for an `allow` or a `block`.
- The answer comes from the confirmations of the call by the person.
  From Topos v0.66.0, it also comes from the confirmations of the network
  approvals that the call caused. A record has one answer. For a call
  that the person saw (`ask` or `flag`), it is the first answer, which is
  the review. For a call that ran without the person, it is the last
  answer, which the model learns.

Because of this, after `limen replay topos --keep`, the ledger gives the
error rate of each type of rule. From v0.68.0, Topos sends each live
decision with the same source. Before v0.68.0, it sent `topos/<mode>`.

A replay does not include these calls. The report gives the number of
each:

- A call that Topos removed from the log.
- A call without a verdict, from a Topos version that did not record
  verdicts.
- A call with a verdict that Limen does not know, or that does not give a
  valid record.
- A call with the same tool call identifier as an earlier call in its
  session.

A command can connect to a host outside the network of the session. Then
Topos asks you for a network approval. From Topos v0.66.0, Topos sends
your answer to the approval to Limen as an answer to the call that made
the connection. A replay reads these answers in the same way. A message
in place of an answer is not an answer.

Topos can send more than one answer for a call, and the model learns the
last answer. A replay keeps one answer for each call. Because of this,
for a call that you saw, the model of a replay can learn a different
answer than the model of Topos.

A replay reads only a data directory on your computer. It does not read
the sessions of hosted Topos.

### Claude Code and Codex

The hooks for Claude Code and Codex do not exist yet. We plan to add
them after Limen is open source. [Spec 005](../specs/005-adapters.md)
describes them. These notes give what a hook must do.

**Claude Code** calls the server from a command hook. A `PreToolUse` hook
returns allow, ask, or deny. Apply the verdicts as follows:

- `allow`: return allow.
- `flag`: return allow, and add the action to your review queue.
- `ask`: return ask.
- `block`: return deny.

The permission rules of Claude Code can still ask about an action or deny
it after the hook allowed it. The hook cannot see this. Because of this,
the recorded review probability can be lower than the real frequency with
which a person saw such actions. We did not yet examine how the answer of
a person to an `ask` appears in the hook events of Claude Code. The
design of the adapter depends on it.

**Codex** ([github.com/openai/codex](https://github.com/openai/codex))
uses its auto-review reviewer only if its static rules would otherwise
ask. The reviewer returns a denial to the agent. A person sees an action
only after repeated denials. Nobody answers the actions that the reviewer
allowed. Because of this, the false allow rate of the reviewer for a
person is `null`. It stays `null` until a person examines a part of those
actions, selected at random for audit.
