# Writing an authorization endpoint

This page is for a person who writes the authorization endpoint of a
hosted `limend`. Limen verifies who a caller is. It does not decide what
the caller can do. For each request, it asks one question: can this
subject do this action on this resource? Then it obeys the answer.
Without `LIMEN_AUTHORIZER_URL`, the owner policy answers
([The owner policy](#the-owner-policy)). If you set
`LIMEN_AUTHORIZER_URL`, your endpoint answers. Use an endpoint to add
organizations, services that act for their persons, and shared reports.

The Go package
[`latere.ai/x/limen/authorizer`](https://pkg.go.dev/latere.ai/x/limen/authorizer)
([source](../authorizer)) contains the vocabulary: each permission and
the kind of resource that it applies to. Import the package. Do not copy
the strings.

## The question

`limend` sends one `POST` to `LIMEN_AUTHORIZER_URL` for each question,
with `Authorization: Bearer <token>` from `LIMEN_AUTHORIZER_TOKEN` or
`LIMEN_AUTHORIZER_TOKEN_FILE`. It waits 5 seconds or less for an answer.
A question for a prediction:

```json
{
  "subject": "https://issuer.example|usr_42",
  "issuer": "https://issuer.example",
  "sub": "usr_42",
  "claims": {"iss": "https://issuer.example", "sub": "usr_42", "aud": "limen", "email": "ada@example.com"},
  "action": "case.predict",
  "resource": {"kind": "Case", "id": "https://issuer.example|usr_42", "org": "org_7", "subject": "https://issuer.example|usr_42"},
  "request": {"id": "req_7f3c9a", "ip": "203.0.113.7", "user_agent": "curl/8.7.1"}
}
```

- `subject` is the issuer and the `sub` of the token, with a pipe
  between them. Limen names each person and each caller in this form.
- `claims` are all the claims of the token, as the issuer signed them.
  Limen reads none of them. Your endpoint decides if a caller is a
  person or a service, and if a person is a member of an organization.
- `action` is one of the permissions below.
- `resource` has its `kind` and its `id`, and the fields of the
  permission next to them.
- `request` has the request ID, the network address of the peer, and the
  user agent. Limen does not read `X-Forwarded-For`. Behind a proxy, `ip`
  is the address of the proxy.

## The permissions

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
| `GET /v1/calibration` | `reliability.read` | `Reliability` | `org`, `subject` |
| `GET /v1/profiles` | `profile.read` | `Profile` | `org`, `action_kind`, `agent`, and `subject` for a cost profile of a person or the context of a person |
| `PUT /v1/profiles` | `profile.write` | `Profile` | `org`, `action_kind`, `agent`, and `subject` for a cost profile of a person or the context of a person |
| `DELETE /v1/profiles` | `profile.write` | `Profile` | `org`, `action_kind`, `agent`, and `subject` for a cost profile of a person or the context of a person |
| `POST /v1/replays` | `replay.create` | `Replay` | `org` |
| `POST /v1/subjects/forget` | `subject.forget` | `Subject` | `org`, `subject` |
| `GET /v1/explanations` | `case.list` | `Case` | `org`, `subject` |
| `GET /v1/learned` | `case.list` | `Case` | `org`, `subject` |
| `POST /v1/previews` | `reliability.read` | `Reliability` | `org`, `subject` |
| `GET /v1/reports` | `report.read` | `Report` | `org` |

The `Profile` resource gives the kind of action as `action_kind`. The
reason is that the resource object of the shared authorizer uses `kind`
for the kind of the resource itself.

A cost profile can be for the context of a person. The `org` of such a
cost profile is the subject of the person, with the issuer and the `sub`
and a pipe between them. A decision point puts the actions of a person
without an organization in this context. Then the `Profile` resource also
has `subject`, equal to `org`, and the shared owner policy lets the
person read and save these cost profiles.

An organization ID never contains a pipe. Because of this, the cost
profiles of an organization have no `subject`, and only an admin can read
or save them. Replays have no `subject`, also for the context of a
person.

A cost profile of a person has `subject` in its `Profile` resource. The
shared owner policy lets that person read, save, and remove it. An
authorization endpoint must decide it for that person and for no other
person. The owners of an organization must not read or save the cost
profile of a different person. A resource never names two persons: in the
context of a person, `subject` is equal to `org`.

A `Report` resource has no `subject`, because a report is about an
organization. Under the shared owner policy, only an admin can read a
report. An authorization endpoint must not let a person read the report
of an organization only because the person belongs to it.

Usually, the caller is the person of the request. A decision point can
predict, decide, or answer for the person that it runs for. An agent
harness is an example. The authorization endpoint must give this decision
point the `Case` permissions for that person. These permissions also let
it send an answer with `by`.

## The answer

```json
{"allow": true, "reason": "", "ttl": 60}
```

| Member | Meaning |
|---|---|
| `allow` | Required. A body without it is not an answer, and Limen refuses the request. |
| `reason` | Optional. Limen returns it to the caller in `details.detail` of the `403` response, with the permission, the resource, and the subject of the caller. Write only text that the caller can read. |
| `ttl` | Optional. The number of seconds that Limen can use an allow again for the same question: 60 if it is not set, and 600 or less. Limen uses a deny again for 5 seconds. |

Limen reads no other member.

## When the endpoint does not answer

If the endpoint does not answer in time, answers with an error, or
answers without `allow`, Limen has no decision. It refuses the request
with `authorizer_unavailable`, status 503, and does not do the work of
the route. A decision point that calls Limen through `FailClosed` then
gets the verdict `ask` for the action. A failure never allows an action.

## The owner policy

Without an endpoint, the owner policy decides:

- A person can do the operations for their own subject: predict, decide,
  answer, list cases, read explanations and reports, and forget their
  data.
- A person can read and save the cost profiles of their own context, and
  read, save, and remove their own cost profile in an organization.
- A subject in `LIMEN_ADMINS` can do all operations for all persons.
- Only an admin can read or save the cost profiles of an organization,
  send replays, and read the report of an organization.

The owner policy has no organizations and no services that act for
persons, other than the admins. For these, use an endpoint.

## Test an endpoint

The package `latere.ai/x/pkg/authz/conformance` sends the cases that
each authorization endpoint must pass, with the vocabulary of Limen:

```go
conformance.Run(t, url, token, conformance.WithVocabulary(authorizer.Vocabulary()))
```
