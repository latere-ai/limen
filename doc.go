// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package cribrum is the contract of Cribrum, the oversight layer for automated
// actions: which actions may run on their own, which a person should see,
// and how often each source of those decisions is wrong.
//
// An automated actor, an agent most often, proposes an [Action]: a tool
// call, a network request, a push, a payment. A decision point, the code
// that lets the action run or not, owns the decision. Cribrum serves it in
// three orthogonal parts:
//
//   - The [Predictor] predicts, for the person on whose behalf the action
//     runs, the probability that they would approve it, and suggests a
//     verdict from the costs their organization states ([Prediction]).
//   - The decision point decides. It composes the suggestion with the
//     ceiling its deterministic rules leave open and with random review
//     sampling (latere.ai/x/pkg/verdict.Decide), applies the verdict, and
//     records it with the probability that a person sees the action
//     ([Decision]).
//   - The [Ledger] keeps every decision and the person's [Answer], learns
//     from them, and reports how often any source of decisions allows
//     what the person would deny ([Reliability]): Cribrum's own predictions,
//     the decisions applied, a rule, or an LLM judge whose verdict arrived
//     as a [Signal].
//
// The ledger needs no predictor: a harness that decides by rules alone can
// record its decisions and learn how often its rules, and the judge it
// consults, are wrong. The predictor needs the ledger, because the ledger's
// answers are what it learns from.
//
// Every record is keyed by the action's [Key]: the organization, the
// subject (the person whose approvals are learned), and the decision
// point's own id for the action. The package holds the types, the
// interfaces, the error codes, and the pure functions that define the
// contract's arithmetic: the thresholds a cost profile implies
// ([Costs.Thresholds]), the verdict an approval probability implies
// ([Thresholds.Verdict]), the number of clean reviews a certificate needs
// ([CertificationSize]), and the fail-closed wrapper every decision point
// should put around a predictor ([FailClosed]). It imports the standard
// library and latere.ai/x/pkg/verdict only.
package cribrum
