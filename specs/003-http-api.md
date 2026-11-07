---
title: "HTTP API: routes, bodies, statuses, the authorizer action and resource fields per route, subject binding, limits"
status: complete
track: core
depends_on: [001-architecture.md, 002-contract.md]
affects: [api/, authorizer/, client/, cmd/limend/, server/]
effort: medium
created: 2026-09-28
updated: 2026-11-03
author: changkun
---

# HTTP API

## Overview

`limend` serves the contract of [[002-contract]] over HTTP for decision
points that do not embed the engine: hosted agent runners, hook
commands, decision points written in other languages. `client/` is the
Go implementation of `limen.Service` over this API, so a Go decision
point changes nothing but its constructor to move between the engine and
the server. `api/openapi.yaml` is the same surface as an OpenAPI 3.1
document.

## Design

### Routes

`limend` serves the routes under `/v1`. *Amended 2026-11-01:* with
`LIMEN_BASE_PATH` set, for example to `/v1/oversight`, it also serves
every route with that prefix in place of `/v1`, with the same handler and
the same authorizer question, so an ingress that forwards a public prefix
unchanged reaches it (`/v1/oversight/learned` is `/v1/learned`). The value
is an absolute path with no trailing slash and is not `/v1`; `limend`
refuses any other at start (exit 2). The client takes the same prefix as
`Options.BasePath`.

`GET /v1/version`, and `GET <base path>/version` with a base path, is the
one route that takes no token and asks the authorizer nothing. It answers
`{"version", "commit", "build_time"}`, the values `limend` was built with,
so the release smoke reads which build serves a public prefix and compares
`version` with the tag. It is not in the contract of `api/openapi.yaml`.

| Method | Path | Body | Success | Authorizer action | Contract |
|---|---|---|---|---|---|
| POST | `/v1/predictions` | `Action` | 200 `Prediction` | `case.predict` | `Predictor.Predict` |
| POST | `/v1/decisions` | `Decision` | 204 | `case.decide` | `Ledger.Record` |
| POST | `/v1/answers` | `Answer` | 204 | `case.answer` | `Ledger.Answer` |
| GET | `/v1/cases?org&subject&action_id&pending&cursor&limit` | none | 200 `Page` | `case.list` | `Ledger.Cases` |
| GET | `/v1/reliability?org&subject&of&from&to` | none | 200 `Reliability` | `reliability.read` | `Ledger.Reliability` |
| GET | `/v1/profiles?org&kind&agent&subject` | none | 200 `Profile` (the effective one) | `profile.read` | `Admin.Profile` |
| PUT | `/v1/profiles` | `Profile` | 200 `Profile` (as stored) | `profile.write` | `Admin.PutProfile` |
| DELETE | `/v1/profiles?org&kind&agent&subject` | none | 204 | `profile.write` | `Admin.DeleteProfile` |
| POST | `/v1/replays?keep` | NDJSON of `ReplayRecord` | 200 `ReplayReport` | `replay.create` | `Admin.Replay` |
| POST | `/v1/subjects/forget` | `{"org", "subject"}` | 204 | `subject.forget` | `Admin.Forget` |
| GET | `/v1/explanations?org&subject&action_id` | none | 200 `Explanation` | `case.list` | `Explainer.Explain` |
| GET | `/v1/learned?org&subject&agent&kind` | none | 200 `Learned` | `case.list` | `Explainer.Learned` |
| POST | `/v1/previews` | `PreviewRequest` | 200 `Preview` | `reliability.read` | `Explainer.Preview` |
| GET | `/v1/reports?org&from&to` | none | 200 `Report` | `report.read` | `Reporter.Report` |
| GET | `/v1/calibration?org&subject&from&to` | none | 200 `Calibration` | `reliability.read` | `Ledger.Calibration` |

The explanations, learned and previews routes are
[[009-explanations]]'s, added 2026-11-01 with the code `no_case` (404)
for an explanation of an action Limen has no record of. The removal of
a profile, `subject` on the profile routes, and the reports are
[[010-organizations]]'s, added 2026-11-02 (Amendments).

Every route answers the errors of [[002-contract]] that its contract
method can return, plus `unauthenticated`, `forbidden`, `body_too_large`
for a body past its limit, `internal` and `authorizer_unavailable`; the
OpenAPI document lists them per operation. `rate_limited` is reserved for
an installation that puts a rate limit in front of `limend`, which itself
limits nothing.

### The authorizer

Each route asks the installation's authorization endpoint
(`latere.ai/x/pkg/authz`) one action of `authorizer.Vocabulary()` before
it acts, about a resource whose kind and fields are fixed per action:

| Action | Resource kind | Resource fields |
|---|---|---|
| `case.predict`, `case.decide`, `case.answer`, `case.list` | `Case` | `org`, `subject` |
| `reliability.read` | `Reliability` | `org`, `subject` |
| `profile.read`, `profile.write` | `Profile` | `org`, `action_kind`, `agent` (empty when unscoped), and `subject` for a person's own context (below). The field is `action_kind`, not `kind`, because the shared authorizer's flat wire object reserves `kind` for the resource kind |
| `replay.create` | `Replay` | `org`, asked once for each organization the records name, because the route takes no `org` of its own |
| `subject.forget` | `Subject` | `org`, `subject` |
| `report.read` | `Report` | `org` ([[010-organizations]]) |

With no endpoint configured, `limend` applies the shared owner policy: a
caller acts on cases and reports whose `subject` is the caller's own
rendered subject, and forgets that subject, admins act on everything,
and every other request is refused.

*Amended 2026-11-01: a person's own context.* A decision point keys the
actions of a person with no organization by the person's rendered
subject as `org` (issuer and `sub` joined by `|`). A profile whose `org`
contains `|` is such a context, and its `Profile` resource carries
`subject` equal to `org`, so the owner policy lets that person read and
write it. No organization id contains `|`, so an organization's profiles
carry no `subject` and stay its admins'. A replay's resource carries no
`subject` for any org, so replays stay admin-only.

### Subject binding and relays

A prediction, a decision and an answer name a subject, and in general
the caller is not that subject: a harness predicts for the person its
agent works for. The authorizer decides whether the caller may act for
the subject, from the resource fields above; nothing in a body is
trusted to grant it. A caller that may `case.answer` for a subject may
set `Answer.by` to say who answered; without that permission `by` is
ignored and the answerer is the caller. Decisions carry the review
probability the ledger weights answers by, so `case.decide` is granted
to decision points and not to the people who answer.

### Ordering and pagination

`GET /v1/cases` returns a subject's cases oldest first by the time Limen
received the action, `{"entries": [...], "next_cursor": "..."}`, where
`next_cursor` is present only when a further page exists. `limit` runs
from 1 to 500 with a default of 100; a value outside is `invalid_query`.
`pending=true` narrows the page to actions whose decision was shown
(`flag` or `ask`) and that have no review yet: the review queue.
`action_id` narrows it to one case.

### Bodies and limits

- Request bodies are JSON (`application/json`), except a replay, which
  is newline-delimited JSON (`application/x-ndjson`), one `ReplayRecord`
  per line, at most 100000 records; beyond that the server answers
  `body_too_large` before learning anything.
- Every other body is at most 1 MiB; an `Action` is bounded further by
  its 64 attributes.
- Receivers ignore unknown fields, so a newer client works against an
  older server; known fields are validated strictly, and a value that
  breaks a rule is the route's `invalid_*` code naming every field at
  fault.
- `GET /v1/profiles` answers the effective profile for the scope, the
  default included (`"default": true`), and never 404. `PUT` derives
  `thresholds`, fills a zero `certify` with the default, and ignores a
  sent `thresholds` or `default`.
- Times in bodies are RFC 3339. Limen stamps its own receive times;
  `ReplayRecord.at` is the one time a caller supplies, and only for
  history.

### Errors

The error body is the family's envelope:

```json
{"error": {"code": "invalid_action", "message": "<the code's fixed sentence>",
  "details": {"request_id": "...", "detail": "<developer detail>", "fields": ["kind"]}}}
```

`message` is `Code.Message()`, `detail` is the `*Error`'s detail, and
`fields` its fields. A 503 answer to a prediction means the decision
point holds the action for a person (`FailClosed` does this for a Go
caller).

### The OpenAPI document and its test

`api/openapi.yaml` is written by hand and held to the code by
`api/openapi_test.go`, which fails when:

- a schema's properties differ from the JSON names of its Go type
  (embedded structs flattened), or a field the encoder always writes is
  not `required`;
- an enum differs from the Go constants (verdicts, effects);
- a response for an error code differs from `"<status> <code>:
  <message>"`, or a code has no response;
- an operation's `x-limen-action` is not in the vocabulary, an action is
  asked by no route, or an error response sits under a status that is not
  its code's.

## Acceptance criteria

| # | Criterion | Proved by |
|---|---|---|
| 1 | The OpenAPI document agrees with the Go types, the error table and the vocabulary in the four ways listed | `api/openapi_test.go` |
| 2 | Every route asks exactly its action with exactly the resource fields above before acting, and acts on nothing when the authorizer refuses or is unavailable | `limend` handler tests against `authz/stub` |
| 3 | A caller the authorizer does not allow for a subject cannot predict, decide, answer, list or read reliability for that subject | the same tests |
| 4 | `Answer.by` is recorded only for a caller allowed `case.answer` on the case | a handler test |
| 5 | A replay of 100001 records is `body_too_large` and learns nothing; an invalid record is `invalid_replay` naming its line | handler tests |
| 6 | An unknown request field is ignored, and a known field out of range is the route's `invalid_*` naming it | handler tests |
| 7 | `client/` satisfies `limen.Service` and passes the conformance suite against a running `limend` | an e2e test with `conformance.Run` ([[006-conformance]]) |
| 8 | With a base path, every route answers under it as under `/v1`, with the same authorizer question; an invalid base path stops `limend` with exit 2 | `server` and `cmd/limend` tests |
| 9 | A person reads and writes the profiles of their own context and no other person's; a non-admin cannot write an organization's profile; a replay is admin-only for a person's own context too | `server` tests under the owner policy |

## Amendments

**2026-11-01.** Two contract decisions made when [[009-explanations]]
was built, each written into the section it changes:

- *Base path* (Routes): `LIMEN_BASE_PATH` serves the API under a second
  prefix, so a hosted installation routes a public path such as
  `/v1/oversight` to `limend` without a rewrite. The earlier sentence,
  that the server is told its public base for the links it writes, is
  replaced: `limend` writes no links.
- *A person's own context* (The authorizer): profiles of a context keyed
  by a person's rendered subject are that person's. Criteria 8 and 9 hold
  both.
- *Explanations* (Routes): three routes and the code `no_case`.

**2026-11-02.** [[010-organizations]] adds two routes and a field, with
no new error code: `DELETE /v1/profiles`, asked as `profile.write` on the
scope's `Profile`; `GET /v1/reports`, asked as the new action
`report.read` on the new resource kind `Report` with `org`; and `subject`
on a profile's scope, which a `Profile` resource carries.

**2026-11-03.** Wave 1 of [[011-consent]] adds optional fields and no
route, action or resource: the claims of a scope on an action, `scope`
and `cover` on a prediction, `cover` on an explanation and a learned
entry, the six fields of how a person answered on `POST /v1/answers`,
on a recorded answer and on a replay record with `answered_at`,
`consent` on a profile, and `considered` on a certificate. The sentence
of `invalid_answer` names a value that is not permitted. `api/openapi.yaml`
describes each one, and its test pairs the new schemas with their Go
types and the new lists with their constants. `GET /v1/calibration`
waits for wave 2.

**2026-11-03.** Wave 2 of [[011-consent]] adds one route and one
optional field, with no new action, resource kind or error code:
`GET /v1/calibration?org&subject&from&to`, asked as `reliability.read`
on the subject's `Reliability`, with `org` and `subject`, as
`GET /v1/reliability` is, because the report reads the same reviews; and
`calibration` on an explanation. An invalid window is `invalid_query`.
`api/openapi.yaml` describes the route and the schemas `Calibration`,
`CalibrationBin` and `CalibrationState`, and its tests hold them to the
Go types and the states to their constants. `TestAuthorizerQuestions`
holds the question and `TestCalibrationIsThePersons` the owner policy.

## Outcome

Recorded on 2026-11-01, after v0.3.0. The API is built as designed and is
in production: a hosted `limend` serves it under
`LIMEN_BASE_PATH=/v1/oversight` for hosted Topos and for the clients
that show a person their explanations. Every criterion has a passing
test in the tree.

### What was built

| Piece | Where |
|---|---|
| the handler: the routes, authentication (`StaticToken`, `JWT`), the owner policy, the authorizer questions, the base path (`CheckBasePath`), the error envelope | `server/` |
| the program, its two modes of access and its configuration | `cmd/limend` |
| the Go client | `client/` |
| the authorizer vocabulary | `authorizer/` |
| the OpenAPI document and its test | `api/openapi.yaml`, `api/openapi_test.go` |

| # | Proof in the tree |
|---|---|
| 1 | `TestSchemasMatchTypes`, `TestNoClosedSchemas`, `TestEnumsMatchConstants`, `TestResponsesMatchErrorTable`, `TestOperations`; the server's `TestRoutesMatchOpenAPI` |
| 2 | `TestAuthorizerQuestions`, `TestRefusalsActOnNothing` in `server/` |
| 3 | `TestSubjectBinding`, `TestOwnerPolicy`; end to end, `TestHostedOwnerPolicy` in `cmd/limend` |
| 4 | `TestAnswerBy` |
| 5 | `TestReplayLimits`, `TestReplayReadError` |
| 6 | `TestDecoding`, `TestQueryErrors` |
| 7 | `TestConformance` in `cmd/limend`, which starts `limend` on a fresh ledger directory for each case; also `TestConformanceEngine`, `TestConformanceStub` in `client/` |
| 8 | `TestBasePath` in `server/` and in `cmd/limend`, and `TestConfiguration` |
| 9 | `TestOwnContextProfile` |

### What diverges from the design as written

| What it said | What was built | Why |
|---|---|---|
| the proofs of criteria 2 to 6 are `limend` handler tests | they are tests of `server/` | the handler moved to its own package so the client tests and `cmd/limend` share it |
| a replay is bounded by its record count | the upload is also at most 256 MiB, and each line at most 1 MiB, both `body_too_large` | a record count alone does not bound the memory a line can take |
| the owner policy covers cases and reports | it also lets a caller forget its own subject | a person's records are theirs ([[001-architecture]], invariant 8) |
| not in the design | the server keeps a caller's `X-Request-Id` or makes one; an error that is not the contract's answers `unavailable`, and a panic `internal`; `CheckBasePath` also refuses a space and the characters `?`, `#`, `{` and `}`; the authorization endpoint's bearer comes from `LIMEN_AUTHORIZER_TOKEN` or `LIMEN_AUTHORIZER_TOKEN_FILE` | operation details; `docs/api.md` gives the limits and the request ID, and `docs/configuration.md` the variables |

### What this leaves open

| Open | Why |
|---|---|
| a rate limit | `rate_limited` is reserved for an installation that limits in front of `limend` |
| a `limend` that more than one process serves | the ledger is the directory store, so one process serves it until the shared store of [[004-engine]] exists |
