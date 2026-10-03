# API

This page describes the HTTP API of the Limen server, `limend`. Use the
API for a decision point that does not run the engine in its own process.
The [OpenAPI document](../api/openapi.yaml) contains the same routes.
[Integration](integrating.md) describes the Go interfaces for a decision
point in Go. [Running limend](running.md) describes how to run the
server.

## Conventions

- **Base path.** The server supplies each route under `/v1`. A platform
  that hosts Limen can put the routes under a different prefix.
- **Authentication.** Each request must have a bearer token:
  `Authorization: Bearer <token>`. The token must come from an issuer
  that the installation lists, for the audience that the installation
  verifies. A `limend` for one person accepts only the token in
  `LIMEN_TOKEN` ([Running limend](running.md)).
- **Permissions.** Limen verifies the token. Then Limen asks the
  authorization endpoint of the installation if the caller has the
  permission of the route. Limen asks before it does the work of the
  route. Each route below gives its permission.
- **Bodies.** Requests and responses are JSON. The replay upload is
  different: it is newline-delimited JSON. Times are RFC 3339.
- **Unknown fields.** A receiver ignores the fields that it does not know.
  Because of this, a newer client can send requests to an older server.
  The receiver validates the fields that it knows strictly. One error
  names all the incorrect fields.
- **Keys.** Three fields identify each record: `org`, `subject`, and
  `action_id`. The decision point gives each action its `action_id`. The
  `action_id` must be unique for the organization and the person. The
  server does not make identifiers.
- **Repeated requests.** If you request a prediction for the same action
  again, you get the first prediction, with status 200. If you send a
  different action with a key that is already in use, Limen refuses it
  with `action_conflict`. Each action has one decision only. Limen refuses
  a second decision with `already_decided`.
- **Order.** A prediction is optional. A decision is necessary before an
  answer. If an action has no decision, Limen refuses the answer with
  `no_decision`.
- **Time.** Limen records the time of each record when the record
  arrives. Limen counts the decisions of each person itself. No request
  contains a time, except a record in a replay.
- **Size.** A JSON body can be 1 MiB or less. A replay upload can be
  256 MiB or less, with not more than 100,000 records, and each line can
  be 1 MiB or less. If a request is larger, Limen refuses it with
  `body_too_large`.
- **Pages.** A list returns `{"entries": [...], "next_cursor": "..."}`.
  The `next_cursor` is present only if there is a next page. To get the
  next page, send this value as `cursor`. The `limit` is 1 to 500. The
  default is 100.

## Predictions

`POST /v1/predictions`. Permission: `case.predict`.

The body is an action. It contains what the decision point sees before
the action runs.

```json
{
  "org": "org_7",
  "subject": "https://issuer.example|usr_42",
  "action_id": "ses_9f/toolu_01",
  "actor": {"kind": "agent", "id": "agt_ci", "session": "ses_9f"},
  "kind": "tool",
  "name": "bash",
  "effect": "external",
  "reversible": false,
  "input": {"command": "git push origin main"},
  "target": {"kind": "ref", "id": "refs/heads/main", "sensitivity": 0.9},
  "environment": {"kind": "sandbox", "credentialed": true},
  "attributes": {"protected": true},
  "signals": [
    {"source": "rules/1", "score": 0.4},
    {"source": "classifier/claude-auto-mode", "verdict": "allow"}
  ]
}
```

| Field | Required | Meaning |
|---|---|---|
| `org`, `subject`, `action_id` | yes | The key. |
| `actor` | no | The agent that proposed the action: `kind`, `id`, and `session`. If `actor` is present, `kind` is necessary. |
| `kind`, `name` | yes | The domain of the action and the operation. |
| `effect` | yes | `none`, `read`, `write`, or `external`. |
| `reversible` | no | `true` if the action can be undone. |
| `input` | no | Any JSON. Limen reads it only to calculate the features, with fixed code. Limen does not keep it. |
| `target` | no | `kind`, `id`, and `sensitivity` from 0 to 1. If `target` is present, `kind` and `id` are necessary. |
| `environment` | no | `kind` and `credentialed`. |
| `attributes` | no | Not more than 64 values. Each value is a string, a finite number, or a boolean. |
| `signals` | no | Each signal has a `source`. It also has a `verdict`, a `score` from 0 to 1, or both. |

The response is the prediction:

```json
{
  "org": "org_7",
  "subject": "https://issuer.example|usr_42",
  "action_id": "ses_9f/toolu_01",
  "verdict": "ask",
  "approve": 0.58,
  "uncertainty": 0.71,
  "thresholds": {"allow_above": 0.9, "block_below": 0.25},
  "profile": {"org": "org_7"},
  "audit_rate": 0.05,
  "source": "learned/3",
  "reason": "The approval probability is 0.58, between the block threshold (0.25) and the allow threshold (0.90). Limen suggests ask."
}
```

| Field | Meaning |
|---|---|
| `verdict` | The suggested verdict: `allow`, `ask`, or `block`. It is never `flag`. Only the decision point applies `flag`. |
| `approve` | The approval probability. |
| `uncertainty` | The uncertainty of the approval probability. An action that is not similar to an earlier action has a high value. |
| `thresholds` | The thresholds of the cost profile. Above `allow_above`, the suggested verdict is `allow`. Below `block_below`, it is `block`. Between the two values, it is `ask`. |
| `profile` | The scope of the cost profile that applies. |
| `audit_rate` | The audit rate of the cost profile. |
| `source` | The model that made the prediction: `learned/<version>`. |
| `reason` | One sentence for the record of the person. Do not show it to an agent. |

Errors: `invalid_action`, `action_conflict`, `body_too_large`, and the
common errors. If the status is `503`, the decision point must hold the
action for a person.

## Decisions

`POST /v1/decisions`. Permission: `case.decide`.

```json
{
  "org": "org_7",
  "subject": "https://issuer.example|usr_42",
  "action_id": "ses_9f/toolu_01",
  "verdict": "flag",
  "review_probability": 0.05,
  "source": "topos/progressive"
}
```

| Field | Required | Meaning |
|---|---|---|
| `org`, `subject`, `action_id` | yes | The key. |
| `verdict` | yes | The applied verdict: `allow`, `flag`, `ask`, or `block`. |
| `review_probability` | yes | The review probability. The decision point sets it before the action runs. It is more than 0 for `flag` and `ask`. It is less than 1 for `allow` and `block`. [The ledger](ledger.md#the-review-probability) gives the value for each situation. |
| `source` | yes | The source of the verdict, for example `rules/1` or `topos/progressive`. |
| `action` | if Limen has no prediction for the action | The action, as in a prediction, with the same key. If Limen has a prediction, the action must be the same. |

The response is status 204 with no body.

Errors:

- `invalid_decision`.
- `already_decided`, status 409: the action has a decision already.
- `action_conflict`, status 409: the action is different from the action
  in the prediction.
- `body_too_large`.
- The common errors.

## Answers

`POST /v1/answers`. Permission: `case.answer`.

```json
{"org": "org_7", "subject": "https://issuer.example|usr_42", "action_id": "ses_9f/toolu_01", "approve": true}
```

| Field | Required | Meaning |
|---|---|---|
| `org`, `subject`, `action_id` | yes | The key. |
| `approve` | yes | `true` if the person approves the action. For an action that ran before the review, `true` if the person would approve it. |
| `by` | no | The person who answered, if the caller sends the answer for a different person. Limen keeps `by` only if the caller is not the `subject`. Otherwise, Limen records the caller. In all cases, the caller needs the permission `case.answer` for the `subject`. |

The response is status 204 with no body. If the decision showed the
action to a person, the first answer is the review of the action. Only
reviews go into the error rates. All other answers only teach the model.

Errors: `invalid_answer`, `no_decision`, and the common errors.

## Cases

`GET /v1/cases?org=...&subject=...`. Permission: `case.list`.

The response gives the cases of one person in one organization. The
oldest case is first. Each case contains the action without its input,
the prediction if there is one, the decision, and the answers.

| Parameter | Meaning |
|---|---|
| `org`, `subject` | Required. |
| `action_id` | Gives only the case of this action. |
| `pending` | `true` gives the review queue: the actions that a decision showed to the person and that have no review yet. |
| `cursor` | The `next_cursor` of the previous page. |
| `limit` | 1 to 500. The default is 100. |

```json
{
  "entries": [
    {
      "action": {"org": "org_7", "subject": "https://issuer.example|usr_42", "action_id": "ses_9f/toolu_01", "kind": "tool", "name": "bash", "effect": "external"},
      "prediction": {"verdict": "ask", "approve": 0.58, "...": "..."},
      "decision": {"verdict": "ask", "review_probability": 1, "source": "topos/progressive", "...": "..."},
      "answers": [{"approve": true, "by": "https://issuer.example|usr_42", "at": "2026-10-02T10:03:40Z", "review": true}]
    }
  ],
  "next_cursor": "c_8Qx2"
}
```

In an answer, `review` is `true` if the answer is the review of the
action. Only this answer goes into the error rates.

Errors: `invalid_query` and the common errors.

## Reliability

`GET /v1/reliability?org=...&subject=...&of=...&from=...&to=...`.
Permission: `reliability.read`.

The response is the reliability report of one source, for one person, in
one period of time. The report shows how often the verdicts of the source
do not agree with the answers of the person. Set `of` to the source:

- `learned` for the suggested verdicts of Limen.
- `applied` for the verdicts that decision points applied.
- The `source` of a decision, for example `rules/1`.
- The `source` of a signal, for example `classifier/claude-auto-mode`.

The `from` and `to` parameters are RFC 3339 times. The `from` time must
be before the `to` time.

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
| `false_allow` | The false allow rate: the part of the allows of the source that the person would deny. |
| `false_block` | The false block rate: the part of the blocks of the source that the person would approve. |
| `value` | The estimate. It is `null` if a person could see none of these actions. Then Limen cannot estimate the rate. `null` does not mean zero. |
| `interval` | A 95% interval. It is present if `value` is present. |
| `covered` | The part of the allows (or blocks) of the source that a person could see. The estimate applies to this part only. |
| `reviews`, `audits` | The number of reviews in the estimate, and the number of audits among them. |
| `unanswered` | The number of actions that a decision showed to the person and that have no review yet. |
| `certificate` | A test of the claim of the cost profile: a false allow rate below `alpha`, with confidence `1 - delta`. `clean` counts the audits of allows of the source in the period that the person approved, from the most recent, without a gap. A denied allow stops the count. `needed` is the number that the claim needs. `holds` is `true` if `clean` is equal to or more than `needed`. |

The numbers above are examples. Errors: `invalid_query` and the common
errors.

## Cost profiles

`GET /v1/profiles?org=...&kind=...&agent=...`. Permission:
`profile.read`.

`PUT /v1/profiles`. Permission: `profile.write`.

```json
{
  "org": "org_7",
  "kind": "spend",
  "costs": {"false_allow": 50, "false_block": 4, "ask": 1},
  "audit_rate": 0.05,
  "certify": {"alpha": 0.01, "delta": 0.05}
}
```

A `GET` returns the cost profile that applies to the scope. This is the
most specific saved cost profile, in this order: kind and agent, agent,
kind, organization. If there is no saved cost profile, the `GET` returns
the default cost profile, with `"default": true`.

A `PUT` saves the cost profile for the scope in its body. The response to
a `GET` and to a `PUT` contains the `thresholds` that Limen calculates
from the costs. For the example above, they are
`{"allow_above": 0.98, "block_below": 0.25}`. Limen ignores a value that
you send for `thresholds` or `default`.

| Field | Meaning |
|---|---|
| `org` | Required. `kind` and `agent` make the scope narrower. |
| `costs.false_allow` | The cost of a false allow: the action runs and the person would deny it. It must be more than two times `costs.ask`. |
| `costs.false_block` | The cost of a false block: the action does not run and the person would approve it. It must be more than two times `costs.ask`. |
| `costs.ask` | The cost of one `ask`. It must be more than 0. |
| `audit_rate` | The audit rate, from 0 to 1. |
| `certify` | Optional: `alpha` in (0, 1] and `delta` in (0, 1). Without `certify`, the claim is a false allow rate below 1% with 95% confidence. This claim needs 300 clean audits. |

Errors:

- `invalid_query` for a `GET`.
- `invalid_profile` for a `PUT`.
- `ask_too_costly` for a `PUT`. With these costs, an approval probability
  of 0.5 is outside the ask range. Limen would then suggest `allow` or
  `block` for a person that it has no answers from.
- The common errors.

## Replays

`POST /v1/replays`. Permission: `replay.create`.

Add `?keep=true` to add the records to the live models and to the ledger.
Without `keep`, the replay uses a temporary model and keeps nothing.

The body is newline-delimited JSON
(`Content-Type: application/x-ndjson`). Each line is one record. The
[size limits](#conventions) apply.

```json
{"at": "2026-09-02T09:12:03Z", "action": {"...": "..."}, "verdict": "ask", "review_probability": 1, "source": "rules/1", "approve": true}
{"at": "2026-09-02T09:15:44Z", "action": {"...": "..."}, "verdict": "allow", "review_probability": 0, "source": "rules/1"}
{"at": "2026-09-02T11:40:10Z", "action": {"...": "..."}, "verdict": "flag", "review_probability": 0.05, "source": "rules/1", "approve": false}
```

Limen predicts the records in the order of `at`. It predicts each record
before it learns the answer of that record. Then it reports what it would
suggest:

```json
{
  "records": 12840,
  "answered": 3102,
  "would": {"allow": 9811, "ask": 2790, "block": 239},
  "allowed_denied": 41,
  "blocked_approved": 17,
  "false_allow": {"value": 0.021, "interval": [0.012, 0.034], "covered": 0.19, "reviews": 1904, "audits": 0, "unanswered": 0},
  "kept": false
}
```

The numbers are examples. Errors: `invalid_replay`, with the incorrect
records in the details, `body_too_large`, and the common errors.

## Forget a person

`POST /v1/subjects/forget`. Permission: `subject.forget`.

```json
{"org": "org_7", "subject": "https://issuer.example|usr_42"}
```

This request deletes all data that Limen has about the person in the
organization. This data is the cases, the answers, and the model that
Limen learned from them. The response is status 204. You can send the request again
with the same result. If Limen has no data about the person, the response
is also status 204.

Errors: `invalid_query` and the common errors.

## Errors

All errors have the same shape:

```json
{
  "error": {
    "code": "invalid_action",
    "message": "The action has a missing field or a value that is not permitted. Correct the fields that the details list, then send the action again.",
    "details": {
      "request_id": "req_7f3c9a",
      "detail": "kind is empty; environment.kind is empty",
      "fields": ["kind", "environment.kind"]
    }
  }
}
```

Use `code` for decisions in your program. Show `message` to a person.
Keep `details` for the person who finds the cause of a problem. Each code
has one status and one message:

| Code | Status | Message |
|---|---|---|
| `invalid_action` | 400 | The action has a missing field or a value that is not permitted. Correct the fields that the details list, then send the action again. |
| `invalid_decision` | 400 | The decision has a missing field or a value that is not permitted. Correct the fields that the details list, then send the decision again. |
| `invalid_answer` | 400 | The answer has a missing field. Correct the fields that the details list, then send the answer again. |
| `invalid_profile` | 400 | The cost profile has a missing field or a value that is not permitted. Correct the fields that the details list, then save the cost profile again. |
| `invalid_query` | 400 | The request has a missing field or a value that is not permitted. Correct the fields that the details list, then send the request again. |
| `invalid_replay` | 400 | A replayed record has a missing field or a value that is not permitted. Correct the records that the details list, then send the replay again. |
| `ask_too_costly` | 422 | With these costs, Limen would not ask about an action of a person that it has no answers from. Make the cost of each type of error more than two times the cost of an ask. |
| `action_conflict` | 409 | A different action already uses this action ID. Give each action its own ID. |
| `already_decided` | 409 | This action already has a decision. Limen accepts one decision for each action. |
| `no_decision` | 409 | This action has no recorded decision. Record the decision, then send the answer. |
| `unauthenticated` | 401 | The request has no valid credential. Sign in again, then send the request again. |
| `forbidden` | 403 | You do not have permission for this request. Ask an administrator of the organization for access. |
| `body_too_large` | 413 | The request is larger than the limit of Limen. Send it in smaller parts. |
| `rate_limited` | 429 | There are too many requests. Wait a short time, then send the request again. |
| `internal` | 500 | Limen had an internal failure. Send the request again. If the failure continues, report it with the request ID. |
| `authorizer_unavailable` | 503 | The permission service is not available. Send the request again after a short time. |
| `unavailable` | 503 | Limen is not available now. Hold the action for a person, and send the request again after a short time. |

Each route can return the common errors: `unauthenticated`, `forbidden`,
`rate_limited`, `internal`, `authorizer_unavailable`, and `unavailable`.

## Permissions

Each route asks the authorization endpoint for one permission on one
resource. An authorization endpoint for Limen answers these permissions
and no others:

| Route | Permission | Resource kind | Resource fields |
|---|---|---|---|
| `POST /v1/predictions` | `case.predict` | `Case` | `org`, `subject` |
| `POST /v1/decisions` | `case.decide` | `Case` | `org`, `subject` |
| `POST /v1/answers` | `case.answer` | `Case` | `org`, `subject` |
| `GET /v1/cases` | `case.list` | `Case` | `org`, `subject` |
| `GET /v1/reliability` | `reliability.read` | `Reliability` | `org`, `subject` |
| `GET /v1/profiles` | `profile.read` | `Profile` | `org`, `action_kind`, `agent` |
| `PUT /v1/profiles` | `profile.write` | `Profile` | `org`, `action_kind`, `agent` |
| `POST /v1/replays` | `replay.create` | `Replay` | `org` |
| `POST /v1/subjects/forget` | `subject.forget` | `Subject` | `org`, `subject` |

The `Profile` resource gives the kind of action as `action_kind`. The
reason is that the resource object of the shared authorizer uses `kind`
for the kind of the resource itself.

Usually, the caller is the person of the request. A decision point can
predict, decide, or answer for the person that it runs for. An agent
harness is an example. The authorization endpoint must give this decision
point the `Case` permissions for that person. These permissions also let
it send an answer with `by`.
