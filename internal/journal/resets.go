package journal

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/cauebraz/kubedrill/internal/atomicfile"
)

const resetsFile = "resets.json"

// ErrActive is returned by ResetProgress when the active episode is in the
// scope of the reset.
var ErrActive = errors.New("journal: an episode in that scope is active")

// ErrNoReset is returned by RestoreProgress when no reset is recorded for
// the scope.
var ErrNoReset = errors.New("journal: no reset recorded")

// Reset is one progress reset: the finished episodes of its scope that
// ended at or before At no longer count toward progress. Stages holds what
// the caller saved with it (the stage progress of the browser workspace it
// cleared), returned untouched by RestoreProgress.
type Reset struct {
	At     time.Time       `json:"at"`
	Stages json.RawMessage `json:"stages,omitempty"`
}

// Resets are the reset markers of a journal, as resets.json stores them:
// All applies to every scenario, Scenarios to one scenario each.
type Resets struct {
	All       *Reset           `json:"all,omitempty"`
	Scenarios map[string]Reset `json:"scenarios,omitempty"`
}

// ResetResult is what ResetProgress wrote or RestoreProgress removed: the
// scope (a scenario id, empty for every scenario), the finished episodes
// that stopped counting or count again, and the marker itself.
type ResetResult struct {
	ScenarioID string
	Runs       int
	Reset      Reset
}

// Cutoff is the time at or before which a finished episode of scenarioID
// no longer counts: the later of the marker of everything and the marker
// of that scenario. It is zero when neither exists.
func (r Resets) Cutoff(scenarioID string) time.Time {
	var at time.Time
	if r.All != nil {
		at = r.All.At
	}
	if s, ok := r.Scenarios[scenarioID]; ok && s.At.After(at) {
		at = s.At
	}
	return at
}

// Counts reports whether e counts toward progress: it is active, or it
// ended after the cutoff of its scenario.
func (r Resets) Counts(e Episode) bool {
	if e.EndedAt == nil {
		return true
	}
	cut := r.Cutoff(e.ScenarioID)
	return cut.IsZero() || e.EndedAt.After(cut)
}

// Filter returns the episodes of eps that count, in their order.
func (r Resets) Filter(eps []Episode) []Episode {
	out := make([]Episode, 0, len(eps))
	for _, e := range eps {
		if r.Counts(e) {
			out = append(out, e)
		}
	}
	return out
}

// Get returns the marker of scenarioID, or of everything when scenarioID
// is empty.
func (r Resets) Get(scenarioID string) (Reset, bool) {
	if scenarioID == "" {
		if r.All == nil {
			return Reset{}, false
		}
		return *r.All, true
	}
	s, ok := r.Scenarios[scenarioID]
	return s, ok
}

// with returns a copy of r with the marker of scenarioID (everything when
// empty) set to m, or removed when m is nil.
func (r Resets) with(scenarioID string, m *Reset) Resets {
	out := Resets{Scenarios: make(map[string]Reset, len(r.Scenarios))}
	for k, v := range r.Scenarios {
		out.Scenarios[k] = v
	}
	if r.All != nil {
		a := *r.All
		out.All = &a
	}
	switch {
	case scenarioID == "":
		out.All = m
	case m == nil:
		delete(out.Scenarios, scenarioID)
	default:
		out.Scenarios[scenarioID] = *m
	}
	if len(out.Scenarios) == 0 {
		out.Scenarios = nil
	}
	return out
}

// moved counts the finished episodes in scope (scenarioID, every scenario
// when empty) that count under one of before and after and not the other.
func moved(eps []Episode, scenarioID string, before, after Resets) int {
	n := 0
	for _, e := range eps {
		if e.EndedAt == nil || (scenarioID != "" && e.ScenarioID != scenarioID) {
			continue
		}
		if before.Counts(e) != after.Counts(e) {
			n++
		}
	}
	return n
}

// Resets returns the reset markers. It reads resets.json without the lock:
// the file is only ever replaced by a rename, so it is always whole. A
// missing file reads as no marker.
func (s *Store) Resets() (Resets, error) {
	path := s.path(resetsFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Resets{}, nil
	}
	if err != nil {
		return Resets{}, fmt.Errorf("read resets: %w", err)
	}
	var r Resets
	if err := json.Unmarshal(data, &r); err != nil {
		return Resets{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return r, nil
}

// ResetProgress records a reset of scenarioID, or of every scenario when it
// is empty, at the current time, under the journal lock. It fails with
// ErrActive when the active episode is of that scenario (any active
// episode for every scenario). stages, when not nil, receives the Stages of
// the marker it replaces (nil when there is none) and returns the Stages
// to store; its error stops the reset before anything is written. The
// result counts the finished episodes that stopped counting.
func (s *Store) ResetProgress(scenarioID string, stages func(prev json.RawMessage) (json.RawMessage, error)) (ResetResult, error) {
	scope := scopeName(scenarioID)
	unlock, err := s.lock()
	if err != nil {
		return ResetResult{}, fmt.Errorf("reset progress of %s: %w", scope, err)
	}
	defer unlock()
	cur, err := s.current(true)
	if err != nil {
		return ResetResult{}, fmt.Errorf("reset progress of %s: %w", scope, err)
	}
	if cur != nil && (scenarioID == "" || cur.ScenarioID == scenarioID) {
		return ResetResult{}, fmt.Errorf("reset progress of %s: %w: %s (episode %s)", scope, ErrActive, cur.ScenarioID, cur.ID)
	}
	before, err := s.Resets()
	if err != nil {
		return ResetResult{}, fmt.Errorf("reset progress of %s: %w", scope, err)
	}
	m := Reset{At: s.now().UTC()}
	if stages != nil {
		prev, _ := before.Get(scenarioID)
		if m.Stages, err = stages(prev.Stages); err != nil {
			return ResetResult{}, fmt.Errorf("reset progress of %s: %w", scope, err)
		}
	}
	after := before.with(scenarioID, &m)
	eps, err := s.readEpisodes()
	if err != nil {
		return ResetResult{}, fmt.Errorf("reset progress of %s: %w", scope, err)
	}
	if err := s.writeResets(after); err != nil {
		return ResetResult{}, fmt.Errorf("reset progress of %s: %w", scope, err)
	}
	return ResetResult{ScenarioID: scenarioID, Runs: moved(eps, scenarioID, before, after), Reset: m}, nil
}

// RestoreProgress removes the reset marker of scenarioID, or the marker of
// every scenario when it is empty (the markers of single scenarios stay),
// under the journal lock. It fails with ErrNoReset when there is none. The
// result holds the removed marker, with the Stages saved by ResetProgress,
// and counts the finished episodes that count again.
func (s *Store) RestoreProgress(scenarioID string) (ResetResult, error) {
	scope := scopeName(scenarioID)
	unlock, err := s.lock()
	if err != nil {
		return ResetResult{}, fmt.Errorf("restore progress of %s: %w", scope, err)
	}
	defer unlock()
	before, err := s.Resets()
	if err != nil {
		return ResetResult{}, fmt.Errorf("restore progress of %s: %w", scope, err)
	}
	m, ok := before.Get(scenarioID)
	if !ok {
		return ResetResult{}, fmt.Errorf("restore progress of %s: %w", scope, ErrNoReset)
	}
	after := before.with(scenarioID, nil)
	eps, err := s.readEpisodes()
	if err != nil {
		return ResetResult{}, fmt.Errorf("restore progress of %s: %w", scope, err)
	}
	if err := s.writeResets(after); err != nil {
		return ResetResult{}, fmt.Errorf("restore progress of %s: %w", scope, err)
	}
	return ResetResult{ScenarioID: scenarioID, Runs: moved(eps, scenarioID, before, after), Reset: m}, nil
}

func (s *Store) writeResets(r Resets) error {
	path := s.path(resetsFile)
	if r.All == nil && len(r.Scenarios) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
		return nil
	}
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encode resets: %w", err)
	}
	return atomicfile.Write(path, append(data, '\n'), 0o600)
}

func scopeName(scenarioID string) string {
	if scenarioID == "" {
		return "every scenario"
	}
	return scenarioID
}
