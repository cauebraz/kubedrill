package journal

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
)

var resetBase = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

func endedAt(id, scenario string, end time.Time) Episode {
	return Episode{ID: id, ScenarioID: scenario, StartedAt: end.Add(-5 * time.Minute), EndedAt: &end, Outcome: OutcomeSolved}
}

func TestResetsCounts(t *testing.T) {
	at := resetBase
	all := Reset{At: at.Add(time.Hour)}
	tests := []struct {
		name   string
		resets Resets
		ep     Episode
		want   bool
	}{
		{"no marker", Resets{}, endedAt("a", "001", at), true},
		{"ended before the scenario marker", Resets{Scenarios: map[string]Reset{"001": {At: at}}}, endedAt("a", "001", at.Add(-time.Second)), false},
		{"ended at the scenario marker", Resets{Scenarios: map[string]Reset{"001": {At: at}}}, endedAt("a", "001", at), false},
		{"ended after the scenario marker", Resets{Scenarios: map[string]Reset{"001": {At: at}}}, endedAt("a", "001", at.Add(time.Second)), true},
		{"another scenario's marker", Resets{Scenarios: map[string]Reset{"002": {At: at}}}, endedAt("a", "001", at.Add(-time.Second)), true},
		{"active episode", Resets{All: &all}, Episode{ID: "a", ScenarioID: "001", StartedAt: at, Outcome: OutcomeActive}, true},
		{"the all marker is later than the scenario marker", Resets{All: &all, Scenarios: map[string]Reset{"001": {At: at}}}, endedAt("a", "001", at.Add(30*time.Minute)), false},
		{"the scenario marker is later than the all marker", Resets{All: &Reset{At: at}, Scenarios: map[string]Reset{"001": {At: at.Add(time.Hour)}}}, endedAt("a", "001", at.Add(30*time.Minute)), false},
		{"after both markers", Resets{All: &all, Scenarios: map[string]Reset{"001": {At: at}}}, endedAt("a", "001", at.Add(2*time.Hour)), true},
	}
	for _, tt := range tests {
		if got := tt.resets.Counts(tt.ep); got != tt.want {
			t.Errorf("%s: Counts got %v, want %v", tt.name, got, tt.want)
		}
	}
	eps := []Episode{endedAt("a", "001", at.Add(-time.Minute)), endedAt("b", "002", at.Add(-time.Minute)), endedAt("c", "001", at.Add(time.Minute))}
	got := Resets{Scenarios: map[string]Reset{"001": {At: at}}}.Filter(eps)
	if len(got) != 2 || got[0].ID != "b" || got[1].ID != "c" {
		t.Errorf("Filter: got %v, want b and c in order", got)
	}
}

// resetStore opens a journal whose clock returns *now and holds the
// finished episodes eps.
func resetStore(t *testing.T, now *time.Time, eps ...Episode) *Store {
	t.Helper()
	s := openTest(t)
	s.now = func() time.Time { return *now }
	for _, e := range eps {
		if err := s.appendEpisode(e); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// counted is every episode of s that counts under its reset markers.
func counted(s *Store) ([]Episode, error) {
	eps, err := s.Episodes("")
	if err != nil {
		return nil, err
	}
	r, err := s.Resets()
	if err != nil {
		return nil, err
	}
	return r.Filter(eps), nil
}

func TestResetProgressWritesAndRestores(t *testing.T) {
	now := resetBase
	s := resetStore(t, &now,
		endedAt("a1", "001", resetBase.Add(-3*time.Hour)),
		endedAt("a2", "001", resetBase.Add(-2*time.Hour)),
		endedAt("b1", "002", resetBase.Add(-time.Hour)))
	var seen json.RawMessage = json.RawMessage("x")
	res, err := s.ResetProgress("001", func(prev json.RawMessage) (json.RawMessage, error) {
		seen = prev
		return json.RawMessage(`{"001":{"learn":{"done":true}}}`), nil
	})
	if err != nil {
		t.Fatalf("ResetProgress: %v", err)
	}
	if seen != nil {
		t.Errorf("stages: prev got %s, want nil for the first reset", seen)
	}
	if res.ScenarioID != "001" || res.Runs != 2 || !res.Reset.At.Equal(now) {
		t.Errorf("result: got %+v, want 001 with 2 runs at %s", res, now)
	}
	got, err := counted(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "b1" {
		t.Errorf("counted after the reset of 001: got %v, want b1 only", got)
	}
	all, _ := s.Episodes("")
	if len(all) != 3 {
		t.Errorf("Episodes after a reset: got %d, want the 3 kept", len(all))
	}
	info, err := os.Stat(s.path(resetsFile))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("resets.json: %v, mode %v, want 0600", err, info)
	}

	now = now.Add(time.Hour)
	if _, err := s.ResetProgress("001", func(prev json.RawMessage) (json.RawMessage, error) {
		seen = prev
		return prev, nil
	}); err != nil {
		t.Fatalf("second ResetProgress: %v", err)
	}
	if string(seen) != `{"001":{"learn":{"done":true}}}` {
		t.Errorf("second reset: prev got %s, want the stages of the first", seen)
	}

	res, err = s.RestoreProgress("001")
	if err != nil {
		t.Fatalf("RestoreProgress: %v", err)
	}
	if res.Runs != 2 || string(res.Reset.Stages) != `{"001":{"learn":{"done":true}}}` {
		t.Errorf("restore: got %+v, want 2 runs and the saved stages", res)
	}
	if got, _ := counted(s); len(got) != 3 {
		t.Errorf("counted after the restore: got %d, want 3", len(got))
	}
	if _, err := os.Stat(s.path(resetsFile)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("resets.json after the last restore: %v, want removed", err)
	}
	if _, err := s.RestoreProgress("001"); !errors.Is(err, ErrNoReset) {
		t.Errorf("restore without a marker: got %v, want ErrNoReset", err)
	}
}

func TestResetEverythingKeepsScenarioMarkers(t *testing.T) {
	now := resetBase
	s := resetStore(t, &now,
		endedAt("a1", "001", resetBase.Add(-2*time.Hour)),
		endedAt("b1", "002", resetBase.Add(-time.Hour)))
	if _, err := s.ResetProgress("001", nil); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	res, err := s.ResetProgress("", nil)
	if err != nil {
		t.Fatalf("reset everything: %v", err)
	}
	if res.Runs != 1 {
		t.Errorf("reset everything: got %d runs, want 1 (a1 no longer counted already)", res.Runs)
	}
	if got, _ := counted(s); len(got) != 0 {
		t.Errorf("counted after resetting everything: got %v", got)
	}
	res, err = s.RestoreProgress("")
	if err != nil {
		t.Fatalf("restore everything: %v", err)
	}
	if res.Runs != 1 {
		t.Errorf("restore everything: got %d runs, want 1 (b1; 001 keeps its own marker)", res.Runs)
	}
	r, _ := s.Resets()
	if r.All != nil || len(r.Scenarios) != 1 {
		t.Errorf("markers after restoring everything: got %+v, want the 001 marker only", r)
	}
	if _, err := s.RestoreProgress(""); !errors.Is(err, ErrNoReset) {
		t.Errorf("second restore of everything: got %v, want ErrNoReset", err)
	}
}

func TestResetProgressRefusesTheActiveScope(t *testing.T) {
	now := resetBase
	s := resetStore(t, &now, endedAt("a1", "001", resetBase.Add(-time.Hour)))
	if err := s.Start(Episode{ScenarioID: "001", StartedAt: resetBase}); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		scope string
		want  error
	}{
		{"001", ErrActive},
		{"", ErrActive},
		{"002", nil},
	}
	for _, tt := range tests {
		_, err := s.ResetProgress(tt.scope, nil)
		if !errors.Is(err, tt.want) || (tt.want == nil && err != nil) {
			t.Errorf("reset %q with 001 active: got %v, want %v", tt.scope, err, tt.want)
		}
	}
	r, _ := s.Resets()
	if r.All != nil || len(r.Scenarios) != 1 {
		t.Errorf("markers after the refusals: got %+v, want only 002", r)
	}
	stop := errors.New("stop")
	if _, err := s.ResetProgress("003", func(json.RawMessage) (json.RawMessage, error) { return nil, stop }); !errors.Is(err, stop) {
		t.Errorf("stages error: got %v, want it returned", err)
	}
	if r, _ := s.Resets(); len(r.Scenarios) != 1 {
		t.Errorf("a failed stages callback wrote a marker: %+v", r)
	}
}

func TestResetsReadsAMissingOrBrokenFile(t *testing.T) {
	s := openTest(t)
	if r, err := s.Resets(); err != nil || r.All != nil || r.Scenarios != nil {
		t.Errorf("missing file: got %+v, %v", r, err)
	}
	if err := os.WriteFile(s.path(resetsFile), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resets(); err == nil {
		t.Error("broken file: got nil error")
	}
	if _, err := counted(s); err == nil {
		t.Error("counted over a broken file: got nil error")
	}
}
