// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

import (
	"errors"
	"fmt"
	"strings"
)

// Code is the stable identifier of one row of the error table. A code
// never changes its string or its meaning; a new failure is a new code.
type Code string

// The error table. Each code has one HTTP status and one fixed sentence for
// the person who reads it ([Code.Message]); what varies between two
// occurrences travels in [Error.Detail] and [Error.Fields].
const (
	CodeInvalidAction         Code = "invalid_action"
	CodeInvalidDecision       Code = "invalid_decision"
	CodeInvalidAnswer         Code = "invalid_answer"
	CodeInvalidProfile        Code = "invalid_profile"
	CodeInvalidQuery          Code = "invalid_query"
	CodeInvalidReplay         Code = "invalid_replay"
	CodeAskTooCostly          Code = "ask_too_costly"
	CodeActionConflict        Code = "action_conflict"
	CodeAlreadyDecided        Code = "already_decided"
	CodeNoDecision            Code = "no_decision"
	CodeUnauthenticated       Code = "unauthenticated"
	CodeForbidden             Code = "forbidden"
	CodeBodyTooLarge          Code = "body_too_large"
	CodeRateLimited           Code = "rate_limited"
	CodeInternal              Code = "internal"
	CodeAuthorizerUnavailable Code = "authorizer_unavailable"
	CodeUnavailable           Code = "unavailable"
)

type row struct {
	status  int
	message string
}

// table is the one place a code's status and sentence are written. The
// sentences are for the person reading an API response or a harness's
// output, so they say what happened and what to do next, and never name a
// package, a field path, or a value; those are the detail's.
var table = map[Code]row{
	CodeInvalidAction:         {400, "The action has a missing field or a value that is not permitted. Correct the fields that the details list, then send the action again."},
	CodeInvalidDecision:       {400, "The decision has a missing field or a value that is not permitted. Correct the fields that the details list, then send the decision again."},
	CodeInvalidAnswer:         {400, "The answer has a missing field. Correct the fields that the details list, then send the answer again."},
	CodeInvalidProfile:        {400, "The cost profile has a missing field or a value that is not permitted. Correct the fields that the details list, then save the cost profile again."},
	CodeInvalidQuery:          {400, "The request has a missing field or a value that is not permitted. Correct the fields that the details list, then send the request again."},
	CodeInvalidReplay:         {400, "A replayed record has a missing field or a value that is not permitted. Correct the records that the details list, then send the replay again."},
	CodeAskTooCostly:          {422, "With these costs, Limen would not ask about an action of a person that it has no answers from. Make the cost of each type of error more than two times the cost of an ask."},
	CodeActionConflict:        {409, "A different action already uses this action ID. Give each action its own ID."},
	CodeAlreadyDecided:        {409, "This action already has a decision. Limen accepts one decision for each action."},
	CodeNoDecision:            {409, "This action has no recorded decision. Record the decision, then send the answer."},
	CodeUnauthenticated:       {401, "The request has no valid credential. Sign in again, then send the request again."},
	CodeForbidden:             {403, "You do not have permission for this request. Ask an administrator of the organization for access."},
	CodeBodyTooLarge:          {413, "The request is larger than the limit of Limen. Send it in smaller parts."},
	CodeRateLimited:           {429, "There are too many requests. Wait a short time, then send the request again."},
	CodeInternal:              {500, "Limen had an internal failure. Send the request again. If the failure continues, report it with the request ID."},
	CodeAuthorizerUnavailable: {503, "The permission service is not available. Send the request again after a short time."},
	CodeUnavailable:           {503, "Limen is not available now. Hold the action for a person, and send the request again after a short time."},
}

// Codes returns every code in the table, in the order the constants are
// declared.
func Codes() []Code {
	return []Code{
		CodeInvalidAction, CodeInvalidDecision, CodeInvalidAnswer, CodeInvalidProfile,
		CodeInvalidQuery, CodeInvalidReplay, CodeAskTooCostly, CodeActionConflict,
		CodeAlreadyDecided, CodeNoDecision, CodeUnauthenticated, CodeForbidden,
		CodeBodyTooLarge, CodeRateLimited, CodeInternal, CodeAuthorizerUnavailable,
		CodeUnavailable,
	}
}

// Message is the code's fixed sentence for the person reading it, or the
// empty string for a code outside the table.
func (c Code) Message() string { return table[c].message }

// Status is the HTTP status the code is served with, or 500 for a code
// outside the table.
func (c Code) Status() int {
	if r, ok := table[c]; ok {
		return r.status
	}
	return 500
}

// Error is a failure of the contract: one code, the fields at fault, and a
// detail for whoever debugs it. Its [Error.Error] string is the developer
// detail; a user surface shows [Code.Message] instead.
type Error struct {
	Code   Code
	Fields []string // the JSON paths at fault, e.g. "environment.kind"
	Detail string   // what was observed and what was expected
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(string(e.Code))
	if len(e.Fields) > 0 {
		fmt.Fprintf(&b, " [%s]", strings.Join(e.Fields, ", "))
	}
	if e.Detail != "" {
		b.WriteString(": ")
		b.WriteString(e.Detail)
	}
	return b.String()
}

// CodeOf returns the code of the first [*Error] in err's chain, or
// [CodeInternal] when there is none.
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeInternal
}

// problems collects the fields at fault in one validation pass, so a
// caller learns every problem at once rather than one per round trip.
type problems struct {
	fields []string
	notes  []string
}

func (p *problems) add(field, note string) {
	p.fields = append(p.fields, field)
	p.notes = append(p.notes, field+" "+note)
}

func (p *problems) err(code Code) error {
	if len(p.fields) == 0 {
		return nil
	}
	return &Error{Code: code, Fields: p.fields, Detail: strings.Join(p.notes, "; ")}
}
