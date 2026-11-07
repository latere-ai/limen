# Security

This page is for the operator of `limend` and for the author of a
decision point. It tells what Limen guarantees, what `limend` sends to
other services, what it keeps and writes in its logs, what it does not
defend, and what an operator keeps safe. How to report a vulnerability,
and the tests that check each guarantee, are in
[`SECURITY.md`](../SECURITY.md).

## What Limen guarantees

- **A failure of Limen never suggests allow.** `limen.FailClosed` turns
  an error or a timeout into the verdict `ask`, and `verdict.Decide`
  never permits more than the ceiling from your rules.
- **An action that Limen knows nothing about gets `ask`,** and Limen
  suggests `allow` only where considered approvals of the person cover
  the scope of the action.
- **Only the decision point sets the review probability,** with the
  permission `case.decide`, and one decision for each action.
- **Limen never keeps the input of an action,** and never gives it to a
  language model.
- **Each route but `GET /v1/version` needs a token and a permission.** A
  deny, or no answer from the authorization endpoint, changes nothing.
- **Each organization is separate,** and `Forget` removes all the
  records of a person in one organization.

## What limend sends, and to whom

`limend` calls two kinds of service, and no other. It sends no
telemetry: it ignores the `OTEL_*` environment variables.

| Service | Credential | What `limend` sends |
|---|---|---|
| Each issuer in `LIMEN_OIDC_ISSUERS`, in hosted mode | None | Requests for the discovery document and the key set: when `limend` starts, when the cached key set is 5 minutes old, and for a token with an unknown key, not more than one time in 15 seconds. They contain nothing about a caller. |
| The authorization endpoint in `LIMEN_AUTHORIZER_URL` | `LIMEN_AUTHORIZER_TOKEN` or `LIMEN_AUTHORIZER_TOKEN_FILE` | One question for each request that Limen has no fresh answer for: the subject, each claim of the token of the caller, the permission, the resource, the request ID, the client address, and the user agent. [Writing an authorization endpoint](authorizer.md#the-question) gives the fields. |

The authorization endpoint sees each claim of the token of each caller.
A claim can be an email address or other personal data. The endpoint
never sees the input of an action or a record of the ledger.

## What limend keeps

`limend` keeps all its data in the ledger directory, `LIMEN_DATA_DIR`.
It writes each file with the mode 0600, and each directory with the mode
0700. Nothing is encrypted.

| Kept | Contents | Removed by |
|---|---|---|
| The cases of a person, in `subjects/<org>/<person>/cases.ndjson` | Each field of each action except `input`, the features that Limen calculates from the input, the prediction, the decision, and each answer with the way that the person answered | `Forget` |
| The models of a person, in `subjects/<org>/<person>/snapshots/` | The fitted model: the features and the answers that it learned from | `Forget` |
| The names of a person, in `subjects/<org>/<person>/meta.json` | The organization and the subject | `Forget` |
| The cost profiles of an organization, in `profiles/<org>.json` | Each saved cost profile, with the cost profile of each person | `DELETE /v1/profiles`. `Forget` removes the cost profile of the person. |
| The kept weeks of the report of an organization, in `reports/<org>/` | Counts and error rates in cells with 5 or more persons, and the IDs of the agents of these cells | Nothing. They contain no record of a person. |

Limen removes nothing by itself. The records of a person stay until
`Forget`, or until you remove the directory. A copy of the directory, for
example a backup, keeps the records after `Forget`.

## What limend writes in its logs

`limend` writes to the standard output and the standard error. It writes
no line for each request.

- When it starts, it writes the address, the ledger directory, the
  model, the issuers, and the authorization endpoint without its user
  information and its query. In one-person mode, it writes the subject.
  It never writes a token.
- For each response with the status 500 or more, it writes one line: the
  request ID, the method, the path without the query, the error code,
  and the text of the error. This text can contain the subject of the
  person and a path in the ledger directory. When the authorization
  endpoint does not answer, the line also gives the cause, with the
  address of the endpoint without its user information and its query.
  The response to the caller gives only the request ID.
- It writes no input of an action, no body, and no query.

## For the author of a decision point

- Use `limen.FailClosed` and `verdict.Decide`. The guarantees about a
  failure are in these two functions, in your process, and not in
  `limend`.
- Give each action its own `action_id`. A prediction for an earlier key
  gives the first prediction again, also if the input changed.
- Do not put a secret in a field other than `input`. Limen keeps the
  other fields, and returns them in `GET /v1/cases`.
- A `403` gives the reason of the authorization endpoint, and a `401`
  the reason why the token was refused. [API](api.md#errors) gives each
  error.

## What Limen does not defend

[`SECURITY.md`](../SECURITY.md#what-is-not-defended) gives the full list.
These are the limits that an operator plans for:

- **The owner policy has no organizations.** Without an authorization
  endpoint, a person can predict and answer for their own subject under
  each organization name. In hosted mode, only a subject in
  `LIMEN_ADMINS` can record a decision.
- **Load.** `limend` limits the rate of each client address, the time
  that a request takes, the length of a field, and the persons of an
  organization ([conventions](api.md#conventions)). The rate is for one
  replica and one address. Many addresses together can send more, and
  `limend` does not limit the number of organizations. Behind a proxy,
  set `LIMEN_TRUSTED_PROXIES`.
- **A permission withdrawn moments ago.** Limen uses an allow again for
  its `ttl`, 600 seconds or less, and accepts a token until it expires.
- **One replica.** While `limend` restarts, no decision point can ask
  Limen.

## For an operator

| Secret | What its holder can do |
|---|---|
| `LIMEN_TOKEN` | Act as the person in `LIMEN_SUBJECT`, who can do each operation for each person in the ledger. |
| `LIMEN_AUTHORIZER_TOKEN`, `LIMEN_AUTHORIZER_TOKEN_FILE` | Ask your authorization endpoint each question as `limend`, and learn what each subject can do. |
| The ledger directory | Read the records of each person, and change them. |

Next to the secrets:

- Put in `LIMEN_ADMINS` only the service that runs your decision point.
  A subject in it can read and change the ledger of each person.
- Keep `LIMEN_OIDC_AUDIENCE` to the audiences that you mean. Limen
  accepts a token for each of them.
- Use `https` for `LIMEN_OIDC_ISSUERS` and `LIMEN_AUTHORIZER_URL`.
  `limend` accepts `http`, and then the bearer of the authorization
  endpoint goes in clear text.
- [What a production installation must have](install.md#what-a-production-installation-must-have)
  gives the rest.
