// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package limen is the contract of Limen, the oversight layer for automated
// actions: which actions may run on their own, which a person should see,
// and how often each source of those decisions is wrong.
//
// An automated actor, an agent most often, proposes an [Action]: a tool
// call, a network request, a push, a payment. A decision point, the code
// that lets the action run or not, owns the decision. Limen serves it in
// three orthogonal parts:
//
//   - The [Predictor] predicts, for the person on whose behalf the action
//     runs, the probability that they would approve it, and suggests a
//     verdict from the costs their organization states ([Prediction]),
//     never an allow before the person's approvals meet the answer
//     minimum ([MinAllows], [MinSessions]).
//   - The decision point decides. It composes the suggestion with the
//     ceiling its deterministic rules leave open and with random review
//     sampling (latere.ai/x/pkg/verdict.Decide), applies the verdict, and
//     records it with the probability that a person sees the action
//     ([Decision]).
//   - The [Ledger] keeps every decision and the person's [Answer], learns
//     from them, and reports how often any source of decisions allows
//     what the person would deny ([Reliability]): Limen's own predictions,
//     the decisions applied, a rule, or an LLM judge whose verdict arrived
//     as a [Signal].
//
// The [Explainer] shows the person what the model decides from: why one
// prediction is what it is ([Explanation]), what the model has learned
// about them for each kind of action ([Learned]), and what another cost
// profile would have done ([Preview]). Each is exact for the model, and
// none is for an agent to read.
//
// The [Reporter] reports an organization's agents by week across all its
// persons, and never one person ([Report]). A person's own cost profile
// only ever makes the organization's stricter for that person
// ([Stricter]).
//
// A [Service] is the [Predictor], the [Ledger], the [Admin] interface for
// cost profiles, replays and forgetting, the [Explainer], and the
// [Reporter]. The engine
// (latere.ai/x/limen/engine), the HTTP client (latere.ai/x/limen/client)
// and the stub (latere.ai/x/limen/stub) each implement it.
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
// ([Thresholds.Verdict]), the answer minimum that holds an allow
// ([Hold]), the number of clean reviews a certificate needs
// ([CertificationSize]), the meet of two cost profiles ([Stricter]), and
// the fail-closed wrapper every decision point
// should put around a predictor ([FailClosed]). It imports the standard
// library and latere.ai/x/pkg/verdict only.
package limen
