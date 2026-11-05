# Changelog

Each release tag has a section in this file. The section is the body of
the GitHub release. The pre-push hook and the release workflow refuse a
tag without a section. Add each change under `Unreleased` when it lands.
`lateregate release vX.Y.Z` makes that text the section of the tag, and
then commits, tags, and pushes.

A section tells the users of the release what changed. It does not repeat
the commit log.

## Unreleased

## v0.9.1 - 2026-11-05

### Changed

- **A request id Limen mints starts with `req_`,** as the other services'
  ids do. An id the client sends is still kept when it is printable and at
  most 128 bytes.

## v0.9.0 - 2026-11-05

### Added

- **A release can deploy itself.** The release workflow has a `deploy` job
  between the build and the publish, which applies `deploy/prod` with the
  image pinned by digest, waits for the rollout and runs
  `tools/smoke/release.sh` against the live origin. It runs only where the
  repository variable `LIMEN_RELEASE_DEPLOY` is set, so a fork publishes
  every artifact and deploys nothing. `deploy/prod` holds Latere's
  production manifests and `deploy/bootstrap` the identity the job deploys
  with and the one policy it may not write.
- **`GET /v1/version` is public.** `limend` answers the build's
  `version`, `commit` and `build_time` with no token, and under
  `LIMEN_BASE_PATH` as well, for example `/v1/oversight/version`, so a
  deployment behind an ingress that forwards only that prefix shows which
  release it runs. The release smoke compares the served version with the
  tag.
- Figures from simulations that run the code of Limen, in the README, in
  [Theory](docs/theory.md), and in [The ledger](docs/ledger.md).
  `make figures` makes them again.
  [Figures](docs/figures/README.md) tells what each figure shows and what
  ran.

### Changed

- The named cost profile Strict has the costs 20, 4, and 1, and its
  upper threshold is 0.95. Before, it had the costs 50, 4, and 1, and
  its upper threshold was 0.98. With only approvals, the approval
  probability of the default model stops at 0.978, so no action got
  `allow` with the old Strict. With the new Strict, an action gets
  `allow` after 20 approvals with the details open, from 2 sessions.
  The `thresholds` figure, its data, the conformance cases, and specs
  010 and 011 use the new costs. Limen keeps the costs of a saved cost
  profile, not a name: a cost profile saved with the costs 50, 4, and 1
  keeps the upper threshold 0.98, and a client shows it as a custom
  profile.
- The documents tell how Limen works and how to use it, and no longer
  give the status of a deployment. The notes for Topos are for Topos
  v0.69.0 or later.
- The documents correct these statements. The behavior of Limen does
  not change.
  - The cover rule does not change a `block`, because it removes only
    `allow` from the choices. Below the block threshold, the verdict of
    least expected cost is still `block`.
  - The review probability of each decision sets which cases get a
    review, not the thresholds. An action above the allow threshold that
    the cover rule changed to an `ask` gets a review when a person sees
    it. With an audit rate of 0, a bin of only automatic verdicts stays
    `not_estimable`.
  - The ledger keeps the action without its input. This includes the
    `kind` and the `id` of the target, which find a first contact. If the
    ledger must not keep a path or a host, send an opaque `id`.
  - Limen does not measure `details_opened`, `surface`, or the session.
    The weights protect against a person who does not pay attention, not
    against a decision point that reports incorrectly.
  - Each action that Limen predicts or that the decision point records
    adds one to the time of the model, also without an answer.
  - A new type of action shares the tolerance of the person. Denials of
    other actions can move it below the block threshold.
  - The ask rate of Proposition 3 is the rate of the policy of least
    expected cost, not a limit for all policies. The actions of an
    automatic verdict get answers from audits, not never.

## v0.8.0 - 2026-11-03

### Added

- The calibration report, `GET /v1/calibration?org&subject&from&to`. It
  compares the approval probabilities that Limen stated for one person
  with the approval rates that the reviews measured. It has ten bins of
  `approve`: 0 to 0.1, 0.1 to 0.2, and so on to 0.9 to 1. It asks
  `reliability.read` about the `Reliability` of the person, as
  `GET /v1/reliability` does. The authorization vocabulary does not
  change.
  - Each bin has `from`, `to`, `cases`, `covered`, `stated`, `observed`,
    `interval`, `reviews`, `considered`, and `state`. `observed` counts
    each review with the weight 1 divided by its review probability, as
    a reliability report does. The weight of an answer does not apply.
  - `state` is `calibrated`, `overconfident`, `underconfident`, or
    `not_estimable`. A bin is `not_estimable` if the effective size of
    its reviews is less than 20. Then `observed` is `null`, which does
    not mean zero.
  - The report also has `error` and `estimable`. `error` is the mean
    distance between `stated` and `observed` over the estimable bins.
    `estimable` is the part of the cases in those bins.
- An explanation has `calibration`: the `state` of the bin of its
  recorded prediction in the report of the last 90 days. A client shows
  an approval probability, or a recommendation before the answer, only
  if it is `calibrated`. For a prediction above the allow threshold, the
  certificate of `learned` must also hold. Limen calculates it with each
  explanation, which takes about 3% of the time of the explanation.
- The conformance cases `Calibration`, `CalibrationWeights`, and
  `ExplanationCalibration`.
- In Go: `Ledger.Calibration`, `CalibrationQuery`, `Calibration`,
  `CalibrationBin`, `CalibrationState`, `CalibrationStates`,
  `CalibrationBinOf`, `CalibrationEdges`, the constants of the states,
  `CalibrationBins`, `CalibrationMinEffective`, `CalibrationWindow`, and
  `estimate.Calibrate` and `estimate.CalibrationOf`. A `Row` has
  `Stated`, the approval probability of the prediction.
- `learned/2`, a second model version. It does not serve until an
  operator sets `LIMEN_MODEL=learned/2`. `learned/1` stays the default.
  - Its features, `features/2`, hold the scope of an action: `undo`,
    each boundary of `crosses`, `target_class`, `contact`, and the
    holds. An approval of `publish: update` then tells the model less
    about `publish: make public`.
  - Its fit gives each answer its weight under `weights/2`. An approval
    from one gesture for many actions, or in less than 2000 ms, has the
    weight 0.2. An approval that followed a suggestion that the client
    showed, or that came after 50 reviews in its session, has half of
    its weight. An answer from a standing rule has the weight 0. Each
    other answer has the weight 1. With each weight 1, the fit is the
    fit of `learned/1`.
  - The cover rule keeps `weights/1`, and no error rate reads a weight.
- `LIMEN_MODEL` sets the model version of `limend`: `learned/1` (the
  default) or `learned/2`. `limend` writes the version when it starts.
  Each version keeps its own fitted models, and each case keeps the
  features of `learned/1`. To go back, set `learned/1` again. After a
  change, the predictions for a person come from the prior until the
  first fit of the new version, approximately one second.
- `limen compare topos`, `limen compare file`, and
  `limen compare ledger --store DIR`. They replay recorded decisions, or
  the cases of a ledger, through `learned/1` and `learned/2`. For all
  persons and for each person, they give the verdicts of each version,
  the changes, the allows that `learned/2` does not give or gives, the
  verdicts of `learned/2` that permit more, and the calibration error and
  the Brier score of each version. The calibration error comes from the
  calibration report of each version over its replayed predictions. A
  model comparison of a ledger reads the features and the scope that each
  case kept.
- `limen replay --model` replays with `learned/1` or `learned/2`.
- In Go: `engine.Model`, `engine.Learned1`, `engine.Learned2`,
  `engine.ParseModel`, `engine.Options.Model`, `Engine.Compare`,
  `Engine.CompareLedger`, `gp.FitWeighted`, `featurize.Of2`,
  `estimate.Policy.Graded`, and `file.Store.Orgs`.

### Changed

- `limen.Ledger` has the method `Calibration`. An implementation of
  `limen.Service` outside this module must add it.
- A person with answers never gets the reason "Limen has no answers from
  this person yet." Before, a model that had not learned the answers of
  the person yet gave it.

## v0.7.0 - 2026-11-03

### Added

- The scope of an action: if it can be undone (`undo`), the boundaries
  that it crosses (`crosses`), the class of its target (`target_class`),
  and if it is a first contact (`contact`). Each dimension also has the
  value `unknown`.
  - Limen reads the scope from the input with a fixed table of shell
    commands. For example, `git push --force` cannot be undone, a refspec
    that names `main` is the default branch, `npm publish` makes
    something public, and `curl -d` sends data.
  - A decision point can send claims: `scope.undo`, `scope.crosses`,
    `scope.contact`, `target.class`, `environment.holds`, and
    `environment.repositories`. A claim makes the scope wider and never
    narrower. A hold (`egress`, `protected_refs`, `checkpoints`) makes
    the reading of Limen narrower. A word outside a list is `unknown`,
    and it is not an error.
  - A prediction gives the scope that Limen used in `scope`. A boundary
    of `crosses` that is not known has no key. Limen never keeps the
    input, and the scope holds only words of the lists.
- The cover rule. Limen suggests `allow` only if considered approvals of
  the same family cover the action: approvals whose scope the scope of
  the action is within. In each family, they must have a weight of 3 or
  more, from 2 sessions or more. Until then, an `allow` from the
  thresholds becomes `ask`.
  - A prediction has `cover`: `covered`, `weight`, `sessions`,
    `min_weight`, and `min_sessions`. An explanation also has
    `cover.nearest`, the approvals nearest to covering the action. An
    entry of `GET /v1/learned` has `cover`.
  - A cost profile can make the settings greater with `consent`:
    `min_weight` and `min_sessions`. It cannot make them smaller. The
    meet of two cost profiles takes the greater value.
  - The rule applies to predictions, explanations, the entries of
    `GET /v1/learned`, and replays. A preview applies the cover that each
    prediction recorded.
- New kinds of `ask`: `short`, `wider`, and `few`.
  - `short`: the evidence ratio is 0.5 or more, the approval probability
    is in the ask range, and the answers of the family agree.
  - `wider`: the thresholds give `allow`, and the person never allowed an
    action of this reach.
  - `few`: the thresholds give `allow`, and approvals cover the action,
    but not with enough weight or not from enough sessions.
  - A decision point can let its rules decide `unsure`, `short` and
    `few`, and keep `split` and `wider` for the person. A decision point
    that does not know a kind reads it as `unsure`.
- How the person answered. `POST /v1/answers` takes `answer_ms`,
  `waited_ms`, `details_opened`, `suggestion_shown`, `surface`, and
  `remembered`, each optional. A recorded answer keeps them. They set the
  weight of the answer, `weights/1`, which is 0 or 1. An approval has
  weight 0 if a rule gave it, if one gesture answered many actions, if
  it came in less than 2000 ms, if it followed a suggestion that the
  client showed, or if 50 reviews came before it in its session. Except
  for a rule, the weight is 1 if the person opened the details. Only the
  cover rule reads the weight. The model still learns each answer, and
  no error rate reads the weight.
- `LIMEN_ANSWER_FLOOR_MS` (default `2000`) and `LIMEN_ANSWER_FATIGUE`
  (default `50`) set the weight of an answer. `0` turns a rule off, and
  `limend` writes a warning.
- The certificate of a reliability report has `considered`: the audits of
  its clean run whose answer has weight 1. The claim does not read it.
- A replay record can have `answered_at` and the fields of how the person
  answered. `limen replay topos` sets `answered_at` from the time of the
  confirmation.
- The conformance cases `CoverCapsAllow`, `ScopeOfAnAction`,
  `AnswerWeights`, `ConsentOfAProfile`, `AskReasons`, and `AnswerFields`.
- In Go: `Reach`, `ReachClaim`, `Crosses`, `Bound`, `Order`, `Cover`,
  `ExplainedCover`, `Approval`, `Consent`, `HowAnswered`, `MinWeight`,
  `AnswerFloor`, `AnswerFatigue`, and the shared arithmetic in
  `estimate`: `Record`, `Policy`, `Cover`, `Judge`, `Nearest`,
  `Disagree`, and `AskReason`. `featurize.Read` reads the scope, the
  families and the label of an action.

### Changed

- The cover rule replaces the answer minimum of v0.6.0. It keeps
  `LIMEN_MIN_ALLOWS` and `LIMEN_MIN_SESSIONS` with the same defaults. It
  counts by family and scope in place of the label, and it counts only
  approvals with weight. An approval answered quickly, with no details
  opened, no longer counts.
- An action without `actor.session` counts in the session of the UTC day
  when Limen received it. Before, each such action was a session of its
  own.
- `split` now means that the answers of the family have an approval and a
  denial. Answers that agree give `short`. Hosted Topos keeps a `split`
  ask for the person, so agreeing answers no longer make each next action
  wait.
- The reason of an `ask` states no probability and no direction.
  Examples: "The answers of this person about actions like this do not
  agree. Limen suggests ask." and "This person never allowed an action of
  this reach: "publish: make public". Limen suggests ask." The reasons
  of `allow` and `block` keep their probabilities.
- Labels are `labels/2`: the label of `labels/1` and a phrase from the
  scope, for example `publish: make public`, `publish: update`, or
  `bash: git push --force, default branch, cannot undo`. A case from
  before keeps its `labels/1` label. The cover rule reads its scope from
  its stored records, and an unknown reach of an old approval never
  covers a known reach of a new action.
- `invalid_answer` also covers a value that is not permitted.
- A prediction reads the cases of a person from memory: the engine keeps
  them beside the model of the person and changes them with each write.
  If Limen cannot read the cases to load the model, the prediction
  fails, and `FailClosed` suggests `ask`.

### Removed

- `minimum` is no longer set. A prediction, an explanation and an entry
  of `GET /v1/learned` keep the field for readers of v0.6, and it is
  deprecated. In Go, `MinAllows`, `Tally`, `Hold`, and the methods of
  `Minimum` are removed. `engine.Options` and `stub.Options` have
  `MinWeight` in place of `MinAllows`. `SessionOf` takes the time when
  Limen received the action.

## v0.6.0 - 2026-11-02

### Added

- A prediction, an explanation, and an entry of `GET /v1/learned` have
  `minimum` if the answer minimum changed `allow` to `ask`. It gives
  `allows`, `sessions`, `min_allows`, and `min_sessions`. An explanation
  also has `reason`, one sentence for the person. In Go, they are
  `Minimum` and `Explanation.Reason`, with `MinAllows`, `MinSessions`,
  `SessionOf`, `Tally`, and `Hold`.
- `LIMEN_MIN_ALLOWS` and `LIMEN_MIN_SESSIONS` set the answer minimum of
  `limend`. The defaults are 3 and 2. `0` turns a part off, for tests,
  and `limend` writes a warning. In Go, they are `MinAllows` and
  `MinSessions` in `engine.Options` and `stub.Options`. A negative value
  turns a part off.
- The conformance case `AnswerMinimum`.

### Changed

- A safety rule, the answer minimum. Limen suggests `allow` for an action
  only if the person approved actions with the same label 3 times or
  more, in 2 sessions or more. Until then, an `allow` from the thresholds
  becomes `ask`. The rule is temporary, until a design for consent
  replaces it.
  - Limen counts each action whose most recent answer is an approval. A
    denial never counts.
  - Limen counts the sessions by `actor.session`. An action without a
    session is a session of its own.
  - The rule applies to predictions, explanations, the entries of
    `GET /v1/learned`, and replays. It never changes a `block` or an
    `ask`.
  - Previews and reliability reports read the recorded predictions and
    do not change.
  - If Limen cannot count the approvals, it suggests `ask`.

  For example, one approval of `publish` gave the approval probability
  0.73. With the upper threshold 0.65, Limen suggested `allow`. Now it
  suggests a `split` ask, which hosted Topos in `learned` mode shows to
  the person.
- Send `actor.session` for each action of an agent. Without it, each
  action is a session of its own, and the minimum of sessions has no
  effect. Topos already sends it.
- The integration page tells how hosted Topos v0.68.0 sets the
  organization of a call with `TOPOS_DECISIONS_ORG`. With `agent`, a call
  of an agent that an organization owns goes to Limen with the ID of that
  organization. Then the cost profiles of that organization apply. Each
  `agent.tool_use` event records the organization as `decision_org`.
- The integration page gives the source of a decision of Topos v0.68.0
  and later: `topos/<mode>/<rule>`, also for a live decision.
- `SECURITY.md` names the supported versions: v0.5 and v0.4.
- The README tells which parts come after Limen is open source:
  - The hooks for Claude Code and Codex.
  - A store that more than one server can share.
  - Two model versions side by side.
  - A table of cases for `verdict.Decide`.

## v0.5.1 - 2026-11-02

### Fixed

- The documents gave a wrong meaning for an `ask` with `ask_kind`
  `split`. They told that the answers of the person disagree, and that
  more answers do not change the suggestion. A `split` ask has an
  evidence ratio of 0.5 or more, with the approval probability still in
  the ask range. The answers can disagree, or they can agree but not
  enough yet to pass a threshold. For example, two approvals and no
  denial under the upper threshold 0.90 give a `split` ask, and more
  approvals change it to `allow`. The behavior of Limen does not change.
- The integration page tells how a client finds answers that disagree.
  One sign is an approval and a denial of the same `label` in the
  `evidence` of an explanation. The other sign is a `learned` entry with
  `approved` and `denied` both more than 0. A `split` ask alone does not
  tell it.

## v0.5.0 - 2026-11-02

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
  decided the call. Because of this, the ledger gives the error rate of
  each type of rule. A decision from an earlier log keeps the source `topos/<mode>`.
- `limen replay topos` reads an answer to a network approval as an answer
  to the call that made the connection, as Topos v0.66.0 sends it. A
  record has one answer. For a call that a person saw, it is the first
  answer, which is the review. For a call that ran without a person, it is
  the last answer, which the model learns.
- A call in `limen replay topos` has the attribute `attended`, as Topos
  v0.56.0 and later send it with each call. Because of this, a replay
  calculates the same features as the live session.

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
