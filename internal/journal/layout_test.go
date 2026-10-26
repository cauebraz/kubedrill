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

// TestStartRemovesCurrentWhenWriteFails covers the write failure after the
// previous episode was appended as failed. The only failure a test can
// provoke without a seam is EACCES on the journal directory (mode 0500):
// appending to the existing episodes.jsonl still works (the file itself is
// writable), the temporary file for current.json cannot be created, and the
// cleanup removal of current.json fails with EACCES too, so Start reports
// both failures and current.json keeps naming the episode that was just
// appended as failed. That is the documented limitation of the EACCES case;
// with any other write failure the removal succeeds and no episode is both
// current and finished.
func TestStartRemovesCurrentWhenWriteFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory modes")
	}
	s := openTest(t)
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
	if !errors.Is(err, os.ErrPermission) {
		t.Errorf("error %v does not wrap the permission error", err)
	}
	for _, want := range []string{"start episode c", "write " + s.path(currentFile), "remove current"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	if err := os.Chmod(s.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	cur, err := s.Current()
	if err != nil || cur == nil || cur.ID != "b" {
		t.Errorf("current after the failed Start: got (%+v, %v), want b still current (EACCES case)", cur, err)
	}
	eps, err := s.Episodes("")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range eps {
		ids = append(ids, e.ID+":"+string(e.Outcome))
	}
	if got := strings.Join(ids, ","); got != "a:solved,b:failed,b:active" {
		t.Errorf("episodes after the failed Start: got %s, want a solved, b appended as failed and b still active", got)
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
