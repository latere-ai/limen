// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package topos

import (
	"slices"
	"testing"
	"time"

	"latere.ai/x/topos/session"
)

// Topos sends an approval's answer when it settles the approval, at its
// approval.decided, so answers follow the settlements and not the
// requests. The approvals a confirmation settled that have no
// approval.decided yet come last, in the order of their requests. An
// unreadable approval.decided, or a second one, settles nothing.
func TestAnswersFollowTheSettlements(t *testing.T) {
	start := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	person := session.Sender{Subject: "u", Kind: session.SenderPerson}
	var events []session.Event
	add := func(typ session.Type, v any) {
		b, err := session.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, session.Event{ID: session.NewID(session.PrefixEvent), Type: typ, Payload: b, Time: start.Add(time.Duration(len(events)) * time.Second)})
	}
	request := func(id string) {
		add(session.TypeApprovalRequested, session.ApprovalRequested{ApprovalID: id, ToolUseID: "toolu_1", Verdict: "ask"})
	}
	confirm := func(id, decision string) {
		add(session.TypeUserToolConfirmation, session.UserToolConfirmation{Sender: person, ApprovalID: id, Decision: decision})
	}
	decide := func(id, decision string) {
		add(session.TypeApprovalDecided, session.ApprovalDecided{ApprovalID: id, Decision: decision, By: person})
	}
	add(session.TypeUserToolConfirmation, session.UserToolConfirmation{Sender: person, ToolUseID: "toolu_1", Decision: session.DecisionAllow})
	request("apr_1")
	request("apr_2")
	request("apr_3")
	request("apr_4")
	confirm("apr_2", session.DecisionDeny)
	decide("apr_2", session.DecisionDeny)
	confirm("apr_1", session.DecisionAllow)
	decide("apr_1", session.DecisionAllow)
	confirm("apr_4", session.DecisionDeny)
	confirm("apr_3", session.DecisionAllow)
	events = append(events, session.Event{ID: session.NewID(session.PrefixEvent), Type: session.TypeApprovalDecided, Payload: []byte(`{"approval_id":5}`)})
	decide("apr_2", session.DecisionAllow)
	got := answersOf(events)["toolu_1"]
	var approves []bool
	var ats []time.Duration
	for _, a := range got {
		approves = append(approves, a.approve)
		if a.at.IsZero() {
			ats = append(ats, -1)
		} else {
			ats = append(ats, a.at.Sub(start))
		}
	}
	if want := []bool{true, false, true, true, false}; !slices.Equal(approves, want) {
		t.Errorf("answers %v, want %v", approves, want)
	}
	// Each answer has the time of its confirmation or of its
	// approval.decided; the approvals settled at the end have none.
	if want := []time.Duration{0, 6 * time.Second, 8 * time.Second, -1, -1}; !slices.Equal(ats, want) {
		t.Errorf("times %v, want %v", ats, want)
	}
}
