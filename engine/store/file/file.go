// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package file is a [store.Store] in a directory, for a self-hosted Limen
// that runs as one process (spec 004).
//
// The layout:
//
//	<dir>/LOCK                                   held by the owning process
//	<dir>/profiles/<org>.json                    every saved profile of an organization
//	<dir>/subjects/<org>/<subject>/meta.json     the org and subject, for a reader
//	<dir>/subjects/<org>/<subject>/cases.ndjson  the subject's case log
//	<dir>/subjects/<org>/<subject>/snapshots/<version>.snap
//	<dir>/reports/<org>/<week>.json              a kept week of a report, named by its first day
//
// Names are encoded so that any string is a safe path element on a case
// insensitive file system: lowercase letters, digits and '-' stay, every
// other byte becomes '%' and two lowercase hex digits, the empty string is
// "_e", and a name whose encoding passes 200 bytes is "_h" and the hex
// SHA-256 of the string. '_' is always escaped, so the two reserved forms
// never collide with an encoded name.
//
// The case log is append-only NDJSON: every Put appends the whole case as
// one line and syncs the file, and opening a subject replays the log, the
// last line for a key winning. A crash in the middle of an append leaves a
// final line that is incomplete or does not parse; replay ignores it and
// truncates the file back to the last complete line. A line that does not
// parse anywhere else is damage, and the subject fails to load rather than
// losing cases silently. When a log holds more than twice as many lines as
// live cases, plus 64, it is rewritten with one line per case, atomically.
//
// The counter of [store.Store.NextT] lives in memory and resumes after a
// reopen at one past the largest ordinal the log holds, so an ordinal a
// stored case holds is never handed out again. Profiles, snapshots and
// kept weeks are written whole, to a temporary file that is synced and
// renamed. Forget removes a subject's directory and leaves the kept weeks,
// which hold no person's record.
//
// One process owns a directory. Open takes an exclusive lock on LOCK and
// fails with [ErrLocked] while another process, or another open store in
// this process, holds it. On Unix the lock is an flock, which the kernel
// releases when the owner exits, so a crash leaves no stale lock. Elsewhere
// LOCK is created exclusively and removed by Close; after a crash an
// operator removes it once no process uses the directory.
package file

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"latere.ai/x/pkg/atomicfile"

	"latere.ai/x/limen"
	"latere.ai/x/limen/engine/store"
)

// ErrLocked is returned by Open when another owner holds the directory.
var ErrLocked = errors.New("file store: the directory is locked by another store")

// ErrClosed is returned by every method after Close.
var ErrClosed = errors.New("file store: closed")

// compactSlack is how many lines beyond twice the live cases a log may hold
// before it is rewritten.
const compactSlack = 64

// Options tune a store. The zero value is durable.
type Options struct {
	// NoSync skips the fsync after each append and each whole-file write.
	// Writes stay atomic per line and per file, but a crash may lose the most
	// recent ones. It suits tests and a replay that keeps nothing.
	NoSync bool
}

// Store is a [store.Store] in a directory.
type Store struct {
	dir  string
	sync bool
	lock *os.File

	mu       sync.Mutex
	closed   bool
	subjects map[subjectKey]*subject
	profiles map[string]map[limen.Scope]limen.Profile
}

var _ store.Store = (*Store)(nil)

type subjectKey struct{ org, subject string }

// subject is one subject's cases as the log holds them, in memory.
type subject struct {
	mu     sync.Mutex
	dir    string
	loaded bool
	keys   []string          // action ids in the order first stored
	latest map[string][]byte // action id -> the case's JSON
	lines  int               // lines in the log
	next   int64             // the next ordinal NextT hands out
	exists bool              // the directory exists on disk
}

// Open opens the store in dir, creating the directory when it is missing,
// and takes its lock. It is [OpenWith] with durable options.
func Open(dir string) (*Store, error) { return OpenWith(dir, Options{}) }

// OpenWith opens the store in dir with options.
func OpenWith(dir string, o Options) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("file store: create %s: %w", dir, err)
	}
	l, err := lock(filepath.Join(dir, "LOCK"))
	if err != nil {
		return nil, err
	}
	return &Store{
		dir:      dir,
		sync:     !o.NoSync,
		lock:     l,
		subjects: map[subjectKey]*subject{},
		profiles: map[string]map[limen.Scope]limen.Profile{},
	}, nil
}

// Close releases the lock. Every write was synced when it returned, so
// there is nothing to flush. Close is idempotent.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return unlock(s.lock)
}

// encode turns a string into a safe path element (see the package doc).
func encode(name string) string {
	if name == "" {
		return "_e"
	}
	var b strings.Builder
	for i := range len(name) {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02x", c)
	}
	if b.Len() > 200 {
		sum := sha256.Sum256([]byte(name))
		return "_h" + hex.EncodeToString(sum[:])
	}
	return b.String()
}

// subjectFor returns the subject's state, registering it on first use.
func (s *Store) subjectFor(ctx context.Context, org, name string) (*subject, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	k := subjectKey{org, name}
	sub, ok := s.subjects[k]
	if !ok {
		sub = &subject{dir: filepath.Join(s.dir, "subjects", encode(org), encode(name))}
		s.subjects[k] = sub
	}
	return sub, nil
}

// load replays the subject's log once. The caller holds sub.mu.
func (sub *subject) load() error {
	if sub.loaded {
		return nil
	}
	sub.latest = map[string][]byte{}
	path := filepath.Join(sub.dir, "cases.ndjson")
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		_, statErr := os.Stat(sub.dir)
		sub.exists = statErr == nil
		sub.loaded = true
		return nil
	case err != nil:
		return fmt.Errorf("file store: read %s: %w", path, err)
	}
	sub.exists = true
	good := 0 // the length of the log up to its last complete, parsable line
	lines := bytes.SplitAfter(data, []byte("\n"))
	for i, line := range lines {
		if len(line) == 0 {
			continue
		}
		last := i == len(lines)-1 || (i == len(lines)-2 && len(lines[len(lines)-1]) == 0)
		var c store.Case
		if line[len(line)-1] != '\n' || json.Unmarshal(line, &c) != nil {
			if last {
				break // a torn final append: drop it below
			}
			return fmt.Errorf("file store: %s line %d does not parse; the log is damaged", path, i+1)
		}
		id := c.Action.ActionID
		if _, seen := sub.latest[id]; !seen {
			sub.keys = append(sub.keys, id)
		}
		sub.latest[id] = slices.Clone(bytes.TrimSuffix(line, []byte("\n")))
		sub.next = max(sub.next, c.T+1)
		sub.lines++
		good += len(line)
	}
	if good < len(data) {
		if err := os.Truncate(path, int64(good)); err != nil {
			return fmt.Errorf("file store: truncate the torn end of %s: %w", path, err)
		}
	}
	sub.loaded = true
	return nil
}

// ensureDir creates the subject's directory and its meta.json once.
func (s *Store) ensureDir(sub *subject, org, name string) error {
	if sub.exists {
		return nil
	}
	if err := os.MkdirAll(sub.dir, 0o700); err != nil {
		return fmt.Errorf("file store: create %s: %w", sub.dir, err)
	}
	meta, err := json.Marshal(map[string]string{"org": org, "subject": name})
	if err != nil {
		return err
	}
	if err := s.write(filepath.Join(sub.dir, "meta.json"), meta); err != nil {
		return err
	}
	sub.exists = true
	return nil
}

func (s *Store) write(path string, data []byte) error {
	if s.sync {
		return atomicfile.WriteSync(path, data, 0o600)
	}
	return atomicfile.Write(path, data, 0o600)
}

// Case implements [store.Store].
func (s *Store) Case(ctx context.Context, k limen.Key) (store.Case, bool, error) {
	sub, err := s.subjectFor(ctx, k.Org, k.Subject)
	if err != nil {
		return store.Case{}, false, err
	}
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if err := sub.load(); err != nil {
		return store.Case{}, false, err
	}
	b, ok := sub.latest[k.ActionID]
	if !ok {
		return store.Case{}, false, nil
	}
	var c store.Case
	return c, true, json.Unmarshal(b, &c)
}

// Put implements [store.Store]: one appended, synced line.
func (s *Store) Put(ctx context.Context, c store.Case) error {
	k := c.Action.Key
	sub, err := s.subjectFor(ctx, k.Org, k.Subject)
	if err != nil {
		return err
	}
	c.Action.Input = nil
	b, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("file store: encode case %s: %w", k.ActionID, err)
	}
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if err := sub.load(); err != nil {
		return err
	}
	if err := s.ensureDir(sub, k.Org, k.Subject); err != nil {
		return err
	}
	if err := s.append(filepath.Join(sub.dir, "cases.ndjson"), append(slices.Clone(b), '\n')); err != nil {
		return err
	}
	if _, seen := sub.latest[k.ActionID]; !seen {
		sub.keys = append(sub.keys, k.ActionID)
	}
	sub.latest[k.ActionID] = b
	sub.next = max(sub.next, c.T+1)
	sub.lines++
	if sub.lines > 2*len(sub.latest)+compactSlack {
		return s.compact(sub)
	}
	return nil
}

func (s *Store) append(path string, line []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("file store: open %s: %w", path, err)
	}
	_, werr := f.Write(line)
	var serr error
	if werr == nil && s.sync {
		serr = f.Sync()
	}
	cerr := f.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		return fmt.Errorf("file store: append to %s: %w", path, err)
	}
	return nil
}

// compact rewrites the subject's log with one line per case, in the order
// the cases were first stored. The caller holds sub.mu.
func (s *Store) compact(sub *subject) error {
	var buf bytes.Buffer
	for _, id := range sub.keys {
		buf.Write(sub.latest[id])
		buf.WriteByte('\n')
	}
	if err := s.write(filepath.Join(sub.dir, "cases.ndjson"), buf.Bytes()); err != nil {
		return fmt.Errorf("file store: compact %s: %w", sub.dir, err)
	}
	sub.lines = len(sub.keys)
	return nil
}

// NextT implements [store.Store].
func (s *Store) NextT(ctx context.Context, org, name string) (int64, error) {
	sub, err := s.subjectFor(ctx, org, name)
	if err != nil {
		return 0, err
	}
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if err := sub.load(); err != nil {
		return 0, err
	}
	t := sub.next
	sub.next++
	return t, nil
}

// Cases implements [store.Store].
func (s *Store) Cases(ctx context.Context, org, name string) ([]store.Case, error) {
	sub, err := s.subjectFor(ctx, org, name)
	if err != nil {
		return nil, err
	}
	sub.mu.Lock()
	if err := sub.load(); err != nil {
		sub.mu.Unlock()
		return nil, err
	}
	blobs := make([][]byte, len(sub.keys))
	for i, id := range sub.keys {
		blobs[i] = sub.latest[id]
	}
	sub.mu.Unlock()
	out := make([]store.Case, len(blobs))
	for i, b := range blobs {
		if err := json.Unmarshal(b, &out[i]); err != nil {
			return nil, err
		}
	}
	slices.SortStableFunc(out, func(a, b store.Case) int { return int(a.T - b.T) })
	return out, nil
}

// orgProfiles returns the organization's profiles, loading its file once.
// The caller holds s.mu.
func (s *Store) orgProfiles(org string) (map[limen.Scope]limen.Profile, error) {
	if ps, ok := s.profiles[org]; ok {
		return ps, nil
	}
	ps := map[limen.Scope]limen.Profile{}
	path := filepath.Join(s.dir, "profiles", encode(org)+".json")
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("file store: read %s: %w", path, err)
	default:
		var list []limen.Profile
		if err := json.Unmarshal(data, &list); err != nil {
			return nil, fmt.Errorf("file store: %s does not parse: %w", path, err)
		}
		for _, p := range list {
			ps[p.Scope] = p
		}
	}
	s.profiles[org] = ps
	return ps, nil
}

// Profile implements [store.Store].
func (s *Store) Profile(ctx context.Context, sc limen.Scope) (limen.Profile, bool, error) {
	if err := ctx.Err(); err != nil {
		return limen.Profile{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return limen.Profile{}, false, ErrClosed
	}
	ps, err := s.orgProfiles(sc.Org)
	if err != nil {
		return limen.Profile{}, false, err
	}
	p, ok := ps[sc]
	return p, ok, nil
}

// PutProfile implements [store.Store]: the organization's file is written
// whole.
func (s *Store) PutProfile(ctx context.Context, p limen.Profile) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	ps, err := s.orgProfiles(p.Org)
	if err != nil {
		return err
	}
	next := make(map[limen.Scope]limen.Profile, len(ps)+1)
	maps.Copy(next, ps)
	next[p.Scope] = p
	return s.writeProfiles(p.Org, next)
}

// writeProfiles writes the organization's file whole, sorted by scope, so
// it reads the same however it was built, and keeps ps as its profiles.
// The caller holds s.mu.
func (s *Store) writeProfiles(org string, ps map[limen.Scope]limen.Profile) error {
	list := make([]limen.Profile, 0, len(ps))
	for _, v := range ps {
		list = append(list, v)
	}
	slices.SortFunc(list, func(a, b limen.Profile) int {
		return strings.Compare(a.Kind+"\x00"+a.Agent+"\x00"+a.Subject, b.Kind+"\x00"+b.Agent+"\x00"+b.Subject)
	})
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Join(s.dir, "profiles")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("file store: create %s: %w", dir, err)
	}
	if err := s.write(filepath.Join(dir, encode(org)+".json"), data); err != nil {
		return fmt.Errorf("file store: write profiles of %s: %w", org, err)
	}
	s.profiles[org] = ps
	return nil
}

// DeleteProfile implements [store.Store]: the organization's file is
// written whole without the scope, as a save writes it.
func (s *Store) DeleteProfile(ctx context.Context, sc limen.Scope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	ps, err := s.orgProfiles(sc.Org)
	if err != nil {
		return err
	}
	if _, ok := ps[sc]; !ok {
		return nil
	}
	next := maps.Clone(ps)
	delete(next, sc)
	return s.writeProfiles(sc.Org, next)
}

// Subjects implements [store.Store]: the subjects whose directory under
// the organization holds a meta.json, read from it, since a directory's
// name may be a hash.
func (s *Store) Subjects(ctx context.Context, org string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}
	dir := filepath.Join(s.dir, "subjects", encode(org))
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return []string{}, nil
	case err != nil:
		return nil, fmt.Errorf("file store: list %s: %w", dir, err)
	}
	out := []string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name(), "meta.json")
		data, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			// A directory that a crash left before its meta.json holds no
			// case.
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("file store: read %s: %w", path, err)
		}
		var meta struct{ Org, Subject string }
		if err := json.Unmarshal(data, &meta); err != nil || meta.Org != org {
			return nil, fmt.Errorf("file store: %s does not name a subject of %s", path, org)
		}
		out = append(out, meta.Subject)
	}
	slices.Sort(out)
	return out, nil
}

func (s *Store) weekPath(org string, from time.Time) string {
	return filepath.Join(s.dir, "reports", encode(org), from.UTC().Format("2006-01-02")+".json")
}

// WeekReport implements [store.Store].
func (s *Store) WeekReport(ctx context.Context, org string, from time.Time) (limen.WeekReport, bool, error) {
	if err := ctx.Err(); err != nil {
		return limen.WeekReport{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return limen.WeekReport{}, false, ErrClosed
	}
	path := s.weekPath(org, from)
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return limen.WeekReport{}, false, nil
	case err != nil:
		return limen.WeekReport{}, false, fmt.Errorf("file store: read %s: %w", path, err)
	}
	var w limen.WeekReport
	if err := json.Unmarshal(data, &w); err != nil {
		return limen.WeekReport{}, false, fmt.Errorf("file store: %s does not parse: %w", path, err)
	}
	return w, true, nil
}

// PutWeekReport implements [store.Store]: the week is written whole.
func (s *Store) PutWeekReport(ctx context.Context, org string, w limen.WeekReport) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	path := s.weekPath(org, w.From)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("file store: create %s: %w", filepath.Dir(path), err)
	}
	if err := s.write(path, data); err != nil {
		return fmt.Errorf("file store: write the week %s: %w", path, err)
	}
	return nil
}

func snapshotPath(sub *subject, version string) string {
	return filepath.Join(sub.dir, "snapshots", encode(version)+".snap")
}

// Snapshot implements [store.Store].
func (s *Store) Snapshot(ctx context.Context, org, name, version string) ([]byte, bool, error) {
	sub, err := s.subjectFor(ctx, org, name)
	if err != nil {
		return nil, false, err
	}
	sub.mu.Lock()
	defer sub.mu.Unlock()
	b, err := os.ReadFile(snapshotPath(sub, version))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, false, nil
	case err != nil:
		return nil, false, fmt.Errorf("file store: read snapshot: %w", err)
	}
	return b, true, nil
}

// PutSnapshot implements [store.Store].
func (s *Store) PutSnapshot(ctx context.Context, org, name, version string, state []byte) error {
	sub, err := s.subjectFor(ctx, org, name)
	if err != nil {
		return err
	}
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if err := sub.load(); err != nil {
		return err
	}
	if err := s.ensureDir(sub, org, name); err != nil {
		return err
	}
	path := snapshotPath(sub, version)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("file store: create %s: %w", filepath.Dir(path), err)
	}
	if err := s.write(path, state); err != nil {
		return fmt.Errorf("file store: write snapshot %s: %w", path, err)
	}
	return nil
}

// Forget implements [store.Store]: the subject's directory goes, and its
// state in memory with it.
func (s *Store) Forget(ctx context.Context, org, name string) error {
	sub, err := s.subjectFor(ctx, org, name)
	if err != nil {
		return err
	}
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if err := os.RemoveAll(sub.dir); err != nil {
		return fmt.Errorf("file store: remove %s: %w", sub.dir, err)
	}
	// Reset field by field: sub.mu is held and must survive.
	sub.loaded, sub.exists = false, false
	sub.keys, sub.latest = nil, nil
	sub.lines, sub.next = 0, 0
	return nil
}
