package journal

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

// requireEnforcedModes skips the test unless the filesystem under dir
// refuses writes that file and directory modes forbid; root and bind
// mounts such as Docker Desktop's virtiofs ignore them.
func requireEnforcedModes(t *testing.T, dir string) {
	t.Helper()
	probe, err := os.MkdirTemp(dir, "modes-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chmod(probe, 0o700)
		_ = os.RemoveAll(probe)
	}()
	file := filepath.Join(probe, "ro")
	if err := os.WriteFile(file, nil, 0o400); err != nil {
		t.Fatal(err)
	}
	if f, err := os.OpenFile(file, os.O_WRONLY, 0); err == nil {
		f.Close()
		t.Skip("the filesystem ignores file modes (root or a bind mount such as virtiofs)")
	}
	if err := os.Chmod(probe, 0o500); err != nil {
		t.Fatal(err)
	}
	if f, err := os.Create(filepath.Join(probe, "new")); err == nil {
		f.Close()
		t.Skip("the filesystem ignores directory modes (root or a bind mount such as virtiofs)")
	}
}

func TestOpenCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "kubedrill")
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if s.Dir() != dir {
		t.Fatalf("Dir: got %q, want %q", s.Dir(), dir)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("dir mode: got %o, want 700", got)
	}
}

func TestOpenDefaultUsesXDG(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_DATA_HOME", base)
	s, err := Open("")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	want := filepath.Join(base, "kubedrill")
	if s.Dir() != want {
		t.Fatalf("Dir: got %q, want %q", s.Dir(), want)
	}
}

func TestStartUpdateFinishRoundTrip(t *testing.T) {
	s := openTest(t)
	if cur, err := s.Current(); err != nil || cur != nil {
		t.Fatalf("Current on empty store: got %v, %v; want nil, nil", cur, err)
	}
	if err := s.Start(Episode{ScenarioID: "000-hello-down", Seed: 7, Variant: "a"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	cur, err := s.Current()
	if err != nil || cur == nil {
		t.Fatalf("Current: got %v, %v", cur, err)
	}
	if !strings.HasPrefix(cur.ID, "000-hello-down-") {
		t.Fatalf("ID: got %q, want prefix 000-hello-down-", cur.ID)
	}
	parts := strings.Split(cur.ID, "-")
	if got := parts[len(parts)-1]; len(got) != 4 {
		t.Fatalf("ID suffix: got %q, want 4 hex chars", got)
	}
	if cur.Outcome != OutcomeActive {
		t.Fatalf("Outcome: got %q, want %q", cur.Outcome, OutcomeActive)
	}
	if cur.StartedAt.IsZero() {
		t.Fatal("StartedAt: got zero, want set")
	}

	cur.HintsUsed = 2
	if err := s.Update(*cur); err != nil {
		t.Fatalf("Update: %v", err)
	}
	again, err := s.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if again.HintsUsed != 2 {
		t.Fatalf("HintsUsed after Update: got %d, want 2", again.HintsUsed)
	}

	other := *cur
	other.ID = "someone-else"
	if err := s.Update(other); !errors.Is(err, ErrNotCurrent) {
		t.Fatalf("Update with wrong ID: got %v, want ErrNotCurrent", err)
	}

	done, err := s.Finish(OutcomeSolved)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if done.Outcome != OutcomeSolved || done.EndedAt == nil || done.HintsUsed != 2 {
		t.Fatalf("Finish returned %+v; want solved, ended, hints 2", done)
	}
	if cur, err := s.Current(); err != nil || cur != nil {
		t.Fatalf("Current after Finish: got %v, %v; want nil, nil", cur, err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir(), currentFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("current.json after Finish: got %v, want not exist", err)
	}
	if _, err := s.Finish(OutcomeSolved); !errors.Is(err, ErrNoCurrent) {
		t.Fatalf("Finish without current: got %v, want ErrNoCurrent", err)
	}
	eps, err := s.Episodes("")
	if err != nil {
		t.Fatalf("Episodes: %v", err)
	}
	if len(eps) != 1 || eps[0].ID != cur.ID || eps[0].Outcome != OutcomeSolved {
		t.Fatalf("Episodes: got %+v, want one solved %s", eps, cur.ID)
	}
}

func TestStartFailsCurrent(t *testing.T) {
	s := openTest(t)
	if err := s.Start(Episode{ID: "first", ScenarioID: "a"}); err != nil {
		t.Fatalf("Start first: %v", err)
	}
	if err := s.Start(Episode{ID: "second", ScenarioID: "b"}); err != nil {
		t.Fatalf("Start second: %v", err)
	}
	cur, err := s.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if cur.ID != "second" {
		t.Fatalf("Current ID: got %q, want second", cur.ID)
	}
	eps, err := s.Episodes("")
	if err != nil {
		t.Fatalf("Episodes: %v", err)
	}
	if len(eps) != 2 {
		t.Fatalf("Episodes: got %d, want 2", len(eps))
	}
	if eps[0].ID != "first" || eps[0].Outcome != OutcomeFailed || eps[0].EndedAt == nil {
		t.Fatalf("failed episode: got %+v", eps[0])
	}
	if eps[1].ID != "second" || eps[1].Outcome != OutcomeActive {
		t.Fatalf("current episode last: got %+v", eps[1])
	}
}

func TestEpisodesOrderAndFilter(t *testing.T) {
	s := openTest(t)
	for i, sc := range []string{"x", "y", "x"} {
		if err := s.Start(Episode{ID: string(rune('a' + i)), ScenarioID: sc}); err != nil {
			t.Fatalf("Start %d: %v", i, err)
		}
		if _, err := s.Finish(OutcomeSolved); err != nil {
			t.Fatalf("Finish %d: %v", i, err)
		}
	}
	if err := s.Start(Episode{ID: "d", ScenarioID: "x"}); err != nil {
		t.Fatalf("Start current: %v", err)
	}
	all, err := s.Episodes("")
	if err != nil {
		t.Fatalf("Episodes all: %v", err)
	}
	if got := ids(all); got != "a,b,c,d" {
		t.Fatalf("Episodes all order: got %s, want a,b,c,d", got)
	}
	x, err := s.Episodes("x")
	if err != nil {
		t.Fatalf("Episodes x: %v", err)
	}
	if got := ids(x); got != "a,c,d" {
		t.Fatalf("Episodes x: got %s, want a,c,d", got)
	}
	none, err := s.Episodes("z")
	if err != nil {
		t.Fatalf("Episodes z: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("Episodes z: got %d, want 0", len(none))
	}
}

func ids(eps []Episode) string {
	out := make([]string, 0, len(eps))
	for _, e := range eps {
		out = append(out, e.ID)
	}
	return strings.Join(out, ",")
}

func writeJSONL(t *testing.T, s *Store, eps []Episode) {
	t.Helper()
	var b strings.Builder
	for _, e := range eps {
		data, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(s.Dir(), episodesFile), []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}
}

func TestEpisodesCorruptLine(t *testing.T) {
	s := openTest(t)
	content := `{"id":"a","scenario_id":"s","outcome":"solved"}

{not json}
{"id":"b","scenario_id":"s","outcome":"solved"}
`
	if err := os.WriteFile(filepath.Join(s.Dir(), episodesFile), []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	eps, err := s.Episodes("")
	if err != nil {
		t.Fatalf("Episodes: got error %v, want the readable episodes", err)
	}
	if got := ids(eps); got != "a,b" {
		t.Fatalf("Episodes: got %s, want a,b (corrupt line skipped)", got)
	}
	problems := s.Problems()
	if len(problems) != 1 || !strings.Contains(problems[0], "line 3") || !strings.Contains(problems[0], episodesFile) {
		t.Fatalf("Problems: got %v, want one entry naming line 3 of %s", problems, episodesFile)
	}
	raw, err := os.ReadFile(filepath.Join(s.Dir(), episodesFile))
	if err != nil || string(raw) != content {
		t.Fatalf("episodes.jsonl was rewritten: got %q, %v", raw, err)
	}
	if err := s.Start(Episode{ID: "c", ScenarioID: "s"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := s.Finish(OutcomeSolved); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	eps, err = s.Episodes("")
	if err != nil {
		t.Fatalf("Episodes after append: %v", err)
	}
	if got := ids(eps); got != "a,b,c" {
		t.Fatalf("Episodes after append: got %s, want a,b,c", got)
	}
	if got := s.Problems(); len(got) != 1 {
		t.Fatalf("Problems after append: got %v, want the same single entry", got)
	}
	writeJSONL(t, s, []Episode{{ID: "clean", ScenarioID: "s", Outcome: OutcomeSolved}})
	if _, err := s.Episodes(""); err != nil {
		t.Fatalf("Episodes clean: %v", err)
	}
	if got := s.Problems(); len(got) != 0 {
		t.Fatalf("Problems after clean read: got %v, want none", got)
	}
}

func TestFileModes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "loose")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("dir mode after Open: got %o, want 700", got)
	}
	if err := s.Start(Episode{ID: "e", ScenarioID: "s"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	info, err = os.Stat(filepath.Join(dir, currentFile))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("current.json mode: got %o, want 600", got)
	}
	if _, err := s.Finish(OutcomeSolved); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	info, err = os.Stat(filepath.Join(dir, episodesFile))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("episodes.jsonl mode: got %o, want 600", got)
	}
}

func TestStartAppendsBeforeWritingCurrent(t *testing.T) {
	s := openTest(t)
	requireEnforcedModes(t, s.Dir())
	if err := s.Start(Episode{ID: "first", ScenarioID: "a"}); err != nil {
		t.Fatalf("Start first: %v", err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir(), episodesFile), nil, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(s.Dir(), episodesFile), 0o400); err != nil {
		t.Fatal(err)
	}
	err := s.Start(Episode{ID: "second", ScenarioID: "b"})
	if err == nil {
		t.Fatal("Start with read-only episodes file: got nil error, want error")
	}
	if !strings.Contains(err.Error(), "fail first") {
		t.Fatalf("Start error: got %q, want it to name the abandoned episode", err)
	}
	cur, err := s.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if cur == nil || cur.ID != "first" {
		t.Fatalf("Current after failed abandon: got %v, want first still active (nothing written)", cur)
	}
}

func TestEpisodesSkipsBlankLines(t *testing.T) {
	s := openTest(t)
	content := "\n{\"id\":\"a\",\"scenario_id\":\"s\",\"outcome\":\"solved\"}\n\n\n{\"id\":\"b\",\"scenario_id\":\"s\",\"outcome\":\"solved\"}\n\n"
	if err := os.WriteFile(filepath.Join(s.Dir(), episodesFile), []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	eps, err := s.Episodes("")
	if err != nil {
		t.Fatalf("Episodes: %v", err)
	}
	if got := ids(eps); got != "a,b" {
		t.Fatalf("Episodes: got %s, want a,b", got)
	}
}

func TestCurrentSurvivesCrashBeforeRename(t *testing.T) {
	s := openTest(t)
	if err := s.Start(Episode{ID: "old", ScenarioID: "s"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	partial := filepath.Join(s.Dir(), currentFile+".crash.tmp")
	if err := os.WriteFile(partial, []byte(`{"id":"new","scen`), 0o600); err != nil {
		t.Fatalf("write partial: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(s.Dir(), currentFile))
	if err != nil {
		t.Fatalf("read current: %v", err)
	}
	var e Episode
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatalf("current.json does not parse: %v", err)
	}
	if e.ID != "old" {
		t.Fatalf("current ID: got %q, want old", e.ID)
	}
	cur, err := s.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if cur.ID != "old" {
		t.Fatalf("Current ID: got %q, want old", cur.ID)
	}

	if err := s.Update(Episode{ID: "old", ScenarioID: "s", HintsUsed: 1}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	entries, err := os.ReadDir(s.Dir())
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, en := range entries {
		if strings.HasSuffix(en.Name(), ".tmp") && en.Name() != filepath.Base(partial) {
			t.Fatalf("leftover temp file after atomic write: %s", en.Name())
		}
	}
	data, err = os.ReadFile(filepath.Join(s.Dir(), currentFile))
	if err != nil {
		t.Fatalf("read current: %v", err)
	}
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatalf("current.json after Update does not parse: %v", err)
	}
	if e.HintsUsed != 1 {
		t.Fatalf("HintsUsed: got %d, want 1", e.HintsUsed)
	}
}

func TestStartKeepsGivenStartedAt(t *testing.T) {
	s := openTest(t)
	start := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	if err := s.Start(Episode{ID: "e", ScenarioID: "s", StartedAt: start}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	cur, err := s.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if !cur.StartedAt.Equal(start) {
		t.Fatalf("StartedAt: got %v, want %v", cur.StartedAt, start)
	}
}

func TestCurrentCorrupt(t *testing.T) {
	s := openTest(t)
	if err := os.WriteFile(filepath.Join(s.Dir(), currentFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(s.Dir(), currentFile)
	tests := []struct {
		name string
		call func() error
		want string
	}{
		{name: "Current", call: func() error { _, err := s.Current(); return err }, want: "decode " + wantPath},
		{name: "Start", call: func() error { return s.Start(Episode{ID: "x", ScenarioID: "s"}) }, want: "start episode: decode " + wantPath},
		{name: "Update", call: func() error { return s.Update(Episode{ID: "x"}) }, want: "update episode x: decode " + wantPath},
		{name: "Finish", call: func() error { _, err := s.Finish(OutcomeSolved); return err }, want: "finish episode: decode " + wantPath},
		{name: "Episodes", call: func() error { _, err := s.Episodes(""); return err }, want: "decode " + wantPath},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil {
				t.Fatalf("%s with corrupt current.json: got nil error, want %q", tc.name, tc.want)
			}
			if !strings.HasPrefix(err.Error(), tc.want) {
				t.Errorf("%s error: got %q, want prefix %q", tc.name, err.Error(), tc.want)
			}
		})
	}
	raw, err := os.ReadFile(wantPath)
	if err != nil || string(raw) != "{not json" {
		t.Fatalf("current.json was rewritten: got %q, %v", raw, err)
	}
}

func TestUpdateWithoutCurrent(t *testing.T) {
	s := openTest(t)
	err := s.Update(Episode{ID: "ghost", ScenarioID: "s"})
	if !errors.Is(err, ErrNotCurrent) {
		t.Fatalf("Update without current: got %v, want ErrNotCurrent", err)
	}
	if got, want := err.Error(), "update episode ghost: "+ErrNotCurrent.Error(); got != want {
		t.Errorf("error text: got %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(s.Dir(), currentFile)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("current.json after rejected Update: got %v, want not exist", err)
	}
}

func TestAppendWithoutTrailingNewline(t *testing.T) {
	s := openTest(t)
	content := "{\"id\":\"a\",\"scenario_id\":\"s\",\"outcome\":\"solved\"}\n{\"id\":\"b\",\"scenario_id\":\"s\",\"outcome\":\"solved\"}"
	if err := os.WriteFile(filepath.Join(s.Dir(), episodesFile), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	eps, err := s.Episodes("")
	if err != nil {
		t.Fatalf("Episodes: %v", err)
	}
	if got := ids(eps); got != "a,b" {
		t.Fatalf("Episodes without trailing newline: got %s, want a,b", got)
	}
}

func TestAppendAfterMissingTrailingNewline(t *testing.T) {
	s := openTest(t)
	content := "{\"id\":\"a\",\"scenario_id\":\"s\",\"outcome\":\"solved\"}\n{\"id\":\"b\",\"scenario_id\":\"s\",\"outcome\":\"solved\"}"
	if err := os.WriteFile(filepath.Join(s.Dir(), episodesFile), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(Episode{ID: "c", ScenarioID: "s"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := s.Finish(OutcomeSolved); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	eps, err := s.Episodes("")
	if err != nil {
		t.Fatalf("Episodes after append: %v", err)
	}
	if got := ids(eps); got != "a,b,c" {
		t.Errorf("Episodes after append to a file without trailing newline: got %s, want a,b,c", got)
	}
	if got := s.Problems(); len(got) != 0 {
		t.Errorf("Problems after append: got %v, want none", got)
	}
}

func TestReadLegacyEpisodes(t *testing.T) {
	s := openTest(t)
	content := `{"id":"old","scenario_id":"s","seed":3,"variant":"a","started_at":"2026-01-01T10:00:00Z","ended_at":"2026-01-01T10:20:00Z","outcome":"expired","hints_used":1}
{"id":"ab","scenario_id":"s","seed":4,"variant":"a","started_at":"2026-01-02T10:00:00Z","ended_at":"2026-01-02T10:20:00Z","outcome":"abandoned","hints_used":0}
{"id":"new","scenario_id":"s","seed":5,"started_at":"2026-01-03T10:00:00Z","ended_at":"2026-01-03T10:20:00Z","outcome":"timed_out","mode":"exam","namespace":"task-s-abcde"}
`
	if err := os.WriteFile(filepath.Join(s.Dir(), episodesFile), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	eps, err := s.Episodes("")
	if err != nil {
		t.Fatalf("Episodes: %v", err)
	}
	tests := []struct {
		id        string
		outcome   Outcome
		mode      Mode
		namespace string
	}{
		{"old", OutcomeTimedOut, ModePractice, ""},
		{"ab", OutcomeAbandoned, ModePractice, ""},
		{"new", OutcomeTimedOut, ModeExam, "task-s-abcde"},
	}
	if len(eps) != len(tests) {
		t.Fatalf("Episodes: got %d, want %d", len(eps), len(tests))
	}
	for i, tc := range tests {
		e := eps[i]
		if e.ID != tc.id || e.Outcome != tc.outcome || e.Mode != tc.mode || e.Namespace != tc.namespace {
			t.Errorf("episode %d: got id %s outcome %s mode %s ns %q, want %s %s %s %q", i, e.ID, e.Outcome, e.Mode, e.Namespace, tc.id, tc.outcome, tc.mode, tc.namespace)
		}
	}
	if len(s.Problems()) != 0 {
		t.Errorf("Problems: got %v, want none", s.Problems())
	}
}

func TestStartDefaultsModeToPractice(t *testing.T) {
	s := openTest(t)
	if err := s.Start(Episode{ID: "a", ScenarioID: "s"}); err != nil {
		t.Fatal(err)
	}
	cur, err := s.Current()
	if err != nil || cur.Mode != ModePractice {
		t.Fatalf("Current: got %+v, %v; want mode practice", cur, err)
	}
}

func TestNewFieldsRoundTrip(t *testing.T) {
	s := openTest(t)
	at := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	deadline := at.Add(15 * time.Minute)
	matched := true
	first := at.Add(time.Minute)
	e := Episode{
		ID: "a", ScenarioID: "s", Seed: 9, StartedAt: at,
		Namespace:   "task-s-x1y2z",
		Mode:        ModeExam,
		Deadline:    &deadline,
		HintReveals: []time.Time{at.Add(2 * time.Minute)},
		Diagnosis:   &Diagnosis{Text: "the image tag does not exist", At: at.Add(3 * time.Minute), Matched: &matched},
		Telemetry:   &Telemetry{Source: "audit+shell", FirstKubectlAt: &first, Reads: 4, Writes: 1, Verdict: "root-cause", Commands: []string{"kubectl get pods"}},
	}
	if err := s.Start(e); err != nil {
		t.Fatal(err)
	}
	done, err := s.Finish(OutcomeSolved)
	if err != nil {
		t.Fatal(err)
	}
	eps, err := s.Episodes("")
	if err != nil || len(eps) != 1 {
		t.Fatalf("Episodes: got %v, %v", eps, err)
	}
	got, _ := json.Marshal(eps[0])
	want, _ := json.Marshal(*done)
	if string(got) != string(want) {
		t.Errorf("round trip:\ngot  %s\nwant %s", got, want)
	}
}

func TestUpdateEnded(t *testing.T) {
	end := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	t.Run("rewrites the matching line and keeps the others", func(t *testing.T) {
		s := openTest(t)
		content := `{"id":"a","scenario_id":"s","outcome":"solved","ended_at":"2026-03-01T10:00:00Z"}
{broken
{"id":"b","scenario_id":"s","outcome":"solved","ended_at":"2026-03-01T10:00:00Z"}
`
		path := filepath.Join(s.Dir(), episodesFile)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		e := Episode{ID: "a", ScenarioID: "s", Outcome: OutcomeSolved, EndedAt: &end, Mode: ModePractice, Debrief: &Debrief{At: end, NextTimeFirst: "describe the pod"}}
		if err := s.UpdateEnded(e); err != nil {
			t.Fatalf("UpdateEnded: %v", err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
		if len(lines) != 3 || lines[1] != "{broken" || !strings.Contains(lines[0], "describe the pod") || !strings.HasPrefix(lines[2], `{"id":"b"`) {
			t.Fatalf("file after UpdateEnded: got %q", raw)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("mode after rewrite: got %v, %v; want 600", info.Mode().Perm(), err)
		}
		eps, _ := s.Episodes("")
		if len(eps) != 2 || eps[0].Debrief == nil || eps[0].Debrief.NextTimeFirst != "describe the pod" {
			t.Errorf("Episodes after UpdateEnded: got %+v", eps)
		}
	})
	tests := []struct {
		name    string
		content string
		e       Episode
		wantIs  error
		wantSub string
	}{
		{name: "unknown id", content: `{"id":"a","outcome":"solved"}` + "\n", e: Episode{ID: "zz", Outcome: OutcomeSolved, EndedAt: &end}, wantIs: ErrUnknownEpisode},
		{name: "missing file", e: Episode{ID: "a", Outcome: OutcomeSolved, EndedAt: &end}, wantIs: ErrUnknownEpisode},
		{name: "active episode", content: `{"id":"a","outcome":"solved"}` + "\n", e: Episode{ID: "a", Outcome: OutcomeActive}, wantSub: "is not finished"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := openTest(t)
			if tc.content != "" {
				if err := os.WriteFile(filepath.Join(s.Dir(), episodesFile), []byte(tc.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			err := s.UpdateEnded(tc.e)
			if err == nil {
				t.Fatal("UpdateEnded: got nil, want error")
			}
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Errorf("UpdateEnded: got %v, want %v", err, tc.wantIs)
			}
			if tc.wantSub != "" && !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("UpdateEnded: got %q, want it to contain %q", err, tc.wantSub)
			}
		})
	}
}

func TestJournalID(t *testing.T) {
	dir := t.TempDir()
	a, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := a.JournalID()
	if len(id) != 16 || strings.Trim(id, "0123456789abcdef") != "" {
		t.Fatalf("JournalID: got %q, want 16 lowercase hex digits", id)
	}
	info, err := os.Stat(filepath.Join(dir, "journal.id"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("journal.id: got %v, %v, want mode 0600", info, err)
	}
	link := filepath.Join(t.TempDir(), "mounted")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	b, err := Open(link)
	if err != nil {
		t.Fatal(err)
	}
	if b.JournalID() != id {
		t.Errorf("JournalID of the same dir through another path: got %q and %q, want equal", id, b.JournalID())
	}
	if other := openTest(t); other.JournalID() == id {
		t.Errorf("JournalID of another dir: got %q for both, want different", id)
	}
	if err := os.WriteFile(filepath.Join(dir, "journal.id"), []byte("short\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil || !strings.Contains(err.Error(), "journal id") {
		t.Errorf("Open with a malformed journal.id: got %v, want a journal id error", err)
	}
}

func TestOpenConcurrentAgreesOnID(t *testing.T) {
	dir := t.TempDir()
	ids := make([]string, 8)
	var wg sync.WaitGroup
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := Open(dir)
			if err != nil {
				t.Error(err)
				return
			}
			ids[i] = s.JournalID()
		}()
	}
	wg.Wait()
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("concurrent Open ids: got %q, want one id", ids)
		}
	}
}

func TestLockSerializesWriters(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	seed, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	const n = 16
	var eps []Episode
	for i := 0; i < n; i++ {
		end := base.Add(time.Duration(i) * time.Minute)
		eps = append(eps, Episode{ID: fmt.Sprintf("e%d", i), ScenarioID: "s", StartedAt: base, EndedAt: &end, Outcome: OutcomeFailed})
	}
	writeJSONL(t, seed, eps)
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
			e := eps[i]
			e.Debrief = &Debrief{Principle: "p"}
			if err := s.UpdateEnded(e); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got, err := seed.Episodes("s")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range got {
		if e.Debrief == nil {
			t.Errorf("episode %s lost its concurrent debrief", e.ID)
		}
	}
}

func TestModify(t *testing.T) {
	s := openTest(t)
	if _, err := s.Modify("none", func(*Episode) error { return nil }); !errors.Is(err, ErrNoCurrent) {
		t.Errorf("Modify without current: got %v, want ErrNoCurrent", err)
	}
	if err := s.Start(Episode{ID: "a", ScenarioID: "s"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Modify("b", func(*Episode) error { return nil }); !errors.Is(err, ErrNotCurrent) {
		t.Errorf("Modify of another episode: got %v, want ErrNotCurrent", err)
	}
	boom := errors.New("boom")
	if _, err := s.Modify("a", func(e *Episode) error { e.HintsUsed = 9; return boom }); err != boom {
		t.Errorf("Modify with fn error: got %v, want the fn error as is", err)
	}
	if cur, _ := s.Current(); cur.HintsUsed != 0 {
		t.Errorf("fn error: got hints %d stored, want nothing written", cur.HintsUsed)
	}
	got, err := s.Modify("a", func(e *Episode) error { e.HintsUsed++; return nil })
	if err != nil || got.HintsUsed != 1 || got.Outcome != OutcomeActive {
		t.Fatalf("Modify: got %+v, %v, want hints 1 and still active", got, err)
	}
	if _, err := s.Modify("a", func(e *Episode) error { e.ID = "z"; return nil }); err == nil {
		t.Errorf("Modify renaming the episode: got nil error")
	}
	end := time.Date(2026, 5, 1, 12, 0, 0, 0, time.FixedZone("BRT", -3*3600))
	done, err := s.Modify("a", func(e *Episode) error {
		e.Outcome = OutcomeSolved
		e.EndedAt = &end
		return nil
	})
	if err != nil || done.Outcome != OutcomeSolved || !done.EndedAt.Equal(end) {
		t.Fatalf("Modify finishing: got %+v, %v", done, err)
	}
	if cur, err := s.Current(); err != nil || cur != nil {
		t.Errorf("Current after a finishing Modify: got %v, %v, want none", cur, err)
	}
	eps, err := s.Episodes("")
	if err != nil || len(eps) != 1 || eps[0].Outcome != OutcomeSolved || eps[0].HintsUsed != 1 {
		t.Errorf("Episodes after a finishing Modify: got %+v, %v", eps, err)
	}
}

func TestModifyConcurrentProcessesKeepEveryChange(t *testing.T) {
	dir := t.TempDir()
	first, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Start(Episode{ID: "a", ScenarioID: "s"}); err != nil {
		t.Fatal(err)
	}
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
			if _, err := s.Modify("a", func(e *Episode) error {
				e.HintsUsed++
				e.HintReveals = append(e.HintReveals, time.Unix(int64(i), 0))
				return nil
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	cur, err := first.Current()
	if err != nil {
		t.Fatal(err)
	}
	if cur.HintsUsed != n || len(cur.HintReveals) != n {
		t.Errorf("concurrent Modify: got hints %d reveals %d, want %d each", cur.HintsUsed, len(cur.HintReveals), n)
	}
}

func TestTimestampsStoredInUTC(t *testing.T) {
	s := openTest(t)
	zone := time.FixedZone("BRT", -3*3600)
	start := time.Date(2026, 5, 1, 9, 0, 0, 0, zone)
	deadline := start.Add(time.Hour)
	if err := s.Start(Episode{ID: "a", ScenarioID: "s", StartedAt: start, Deadline: &deadline, Mode: ModeExam}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Modify("a", func(e *Episode) error {
		e.HintReveals = []time.Time{start.Add(time.Minute)}
		e.Diagnosis = &Diagnosis{Text: "the image is wrong", At: start.Add(2 * time.Minute)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(s.Dir(), currentFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "-03:00") || !strings.Contains(string(raw), `"started_at":"2026-05-01T12:00:00Z"`) {
		t.Errorf("current.json: got %s, want every timestamp in UTC", raw)
	}
	if _, err := s.Finish(OutcomeFailed); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(filepath.Join(s.Dir(), episodesFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "-03:00") {
		t.Errorf("episodes.jsonl: got %s, want every timestamp in UTC", raw)
	}
}

func TestDurationAt(t *testing.T) {
	start := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	end := start.Add(7 * time.Minute)
	if got := (Episode{StartedAt: start}).DurationAt(start.Add(3 * time.Minute)); got != 3*time.Minute {
		t.Errorf("active DurationAt: got %v, want 3m", got)
	}
	if got := (Episode{StartedAt: start, EndedAt: &end}).DurationAt(start.Add(time.Hour)); got != 7*time.Minute {
		t.Errorf("ended DurationAt: got %v, want 7m", got)
	}
}

func TestUpdateEndedFirstDuplicateAndActiveWithEnd(t *testing.T) {
	end := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	s := openTest(t)
	content := `{"id":"a","scenario_id":"s","outcome":"failed","ended_at":"2026-03-01T10:00:00Z"}
{"id":"a","scenario_id":"s","outcome":"solved","ended_at":"2026-03-01T10:00:00Z"}
`
	path := filepath.Join(s.Dir(), episodesFile)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	e := Episode{ID: "a", ScenarioID: "s", Outcome: OutcomeSolved, EndedAt: &end, Debrief: &Debrief{At: end, Principle: "read events first"}}
	if err := s.UpdateEnded(e); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "read events first") || strings.Contains(lines[1], "read events first") {
		t.Errorf("duplicate ids: got %q, want only the first line rewritten", raw)
	}
	active := Episode{ID: "a", Outcome: OutcomeActive, EndedAt: &end}
	if err := s.UpdateEnded(active); err == nil || !strings.Contains(err.Error(), "is not finished") {
		t.Errorf("UpdateEnded of an active episode with EndedAt: got %v, want a not finished error", err)
	}
}

func TestJournalIDHexOfWrongLength(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"0123456789abcdef01", "0123abcd"} {
		if err := os.WriteFile(filepath.Join(dir, "journal.id"), []byte(id+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(dir); err == nil || !strings.Contains(err.Error(), "want 16 hex digits") {
			t.Errorf("Open with journal.id %q: got %v, want a length error", id, err)
		}
	}
}
