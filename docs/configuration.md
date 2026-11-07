# Configuration

This page is for the operator of `limend`. It gives each environment
variable that `limend` reads, its default, and the values that stop a
start. [Install limend](install.md) tells how to run the server.

`limend` reads its configuration from environment variables. It has no
flags other than `-version`. Set `LIMEN_TOKEN` for one person, or
`LIMEN_OIDC_ISSUERS` for a hosted server. If you set both, `limend` does
not start.

## Both modes

These variables apply in both modes:

| Variable | Meaning | Default |
|---|---|---|
| `LIMEN_ADDR` | The address that the server listens on. | `127.0.0.1:7720`. In the image: `:7720`. |
| `LIMEN_DATA_DIR` | The ledger directory. | `$XDG_STATE_HOME/limen`, or `~/.local/state/limen`. In the image: `/var/lib/limen`. |
| `LIMEN_BASE_PATH` | A second prefix for the routes, in place of `/v1`. Use it if an ingress sends a public path to `limend` without a change. | None |
| `LIMEN_MIN_ALLOWS` | The weight of considered approvals that cover an action, in each family of the action, that Limen needs before it suggests `allow`. A considered approval has the weight 1. `0` turns this setting off. | `3` |
| `LIMEN_MIN_SESSIONS` | The number of different sessions that these approvals must come from. `0` turns this setting off. | `2` |
| `LIMEN_ANSWER_FLOOR_MS` | An approval faster than this number of milliseconds has the weight 0, if the person did not open the details. `0` turns this rule off. The largest value is `86400000`, one day. | `2000` |
| `LIMEN_ANSWER_FATIGUE` | An approval with this number of reviews or more before it in its session has the weight 0, if the person did not open the details. `0` turns this rule off. | `50` |
| `LIMEN_MODEL` | The model version that the server uses: `learned/1` or `learned/2`. Read [Switch the model](install.md#switch-the-model) before you change it. | `learned/1` |

The server always supplies each route under `/v1`. If you set
`LIMEN_BASE_PATH`, the server also supplies each route under that prefix.
For example, with `LIMEN_BASE_PATH=/v1/oversight`, the server supplies
`/v1/oversight/learned` and `/v1/learned`. The two paths are the same
route, with the same permission. The value must start with `/`, must not
end with `/`, and must not be `/v1`. If the value is not correct,
`limend` stops with exit code 2.

`GET /v1/version`, and `GET` on `version` under `LIMEN_BASE_PATH`, answers
the version, the commit and the build time of the binary as JSON. It needs
no token, so a check from outside can read which release a public prefix
reaches.

`LIMEN_MIN_ALLOWS` and `LIMEN_MIN_SESSIONS` set the
[cover rule](theory.md#the-cover-rule). `LIMEN_ANSWER_FLOOR_MS` and
`LIMEN_ANSWER_FATIGUE` set the weight of an answer. Each value must be a
whole number, 0 or more, and `LIMEN_ANSWER_FLOOR_MS` is not more than
one day. If a value is not correct, `limend` stops with exit code 2. If `LIMEN_MODEL` is not a model version, `limend` also stops
with exit code 2. A cost profile can make `min_weight` and `min_sessions`
greater for its scope, but not smaller. A prediction gives the settings
that applied in `cover.min_weight` and `cover.min_sessions`.

> **Warning:** With `0` in `LIMEN_MIN_ALLOWS` or `LIMEN_MIN_SESSIONS`,
> Limen can suggest `allow` after fewer approvals, for example after one
> approval in one session. With `0` in `LIMEN_ANSWER_FLOOR_MS` or
> `LIMEN_ANSWER_FATIGUE`, quick approvals or approvals late in a long
> session build the cover. Use `0` only for tests. If a value is `0`,
> `limend` writes a warning when it starts.

## One person

| Variable | Meaning | Default |
|---|---|---|
| `LIMEN_TOKEN` | The bearer token that each request must have. | None |
| `LIMEN_SUBJECT` | The person that the server answers to. This person can act for each person in the ledger. | `local` |

The token identifies the person in `LIMEN_SUBJECT`. The shared owner
policy decides what this person can do. This person owns the server, so
this person can do all operations. If a request has no token, or a
different token, `limend` refuses it with `unauthenticated`.

## Hosted

| Variable | Meaning | Default |
|---|---|---|
| `LIMEN_OIDC_ISSUERS` | The issuers that `limend` accepts tokens from, separated by commas. | None |
| `LIMEN_OIDC_AUDIENCE` | The audiences that a token for `limend` carries, separated by commas. A token must carry one of them. You must set it. | None |
| `LIMEN_ADMINS` | The subjects that can do all operations, separated by commas. Each subject is the issuer and the `sub` of a token, with a pipe between them. Use it only without `LIMEN_AUTHORIZER_URL`. | None |
| `LIMEN_AUTHORIZER_URL` | The authorization endpoint. If you set it, the endpoint decides each request. | None |
| `LIMEN_AUTHORIZER_TOKEN`, `LIMEN_AUTHORIZER_TOKEN_FILE` | The bearer token for the authorization endpoint, or a file that contains it. Set one of them with `LIMEN_AUTHORIZER_URL`. | None |

When it starts, `limend` reads the key set of each issuer before it
opens its port. If it cannot read a key set, or an issuer does not answer
a request in 10 seconds, it stops with exit code 1. A token must not be older than
one day, and it must carry the time when it was issued.

Without an authorization endpoint, the shared owner policy decides:

- A person can do the operations for their own subject: predict, decide,
  answer, list cases, read explanations and reports, and forget their
  data.
- A person can read and save the cost profiles of their own context. The
  `org` of these cost profiles is the subject of the person.
- A person can read, save, and remove their own cost profile in an
  organization.
- A subject in `LIMEN_ADMINS` can do all operations for all persons. Use
  it for the service that runs the decision point, for example Topos.
- Only an admin can read or save the cost profiles of an organization,
  send replays, and read the report of an organization.

[Writing an authorization endpoint](authorizer.md) gives the question
that `limend` asks for each request, and the permission of each route.

> **Warning:** A subject in `LIMEN_ADMINS` can read and change the ledger
> of each person. Add only the service that runs your decision point.

A platform that has its own permissions can answer for `limend`. Set
`LIMEN_AUTHORIZER_URL` to its authorization endpoint. Then the endpoint
decides each request. Do not set `LIMEN_ADMINS` with it. If both are
set, `limend` does not start and stops with exit code 2.

A hosted server keeps the ledger in a directory, as a server for one
person does. Only one process can use a ledger directory
([The ledger directory](install.md#the-ledger-directory)).

## Values that stop a start

If a value is not correct, `limend` writes the reason on the standard
error and stops. It does not start with a part of its configuration.

| Exit code | Cause |
|---|---|
| 2 | `LIMEN_TOKEN` and `LIMEN_OIDC_ISSUERS` are both set, or neither is set. |
| 2 | A variable of the other mode is set: `LIMEN_OIDC_AUDIENCE`, `LIMEN_ADMINS`, or `LIMEN_AUTHORIZER_URL` with `LIMEN_TOKEN`, or `LIMEN_SUBJECT` with `LIMEN_OIDC_ISSUERS`. |
| 2 | `LIMEN_OIDC_ISSUERS` is set without `LIMEN_OIDC_AUDIENCE`. |
| 2 | `LIMEN_ADMINS` and `LIMEN_AUTHORIZER_URL` are both set. |
| 2 | `LIMEN_AUTHORIZER_URL` is not an `http` or `https` URL. |
| 2 | With `LIMEN_AUTHORIZER_URL`, `LIMEN_AUTHORIZER_TOKEN` and `LIMEN_AUTHORIZER_TOKEN_FILE` are both set or both empty, or the file cannot be read or is empty. |
| 2 | `LIMEN_BASE_PATH`, `LIMEN_MIN_ALLOWS`, `LIMEN_MIN_SESSIONS`, `LIMEN_ANSWER_FLOOR_MS`, `LIMEN_ANSWER_FATIGUE`, or `LIMEN_MODEL` is not correct. |
| 2 | There is no ledger directory: `LIMEN_DATA_DIR`, `XDG_STATE_HOME`, and `HOME` are all empty. |
| 1 | `limend` cannot open the ledger directory, for example because a different process holds it. |
| 1 | `limend` cannot read the key set of an issuer in `LIMEN_OIDC_ISSUERS`, or the issuer does not answer a request in 10 seconds. |
| 1 | `limend` cannot listen on `LIMEN_ADDR`. |
