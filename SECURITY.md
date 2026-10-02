# Security

Report a vulnerability through a private security advisory on this
repository's GitHub page, or to security@latere.ai. Do not open a public
issue for one. You will hear back within three business days, and a fix
for a high severity issue ships within thirty days. Credit in the release
notes on request.

Limen has no release yet. The contract in the module root is pre-release
and may change; once a release is cut, fixes go to the two most recent
minor release series.

Limen informs whether an automated action runs and measures how often
that decision is wrong, so the design commits to these properties, and a
way to break any of them is a vulnerability whatever its other impact:

- **A deterministic deny is final, and a failure never allows.**
  `verdict.Decide` returns `block` under a `block` ceiling, and
  `FailClosed` turns an error or a timeout into `ask`. A path on which a
  failure, or anything Limen returns, makes a decision point allow what
  its ceiling forbids is in scope.
- **The review probability is the decision point's.** Error estimates
  weight each review by one over the probability, recorded with the
  decision, that a person saw the action. A way for anyone but a caller
  the authorizer allows `case.decide` to record or change a decision, to
  record two decisions for one key, to draw a second prediction for one
  action under a new body, or to have an answer counted as a review it
  was not, corrupts every estimate built on it and is in scope.
- **An action's input is never stored, and never reaches a model.**
  Deterministic extractors read `Action.input` at prediction time and
  drop it. A stored input, in any table, file, log or snapshot, and a
  path by which input, a signal source name, or any other caller text
  reaches a language model, are in scope.
- **A subject's records are reached only with permission.** Predicting
  for, answering for, listing or reporting on a subject without the
  authorizer allowing the route's action on that subject is in scope.
- **Organizations are separate.** Every record is keyed by organization,
  subject and action id. A way to read, collide with, or influence
  another organization's cases, models, profiles or reports through a
  key is in scope.
- **Forgetting is complete.** A record, answer, model snapshot or report
  of a subject that survives `Forget` in that organization is in scope.

What each property rests on is in [spec 001](specs/001-architecture.md),
and the cases that check them are in
[spec 006](specs/006-conformance.md).
