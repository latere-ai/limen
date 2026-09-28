# Security

Report a vulnerability through a private security advisory on this
repository's GitHub page, or to security@latere.ai. Do not open a public
issue for one. You will hear back within three business days, and a fix
for a high severity issue ships within thirty days. Credit in the release
notes on request.

Limen has no release yet. The contract in the module root is pre-release
and may change; once a release is cut, fixes go to the two most recent
minor release series.

Limen stands between an agent and what it may do, so the design commits
to four properties, and a way to break any of them is a vulnerability
whatever its other impact:

- **A verdict is never more permissive than its ceiling.** A deterministic
  deny, passed as `ceiling: block`, is final. An assessment that allows,
  flags, or asks about a call whose ceiling forbids it is in scope.
- **A failure never allows.** An error, a timeout, or an unreachable
  service yields `ask`, narrowed by the ceiling. A path on which a failure
  yields `allow` is in scope.
- **An audit's probability is Limen's, not the caller's.** Error estimates
  weight each answer by one over the probability the call had of being
  answered, so a way for a caller to choose that probability, to draw an
  audit sample twice, or to have an answer counted with a probability it
  did not have, corrupts every estimate built on it and is in scope.
- **Text an agent writes never reaches a model.** `Action.input` is read
  by deterministic feature extractors only. A path by which input, a
  signal source name, or any other caller-supplied text is passed to a
  language model is in scope.

Answers are a person's data. A way to read another subject's
assessments or answers without the authorizer's permission, or a record
that survives `Forget`, is in scope.

What each property rests on is in [spec 001](specs/001-architecture.md),
and the cases that check them are in
[spec 006](specs/006-conformance.md).
