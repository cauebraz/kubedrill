// Package journal persists the learner's episodes under the XDG data
// directory (default ~/.local/share/kubedrill). It is the only package that
// knows the file layout; callers see episodes. Mastery is computed by the
// progress package from these episodes.
package journal

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/cauebraz/kubedrill/internal/atomicfile"
	"github.com/cauebraz/kubedrill/internal/grader"
)

// ErrNoCurrent is returned by Finish when no episode is active.
var ErrNoCurrent = errors.New("journal: no current episode")

// ErrNotCurrent is returned by Update when the episode is not the active one.
var ErrNotCurrent = errors.New("journal: episode is not the current one")

// ErrUnknownEpisode is returned by UpdateEnded when no finished episode has
// the given ID.
var ErrUnknownEpisode = errors.New("journal: no finished episode with that id")

const (
	episodesFile = "episodes.jsonl"
	currentFile  = "current.json"
	idFile       = "journal.id"
	lockFile     = ".lock"
	idBytes      = 8
	shellSubdir  = "shell"
	shellLogFile = "commands.jsonl"
	recordings   = "recordings"
)

// ShellLogPath returns <dir>/shell/commands.jsonl, the shell log the
// workstation bashrc appends one JSON line per command to in the journal
// directory dir; telemetry reads it.
func ShellLogPath(dir string) string {
	return filepath.Join(dir, shellSubdir, shellLogFile)
}

// RecordingsDir returns <dir>/recordings, where the workstation writes one
// <namespace>-<unix>.cast asciinema recording per shell session of an
// episode in the journal directory dir.
func RecordingsDir(dir string) string {
	return filepath.Join(dir, recordings)
}

// Outcome separates a run that ended on its own from one cut short: Solved is the
// task state, Abandoned is the learner surrendering (the solution was shown),
// Failed is leaving without solving, TimedOut is an exam time box running
// out.
type Outcome string

// Episode outcomes.
const (
	OutcomeActive    Outcome = "active"
	OutcomeSolved    Outcome = "solved"
	OutcomeAbandoned Outcome = "abandoned"
	OutcomeFailed    Outcome = "failed"
	OutcomeTimedOut  Outcome = "timed_out"
)

// UnmarshalJSON reads an outcome, mapping the retired "expired" value to
// OutcomeTimedOut.
func (o *Outcome) UnmarshalJSON(data []byte) error {
	var v string
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Errorf("decode outcome: %w", err)
	}
	if v == "expired" {
		v = string(OutcomeTimedOut)
	}
	*o = Outcome(v)
	return nil
}

// Mode is how an episode is run: practice allows hints and the solution,
// exam is time boxed and graded once.
type Mode string

// Episode modes. An episode recorded before modes existed reads as practice.
const (
	ModePractice Mode = "practice"
	ModeExam     Mode = "exam"
)

// Diagnosis is the one-line root cause the learner commits to before the
// last hint or the solution. Matched is the learner's own verdict, set in the
// debrief after comparing it with the reference root cause.
type Diagnosis struct {
	Text    string    `json:"text"`
	At      time.Time `json:"at"`
	Matched *bool     `json:"matched,omitempty"`
}

// Debrief is the learner's structured reflection after an episode ends.
type Debrief struct {
	At              time.Time `json:"at"`
	RuledOutEarlier string    `json:"ruled_out_earlier,omitempty"`
	NextTimeFirst   string    `json:"next_time_first,omitempty"`
	Principle       string    `json:"principle,omitempty"`
}

// Verdict classifies the first writes of an episode against the scenario's
// fix targets. Its string values are what episodes.jsonl stores.
type Verdict string

// Verdicts of a Telemetry summary.
const (
	// VerdictRootCause means a write hit one of the scenario's fix targets.
	VerdictRootCause Verdict = "root-cause"
	// VerdictSymptomOnly means writes succeeded but none hit a fix target.
	VerdictSymptomOnly Verdict = "symptom-only"
	// VerdictNoWrites means no write succeeded.
	VerdictNoWrites Verdict = "no-writes"
	// VerdictUnknown means the audit log was not available.
	VerdictUnknown Verdict = "unknown"
)

// Telemetry summarises what the learner did during an episode, from the API
// audit log and the workstation shell log. Source names which of the two
// were available; Verdict is one of the Verdict constants, VerdictUnknown
// without an audit log. FirstWriteTarget is group/resource/name with the
// core group written "core"; FirstWriteMetadataOnly says that write changed
// only labels or annotations (of the object or its pod template), as the
// request body in the audit log shows.
type Telemetry struct {
	Source                 string     `json:"source"`
	FirstKubectlAt         *time.Time `json:"first_kubectl_at,omitempty"`
	FirstWriteAt           *time.Time `json:"first_write_at,omitempty"`
	FirstWriteTarget       string     `json:"first_write_target,omitempty"`
	FirstWriteMetadataOnly bool       `json:"first_write_metadata_only,omitempty"`
	ReadsBeforeFirstWrite  int        `json:"reads_before_first_write"`
	Reads                  int        `json:"reads"`
	Writes                 int        `json:"writes"`
	CommandsTotal          int        `json:"commands_total"`
	KubectlCommands        int        `json:"kubectl_commands"`
	FailedCommands         int        `json:"failed_commands"`
	Verdict                Verdict    `json:"verdict"`
	Recordings             []string   `json:"recordings,omitempty"`
	Commands               []string   `json:"commands,omitempty"`
}

// Episode is one attempt at a scenario. Grades holds every grading run in
// order; the last one decides the outcome. Namespace is the episode's own
// namespace (empty for episodes recorded before per-episode namespaces);
// Deadline is set for exam episodes. Guided marks a practice episode run
// with the step-by-step guide of the browser workspace, which shows the fix.
type Episode struct {
	ID          string         `json:"id"`
	ScenarioID  string         `json:"scenario_id"`
	Seed        int64          `json:"seed"`
	Variant     string         `json:"variant"`
	StartedAt   time.Time      `json:"started_at"`
	EndedAt     *time.Time     `json:"ended_at,omitempty"`
	Outcome     Outcome        `json:"outcome"`
	HintsUsed   int            `json:"hints_used"`
	Grades      []grader.Grade `json:"grades,omitempty"`
	Namespace   string         `json:"namespace"`
	Mode        Mode           `json:"mode"`
	Guided      bool           `json:"guided,omitempty"`
	HintReveals []time.Time    `json:"hint_reveals,omitempty"`
	Diagnosis   *Diagnosis     `json:"diagnosis,omitempty"`
	Debrief     *Debrief       `json:"debrief,omitempty"`
	Telemetry   *Telemetry     `json:"telemetry,omitempty"`
	Deadline    *time.Time     `json:"deadline,omitempty"`
}

// Duration is the wall time of the episode, up to time.Now for an active
// one.
func (e Episode) Duration() time.Duration {
	return e.DurationAt(time.Now())
}

// DurationAt is the wall time of the episode, up to now for an active one.
func (e Episode) DurationAt(now time.Time) time.Duration {
	end := now
	if e.EndedAt != nil {
		end = *e.EndedAt
	}
	return end.Sub(e.StartedAt)
}

// Store reads and writes the journal. The zero value is not usable; use Open.
type Store struct {
	dir     string
	id      string
	now     func() time.Time
	lockCfg lockConfig

	mu       sync.Mutex
	problems []string
}

// Open uses dir, or the XDG data directory when dir is empty, creating it
// when missing. The directory is restricted to its owner (0700) when the
// current user owns it (atomicfile.MkdirPrivate). The journal id is read from <dir>/journal.id, which
// Open creates (16 random hex digits, mode 0600) when missing.
func Open(dir string) (*Store, error) {
	if dir == "" {
		d, err := defaultDir()
		if err != nil {
			return nil, fmt.Errorf("open journal: %w", err)
		}
		dir = d
	}
	if err := atomicfile.MkdirPrivate(dir); err != nil {
		return nil, fmt.Errorf("open journal %s: %w", dir, err)
	}
	s := &Store{dir: dir, now: time.Now, lockCfg: defaultLockConfig()}
	id, err := s.loadID()
	if err != nil {
		return nil, fmt.Errorf("open journal %s: %w", dir, err)
	}
	s.id = id
	return s, nil
}

// loadID reads journal.id, creating it under the journal lock when missing
// so two processes opening a new journal agree on one id.
func (s *Store) loadID() (string, error) {
	if id, err := s.readID(); err == nil || !errors.Is(err, os.ErrNotExist) {
		return id, err
	}
	unlock, err := s.lock()
	if err != nil {
		return "", err
	}
	defer unlock()
	if id, err := s.readID(); err == nil || !errors.Is(err, os.ErrNotExist) {
		return id, err
	}
	b := make([]byte, idBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("journal id: %w", err)
	}
	id := hex.EncodeToString(b)
	if err := atomicfile.Write(s.path(idFile), []byte(id+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("journal id: %w", err)
	}
	return id, nil
}

func (s *Store) readID() (string, error) {
	data, err := os.ReadFile(s.path(idFile))
	if err != nil {
		return "", fmt.Errorf("journal id: %w", err)
	}
	id := string(bytes.TrimSpace(data))
	if len(id) != 2*idBytes {
		return "", fmt.Errorf("journal id in %s: got %q, want %d hex digits", s.path(idFile), id, 2*idBytes)
	}
	if _, err := hex.DecodeString(id); err != nil {
		return "", fmt.Errorf("journal id in %s: %w", s.path(idFile), err)
	}
	return id, nil
}

// Dir returns the directory holding the journal files.
func (s *Store) Dir() string {
	return s.dir
}

// Problems returns the lines of episodes.jsonl the last Episodes call could
// not decode, each as "<path> line <n>: <error>". The
// entries were skipped, not lost: the file is left untouched.
func (s *Store) Problems() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.problems...)
}

func defaultDir() (string, error) {
	if base := os.Getenv("XDG_DATA_HOME"); base != "" {
		return filepath.Join(base, "kubedrill"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "kubedrill"), nil
}

// Current returns the active episode, or nil when none is active. It reads
// current.json without the lock: the file is only ever replaced by a
// rename, so it is always whole. An episode in current.json whose ID is
// already in episodes.jsonl (a finish interrupted between the append and
// the removal of current.json) is finished, not active, and reads as none.
func (s *Store) Current() (*Episode, error) {
	return s.current(false)
}

// current reads current.json. With heal set, which needs the lock, a
// current.json naming an already finished episode is removed.
func (s *Store) current(heal bool) (*Episode, error) {
	data, err := os.ReadFile(s.path(currentFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read current episode: %w", err)
	}
	var e Episode
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, fmt.Errorf("decode %s: %w", s.path(currentFile), err)
	}
	finished, err := s.isFinished(e.ID)
	if err != nil {
		return nil, fmt.Errorf("read current episode: %w", err)
	}
	if finished {
		if heal {
			if err := os.Remove(s.path(currentFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("remove stale current episode %s: %w", e.ID, err)
			}
		}
		return nil, nil
	}
	normalize(&e)
	return &e, nil
}

// isFinished reports whether episodes.jsonl has a line with the given ID.
func (s *Store) isFinished(id string) (bool, error) {
	if id == "" {
		return false, nil
	}
	path := s.path(episodesFile)
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		if lineID(sc.Bytes()) == id {
			return true, nil
		}
	}
	if err := sc.Err(); err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	return false, nil
}

func lineID(line []byte) string {
	var probe struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(bytes.TrimSpace(line), &probe) != nil {
		return ""
	}
	return probe.ID
}

// Start records a new active episode and makes it current. An existing
// active episode is first appended to episodes.jsonl as Failed, then the new
// one is written; when that write fails the current pointer is removed, so
// the old episode is never both ended and active. An empty Mode is recorded
// as practice.
func (s *Store) Start(e Episode) error {
	unlock, err := s.lock()
	if err != nil {
		return fmt.Errorf("start episode: %w", err)
	}
	defer unlock()
	cur, err := s.current(true)
	if err != nil {
		return fmt.Errorf("start episode: %w", err)
	}
	now := s.now()
	e.Outcome = OutcomeActive
	e.EndedAt = nil
	normalize(&e)
	if e.StartedAt.IsZero() {
		e.StartedAt = now
	}
	if e.ID == "" {
		id, err := newID(e.ScenarioID, now)
		if err != nil {
			return fmt.Errorf("start episode: %w", err)
		}
		e.ID = id
	}
	if cur != nil {
		ended := now
		cur.Outcome = OutcomeFailed
		cur.EndedAt = &ended
		if err := s.appendEpisode(*cur); err != nil {
			return fmt.Errorf("start episode %s: fail %s: %w", e.ID, cur.ID, err)
		}
	}
	if err := s.writeCurrent(e); err != nil {
		if cur != nil {
			if rmErr := os.Remove(s.path(currentFile)); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
				return fmt.Errorf("start episode %s: %w", e.ID, errors.Join(err, fmt.Errorf("remove current: %w", rmErr)))
			}
		}
		return fmt.Errorf("start episode %s: %w", e.ID, err)
	}
	return nil
}

// Update rewrites the current episode. It fails with ErrNotCurrent when e
// is not the current one.
func (s *Store) Update(e Episode) error {
	_, err := s.modify(e.ID, func(cur *Episode) error {
		*cur = e
		return nil
	})
	if errors.Is(err, ErrNoCurrent) {
		err = ErrNotCurrent
	}
	if err != nil {
		return fmt.Errorf("update episode %s: %w", e.ID, err)
	}
	return nil
}

// Modify reads the current episode under the journal lock, applies fn to it
// and writes the result, so changes made meanwhile by another process (a
// hint from the page, a check in the shell) are never overwritten. id
// must name the current episode: ErrNoCurrent is returned when none is
// active and ErrNotCurrent when another one is. When fn returns an error
// nothing is written and that error is returned as is. When fn sets an
// outcome other than active, the episode is finished in the same step:
// EndedAt is set to now when fn left it nil, the episode is appended to
// episodes.jsonl and the current pointer is removed. fn must not change the
// ID. The returned episode is what was written.
func (s *Store) Modify(id string, fn func(*Episode) error) (*Episode, error) {
	var fnErr error
	e, err := s.modify(id, func(cur *Episode) error {
		fnErr = fn(cur)
		return fnErr
	})
	if err != nil && fnErr == nil {
		return nil, fmt.Errorf("modify episode %s: %w", id, err)
	}
	return e, err
}

func (s *Store) modify(id string, fn func(*Episode) error) (*Episode, error) {
	unlock, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	cur, err := s.current(true)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return nil, ErrNoCurrent
	}
	if cur.ID != id {
		return nil, fmt.Errorf("%w (current is %s)", ErrNotCurrent, cur.ID)
	}
	if err := fn(cur); err != nil {
		return nil, err
	}
	if cur.ID != id {
		return nil, fmt.Errorf("the change renamed it to %s", cur.ID)
	}
	normalize(cur)
	if cur.Outcome == "" || cur.Outcome == OutcomeActive {
		cur.Outcome = OutcomeActive
		cur.EndedAt = nil
		if err := s.writeCurrent(*cur); err != nil {
			return nil, err
		}
		toUTC(cur)
		return cur, nil
	}
	if err := s.finishLocked(cur); err != nil {
		return nil, err
	}
	return cur, nil
}

// Finish sets the outcome and end time of the current episode and clears
// the current pointer.
func (s *Store) Finish(outcome Outcome) (*Episode, error) {
	unlock, err := s.lock()
	if err != nil {
		return nil, fmt.Errorf("finish episode: %w", err)
	}
	defer unlock()
	cur, err := s.current(true)
	if err != nil {
		return nil, fmt.Errorf("finish episode: %w", err)
	}
	if cur == nil {
		return nil, fmt.Errorf("finish episode: %w", ErrNoCurrent)
	}
	cur.Outcome = outcome
	cur.EndedAt = nil
	if err := s.finishLocked(cur); err != nil {
		return nil, err
	}
	return cur, nil
}

// finishLocked appends e, ended now when EndedAt is nil, to episodes.jsonl
// and removes current.json. The caller holds the lock.
func (s *Store) finishLocked(e *Episode) error {
	if e.EndedAt == nil {
		ended := s.now()
		e.EndedAt = &ended
	}
	if err := s.appendEpisode(*e); err != nil {
		return fmt.Errorf("finish episode %s: %w", e.ID, err)
	}
	toUTC(e)
	if err := os.Remove(s.path(currentFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("finish episode %s: remove current: %w", e.ID, err)
	}
	return nil
}

// Episodes returns every recorded episode, oldest first, the active one
// last. Both files are read under the journal lock, so an episode that
// another process is finishing is seen once. An empty scenarioID returns
// all scenarios.
func (s *Store) Episodes(scenarioID string) ([]Episode, error) {
	unlock, err := s.lock()
	if err != nil {
		return nil, fmt.Errorf("read episodes: %w", err)
	}
	all, err := s.readEpisodes()
	if err == nil {
		var cur *Episode
		if cur, err = s.current(true); err == nil && cur != nil {
			all = append(all, *cur)
		}
	}
	unlock()
	if err != nil {
		return nil, err
	}
	if scenarioID == "" {
		return all, nil
	}
	out := make([]Episode, 0, len(all))
	for _, e := range all {
		if e.ScenarioID == scenarioID {
			out = append(out, e)
		}
	}
	return out, nil
}

// UpdateEnded rewrites a finished episode in episodes.jsonl, matched by ID,
// for example to attach a debrief after a solve. It replaces the whole
// record with e; ModifyEnded changes it in place under the lock instead.
func (s *Store) UpdateEnded(e Episode) error {
	if e.Outcome == OutcomeActive || e.EndedAt == nil {
		return fmt.Errorf("update ended episode %s: outcome %q is not finished", e.ID, e.Outcome)
	}
	if _, err := s.ModifyEnded(e.ID, func(cur *Episode) error {
		*cur = e
		return nil
	}); err != nil {
		return fmt.Errorf("update ended episode %s: %w", e.ID, err)
	}
	return nil
}

// ModifyEnded reads the finished episode id from episodes.jsonl under the
// journal lock, applies fn to it and writes the result back, so a
// read-modify-write of an ended episode (a debrief, the diagnosis verdict)
// never loses a change made meanwhile by another process. The file is
// replaced atomically; lines that do not decode are kept as they are. When
// the ID appears more than once, the first line is the record (as Episodes
// reads it) and is the one rewritten. ErrUnknownEpisode is returned when no
// line has the ID. When fn returns an error nothing is written and that
// error is returned as is. fn must not change the ID and must leave the
// episode finished (an outcome other than active and EndedAt set). The
// returned episode is what was written.
func (s *Store) ModifyEnded(id string, fn func(*Episode) error) (*Episode, error) {
	unlock, err := s.lock()
	if err != nil {
		return nil, fmt.Errorf("modify ended episode %s: %w", id, err)
	}
	defer unlock()
	path := s.path(episodesFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("modify ended episode %s: %w", id, ErrUnknownEpisode)
	}
	if err != nil {
		return nil, fmt.Errorf("modify ended episode %s: read %s: %w", id, path, err)
	}
	lines := bytes.Split(data, []byte{'\n'})
	match := -1
	for i, line := range lines {
		if lineID(line) == id {
			match = i
			break
		}
	}
	if match < 0 {
		return nil, fmt.Errorf("modify ended episode %s: %w", id, ErrUnknownEpisode)
	}
	var e Episode
	if err := json.Unmarshal(bytes.TrimSpace(lines[match]), &e); err != nil {
		return nil, fmt.Errorf("modify ended episode %s: decode %s line %d: %w", id, path, match+1, err)
	}
	normalize(&e)
	if err := fn(&e); err != nil {
		return nil, err
	}
	if e.ID != id {
		return nil, fmt.Errorf("modify ended episode %s: the change renamed it to %s", id, e.ID)
	}
	if e.Outcome == "" || e.Outcome == OutcomeActive || e.EndedAt == nil {
		return nil, fmt.Errorf("modify ended episode %s: outcome %q is not finished", id, e.Outcome)
	}
	normalize(&e)
	toUTC(&e)
	enc, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("modify ended episode %s: encode: %w", id, err)
	}
	lines[match] = enc
	out := bytes.Join(lines, []byte{'\n'})
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	if err := atomicfile.Write(path, out, 0o600); err != nil {
		return nil, fmt.Errorf("modify ended episode %s: %w", id, err)
	}
	return &e, nil
}

// JournalID identifies this journal: the 16 hex digits persisted in
// <dir>/journal.id, so the host and the workstation (which mounts the same
// directory at /data) agree. Namespaces carry it so that cleanup only
// touches namespaces of this journal.
func (s *Store) JournalID() string {
	return s.id
}

func normalize(e *Episode) {
	if e.Mode == "" {
		e.Mode = ModePractice
	}
}

// toUTC converts every timestamp of e to UTC, so the files read the same
// whatever the time zone of the process that wrote them.
func toUTC(e *Episode) {
	utc := func(t *time.Time) {
		if t != nil {
			*t = t.UTC()
		}
	}
	utc(&e.StartedAt)
	utc(e.EndedAt)
	utc(e.Deadline)
	e.HintReveals = slices.Clone(e.HintReveals)
	for i := range e.HintReveals {
		utc(&e.HintReveals[i])
	}
	e.Grades = slices.Clone(e.Grades)
	for i := range e.Grades {
		utc(&e.Grades[i].At)
	}
	if e.Diagnosis != nil {
		d := *e.Diagnosis
		utc(&d.At)
		e.Diagnosis = &d
	}
	if e.Debrief != nil {
		d := *e.Debrief
		utc(&d.At)
		e.Debrief = &d
	}
	if e.Telemetry != nil {
		t := *e.Telemetry
		if t.FirstKubectlAt != nil {
			at := t.FirstKubectlAt.UTC()
			t.FirstKubectlAt = &at
		}
		if t.FirstWriteAt != nil {
			at := t.FirstWriteAt.UTC()
			t.FirstWriteAt = &at
		}
		e.Telemetry = &t
	}
}

func (s *Store) path(name string) string {
	return filepath.Join(s.dir, name)
}

func (s *Store) writeCurrent(e Episode) error {
	toUTC(&e)
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encode episode: %w", err)
	}
	return atomicfile.Write(s.path(currentFile), data, 0o600)
}

func (s *Store) appendEpisode(e Episode) error {
	toUTC(&e)
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encode episode: %w", err)
	}
	path := s.path(episodesFile)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	if missing, err := lacksTrailingNewline(f); err != nil {
		f.Close()
		return fmt.Errorf("inspect %s: %w", path, err)
	} else if missing {
		line = append([]byte{'\n'}, line...)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return fmt.Errorf("append to %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

func lacksTrailingNewline(f *os.File) (bool, error) {
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	if info.Size() == 0 {
		return false, nil
	}
	var last [1]byte
	if _, err := f.ReadAt(last[:], info.Size()-1); err != nil {
		return false, err
	}
	return last[0] != '\n', nil
}

// readEpisodes decodes episodes.jsonl. An ID that appears on more than one
// line (a finish interrupted before current.json was removed, then the same
// episode appended again as failed) is read from its first line only.
func (s *Store) readEpisodes() ([]Episode, error) {
	path := s.path(episodesFile)
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	defer f.Close()
	var out []Episode
	var problems []string
	seen := make(map[string]bool)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e Episode
		if err := json.Unmarshal(line, &e); err != nil {
			problems = append(problems, fmt.Sprintf("%s line %d: %v", path, n, err))
			continue
		}
		if e.ID != "" {
			if seen[e.ID] {
				continue
			}
			seen[e.ID] = true
		}
		normalize(&e)
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	s.mu.Lock()
	s.problems = problems
	s.mu.Unlock()
	return out, nil
}

func newID(scenarioID string, now time.Time) (string, error) {
	b := make([]byte, 2)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("random id: %w", err)
	}
	return fmt.Sprintf("%s-%d-%s", scenarioID, now.Unix(), hex.EncodeToString(b)), nil
}
