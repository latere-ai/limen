# Security

## Reporting a vulnerability

Do not open a public issue for a vulnerability. Report it privately, in
one of these two ways:

- A private vulnerability report on GitHub:
  [report a vulnerability](https://github.com/latere-ai/limen/security/advisories/new).
- An email to security@latere.ai.

The [security policy of the organization](https://github.com/latere-ai/.github/blob/main/SECURITY.md)
gives the response times, how disclosure works, and the safe harbor for
research in good faith. If you ask for credit, we give it in the release
notes.

We fix vulnerabilities in the two most recent minor release series. A
fix comes in a new patch release of each of them. The
[releases page](https://github.com/latere-ai/limen/releases) gives the
releases.

The reports that we want most: a failure or a result of Limen that makes
a decision point allow an action that its rules do not permit; a caller
that records or changes a decision without the permission `case.decide`;
an input of an action in a file, a log, or a response; a caller that
reads or changes the records of a person or an organization without the
permission; and a record of a person that stays after `Forget`. What
[is not defended](#what-is-not-defended) is not a vulnerability. But a
report that one of these limits is wider than this file says is a
vulnerability.

## What is protected

Limen controls which automated actions run without a person, and it
measures how often these decisions are wrong. The table gives each
property, the attacker that it holds against, and the tests that check
it. A method that breaks one of these properties is a vulnerability.

| Property | Holds against | Tests |
|---|---|---|
| **A deny from your rules is final, and a failure of Limen never suggests allow.** `verdict.Decide`, in `latere.ai/x/pkg/verdict`, never returns a verdict more permissive than the ceiling from your rules. `limen.FailClosed` turns an error or a timeout of Limen into the verdict `ask`. The server answers an error that is not in the contract as `unavailable`, and a panic as `internal`. | A failing, slow, or wrong Limen, seen from a decision point that uses `FailClosed` and `verdict.Decide` | `limen.TestDecideNeverExceedsTheCeiling`, `limen.TestFailClosedOnError`, `limen.TestFailClosedOnTimeout`, `engine.TestCoverFailsClosed`, `engine.TestStoreFailuresSurface`, `server.TestErrorMapping`, the conformance rule `FailClosed` |
| **An action that Limen knows nothing about gets `ask`.** A person with no answers has the approval probability 0.5, and Limen refuses a cost profile that puts 0.5 outside the range of `ask`. | A new person, a new agent, or a cost profile that would allow without evidence | `engine/gp.TestPrior`, `limen.TestCostsRefusesDecidingOnNoEvidence`, the conformance rules `UnknownIsAnAsk` and `AskTooCostly` |
| **No allow beyond what the person allowed.** Limen suggests `allow` only if considered approvals of the same scope or a wider scope cover the action. A quick approval, or an approval late in a long session, has the weight 0 if the person did not open the details. A cost profile of a person can only be stricter than that of the organization. | Approvals of a narrower action, and approvals without attention | `engine.TestCoverNeedsTwoSessions`, `engine.TestCoverIsPerFamily`, `engine.TestCapNeverTouchesABlock`, `cmd/limend.TestCoverSettings`, `limen.TestStricterProperties`, the conformance rules `CoverCapsAllow`, `AnswerWeights`, and `PersonProfileNeverLoosens` |
| **Only the decision point sets the review probability.** Limen records a decision only with the permission `case.decide`, and one decision for each action. The review probability must agree with the verdict. Only the first answer to a decision that showed the action to a person is a review. An action with a different digest under an earlier key is `action_conflict`. Under the owner policy in hosted mode, only a subject in `LIMEN_ADMINS` has `case.decide`. | A caller without `case.decide` or `case.answer`, a person who records decisions for their own actions, a second decision for one action, and a changed action under an earlier key | `server.TestAuthorizerQuestions`, `server.TestOwnerPolicyAdminsDecide`, `cmd/limend.TestHostedOnlyAdminsDecide`, `server.TestRefusalsActOnNothing`, `server.TestSubjectBinding`, `limen.TestDecisionValidate`, the conformance rules `OneDecision`, `AnswerRoles`, `ReviewsWeightedByProbability`, and `LaterAnswersNeverEstimate` |
| **Limen never keeps the input of an action, and never gives it to a language model.** Limen discards the `input` before it records a case, and keeps only the features that it calculates from it. `limend` has no client of a language model: it calls only the token issuers and the authorization endpoint. | A person who reads the ledger directory or the responses of the API | `engine/store/file.TestNoFileHoldsTheInput`, `engine.TestStoredScopeHoldsNoInput`, `limen.TestRootImports`, the conformance rule `CasesNeverReturnInput` |
| **Only a caller with permission can get to the records of a person.** Each route except `GET /v1/version` needs a valid token. Then Limen asks the permission of the route about one resource, and does the work only after an allow. A deny or no answer changes nothing. | A caller without a token, and a caller without the permission of the route | `server.TestAuthorizerQuestions`, `server.TestRefusalsActOnNothing`, `server.TestAuthentication`, `server.TestOwnerPolicy`, `server.TestPersonProfileOwnership`, `cmd/limend.TestHostedOwnerPolicy`, `cmd/limend.TestHostedAuthorizer` |
| **Each organization is separate.** Each record has three key fields: the organization, the person, and the action ID. A key of one organization never reads or changes a record of a different organization. Names become safe file names, so a name cannot point outside its directory. | A caller that names a different organization in a key | `engine/store/file.TestContract`, `engine/store/file.TestAwkwardNames`, `engine.TestForgetRemovesTheOwnProfile`, the conformance rule `ForgetIsScoped` |
| **`Forget` removes all the records of a person in one organization:** the cases, the answers, the models, and the cost profile of the person. A fit that was in progress does not write the model again after `Forget`. | A record of a forgotten person that stays in the ledger | `engine.TestForgetDropsTheModel`, `engine.TestAFitInFlightDoesNotReviveAForgottenPerson`, `engine.TestForgetRemovesTheOwnProfile`, the conformance rules `ForgetIsScoped` and `ForgetRemovesThePersonProfile` |
| **A crash does not damage the ledger.** Limen writes each line of a log to the disk before it continues, discards an incomplete last line, and writes each other file whole in one step. One process owns a ledger directory. | A crash or a second process on the same directory | `engine/store/file.TestTornLastLine`, `engine/store/file.TestDamagedLog`, `engine/store/file.TestLock`, `engine/store/file.TestReopen` |
| **A server error gives no detail to the caller.** A response with the status 500 or more has the code, its message, and the request ID only. The text of the error can name a path in the ledger directory, and stays in the log. When the authorization endpoint does not answer, the log line keeps the permission, the resource, and the cause, without the user information and the query of the address. The line that `limend` writes when it starts names the endpoint in the same way. | A caller who causes a failure, a caller with a valid token while the authorization endpoint is down, and a person who reads the logs | `server.TestServerErrorsAnswerOnlyWithTheRequestID`, `server.TestAnOutageDoesNotNameTheAuthorizer`, `cmd/limend.TestTheStartLineDoesNotPrintTheAuthorizerSecrets`, `internal/redact.TestErrorRedactsTheURLOfATransportError` |
| **Each client address has a rate.** `limend` counts each request of a client address before it reads the token, and refuses more than 6,000 in one minute with `rate_limited`. A request must arrive in one minute, and a replay in ten minutes. | A client that sends too many requests or sends them slowly, with or without a token | `server.TestAddressRequestLimit`, `server.TestAddressRequestLimitDefaultAndOff`, `server.TestAddressLimitFollowsTrustedProxies`, `server.TestReplayExtendsItsReadDeadline`, `cmd/limend.TestServerCarriesTheReadTimeouts` |
| **Records have bounds.** Each string field other than `input` is 256 bytes or less, and an action has 64 signals and 64 holds or less. An organization has 10,000 persons or less. A replay asks `replay.create` for an organization before it reads the record after the first record of that organization. | A caller with a valid token that sends long fields, many persons, or a large replay that it cannot send | `limen.TestStringLengths`, `limen.TestListCountBounds`, `engine.TestEngineRefusesLongStrings`, `engine.TestPersonCap`, `engine.TestPersonCapConcurrent`, `engine.TestPersonCapReplay`, `server.TestReplayAsksBeforeReadingTheBody`, `server.TestForgetValidatesLengthsBeforeAsking` |

## What is not defended

These are the known limits. A report that one of these limits is wider
than this file says is a vulnerability.

- **The decision point.** Limen never applies a verdict. A decision
  point that does not use `FailClosed` and `verdict.Decide` has no bound.
  When Limen fails, the `ask` of `FailClosed` is `unsure`, and your rules
  then decide up to their ceiling. Limen takes the verdict, the review
  probability, the source, the session, and the way that the person
  answered as the decision point sends them.
- **The owner policy has no organizations.** Without an authorization
  endpoint, a person can predict, answer, and read for their own subject
  under each organization name. In hosted mode, only a subject in
  `LIMEN_ADMINS` can record a decision. Only an authorization endpoint
  can keep a person out of an organization.
- **A change of the input alone.** Limen does not keep the input, so the
  digest of an action does not include it. A prediction for an earlier
  key with a different input gives the first prediction again. Give each
  action its own `action_id`.
- **The other fields of an action.** Limen keeps each field other than
  `input` as the decision point sent it, and returns it in
  `GET /v1/cases`: the name, the actor, the target, the environment, the
  attributes, and the names of the signal sources. Do not put a secret
  in these fields.
- **Load.** The rate of `limend` is for each client address and each
  replica. Many addresses together can send more. Behind a proxy, set
  `LIMEN_TRUSTED_PROXIES`, or all clients share the address of the proxy.
  `limend` does not limit the number of organizations. A caller with a
  valid token can make `limend` read 1 MiB before Limen asks the
  authorization endpoint.
- **The network.** `limend` supplies plain HTTP only. Without
  `LIMEN_TRUSTED_PROXIES`, the client address is the address of the peer.
  Behind a proxy, it is then the address of the proxy.
- **A permission withdrawn moments ago.** Limen uses an allow of the
  authorization endpoint again for its `ttl`: 60 seconds if the endpoint
  sets none, and 600 seconds or less. Limen accepts a token until it
  expires, and not more than one day after it was issued. Limen cannot
  revoke a token.
- **What an error says.** A `403` gives the reason of the authorization
  endpoint to the caller. A `401` gives the reason why the token was
  refused. A response with the status 500 or more gives only the request
  ID.
- **One replica.** One process owns a ledger directory. While `limend`
  restarts, no decision point can ask Limen, and each decision point
  must fail closed.
- **The owner of the ledger directory.** A person who can read the
  directory reads the records of each person. The files are not
  encrypted. A person who can write to it can change each record.
  `limend` writes its files with the mode 0600, and its directories with
  the mode 0700.
- **Admins.** A subject in `LIMEN_ADMINS`, or a subject that your
  authorization endpoint lets do everything, can read and change the
  ledger of each person. A replay with `keep` records decisions and
  answers for each person in the organizations that it names. A replay
  with `keep` that stops at a conflict keeps the records before the
  conflict.
- **Retention.** Limen removes nothing by itself. The records of a
  person stay until `Forget`. The kept weeks of the report of an
  organization stay after `Forget`, because they contain no record of a
  person: each number in them comes from 5 or more persons.
- **The services that you configure,** and a compromised host. Limen
  believes the token issuers and the authorization endpoint.

## Audit

Limen has had no external security audit. The tests in this repository
check the properties above. They run on each change.

## Supply chain

The quality gate checks the dependencies for known vulnerabilities on
each push that changes the code or its dependencies. A release is built
from a tagged commit whose verification passed, through the shared
release stages in [latere-ai/ci](https://github.com/latere-ai/ci). The
identity of the build stage, run for this repository, signs the image
`ghcr.io/latere-ai/limen`. The image has a bill of materials and a
provenance attestation. The identity of the publish stage signs
`checksums.txt`, which covers the `limend` binaries and the bill of
materials. `test/deploy.TestTheReleaseRunsThroughTheSharedStagesInOrder`
and `test/deploy.TestEveryThirdPartyActionIsPinned` hold the release
workflow to these stages, and each third-party action to a pinned
commit.

### Verifying a release

Set `TAG` to the tag of the release, then run these commands:

```sh
gh release download "$TAG" -R latere-ai/limen -D limen-release
cd limen-release
cosign verify-blob --bundle checksums.txt.sigstore.json \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github\.com/latere-ai/ci/\.github/workflows/image-publish\.yml@' \
  --certificate-github-workflow-repository latere-ai/limen checksums.txt
sha256sum -c checksums.txt

cosign verify "ghcr.io/latere-ai/limen:$TAG" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github\.com/latere-ai/ci/\.github/workflows/image-build\.yml@' \
  --certificate-github-workflow-repository latere-ai/limen
gh attestation verify "oci://ghcr.io/latere-ai/limen:$TAG" -R latere-ai/limen \
  --signer-workflow latere-ai/ci/.github/workflows/image-build.yml
```

On macOS, use `shasum -a 256 -c checksums.txt` in place of `sha256sum`.
The release workflow does the same checks on each release that it
publishes. The releases before v0.9.0 have no provenance attestation, so
`gh attestation verify` applies from v0.9.0.
