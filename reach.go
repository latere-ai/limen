// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limen

import (
	"slices"
	"strings"
)

// The scope of an action is its reach, in four dimensions with closed
// vocabularies (spec 011): whether it can be undone, the boundaries it
// crosses, the class of its target, and whether it is a first contact.
// Each dimension orders its values from the least reach to the most, and
// each has the value [Unknown] beside them.
const (
	// Unknown is the value of a dimension that nobody read or sent. It is
	// within the greatest value of its dimension, the least value is within
	// it, and no middle value is comparable with it.
	Unknown = "unknown"

	// The values of undo. None changes nothing; Reversible can be undone
	// in the workspace with no loss; Recoverable can be undone from a copy
	// or by a second action; Irreversible cannot be undone, or other people
	// already saw it.
	UndoNone         = "none"
	UndoReversible   = "reversible"
	UndoRecoverable  = "recoverable"
	UndoIrreversible = "irreversible"

	// The values of the class of a target: a disposable thing, a thing
	// this session or person made, a thing other people use, and the
	// default or protected branch, production, or a live release.
	ClassScratch = "scratch"
	ClassOwn     = "own"
	ClassShared  = "shared"
	ClassPrimary = "primary"

	// The values of contact: ContactFirst when no earlier approved case of
	// the person has the same target.
	ContactRepeat = "repeat"
	ContactFirst  = "first"
)

// The holds an environment can name: limits it enforces, so a reach
// cannot occur. Each one bounds Limen's reading of the input.
const (
	// HoldEgress: no host outside the network of the session is reachable
	// without its own approval, so crosses.network is no.
	HoldEgress = "egress"
	// HoldProtectedRefs: the git host refuses a push to a protected
	// branch, so a target class of primary that Limen reads from a git push
	// is unknown.
	HoldProtectedRefs = "protected_refs"
	// HoldCheckpoints: the decision point keeps a copy of the workspace
	// that can restore it, so an action that crosses no boundary is at most
	// recoverable.
	HoldCheckpoints = "checkpoints"
)

// The visibility of the repositories a session pushes to.
const (
	RepositoriesPrivate = "private"
	RepositoriesPublic  = "public"
	RepositoriesMixed   = "mixed"
)

// Order is the values of one dimension of a scope, from the least reach
// to the most. The order is partial: [Unknown] sits above the least value
// and below the greatest, and is not comparable with a middle value.
type Order []string

// Undos is the order of undo.
func Undos() Order { return Order{UndoNone, UndoReversible, UndoRecoverable, UndoIrreversible} }

// TargetClasses is the order of the class of a target.
func TargetClasses() Order { return Order{ClassScratch, ClassOwn, ClassShared, ClassPrimary} }

// Contacts is the order of contact. Contact is not in the order of
// scopes, because each target was a first contact at its first approval;
// the order serves the merge of a reading and a claim.
func Contacts() Order { return Order{ContactRepeat, ContactFirst} }

// Bounds is the order of one boundary of crosses.
func Bounds() Order { return Order{string(BoundNo), string(BoundYes)} }

// Holds lists the holds, in the order of their constants.
func Holds() []string { return []string{HoldEgress, HoldProtectedRefs, HoldCheckpoints} }

// Repositories lists the visibilities of repositories.
func Repositories() []string {
	return []string{RepositoriesPrivate, RepositoriesPublic, RepositoriesMixed}
}

// Word is v when it is a value of o or [Unknown], the empty string when v
// is empty, and [Unknown] for any other word: a value outside the
// vocabulary is unknown, and it is not an error.
func (o Order) Word(v string) string {
	switch {
	case v == "":
		return ""
	case v == Unknown || slices.Contains(o, v):
		return v
	}
	return Unknown
}

// rank is the position of v in o, or -1 for [Unknown], an empty word and
// a word outside the vocabulary.
func (o Order) rank(v string) int { return slices.Index(o, v) }

// Within reports whether v is within w. An empty word and a word outside
// the vocabulary are [Unknown].
func (o Order) Within(v, w string) bool {
	i, j := o.rank(v), o.rank(w)
	switch {
	case i >= 0 && j >= 0:
		return i <= j
	case i < 0 && j < 0:
		return true
	case i < 0:
		return j == len(o)-1
	}
	return i == 0
}

// Join is the least value that both v and w are within: the greater of
// the two, or the greatest value when they are not comparable. An empty
// word is no value, so the join is the other one.
func (o Order) Join(v, w string) string {
	v, w = o.Word(v), o.Word(w)
	switch {
	case v == "":
		return w
	case w == "":
		return v
	case o.Within(v, w):
		return w
	case o.Within(w, v):
		return v
	}
	return o[len(o)-1]
}

// Bound is one boundary of [Crosses]: [BoundYes], [BoundNo], or
// [BoundUnknown], the empty value. On the wire it is true, false, or no
// key, so a bound outside these three is unknown.
type Bound string

const (
	BoundUnknown Bound = ""
	BoundNo      Bound = "no"
	BoundYes     Bound = "yes"
)

// IsZero reports whether b is unknown, so that a field of [Crosses] with
// omitzero leaves an unknown bound out.
func (b Bound) IsZero() bool { return b != BoundNo && b != BoundYes }

// MarshalJSON writes yes as true and no as false; an unknown bound, which
// omitzero leaves out, is null.
func (b Bound) MarshalJSON() ([]byte, error) {
	switch b {
	case BoundYes:
		return []byte("true"), nil
	case BoundNo:
		return []byte("false"), nil
	}
	return []byte("null"), nil
}

// UnmarshalJSON reads true as yes and false as no. Any other value is
// unknown, and not an error, so a newer decision point works with an
// older Limen.
func (b *Bound) UnmarshalJSON(data []byte) error {
	switch strings.TrimSpace(string(data)) {
	case "true":
		*b = BoundYes
	case "false":
		*b = BoundNo
	default:
		*b = BoundUnknown
	}
	return nil
}

// Crosses are the boundaries an action crosses. Network, Organization and
// Public each make Remote yes in a scope Limen reads.
type Crosses struct {
	// Machine: the action changes the machine outside the workspace, for
	// example a home or system path.
	Machine Bound `json:"machine,omitzero"`
	// Remote: the action reaches a system other than the machine.
	Remote Bound `json:"remote,omitzero"`
	// Network: it reaches a host outside the network of the session.
	Network Bound `json:"network,omitzero"`
	// Organization: it reaches a party that the organization does not own.
	Organization Bound `json:"organization,omitzero"`
	// Public: it makes something readable by anyone.
	Public Bound `json:"public,omitzero"`
}

// bounds are the five boundaries, in the order of their fields.
func (c Crosses) bounds() [5]Bound {
	return [5]Bound{c.Machine, c.Remote, c.Network, c.Organization, c.Public}
}

// ReachClaim is what a decision point says about the scope of an action,
// in the words of the vocabularies alone and never with text of the
// input. An empty field, or a bound with no key, is no claim. A word
// outside the vocabulary is a claim of [Unknown], and it is not an error.
// Limen takes the greater of each claim and its own reading of the input,
// so a claim can make the scope wider and never narrower.
type ReachClaim struct {
	// Undo is a value of [Undos].
	Undo string `json:"undo,omitempty"`
	// Crosses holds a bound for each boundary the decision point knows.
	Crosses Crosses `json:"crosses,omitzero"`
	// Contact is [ContactFirst] or [ContactRepeat].
	Contact string `json:"contact,omitempty"`
}

// Reach is the scope of an action as Limen used it (spec 011): its reading
// of the input and the fields of the action, bounded by the holds of the
// environment and merged with the claims of the decision point. Every
// dimension has a value, [Unknown] included; an unknown bound of Crosses
// has no key on the wire. It holds words of the vocabularies alone.
type Reach struct {
	Undo        string  `json:"undo"`
	Crosses     Crosses `json:"crosses"`
	TargetClass string  `json:"target_class"`
	Contact     string  `json:"contact"`
	// TargetKind names the type of the target without its name: the kind
	// of the target the decision point sent or, for a shell action without
	// one, a kind its segments name (ref, repository, package, host, path).
	// It is empty when neither gives one.
	TargetKind string `json:"target_kind,omitempty"`
}

// dimensions are the values of r in the dimensions of the order of scopes,
// with the order of each: undo, each boundary of crosses, and the class of
// the target. Contact is not one of them.
func (r Reach) dimensions() ([7]string, [7]Order) {
	b := r.Crosses.bounds()
	bo := Bounds()
	return [7]string{r.Undo, string(b[0]), string(b[1]), string(b[2]), string(b[3]), string(b[4]), r.TargetClass},
		[7]Order{Undos(), bo, bo, bo, bo, bo, TargetClasses()}
}

// Within reports whether r is within o: in each dimension of the order,
// the value of r is within the value of o. An approval with the scope o
// can cover an action with the scope r.
func (r Reach) Within(o Reach) bool { return r.Exceeds(o) == 0 }

// Exceeds counts the dimensions of the order in which r is not within o:
// zero when r is within o.
func (r Reach) Exceeds(o Reach) int {
	rv, orders := r.dimensions()
	ov, _ := o.dimensions()
	n := 0
	for i := range rv {
		if !orders[i].Within(rv[i], ov[i]) {
			n++
		}
	}
	return n
}
