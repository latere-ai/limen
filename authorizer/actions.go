// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package authorizer is Cribrum's action vocabulary for the shared
// authorizer contract, latere.ai/x/pkg/authz: every question cribrumd asks
// an authorization endpoint before it acts, and the resource kind each
// question is about. An endpoint written for Cribrum answers these actions
// and no others.
package authorizer

import "latere.ai/x/pkg/authz"

// Core is the name the shared contract carries this vocabulary under.
const Core = "cribrum"

// The resource kinds of Cribrum's API. A Case carries the org and subject
// it belongs to; a Profile its org, and its kind and agent when it is
// scoped to them; a Replay and a Subject the org.
const (
	KindCase        = "Case"
	KindReliability = "Reliability"
	KindProfile     = "Profile"
	KindReplay      = "Replay"
	KindSubject     = "Subject"
)

// The actions, one per question. A constant never changes its string and
// never disappears; a new action is a new row in the API spec first and a
// constant here second.
const (
	// ActionCasePredict asks for a prediction about an action whose subject
	// is not, in general, the caller: a harness predicts for the person it
	// runs for.
	ActionCasePredict = "case.predict"
	ActionCaseDecide  = "case.decide"
	// ActionCaseAnswer is also the permission to relay an answer for the
	// subject, which [cribrum.Answer.By] attributes.
	ActionCaseAnswer = "case.answer"
	ActionCaseList   = "case.list"

	ActionReliabilityRead = "reliability.read"

	ActionProfileRead  = "profile.read"
	ActionProfileWrite = "profile.write"

	ActionReplayCreate = "replay.create"

	ActionSubjectForget = "subject.forget"
)

// table is the one place an action is paired with its kind, in the order
// the API spec lists them.
var table = []authz.Action{
	{Name: ActionCasePredict, Kind: KindCase},
	{Name: ActionCaseDecide, Kind: KindCase},
	{Name: ActionCaseAnswer, Kind: KindCase},
	{Name: ActionCaseList, Kind: KindCase},
	{Name: ActionReliabilityRead, Kind: KindReliability},
	{Name: ActionProfileRead, Kind: KindProfile},
	{Name: ActionProfileWrite, Kind: KindProfile},
	{Name: ActionReplayCreate, Kind: KindReplay},
	{Name: ActionSubjectForget, Kind: KindSubject},
}

// labels are the names a person reads for each kind, for a grant picker.
var labels = map[string]string{
	KindCase:        "Cases",
	KindReliability: "Reliability reports",
	KindProfile:     "Cost profiles",
	KindReplay:      "Replays",
	KindSubject:     "People",
}

// Vocabulary returns Cribrum's action table. It panics only if the table
// above is malformed, which its test rules out.
func Vocabulary() authz.Vocabulary { return must(build(table)) }

func build(actions []authz.Action) (authz.Vocabulary, error) {
	v, err := authz.NewVocabulary(Core, actions...)
	if err != nil {
		return authz.Vocabulary{}, err
	}
	return v.WithLabels(labels), nil
}

func must(v authz.Vocabulary, err error) authz.Vocabulary {
	if err != nil {
		panic(err)
	}
	return v
}
