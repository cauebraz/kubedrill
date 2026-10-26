package journal

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLayoutAccessors(t *testing.T) {
	dir := filepath.Join("some", "journal")
	if got, want := ShellLogPath(dir), filepath.Join(dir, "shell", "commands.jsonl"); got != want {
		t.Errorf("ShellLogPath: got %q, want %q", got, want)
	}
	if got, want := RecordingsDir(dir), filepath.Join(dir, "recordings"); got != want {
		t.Errorf("RecordingsDir: got %q, want %q", got, want)
	}
}

func TestStartInUnwritableDirChangesNothing(t *testing.T) {
	s := openTest(t)
	requireEnforcedModes(t, s.Dir())
	if err := s.Start(Episode{ID: "a", ScenarioID: "s"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Finish(OutcomeSolved); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(Episode{ID: "b", ScenarioID: "s"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(s.Dir(), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(s.Dir(), 0o700) })

	err := s.Start(Episode{ID: "c", ScenarioID: "s"})
	if err == nil {
		t.Fatal("Start with an unwritable journal directory: got nil error")
	}
	if !errors.Is(err, os.ErrPermission) || !strings.Contains(err.Error(), "lock journal") {
		t.Errorf("Start error: got %v, want a lock journal permission error", err)
	}
	if err := os.Chmod(s.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	eps, err := s.Episodes("")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range eps {
		ids = append(ids, e.ID+":"+string(e.Outcome))
	}
	if got, want := strings.Join(ids, ","), "a:solved,b:active"; got != want {
		t.Errorf("episodes after the failed Start: got %s, want %s", got, want)
	}
}

func TestModifyClearsEndedAtWhileActive(t *testing.T) {
	s := openTest(t)
	if err := s.Start(Episode{ID: "a", ScenarioID: "s"}); err != nil {
		t.Fatal(err)
	}
	end := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	got, err := s.Modify("a", func(e *Episode) error {
		e.EndedAt = &end
		e.HintsUsed = 1
		return nil
	})
	if err != nil {
		t.Fatalf("Modify: %v", err)
	}
	if got.EndedAt != nil || got.Outcome != OutcomeActive || got.HintsUsed != 1 {
		t.Errorf("Modify leaving the outcome active: got %+v, want EndedAt nil, active, hints 1", got)
	}
	cur, err := s.Current()
	if err != nil || cur == nil || cur.EndedAt != nil || cur.Outcome != OutcomeActive {
		t.Errorf("Current after the Modify: got (%+v, %v), want it active with EndedAt nil", cur, err)
	}
	if eps, _ := s.Episodes(""); len(eps) != 1 || eps[0].Outcome != OutcomeActive {
		t.Errorf("Episodes: got %+v, want only the active episode (nothing appended)", eps)
	}
}
