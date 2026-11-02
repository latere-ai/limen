# Changelog

Each release tag has a section in this file. The section is the body of
the GitHub release. The pre-push hook and the release workflow refuse a
tag without a section. Add each change under `Unreleased` when it lands.
`lateregate release vX.Y.Z` makes that text the section of the tag, and
then commits, tags, and pushes.

A section tells the users of the release what changed. It does not repeat
the commit log.

## Unreleased

### Added

- `DELETE /v1/profiles?org&kind&agent&subject` removes the cost profile
  that is saved for exactly that scope. The response is 204, also if no
  cost profile is saved. Then the next scope in the order applies: an
  agent goes back to the cost profile of its organization. The route asks
  `profile.write`, as a save of the same scope does. In Go, it is
  `Admin.DeleteProfile`.
- A person can save a cost profile for their own actions in one
  organization: a scope with `subject` and no `kind` or `agent`. It can
  only make the cost profile of the organization stricter. A prediction,
  `GET /v1/learned`, an explanation of an action with no prediction, and
  the certificate of a reliability report use the meet of the two cost
  profiles. The `profile` of a prediction has `subject` if the cost
  profile of the person changed a value. `GET /v1/profiles` with
  `subject` returns what the person saved. The Go function
  `limen.Stricter` calculates the meet.
- `GET /v1/reports?org&from&to` gives the report of an organization: the
  counts and the error rates of its agents by week, for all its persons
  together. A week starts on a Monday at 00:00 UTC, and a report has at
  most 13 weeks. A week shows its numbers 7 days after its end, and only
  if 5 or more persons took part. A number that comes from reviews shows
  only if the reviews come from 5 or more persons and no person gave more
  than one half of them. A week divides by agent, and an agent with fewer
  than 5 persons goes into `other`. A week that a report showed never
  changes, also after a forget. The route asks the new permission
  `report.read` on the new resource kind `Report`, with the field `org`.
  In Go, it is the `Reporter` interface.
- The conformance suite has 15 new cases for these rules. `Target` has
  `Clocked`, a service with a clock that the case sets. A target without
  it skips the cases of the report.

### Changed

- `limen.Service` includes `Reporter`, and `Admin` includes
  `DeleteProfile`. A Go type outside this module that implements
  `limen.Service` must add the two methods. A decision point that uses
  `client/`, `engine/` or `stub/` changes nothing.
- `Forget` also removes the cost profile of the person in the
  organization. It does not change a kept week of a report.
- A `Profile` resource has `subject` for the cost profile of a person.
  An authorization endpoint must decide it for that person and for no
  other person.
- The ledger directory keeps each kept week of a report in
  `reports/<org>/<week>.json`.

## v0.4.0 - 2026-11-02

### Added

- The conformance suite has the case `Certificate`. An implementation
  must count the most recent audits that the person approved, back to the
  first denial. It must also need the number of audits that the claim of
  the organization gives.
- The conformance case `UnknownIsAnAsk` also checks that a new person
  gets the approval probability 0.5 and `ask`, under a saved profile at
  each end of the accepted costs. The case `ForgetIsScoped` also checks
  that `Forget` removes the reviews of the person from the reports of the
  organization, and from no other organization.

### Changed

- The documents describe Limen in production. The integration page
  tells how a decision point can use `ask_kind`: its rules decide an
  `unsure` ask, and the person decides a `split` ask. It also gives the
  modes of hosted Topos (`TOPOS_DECISIONS_MODE`) and what a replay of a
  Topos data directory reads. The theory page gives the evidence ratio.
  The page on running `limend` gives the release files, `go install`,
  how to check the server, and its logs. Each spec records what is
  built.
- `SECURITY.md` names the supported versions: v0.3 and v0.2.
- `limen replay topos` reads logs up to Topos v0.66.0. A decision from a
  log of Topos v0.65.0 or later has the source `topos/<mode>/<rule>`, for
  example `topos/confirm/always_allow`. `<rule>` is the type of rule that
  decided the call. Thus the ledger gives the error rate of each type of
  rule. A decision from an earlier log keeps the source `topos/<mode>`.
- `limen replay topos` reads an answer to a network approval as an answer
  to the call that made the connection, as Topos v0.66.0 sends it. A
  record has one answer. For a call that a person saw, it is the first
  answer, which is the review. For a call that ran without a person, it is
  the last answer, which the model learns.
- A call in `limen replay topos` has the attribute `attended`, as Topos
  v0.56.0 and later send it with each call. Thus a replay calculates the
  same features as the live session.

### Fixed

- A reliability query or a preview that has neither `from` nor `to` gets
  an `invalid_query` error that names both fields. Before, the error
  named only `from`.

## v0.3.0 - 2026-11-01

### Added

- A prediction has `evidence_ratio`, and for an `ask` also `ask_kind`
  (`unsure` or `split`), the same values as its explanation. A decision
  point can use them when it decides. For example, its rules can decide
  an `unsure` ask, and a `split` ask stays with the person. The
  `FailClosed` fallback has the kind `unsure`.

## v0.2.0 - 2026-11-01

### Added

- Explanations (spec 009). `GET /v1/explanations` shows why Limen
  suggests what it suggests for one action: the answers of the person
  that changed the prediction most, with the exact share of each, the
  split into what the model learned about the type of action and about
  the recent tolerance of the person, and for an `ask`, if the person has
  few answers (`unsure`) or answers that disagree (`split`).
  `GET /v1/learned` shows what the model learned about a person, for each
  type of action. `POST /v1/previews` shows what a different cost profile
  would do with the recorded predictions of a person. The Go contract has
  the new `Explainer` interface, which `Service` includes.
- Labels. Limen gives each new case a label from a fixed list of words,
  for example `bash: git push --force`. A label never contains the input.
- `LIMEN_BASE_PATH`. `limend` also supplies the API under this prefix, for
  an ingress that sends a public path without a change. The client has
  `Options.BasePath`.
- The error code `no_case` (404), for an explanation of an action that
  Limen has no record of.

### Changed

- A person can read and save the cost profiles of their own context: the
  profiles whose `org` is the subject of the person. The profiles of an
  organization and all replays stay with the admins.

## v0.1.1 - 2026-11-01

### Fixed

- The release publishes its image. The v0.1.0 release built the image but
  stopped at its version check, which read the image's pull progress as
  the version, so v0.1.0 has no published image; use this release.

## v0.1.0 - 2026-11-01

This is the first version of Limen. Limen has three parts:

- The shared verdict vocabulary, `latere.ai/x/pkg/verdict`.
- A ledger. It measures each source of decisions against the answers of
  a person.
- A predictor. It learns the tolerance of each person from the ledger.

The contract:

- `Predictor`, `Ledger`, and `Admin`, the three interfaces.
- `Action`, a general description of an action: a kind and a name. Its
  kinds include tool calls, network access, repository writes, payments,
  and deploys. Its key has three fields: the organization, the person, and
  the action ID of the decision point.
- `Prediction`. It suggests `allow`, `ask`, or `block` with the thresholds
  of a cost profile.
- `Decision`. The decision point records it with the probability that a
  person sees the action.
- `Answer`. The decision for an action sets the role of its answer in an
  estimate.
- Reliability reports for each source, replays, the error codes with
  their messages, and `FailClosed`.
- Limen accepts a cost profile only if each type of error costs more than
  two asks. For this reason, if Limen has no answers from a person, it
  suggests `ask`.
- The HTTP API as an OpenAPI 3.1 document. A test compares it with the Go
  types.

The packages:

- `estimate`: the Horvitz-Thompson estimates that each ledger uses.
- `stub`: a service in memory for the tests of other projects.
- `conformance`: the tests that each implementation must pass.
- `engine/kernel` and `engine/gp`: the additive kernel, the product
  kernel, and the Laplace Gaussian-process classifier. They come from the
  research code and give the same results as its test data.
- `engine`: the service that predicts and keeps the ledger.
  - The `features/1` feature extractor for open sets of tools. For a
    shell command, it reads the programs, the destructive patterns, and
    the writes outside the workspace.
  - Each prediction reads a fixed copy of the model. When answers arrive,
    a background task calculates a new model and replaces the copy.
  - The model counts time in the decisions of each person, not in clock
    time.
  - The store keeps model snapshots. After a restart, Limen loads a
    snapshot and does not calculate the model again.
  - A memory limit for the models in memory.
  - Replays in time order through a separate engine.
- Stores: a store in memory and a store in a directory. The directory
  store keeps one log for each person. Each write adds to the log. It
  writes snapshots and cost profiles as complete files. Only one process
  can use a directory. Each store passes the same contract tests.
- `server` and `client`: the HTTP server and its client. The server
  accepts a bearer token or a JWT and asks the shared authorizer. The
  server and the client pass the conformance tests.

The programs:

- `limend`, the server. It keeps the ledger on disk. It runs in one of two
  modes:
  - For one person: `LIMEN_TOKEN` and `LIMEN_SUBJECT` configure it.
  - Hosted: it accepts tokens from the issuers in `LIMEN_OIDC_ISSUERS` for
    `LIMEN_OIDC_AUDIENCE`. An authorization endpoint
    (`LIMEN_AUTHORIZER_URL`) or the owner policy with `LIMEN_ADMINS`
    decides each request.

  Its container image runs it as a user that is not root, with the ledger
  in a volume.
- `limen`, the command line. `limen replay topos` replays a Topos data
  directory, from Topos v0.11.0 to v0.62.0. `limen replay file` replays a
  file of records. Each replay shows what Limen would suggest. A replay
  skips a call that repeats the ID of an earlier call in its session, and
  an answer to a network approval.

The documents:

- Documents for authors of a decision point and for operators, and the
  design specs.
- The documents for users follow the writing rules of ASD-STE100. A term
  list gives each term one meaning.

### Changed

- **Releases are built, signed and published by the shared release stages
  in latere-ai/ci.** The image signature and the signed `checksums.txt`
  name those stages, run for this repository. Each release attaches the
  `limend` binaries for linux/amd64 and linux/arm64, the bill of materials
  of the image, and `checksums.txt` with its signature.

### Security

- Built with Go 1.27.2 and golang.org/x/net v0.60.0, which fix GO-2026-6611, GO-2026-6612, GO-2026-6613 and GO-2026-6617.
- **Built with gRPC-Go 1.83.2,** which fixes GHSA-2v4p-qf9q-27wj, a crash
  of gRPC xDS servers on a request with neither `:authority` nor `Host`.
  This code runs no xDS server, so the flaw was not reachable.

### Fixed

- **Built with Go 1.27.3,** which fixes GO-2026-6809, a flaw in the
  standard library's certificate verification that this code can reach.
