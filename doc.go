// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package limen is the contract of Limen, the learned input to an agent
// harness's allow, ask, or block decision.
//
// A harness describes one proposed tool call as an [Action] and asks a
// [Gate] to [Gate.Assess] it. The [Assessment] carries the probability that
// the person the agent acts for would approve the call, the verdict that
// probability implies under the organization's stated [Costs], and, when
// the call was sampled for review, the [Audit] probability that makes the
// person's answer usable for an unbiased error estimate. The person's
// answer comes back through [Gate.Answer], and [Gate.Reliability] reports
// how often Limen, or any judge whose verdict arrived as a [Signal], allows
// what that person would deny.
//
// The package holds the types, the interfaces, the error codes, and the
// pure functions that define the contract's arithmetic: the verdict order
// ([Least]), the thresholds a cost profile implies ([Costs.Thresholds]),
// the verdict a score implies ([Thresholds.Verdict]), the number of clean
// audits a certificate needs ([CertificationSize]), and the fail-closed
// wrapper every caller should put around a gate ([FailClosed]). The engine
// that computes an assessment, the HTTP client, the stub and the
// conformance suite live in their own packages. The package imports the
// standard library only, so any harness can depend on it.
//
// Three rules hold for every implementation, and the conformance suite
// checks each: a verdict is never more permissive than the [Action.Ceiling]
// the deterministic layers set; a failure yields [Ask], never [Allow]; and
// an audit's probability is fixed when the call is assessed and is never
// supplied by a caller.
package limen
