// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package topostest

import (
	"context"
	"testing"

	"latere.ai/x/topos/session"
	"latere.ai/x/topos/session/dir"
)

// What Write writes, Topos's own store reads back: the header, the calls,
// the confirmations, and a redaction.
func TestWriteReadsBack(t *testing.T) {
	data := t.TempDir()
	ids := Write(t, data,
		Session{Initiator: "u", Org: "acme", Machine: session.MachineCella, Calls: []Call{
			{ToolUseID: "t1", Name: "bash", Input: `{"command":"ls"}`, Verdict: "ask", Mode: "confirm", Answer: "allow", Remember: "bash:ls"},
			{ToolUseID: "t2", Name: "read", Verdict: "allow", Redact: true},
		}, Raw: []Raw{{Type: "custom.event", Payload: `{"x":1}`}}},
		Session{Initiator: "v"},
	)
	if len(ids) != 2 {
		t.Fatalf("ids %v", ids)
	}
	st, err := dir.Open(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	sess, err := st.Get(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if sess.Initiator.Subject != "u" || sess.Metadata["org"] != "acme" || sess.Machine.Kind != session.MachineCella {
		t.Errorf("header %+v", sess)
	}
	events, err := st.Events(ctx, ids[0], 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var types []session.Type
	redacted := 0
	for _, e := range events {
		types = append(types, e.Type)
		if e.Redacted() {
			redacted++
		}
	}
	if len(events) < 4 || redacted != 1 {
		t.Errorf("events %v, %d redacted", types, redacted)
	}
	other, err := st.Get(ctx, ids[1])
	if err != nil || other.Machine.Kind != session.MachineHost {
		t.Errorf("second session %+v, %v", other, err)
	}
}
