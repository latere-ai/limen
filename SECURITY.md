# Security

## Report a vulnerability

Do not open a public issue for a vulnerability. Use one of these two
channels:

- A private security advisory on the GitHub page of this repository.
- An email to security@latere.ai.

We reply in three business days or less. For a vulnerability of high
severity, we release a fix in thirty days or less. If you ask for credit,
we give it in the release notes.

## Supported versions

We fix vulnerabilities in the two most recent minor release series.

| Version | Supported |
|---|---|
| v0.6.x | Yes |
| v0.5.x | Yes |
| v0.4.x and older | No |

A fix comes in a new patch release of each supported series. Before
v1.0.0, the contract in the module root can change in a minor release.
The [CHANGELOG](CHANGELOG.md) names each incompatible change.

## What is a vulnerability

Limen controls which automated actions run without a person, and it
measures how often these decisions are wrong. For this reason, the design
gives the properties below. A method that breaks one of these properties
is a vulnerability, whatever its other effects are.

- **A deny from your rules is final, and a failure never allows.** If the
  ceiling is `block`, `verdict.Decide` returns `block`. If Limen fails or
  does not answer in time, `FailClosed` returns `ask`. It is in scope if a
  failure, or a result from Limen, makes a decision point allow an action
  that its ceiling does not permit.
- **Only the decision point sets the review probability.** Each error
  rate gives each review the weight one divided by the review probability
  of its decision. These are in scope:
  - A caller without the permission `case.decide` records a decision or
    changes a decision.
  - A caller records two decisions for one action.
  - A caller sends a changed action with the key of an earlier action, and
    gets a new prediction.
  - A caller makes Limen count an answer as a review when it is not a
    review.
- **Limen never keeps the input of an action, and never gives it to a
  language model.** The feature extractors read the `input` of an action
  during the prediction, and then discard it. These are in scope:
  - Limen keeps an input in a table, a file, a log, or a snapshot.
  - An input, the name of a signal source, or other text from a caller
    gets to a language model.
- **Only a caller with permission can get to the records of a person.**
  It is in scope if a caller can predict, answer, list, or read a report
  for a person without the permission for that route.
- **Each organization is separate.** Each record has three key fields:
  the organization, the person, and the action ID. It is in scope if a
  caller can use a key to read or change the records of a different
  organization. The records are the cases, the models, the cost profiles,
  and the reports. It is also in scope if a key of one organization can
  refer to a record of a different organization.
- **`Forget` removes all the data of a person.** `Forget` applies to one
  person in one organization. It is in scope if a record, an answer, a
  model snapshot, the cost profile, or a report of that person stays
  after `Forget`.

[Spec 001](specs/001-architecture.md) gives the basis of each property.
[Spec 006](specs/006-conformance.md) gives the tests that check them.
