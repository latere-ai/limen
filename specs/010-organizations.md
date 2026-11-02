---
title: "Organizations: remove a saved cost profile, a stricter cost profile for one person, and a weekly report that never shows one person"
status: complete
track: core
depends_on: [002-contract.md, 003-http-api.md, 004-engine.md, 006-conformance.md, 008-ledger-and-estimation.md, 009-explanations.md]
affects: [profile.go, service.go, report.go, authorizer/, estimate/, engine/, engine/store/, stub/, conformance/, client/, server/, api/openapi.yaml, docs/api.md, docs/terms.md, docs/integrating.md, CHANGELOG.md]
effort: large
created: 2026-11-02
updated: 2026-11-02
author: changkun
---

# Organizations

## Overview

Limen keeps each record under an organization and a person. A cost
profile applies to an organization, a kind of action, an agent, or a kind
and an agent. Three things that an organization needs are not possible
with the API of v0.3.0:

1. **An agent cannot go back to the cost profile of its organization.**
   `PUT /v1/profiles` saves a cost profile, and no route removes one. If
   an administrator saves a cost profile for one agent, that agent keeps
   it. A copy of the costs of the organization does not follow a later
   change of the organization.
2. **An organization cannot see how its agents do across its persons.**
   A reliability report is for one person. No route adds the records of
   many persons. Such a route can show the records of one person. This
   occurs if the group is small, or if one person gave most of the
   answers.
3. **A person cannot ask for more questions than the organization
   asks.** A scope has no person. A person can want to see more actions in
   one organization. Then they can only ask an administrator to change the
   cost profile for all persons.

This spec adds one route that removes a saved cost profile, a scope with
a person, and one route that reports an organization by week. Each of the
three parts can ship alone, in the order of this spec. A hosted platform
asked for them in its design of oversight in an organization. Limen
states the rules here, and any installation can use them.

## Design

### 1. Remove a saved cost profile

`DELETE /v1/profiles?org&kind&agent&subject` removes the cost profile
that is saved for exactly that scope. The answer is `204` with no body.

- The query names the scope as `GET /v1/profiles` names it. `org` is
  necessary. `kind`, `agent` and `subject` are optional (part 2 adds
  `subject`).
- If no cost profile is saved for the scope, the answer is also `204`.
  The route is idempotent, as `POST /v1/subjects/forget` is.
- After the removal, the order of [[002-contract]] gives the cost profile
  that applies: kind and agent, agent, kind, organization, then the
  default. A removed agent scope then goes back to the cost profile of
  the organization. A removed organization scope goes back to the
  default.
- The removal changes no recorded prediction, decision or answer. A
  prediction keeps the thresholds that it had. The next prediction uses
  the cost profile that applies after the removal.
- An empty `org` is `invalid_query` naming `org`. The rules of part 2 for
  a scope with `subject` also apply, with `invalid_query`.
- The authorizer question is `profile.write` on the `Profile` resource
  of the scope, the same question as a `PUT` for that scope. A person or
  service that can save a cost profile for a scope can also remove it.
  For this reason, a new action, `profile.delete`, is not necessary. An
  authorization endpoint that decides `profile.write` decides the removal
  with no change.
- The Go contract adds `DeleteProfile(ctx, Scope) error` to `Admin`. The
  store adds `DeleteProfile(ctx, Scope) error`. The memory store removes
  the entry. The directory store writes the file of the organization
  again without the scope, as it does for a save.

A client can tell the cost profile of an agent from the cost profile of
its organization with no new field. `GET /v1/profiles?org&agent` returns
the most specific saved cost profile, with its own scope. If the scope of
the answer has `agent`, the agent has its own cost profile. If it has no
`agent`, the cost profile of the organization or the default applies.

### 2. The cost profile of a person

A scope gets a person: `Scope.Subject`, JSON `subject`, optional. A cost
profile with `subject` is the cost profile of that person in that
organization. It applies only to the actions of that person. It can only
make the cost profile of the organization stricter for that person. It
never makes it less strict.

#### Stricter

Cost profile A is stricter than cost profile B if two conditions are
true. The upper threshold of A is the same as or higher than that of B.
The lower threshold of A is the same as or lower than that of B. A
stricter cost profile has a wider ask range on both sides. It asks the
person about more actions, and it decides fewer actions without the
person.

The cost profile that applies to an action of a person is the meet of two
cost profiles:

- The cost profile of the organization. The order of [[002-contract]]
  gives it for the scope of the action, without `subject`.
- The cost profile of the person, if one is saved for
  `{org, subject}`.

The meet takes each number from the cost profile that is stricter for
that number:

| Field | The meet |
|---|---|
| `thresholds.allow_above` | the higher value of the two |
| `thresholds.block_below` | the lower value of the two |
| `costs` | `ask` is 1, `false_allow` is the higher value of the two `false_allow / ask`, and `false_block` is the higher value of the two `false_block / ask` |
| `audit_rate` | the higher value of the two |
| `certify` | the lower `alpha` and the lower `delta` of the two |
| scope | the scope of the cost profile of the organization. It has `subject` if the cost profile of the person changed a value above |
| `default` | `true` only if the cost profile of the organization is the default and the cost profile of the person changed no value |

The costs of the meet give the thresholds of the meet. The upper
threshold, `1 - ask / false_allow`, increases with `false_allow / ask`.
The lower threshold, `ask / false_block`, decreases with
`false_block / ask`. For this reason, the higher ratio of each pair gives
the stricter threshold of that pair (paper, Proposition 2). The thresholds of
the meet are the higher and the lower thresholds of the two exactly, and
the costs agree with them to 1e-12.

The meet of two accepted cost profiles is an accepted cost profile. In
each of the two, `false_allow` and `false_block` are more than two times
`ask`. The higher ratios of the meet are then also more than two. The ask
range of the meet contains one half ([[001-architecture]], invariant 4).

The rule applies when Limen predicts, not when a person saves. A person
can save a cost profile that is less strict than the cost profile of the
organization today. It then changes nothing. If the organization later
makes its own cost profile less strict, the cost profile of the person
then applies. A check when the person saves would refuse a value that a
later change of the organization makes correct. The meet, not a
refusal, prevents a person from making the cost profile of the
organization less strict.

The meet is the root function `Stricter(org, person Profile) Profile`. A
client that shows what applies to a person can calculate the same value.

For example, the three cost profiles that a hosted console names have
these thresholds:

| Name | `allow_above` | `block_below` |
|---|---|---|
| Balanced | 0.65 | 0.35 |
| Careful | 0.90 | 0.25 |
| Strict | 0.98 | 0.25 |

Each one is stricter than the one above it. The meet of two of them is
always the stricter of the two.

#### Where the meet applies

The meet applies to each read that is for one person:

- A prediction: `thresholds`, `audit_rate`, and `profile`. `profile` has
  `subject` if the cost profile of the person changed a value.
- `GET /v1/learned`: the `thresholds` of each entry.
- `GET /v1/explanations` of an action with no recorded prediction.
- The certificate of `GET /v1/reliability`, which uses the `certify` of
  the meet.

A preview does not use it. A preview states its costs in its body, and it
shows what those costs would do.

#### The rules of the scope

- A scope with `subject` has no `kind` and no `agent`. A person has one
  cost profile in one organization, for all the actions of that person
  there. `PUT` refuses a scope with `subject` and `kind` or `agent` with
  `invalid_profile` naming `kind` or `agent`. `GET` and `DELETE` refuse
  it with `invalid_query`.
- In the context of a person, `subject` must be equal to `org`. The
  `org` of that context contains a pipe and is the subject of that
  person ([[003-http-api]]). A different `subject` there is
  `invalid_profile` or `invalid_query` naming `subject`. The resource
  of the authorizer then names one person.
- `Scope.Candidates` does not use `subject`. The order of the cost
  profile of the organization is the same as in v0.3.0.
- `ScopeOf(action)` does not set `subject`. The engine and the stub read
  the cost profile of the person from the key of the action.

#### The routes

| Route | With `subject` |
|---|---|
| `PUT /v1/profiles` | saves the cost profile of the person |
| `GET /v1/profiles?org&subject` | returns the cost profile of the person if one is saved, with `subject` in its scope. Otherwise it returns the cost profile of the organization for the scope `{org}`, with no `subject`, as a `GET` without `subject` returns it |
| `DELETE /v1/profiles?org&subject` | removes the cost profile of the person |

A `GET` with `subject` returns what the person saved, not the meet. A
client must know what the person picked, also if the meet is equal to the
cost profile of the organization today. The client reads the cost
profile of the organization with a second `GET` without `subject`, and
calculates the meet with `Stricter`. A prediction and `GET /v1/learned`
give the meet.

#### Forget removes it

`Forget` of a person in an organization also removes the cost profile of
that person there. It is a record of what that person wants.

A person who left an organization can still forget their records there.
They cannot save or remove a cost profile there. A hosted decider allows
a forget to a person who left, but a cost profile only in the current
organization. Without this rule, the cost profile of a person who left
stays.

Invariant 8 of [[001-architecture]] becomes: forgetting a person in an
organization removes every case, answer and model of theirs there, and
their cost profile there.

#### The authorizer

The `Profile` resource has `subject` if the scope has `subject`. Without
`subject` in the scope, the resource has `subject` equal to `org` if
`org` contains a pipe, as in v0.3.0. The resource never has two
subjects:

- The shared owner policy lets the person read, save and remove their own
  cost profile, because the resource names that person. An administrator
  can do all three for every person.
- An authorization endpoint decides from `org` and `subject`. The owners
  of an organization can save its cost profiles. That rule must not let
  them save or read the cost profile of a different person. The cost
  profile of a person tells how much that person trusts the agents.
- A `GET` with `subject` can return the cost profile of the organization
  if the person saved none. This shows the person no new information.
  The thresholds of the organization are already in each prediction for
  that person and in each entry of `GET /v1/learned`.

### 3. The report of an organization

`GET /v1/reports?org&from&to` reports the actions of the agents of one
organization, and what the reviews of all its persons said, by week. It
never shows the records of one person. Each number in it comes from a
group of persons. If the group is too small, or one person gave most of
the reviews, the report leaves the number out.

#### The query

- `from` and `to` are RFC 3339 times. Each one is a Monday at 00:00 UTC.
  `to` is after `from`, and not more than 13 weeks after it. A time
  that breaks a rule is `invalid_query` naming it.
- The report has one entry for each week in `[from, to)`, oldest first.
- Fixed weeks prevent a difference attack on the windows. If two windows
  can differ by one hour, the difference of their reports shows the
  actions and the answers of the persons in that hour. That can be one
  person. The weeks of a report never overlap, and the API never adds
  two weeks. A client can add the numbers of weeks that it shows. That
  sum shows nothing that the weeks do not show.

#### The state of a week

| `state` | When | What the week has |
|---|---|---|
| `open` | until 7 days after the end of the week | only `from`, `to` and `state` |
| `too_few` | the week is closed, and fewer than 5 persons took part | only `from`, `to` and `state` |
| `ready` | the week is closed, and 5 or more persons took part | the numbers below |

- A person **took part** in a week if one or more of their cases have a
  decision and a receive time in the week.
- A week **closes** 7 days after its end. A review counts in the report
  only if the person gave it before the week closed. A review after that
  is `unanswered` for the report.
- No answer changes a closed week. A week that the report shows cannot
  change between two reads. Two reads then cannot show the difference of
  one review. A person has 7 days to review the actions that the decision
  point showed to them.
- A `too_few` week does not give the number of persons. The answer "too
  few" shows nothing about who took part. "3 persons" can be enough to
  find them.

#### What a ready week has

`total` has the numbers of the week for the whole organization. `agents`
and `other` divide `total` by agent (below). Each of the three is a
`ReportCell`:

| Field | Meaning |
|---|---|
| `subjects` | the number of persons that took part in the cell |
| `decisions` | the number of decided cases in the cell for each applied verdict: `allow`, `flag`, `ask`, `block` |
| `flag_reviews.reviews` | the cases of the cell with the applied verdict `flag` that have a review before the week closed |
| `flag_reviews.denied` | the reviews among them that denied the action: how often a review of an action that ran said that it must ask |
| `applied.false_allow`, `applied.false_block` | the error rates of the applied verdicts, as `of=applied` gives them ([[008-ledger-and-estimation]]) |
| `learned.false_allow`, `learned.false_block` | the error rates of the suggested verdicts of Limen, as `of=learned` gives them |
| `withheld` | the fields that the answer rule removed, by their JSON path |

Each error rate is one `Estimate` for all the cases of the cell together,
not a mean of the rates of the persons. Limen puts the rows of the cases
of all persons in the cell into one estimate of
[[008-ledger-and-estimation]], with the same weights. Each case has its
own review probability, so the estimate over all the rows is as correct
as the estimate for one person. The estimate is the false allow rate of
the agents of the organization, as the persons who saw their actions would
answer. The report has no certificate. A certificate counts the most
recent audits without a denial, and the most recent denial can belong to
one person.

The report has no label, no action ID, no session, no time more exact
than the week, and no subject. The only names in it are agent IDs. An
agent ID names software of the organization, not a person.

#### The group rule

A cell shows its numbers only if 5 or more persons took part in it. The
constant is `ReportMinSubjects`. Its value, 5, is a part of this spec.

Why 5:

- A reader of the report who also took part knows their own numbers. If
  k persons took part, k - 1 persons who share their own numbers can
  find the numbers of the last person. With k equal to 5, four persons
  must share. The reader is one of the four.
- With 2 or 3, a reader alone or with one other person finds a person.
- With 10, most small organizations never see a report. The rule must
  protect each person, and the report must be useful to a team of five.
- Tables of public statistics often leave out cells with fewer than 5
  to 10 persons. 5 is the low end of that range, and the answer rule
  below adds protection.

An installation cannot change the minimum (Decisions, 2).

The group rule alone applies to `subjects` and `decisions`. These counts
tell what the agents did in the sessions of the persons. They do not
tell what a person answered. One person can still cause most of the
actions of a cell. This spec does not apply the answer rule to the
counts (Decisions, 7).

#### The answer rule

A group of 5 persons is not enough if one person gave most of the
reviews. Then the error rate of the cell is close to the rate of that
person. For this reason, each field that reads reviews has its own rule.
These fields are `flag_reviews.denied` and the four error rates.

- The reviews that the field reads must come from 5 or more persons.
- No one person gave more than one half of those reviews.

If a field breaks a rule, the report leaves the field out and adds its
path to `withheld`. A field with no review has no rule to break: an error
rate with no review is `value: null`, as in a reliability report. With no
person above one half, the other persons together gave as many of the
reviews as the largest one. The value then does not show the answers of
one person.

The reviews that each field reads:

| Field | The reviews |
|---|---|
| `flag_reviews.denied` | the reviews of the cases with the applied verdict `flag` |
| `applied.false_allow` | the reviews of the cases that the applied verdict allowed, with a review probability above 0 |
| `applied.false_block` | the reviews of the cases that the applied verdict blocked, with a review probability above 0 |
| `learned.false_allow`, `learned.false_block` | the same, for the suggested verdict |

#### Agents and the other agents

`agents` has one cell for each agent with 5 or more persons in the week.
The agent of a case is `actor.id` if `actor.kind` is `agent`. `other`
has all other cases of the week. These are the cases of the agents with
no cell of their own, and the cases with no agent.

The division must not show a small cell by a subtraction. If `total` and
all cells but one are in the report, `total` minus those cells gives the
last cell. Limen merges the cells in these steps:

1. Each agent with fewer than 5 persons goes into `other`.
2. If `other` has cases and fewer than 5 persons, the agent cell with the
   fewest persons goes into `other`. A tie goes to the lower agent ID.
   Limen does this again until `other` has 5 or more persons, or no
   agent cell is left.
3. If no agent cell is left, the week has `total` only, with no `agents`
   and no `other`.

Each count of `decisions` and `flag_reviews` in `agents` and `other`
adds up to the count in `total`. Each of these cells has 5 or more persons.

The answer rule also applies across the division. A field shows in
`agents` and `other` only if it shows in each of them. If one of them
withholds `flag_reviews.denied`, all of them withhold it. Otherwise
`total` minus the others gives the withheld count.

If every part shows a field, `total` also shows it. Each part has
reviews from 5 or more persons, so the sum has too. A person with no more
than one half of each part has no more than one half of the sum.

#### A closed week does not change

When a week is `too_few` or `ready` for the first time, Limen keeps that
entry of the report. Each later read returns the kept entry with no
change.

- A `Forget` of a person does not change a kept week. Otherwise the
  difference between the week before and after the `Forget` shows the
  numbers of that person to a reader who knows that they left. A kept
  week holds no record of a person. It has counts of 5 or more persons
  and no person with more than one half of the reviews.
- A kept week holds no case, answer or model of a person. A `Forget`
  then still removes everything of the person ([[001-architecture]],
  invariant 8). The owner decided this on 2026-11-02 (Decisions, 1).
- A replay with `keep` that adds history to a kept week does not change
  it.

#### The authorizer

The route asks a new action, `report.read`, on a new resource kind,
`Report`, with the field `org`.

- `reliability.read` is not correct for this route. Its resource names
  one person, and an authorization endpoint lets a person read their own
  reports. A `Reliability` resource with no `subject` would change what
  every rule for `reliability.read` means.
- The resource has no `subject`. Under the shared owner policy, only an
  administrator can read a report.
- A hosted decider lets the owners and the administrators of the
  organization read it, and no other person. A decision point does not
  need it.

#### The cost

A report reads each case of each person of the organization in its
weeks, one time for each week. After the first read, Limen keeps the
week, and a read of it is one read of the store. The work is outside the
path of a decision. The engine takes the lock of one person at a time.

### The contract

| Change | Where |
|---|---|
| `Scope.Subject`, JSON `subject`, optional | `profile.go` |
| `Profile.Validate` and the scope query: the two rules of a scope with `subject` | `profile.go` |
| `Stricter(org, person Profile) Profile`, the meet | `profile.go` |
| `Admin.DeleteProfile(ctx, Scope) error` | `service.go` |
| `Reporter`, with `Report(ctx, ReportQuery) (Report, error)`, in `Service` | `service.go`, `report.go` |
| `ReportQuery` with `Validate`, `Report`, `WeekReport`, `ReportCell`, `AgentCell`, `VerdictCounts`, `FlagReviews`, `SourceRates` | `report.go` |
| `ReportMinSubjects` (5), `ReportClosesAfter` (7 days), `ReportMaxWeeks` (13) | `report.go` |
| `store.Store`: `DeleteProfile`, `Subjects(ctx, org)`, `WeekReport` and `PutWeekReport` | `engine/store` |
| the directory store: `<dir>/reports/<org>/<week>.json` for a kept week | `engine/store/file` |
| `authorizer`: `report.read` on `Report` | `authorizer/` |

No error code is new. The route of the report uses `invalid_query`, and
the removal uses `invalid_query`.

The interfaces `Admin` and `Service` get new methods. A Go type outside
this module that implements `limen.Service` must add them. A decision
point that uses `client/`, `engine/` or `stub/` changes nothing. The
release with part 1 is v0.5.0 (Decisions).

### The HTTP API

| Method | Path | Body | Success | Authorizer action |
|---|---|---|---|---|
| GET | `/v1/profiles?org&kind&agent&subject` | none | 200 `Profile` | `profile.read` |
| PUT | `/v1/profiles` | `Profile`, with `subject` optional | 200 `Profile` | `profile.write` |
| DELETE | `/v1/profiles?org&kind&agent&subject` | none | 204 | `profile.write` |
| GET | `/v1/reports?org&from&to` | none | 200 `Report` | `report.read` |

A `Report`:

```json
{
  "org": "org_7",
  "from": "2026-09-28T00:00:00Z",
  "to": "2026-10-19T00:00:00Z",
  "min_subjects": 5,
  "weeks": [
    {"from": "2026-09-28T00:00:00Z", "to": "2026-10-05T00:00:00Z", "state": "too_few"},
    {
      "from": "2026-10-05T00:00:00Z", "to": "2026-10-12T00:00:00Z", "state": "ready",
      "total": {
        "subjects": 12,
        "decisions": {"allow": 1610, "flag": 92, "ask": 212, "block": 9},
        "flag_reviews": {"reviews": 81, "denied": 2},
        "applied": {"false_allow": {"value": 0.021, "interval": [0.006, 0.07], "covered": 1, "reviews": 81, "audits": 81, "unanswered": 11}},
        "learned": {"false_allow": {"value": 0.012, "interval": [0.002, 0.06], "covered": 0.97, "reviews": 74, "audits": 74, "unanswered": 9}},
        "withheld": ["applied.false_block", "learned.false_block"]
      },
      "agents": [{"agent": "agt_release", "subjects": 9, "decisions": {"allow": 1200, "flag": 70, "ask": 150, "block": 6}, "flag_reviews": {"reviews": 63}, "withheld": ["flag_reviews.denied"]}],
      "other": {"subjects": 7, "decisions": {"allow": 410, "flag": 22, "ask": 62, "block": 3}, "flag_reviews": {"reviews": 18}, "withheld": ["flag_reviews.denied"]}
    },
    {"from": "2026-10-12T00:00:00Z", "to": "2026-10-19T00:00:00Z", "state": "open"}
  ]
}
```

The numbers are examples. The OpenAPI document gets the routes and the
types in the same change as the Go types. Its test holds them to the
code, as for each route.

### The conformance suite

`Target` gets `Clocked`, a function that returns a fresh service whose
clock the case sets. A case that needs a closed week runs against it. A
target with no `Clocked` skips those cases with the reason. The engine,
the stub and the client against `limend` give `Clocked`. The test of
`cmd/limend` gives its server a clock through an option of the test
only.

| Case | The rule |
|---|---|
| `ProfileRemoved` | after a removal of the agent scope, the next prediction for that agent has the thresholds of the organization, and `GET` returns the scope `{org}`. A second removal is no error. A removal of `{org}` gives the default. A recorded prediction keeps its thresholds |
| `ProfileRemovalErrors` | an empty `org`, and `subject` with `kind` or `agent`, are `invalid_query` naming the field |
| `PersonProfileIsTheMeet` | with Balanced for the organization and Careful for person A, A gets the thresholds of Careful with `subject` in `profile`, and person B gets Balanced. The audit rate is the higher one |
| `PersonProfileNeverLoosens` | with Strict for the organization and Careful for person A, A gets Strict with no `subject` in `profile`. With the organization changed to Balanced, A gets Careful |
| `PersonProfileRead` | `GET` with `subject` returns what the person saved, and the cost profile of the organization if the person saved none |
| `PersonProfileErrors` | `subject` with `kind` or `agent` is `invalid_profile`. In the context of a person, a `subject` that is not `org` is `invalid_profile` naming `subject` |
| `ForgetRemovesThePersonProfile` | after `Forget`, the next prediction for the person uses the cost profile of the organization |
| `ReportWeeks` | `from` or `to` not on a Monday at 00:00 UTC, `to` not after `from`, and more than 13 weeks are `invalid_query`. A week that is not closed is `open` |
| `ReportNeedsAGroup` | a closed week with 4 persons is `too_few` with no numbers. With 5, it is `ready` and each count is the sum over the persons |
| `ReportPoolsTheEstimate` | the error rate of a cell is the estimate over the rows of all its cases, equal to `estimate.Rate` over them |
| `ReportAnswerRule` | a field whose reviews come from fewer than 5 persons, or from one person with more than one half, is in `withheld` and absent |
| `ReportMergesSmallAgents` | an agent with fewer than 5 persons is in `other`. If `other` is too small, the smallest agent goes into it. The counts of the parts add up to `total`. A field withheld in one part is withheld in all parts |
| `ReportCountsReviewsBeforeTheClose` | a review after the week closed is `unanswered` in that week |
| `ReportKeepsAClosedWeek` | a read after a `Forget` of a person who took part returns the week as the first read returned it |
| `ReportNamesNoPerson` | no field of a report contains a subject, an action ID, a label or a time other than the bounds of the weeks |

The engine and the stub give the same report for the same ledger. A test
runs both on one scripted history, as for the reliability reports
([[008-ledger-and-estimation]], criterion 7).

### What a decision point must do

Nothing. A prediction already carries the thresholds and the audit rate
that apply. With the cost profile of a person, they are the meet. A
decision point applies them as before.

### What an authorization endpoint must do

- Decide `DELETE /v1/profiles` as `profile.write` on the same resource.
- Decide a `Profile` resource with `subject` for the person that it names
  and for no other person. The owners of an organization must not read
  or save the cost profile of a different person.
- Decide `report.read` on a `Report` resource. A person must not read the
  report of an organization only because they belong to it.

### New terms

The build adds these terms to `docs/terms.md`:

| Term | Meaning | In the API |
|---|---|---|
| cost profile of a person | A cost profile that a person saves for their own actions in one organization. It can make the cost profile of the organization stricter for that person. It cannot make it less strict. | `subject` in a `Profile` |
| stricter | A cost profile with an upper threshold that is the same or higher, and a lower threshold that is the same or lower, is stricter. | |
| report of an organization | The counts and the error rates of the actions of an organization, for all its persons together, by week. | `Report` |
| meet | The cost profile that takes the stricter value of each number from two cost profiles. The cost profile of a person and the cost profile of the organization give the meet that applies to that person. | `Stricter` |
| minimum group | The smallest number of persons that a number of a report of an organization must come from: 5. | `min_subjects` |

## Not in this spec

- A cost profile of a person for one agent or one kind of action. A
  person has one cost profile in one organization.
- A report by person, by kind of action, by label, or by a period other
  than a week.
- A certificate in the report of an organization.
- Who can read the report in a hosted installation. The decider of that
  installation decides it.
- Noise added to the numbers of a report (differential privacy). The
  group rule, the answer rule, the fixed weeks and the kept weeks are the
  protection of this spec.

## Acceptance criteria

| # | Criterion | Proved by |
|---|---|---|
| 1 | `DELETE /v1/profiles` removes exactly the saved scope and is idempotent. It asks `profile.write` on the resource of the scope. The next prediction uses the next scope in the order | the conformance cases `ProfileRemoved` and `ProfileRemovalErrors`, and `TestAuthorizerQuestions` |
| 2 | `Stricter` gives the higher of the two upper thresholds, the lower of the two lower thresholds, the higher audit rate and the stricter claim. Its costs give its thresholds to 1e-12 | a table test in the root package over the three named cost profiles and random accepted costs |
| 3 | The meet of two accepted cost profiles is accepted and puts one half in the ask range | a property test in the root package |
| 4 | A prediction, `learned`, an explanation with no recorded prediction, and the certificate of a person use the meet. A preview does not | the conformance cases `PersonProfileIsTheMeet` and `PersonProfileNeverLoosens`, and engine tests |
| 5 | A scope with `subject` refuses `kind` and `agent`, and in the context of a person, a `subject` that is not `org` | `PersonProfileErrors`, `TestProfileValidate` |
| 6 | A `Profile` resource has `subject` for a scope with `subject`. The owner policy lets only that person read, save and remove it | `server` tests under the owner policy |
| 7 | `Forget` removes the cost profile of the person in the organization | `ForgetRemovesThePersonProfile` |
| 8 | A report accepts only Monday bounds at 00:00 UTC, at most 13 weeks, and shows a week only after it closed | `ReportWeeks` |
| 9 | A week with fewer than 5 persons is `too_few` with no numbers and no count of persons | `ReportNeedsAGroup` |
| 10 | Each field that reads reviews follows the answer rule, also across the division by agent | `ReportAnswerRule`, `ReportMergesSmallAgents` |
| 11 | The division by agent has no cell with fewer than 5 persons, and its counts add up to `total` | `ReportMergesSmallAgents`, and a property test over random ledgers |
| 12 | A kept week does not change after a `Forget`, a late review, or a kept replay | `ReportKeepsAClosedWeek`, `ReportCountsReviewsBeforeTheClose` |
| 13 | A report contains no subject, action ID, label or time more exact than a week | `ReportNamesNoPerson` |
| 14 | `GET /v1/reports` asks `report.read` on `Report` with `org`, and the owner policy lets only an administrator read it | `TestAuthorizerQuestions`, `TestOwnerPolicy` |
| 15 | The engine and the stub give the same report for the same ledger | a parity test |
| 16 | `api/openapi.yaml` describes the routes and the types, and the vocabulary has `report.read` | `api` tests, `TestVocabulary` |

## Decisions

The owner reviewed this spec on 2026-11-02, approved the three parts,
and decided each open question of the draft:

1. **A kept week and Forget.** A week that a report showed never
   changes. A `Forget` leaves the kept weeks, because they hold only
   counts and estimates that passed the group rule and the answer rule.
2. **The minimum group.** 5, fixed. An installation cannot set it.
3. **The division by agent.** The report divides each week by agent,
   with the merge rules above.
4. **The week.** A week starts on a Monday at 00:00 UTC. A report has at
   most 13 weeks, and it shows a week 7 days after its end.
5. **The cost profile of a person for one agent.** Not in this spec. A
   person has one cost profile in one organization.
6. **The claim of the meet.** The meet takes the stricter claim: the
   lower `alpha` and the lower `delta`.
7. **One person and the counts.** The group rule alone applies to
   `subjects`, `decisions` and `flag_reviews.reviews`. The answer rule
   applies to the fields that read reviews.

Two questions of the design of the hosted platform take the default of
that design: Limen gives no count of the
persons who saved a cost profile, and Limen does not tell a person that
their cost profile no longer changes what applies. A client calculates
both with `Stricter` and the two reads of `GET /v1/profiles`.

The build fixes one point that the example above leaves open. Each cell
of a ready week has `applied`, `learned` and `withheld`. An error rate
that breaks the answer rule is absent from `applied` or `learned`, and
its path is in `withheld`. `withheld` is an empty list when the cell
withholds nothing.

v0.4.0 shipped before this build. The release with part 1 is the next
minor release, v0.5.0.

## Outcome

Recorded on 2026-11-02. The three parts are built, and every criterion
has a passing test in the tree. They shipped in v0.5.0, and v0.5.1,
which changes only the documents, is in production since 2026-11-02, in
hosted mode behind an authorization endpoint (`LIMEN_AUTHORIZER_URL`).
From the same day, hosted Topos v0.68.0 keys the calls of an
organization's agents by the organization (`TOPOS_DECISIONS_ORG=agent`,
[[005-adapters]]), so the cost profiles and the report of that
organization apply to them.

### What was built

| Piece | Where |
|---|---|
| `Scope.Subject`, `Scope.Validate`, the scope rules in `Profile.Validate`, `Stricter` | `profile.go` |
| `Admin.DeleteProfile`, `Reporter` in `Service` | `service.go`, `report.go` |
| the report types, `ReportQuery.Validate`, `ReportMinSubjects`, `ReportWeek`, `ReportClosesAfter`, `ReportMaxWeeks`, `WeekStart`, `WeekClosed`, `AnswerFields` | `report.go` |
| the week of a report, built once for every ledger: the group rule, the answer rule, the merge of small agents, the review cutoff | `estimate/report.go` (`Week`, `Weeks`, `OpenWeek`, `ReportCase`) |
| `DeleteProfile`, `Subjects`, `WeekReport`, `PutWeekReport` in the store, the memory store and the directory store | `engine/store`, `engine/store/file` |
| the meet in a prediction, `learned`, an explanation, a certificate and a replay; the removal; the report with kept weeks under a lock per organization; Forget of the person's profile | `engine/` |
| the same in the stub | `stub/` |
| `DELETE /v1/profiles`, `GET /v1/reports`, `subject` on the profile routes, the resources | `server/`, `client/`, `api/openapi.yaml` |
| `report.read` on `Report` | `authorizer/` |
| 15 conformance cases, `Target.Clocked`, a defect for each case | `conformance/` |

| # | Proof in the tree |
|---|---|
| 1 | the conformance cases `ProfileRemoved` and `ProfileRemovalErrors`; `TestAuthorizerQuestions` |
| 2 | `TestStricterNamedProfiles`, `TestStricterProperties` in the root package |
| 3 | `TestStricterProperties` |
| 4 | the conformance cases `PersonProfileIsTheMeet` (prediction, `learned`, explanation, certificate, preview) and `PersonProfileNeverLoosens`; `TestReplayUsesThePersonsProfile` |
| 5 | the conformance case `PersonProfileErrors`; `TestScopeValidate` |
| 6 | `TestPersonProfileOwnership`, `TestOwnerPolicy` in `server/` |
| 7 | the conformance case `ForgetRemovesThePersonProfile`; `TestForgetRemovesTheOwnProfile` |
| 8 | the conformance case `ReportWeeks`; `TestReportQueryValidate`, `TestWeeks` |
| 9 | the conformance case `ReportNeedsAGroup`; `TestWeekNeedsAGroup` |
| 10 | the conformance cases `ReportAnswerRule` and `ReportMergesSmallAgents`; `TestWeekAnswerRule`, `TestWeekMergesSmallAgents` |
| 11 | the conformance case `ReportMergesSmallAgents`; `TestWeekProperties` over random ledgers |
| 12 | the conformance cases `ReportKeepsAClosedWeek` and `ReportCountsReviewsBeforeTheClose`; `TestKeptWeekSurvivesAReopen`; the store suite's `WeekReports` |
| 13 | the conformance case `ReportNamesNoPerson`; `TestReportOwnerPolicy` |
| 14 | `TestAuthorizerQuestions`, `TestOwnerPolicy`, `TestReportOwnerPolicy` |
| 15 | `TestEngineAndStubReportOrganizationsAlike` |
| 16 | `TestSchemasMatchTypes`, `TestEnumsMatchConstants`, `TestOperations`, `TestRoutesMatchOpenAPI`, `TestVocabulary` |

The conformance cases run for the stub, the engine in memory and over a
directory, the client against the engine and the stub, and the client
against a running `limend`, each with a clock.

### What diverges from the design as written

| What it said | What was built | Why |
|---|---|---|
| `Target.Clocked` returns a service whose clock the case sets | `Clocked func(t *testing.T, now func() time.Time) limen.Service` | the case owns the clock and passes its reading |
| the cells of the example omit `applied` and `learned` | each cell of a ready week has `applied`, `learned` and `withheld` (Decisions) | one shape for a client |
| the contract lists the report types | also `ReportWeek`, `WeekStart`, `WeekClosed`, `AnswerFields` and `Scope.Validate` | a client checks a bound, a state and a path with the same functions |
| the computation of a week | one function, `estimate.Week`, that the engine and the stub call | the parity of criterion 15 holds by construction |
| `Forget` removes the person's profile | the engine removes it before the store's `Forget`; the store's `Forget` keeps profiles | a retry of a failed `Forget` finishes it |
| a replay | also meets the person's own profile in its scratch engine | a replay predicts for one person, as a live prediction does |
| `<dir>/reports/<org>/<week>.json` | the week's file is named by its first day, `2026-10-05.json` | a name that every file system takes |
| the resource of a person's profile | its id also ends in the subject | an authorizer that caches by id tells two persons apart |
| the terms of the build | `docs/terms.md` also has week, closed week, kept week, group rule and answer rule | the documents use them |

### What this leaves open

| Open | Why |
|---|---|
| a cost profile of a person for one agent | Decisions, 5 |
| a minimum group that an installation sets | Decisions, 2 |
| a count of the persons who saved a cost profile, and a notice that one no longer changes what applies | Decisions; a client calculates both |

