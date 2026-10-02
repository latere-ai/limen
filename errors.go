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
	CodeInvalidAction:         {400, "The action is missing a field or has a value out of range. Fix the fields listed in the details and send it again."},
	CodeInvalidDecision:       {400, "The decision is missing a field or has a value out of range. Fix the fields listed in the details and send it again."},
	CodeInvalidAnswer:         {400, "The answer is missing a field. Fix the fields listed in the details and send it again."},
	CodeInvalidProfile:        {400, "The cost profile is missing a field or has a value out of range. Fix the fields listed in the details and save it again."},
	CodeInvalidQuery:          {400, "The request is missing a field or has a value out of range. Fix the fields listed in the details and ask again."},
	CodeInvalidReplay:         {400, "A replayed record is missing a field or has a value out of range. Fix the records listed in the details and replay again."},
	CodeAskTooCostly:          {422, "With these costs, Limen would decide an action about someone it knows nothing about without asking. Make each kind of error cost more than two interruptions."},
	CodeActionConflict:        {409, "This action id was already used for a different action. Give each action its own id."},
	CodeAlreadyDecided:        {409, "This action already has a decision. Each action is decided once."},
	CodeNoDecision:            {409, "This action has no recorded decision. Record the decision first, then send the answer."},
	CodeUnauthenticated:       {401, "The request carries no valid credential. Sign in again and retry."},
	CodeForbidden:             {403, "You do not have permission for this. Ask an administrator of the organization for access."},
	CodeBodyTooLarge:          {413, "The request is larger than Limen accepts. Send it in smaller parts."},
	CodeRateLimited:           {429, "Too many requests. Wait a moment and try again."},
	CodeInternal:              {500, "Something failed on Limen's side. Try again; if it keeps failing, report it with the request id."},
	CodeAuthorizerUnavailable: {503, "The permission service is unavailable. Try again shortly."},
	CodeUnavailable:           {503, "Limen is unavailable right now. Hold the action for a person and try again shortly."},
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
