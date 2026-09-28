# Limen

**Learned escalation for agent harnesses: which calls an agent may run on
its own, which wait for a person, and which it may not run at all.**
Limen learns from each person's own approve and deny answers, derives
its thresholds from what your organization says errors and interruptions
cost, and measures how often its automatic verdicts, or an LLM judge's,
allow what that person would have denied.

> **Status: pre-release, contract only.** This repository holds Limen's
> contract (the Go types and interfaces, the HTTP API, the error codes),
> its documentation, and its design specs. The engine that computes an
> assessment, the server (`limend`), the HTTP client, the test stub, the
> conformance suite, and the harness hooks are specified in
> [`specs/`](specs/README.md) but not built yet. Nothing here decides a
> live call today.

## The problem

Agent harnesses gate tool calls with a switch: run it, or refuse it. A
refusal does not pause the run for a person. The agent reads it as a
result and keeps going, often by routing around the obstacle in ways
that are worse than asking would have been.

Where a harness can ask, the question of when to ask is usually a
hand-written list or a threshold someone tuned once. It does not learn
that a person has approved the same test command a hundred times, and it
does not notice when that person moves to an unfamiliar production system
and becomes stricter.

Harnesses that route everything through a shell have no fixed tool list
to write rules against, so they hand the decision to a second language
model, a judge. The judge is opaque. Nobody can say how often it allows
what a given person would deny, and in current designs the person only
sees calls the judge blocked, so its allow errors are never observed.

## How it works

- **Escalate, don't refuse.** Every call gets one of four verdicts:
  `allow`, `flag` (run it and ask afterwards), `ask`, or `block`. When
  Limen is unsure, the call waits for a person instead of failing.
- **Thresholds come from costs.** You state what a wrong allow, a wrong
  block, and one interruption cost. The thresholds follow from those three
  numbers by a closed formula; nobody tunes them.
- **Learn the person, keep the risk.** A Gaussian-process model learns
  each person's tolerance from their answers. It lets that tolerance
  drift over time, but it keeps what it learned about how risky each kind
  of action is, so it does not ask again about everything on a clock.
- **Know its own error rate.** A call Limen allows on its own never gets
  an answer. A small random share of those calls is shown to the person
  anyway, with the sampling probability recorded when the call is
  assessed, and only those answers estimate the false-allow rate without
  bias. The same answers measure any judge whose verdict you pass in.
- **Rules still win.** Your harness's deterministic rules set a ceiling
  on every call. Limen's verdict is never more permissive, and when
  Limen fails or times out, the call waits for a person.

[Principles](docs/principles.md) states each rule and why it holds;
[Theory](docs/theory.md) is the mathematics in brief.

## At a glance

A harness describes a proposed call as an `Action` and asks a `Gate`. The
engine will implement `Gate` in process and the client over HTTP, so a
harness codes against one interface either way. Wrap it with
`FailClosed`, which turns any error or timeout into `ask`.

```go
import "latere.ai/x/limen"

// g is the in-process engine or the HTTP client; both satisfy limen.Gate.
gate := limen.FailClosed(g, 200*time.Millisecond, func(err error) { log.Print(err) })

// FailClosed never returns an error from Assess: a failure is an ask.
as, _ := gate.Assess(ctx, limen.Action{
	Subject: "https://issuer.example|usr_42",
	Org:     "org_7",
	CallID:  "toolu_01",
	Tool:    "bash",
	Effect:  limen.EffectExternal,
	Input:   json.RawMessage(`{"command":"git push origin main"}`),
	Machine: limen.Machine{Kind: limen.MachineSandbox, Credentialed: true},
	Ceiling: limen.Allow, // your deterministic rules impose nothing here
	At:      time.Now(),
})

switch as.Verdict {
case limen.Allow:
	run()
case limen.Flag:
	run()
	queueForReview(as.ID) // the person answers later
case limen.Ask:
	approved := holdForPerson(as.Reason)
	gate.Answer(ctx, as.ID, limen.Answer{Approve: approved, At: time.Now()})
case limen.Block:
	refuse(as.Reason)
}
```

The same call over HTTP:

```http
POST /v1/assessments
Authorization: Bearer <token>
Content-Type: application/json

{
  "subject": "https://issuer.example|usr_42",
  "org": "org_7",
  "call_id": "toolu_01",
  "tool": "bash",
  "effect": "external",
  "input": {"command": "git push origin main"},
  "target": {"kind": "ref", "id": "refs/heads/main", "sensitivity": 0.9},
  "machine": {"kind": "sandbox", "credentialed": true},
  "signals": [
    {"source": "rules/1", "score": 0.4},
    {"source": "judge/claude-auto-mode", "verdict": "allow"}
  ],
  "ceiling": "allow",
  "at": "2026-09-28T10:02:11Z"
}
```

```json
{
  "id": "asm_01J8",
  "call_id": "toolu_01",
  "verdict": "ask",
  "approve": 0.58,
  "uncertainty": 0.71,
  "score": 0.42,
  "thresholds": {"ask_at": 0.1, "block_at": 0.75},
  "profile": "org_7",
  "source": "learned/3",
  "reason": "The chance this person approves (0.58) is inside the ask band (0.25 to 0.90) of profile org_7."
}
```

The judge said `allow`, but Limen does not know this person approves a
push to `main` often enough to skip asking under this organization's
costs, so the call waits. When the person answers, the harness sends
`POST /v1/assessments/asm_01J8/answer` with `{"approve": true, "at": ...}`.
The [API guide](docs/api.md) covers every route.

## Documentation

| Page | |
|---|---|
| [Integrating](docs/integrating.md) | using Limen from a harness: building an action, applying the verdict, sending answers, choosing a cost profile, reading reliability, rolling out |
| [API](docs/api.md) | every HTTP route, its request and response, the errors, and the permission each asks |
| [OpenAPI](api/openapi.yaml) | the same surface as an OpenAPI 3.1 document |
| [Principles](docs/principles.md) | the rules every implementation keeps, and why |
| [Theory](docs/theory.md) | the model, the decision rule, audits, and what can be known about a judge |
| [Specs](specs/README.md) | the design records the implementation is built from |

The Go reference is the package documentation of `latere.ai/x/limen`.

## Research

Limen is built on the paper *Learning When to Ask: Trust Calibration for
Agentic Tool Use, from Known Tools to Opaque LLM Judges*
([arXiv:2605.19151](https://arxiv.org/abs/2605.19151)). Its simulations,
a reference implementation, and machine-checked proofs of its eight
propositions are at
[github.com/changkun/trustcalib](https://github.com/changkun/trustcalib).

```bibtex
@misc{ou2026learning,
  title         = {Learning When to Ask: Trust Calibration for Agentic Tool
                   Use, from Known Tools to Opaque LLM Judges},
  author        = {Ou, Changkun},
  year          = {2026},
  eprint        = {2605.19151},
  archivePrefix = {arXiv},
  primaryClass  = {cs.AI},
  url           = {https://arxiv.org/abs/2605.19151}
}
```

## Contributing

[`CONTRIBUTING.md`](CONTRIBUTING.md) is how to build, test, and send a
change. Report a vulnerability as [`SECURITY.md`](SECURITY.md) describes.

## License

Apache License 2.0. See [`LICENSE`](LICENSE).
