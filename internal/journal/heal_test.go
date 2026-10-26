package journal

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func crashAfterAppend(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.Start(Episode{ID: id, ScenarioID: "s"}); err != nil {
		t.Fatal(err)
	}
	cur, err := s.Current()
	if err != nil || cur == nil {
		t.Fatalf("Current: got %v, %v", cur, err)
	}
	end := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	cur.Outcome = OutcomeSolved
	cur.EndedAt = &end
	if err := s.appendEpisode(*cur); err != nil {
		t.Fatal(err)
	}
}

func outcomes(eps []Episode) string {
	var out []string
	for _, e := range eps {
		out = append(out, e.ID+":"+string(e.Outcome))
	}
	return strings.Join(out, ",")
}

func TestFinishInterruptedBeforeRemovingCurrent(t *testing.T) {
	tests := []struct {
		name string
		next func(s *Store) error
		want string
	}{
		{name: "Current", next: func(s *Store) error {
			cur, err := s.Current()
			if err == nil && cur != nil {
				err = fmt.Errorf("got current %s, want none", cur.ID)
			}
			return err
		}, want: "a:solved"},
		{name: "Start", next: func(s *Store) error { return s.Start(Episode{ID: "b", ScenarioID: "s"}) }, want: "a:solved,b:active"},
		{name: "Finish", next: func(s *Store) error {
			_, err := s.Finish(OutcomeFailed)
			if !errors.Is(err, ErrNoCurrent) {
				return fmt.Errorf("Finish: got %v, want ErrNoCurrent", err)
			}
			return nil
		}, want: "a:solved"},
		{name: "Modify", next: func(s *Store) error {
			_, err := s.Modify("a", func(e *Episode) error { e.HintsUsed = 3; return nil })
			if !errors.Is(err, ErrNoCurrent) {
				return fmt.Errorf("Modify: got %v, want ErrNoCurrent", err)
			}
			return nil
		}, want: "a:solved"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := openTest(t)
			crashAfterAppend(t, s, "a")
			if err := tc.next(s); err != nil {
				t.Fatal(err)
			}
			eps, err := s.Episodes("")
			if err != nil {
				t.Fatal(err)
			}
			if got := outcomes(eps); got != tc.want {
				t.Errorf("episodes after %s: got %s, want %s", tc.name, got, tc.want)
			}
			raw, err := os.ReadFile(filepath.Join(s.Dir(), episodesFile))
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Count(string(raw), `"id":"a"`); got != 1 {
				t.Errorf("lines for a in episodes.jsonl: got %d, want 1", got)
			}
		})
	}
}

func TestEpisodesKeepsFirstRecordPerID(t *testing.T) {
	s := openTest(t)
	content := `{"id":"a","scenario_id":"s","outcome":"solved","ended_at":"2026-03-01T10:00:00Z"}
{"id":"b","scenario_id":"s","outcome":"solved","ended_at":"2026-03-01T11:00:00Z"}
{"id":"a","scenario_id":"s","outcome":"failed","ended_at":"2026-03-01T12:00:00Z"}
`
	if err := os.WriteFile(filepath.Join(s.Dir(), episodesFile), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	eps, err := s.Episodes("")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := outcomes(eps), "a:solved,b:solved"; got != want {
		t.Errorf("Episodes over a duplicated id: got %s, want %s", got, want)
	}
}

func TestModifyEnded(t *testing.T) {
	end := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	content := `{"id":"a","scenario_id":"s","outcome":"solved","ended_at":"2026-03-01T10:00:00Z","diagnosis":{"text":"bad image","at":"2026-03-01T09:00:00Z"}}
{broken
`
	boom := errors.New("boom")
	yes := true
	tests := []struct {
		name    string
		id      string
		fn      func(*Episode) error
		wantErr error
		wantSub string
		check   func(t *testing.T, e *Episode)
	}{
		{name: "sets the matched flag", id: "a", fn: func(e *Episode) error { e.Diagnosis.Matched = &yes; return nil }, check: func(t *testing.T, e *Episode) {
			if e.Diagnosis == nil || e.Diagnosis.Matched == nil || !*e.Diagnosis.Matched || e.Diagnosis.Text != "bad image" {
				t.Errorf("diagnosis: got %+v, want text kept and matched true", e.Diagnosis)
			}
		}},
		{name: "fn error as is", id: "a", fn: func(*Episode) error { return boom }, wantErr: boom},
		{name: "unknown id", id: "zz", fn: func(*Episode) error { return nil }, wantErr: ErrUnknownEpisode},
		{name: "rename refused", id: "a", fn: func(e *Episode) error { e.ID = "z"; return nil }, wantSub: "renamed it to z"},
		{name: "reactivation refused", id: "a", fn: func(e *Episode) error { e.Outcome = OutcomeActive; return nil }, wantSub: "is not finished"},
		{name: "cleared end refused", id: "a", fn: func(e *Episode) error { e.EndedAt = nil; return nil }, wantSub: "is not finished"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := openTest(t)
			path := filepath.Join(s.Dir(), episodesFile)
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := s.ModifyEnded(tc.id, tc.fn)
			if tc.wantErr == boom && err != boom {
				t.Fatalf("ModifyEnded: got %v, want the fn error as is", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("ModifyEnded: got %v, want %v", err, tc.wantErr)
			}
			if tc.wantSub != "" && (err == nil || !strings.Contains(err.Error(), tc.wantSub)) {
				t.Fatalf("ModifyEnded: got %v, want it to contain %q", err, tc.wantSub)
			}
			if tc.check == nil {
				if raw, _ := os.ReadFile(path); string(raw) != content {
					t.Errorf("file after a refused ModifyEnded: got %q, want it unchanged", raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("ModifyEnded: %v", err)
			}
			tc.check(t, got)
			eps, err := s.Episodes("")
			if err != nil || len(eps) != 1 {
				t.Fatalf("Episodes: got %+v, %v", eps, err)
			}
			tc.check(t, &eps[0])
			if !eps[0].EndedAt.Equal(end) {
				t.Errorf("EndedAt: got %v, want %v", eps[0].EndedAt, end)
			}
			if raw, _ := os.ReadFile(path); !strings.Contains(string(raw), "\n{broken\n") {
				t.Errorf("undecodable line: got %q, want it kept", raw)
			}
		})
	}
}

func TestModifyEndedConcurrentProcessesKeepEveryChange(t *testing.T) {
	dir := t.TempDir()
	seed, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	end := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, seed, []Episode{{ID: "a", ScenarioID: "s", EndedAt: &end, Outcome: OutcomeSolved}})
	const n = 16
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := Open(dir)
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := s.ModifyEnded("a", func(e *Episode) error {
				e.HintReveals = append(e.HintReveals, time.Unix(int64(i), 0))
				return nil
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	eps, err := seed.Episodes("")
	if err != nil || len(eps) != 1 {
		t.Fatalf("Episodes: got %+v, %v", eps, err)
	}
	if got := len(eps[0].HintReveals); got != n {
		t.Errorf("concurrent ModifyEnded: got %d changes kept, want %d", got, n)
	}
}
