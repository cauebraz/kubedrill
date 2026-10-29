package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cauebraz/kubedrill/internal/grader"
	"github.com/cauebraz/kubedrill/internal/web"
)

// solveTwice records two clean solves of 001-hello-down.
func solveTwice(t *testing.T, f *fixture) {
	t.Helper()
	f.grader.grades = []grader.Grade{passGrade(), passGrade()}
	for _, seed := range []string{"1", "3"} {
		if code, _, errOut := f.run(t, "", "start", "001-hello-down", "--seed", seed); code != 0 {
			t.Fatalf("start: %s", errOut)
		}
		if code, _, errOut := f.run(t, "", "check"); code != 0 {
			t.Fatalf("check: %s", errOut)
		}
	}
}

// writeStages writes a stage progress file with the learn stage of id done.
func writeStages(t *testing.T, dir, id string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, webProgressFile)
	body := `{"assignments":{"` + id + `":{"meeting":{"done":false},"quiz":{"done":false},"learn":{"done":true,"score":1,"total":1},"practice":{"done":false},"diy":{"done":false},"exam":{"done":false}}},"episodes":{}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func learnDone(t *testing.T, path, id string) bool {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Assignments map[string]web.StageProgress `json:"assignments"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	return f.Assignments[id].Learn.Done
}

// listRow is the row of id in the output of kubedrill list.
func listRow(out, id string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, id+" ") {
			return strings.TrimSpace(l)
		}
	}
	return ""
}

func TestProgressResetAndRestore(t *testing.T) {
	f := newFixture(t)
	solveTwice(t, f)
	stages := writeStages(t, f.journal.dir, "001-hello-down")
	_, out, _ := f.run(t, "", "list")
	if row := listRow(out, "001-hello-down"); !strings.HasSuffix(row, "solved") {
		t.Fatalf("list row before the reset: got %q, want solved", row)
	}

	code, _, errOut := f.run(t, "", "progress", "reset", "001")
	if code != 1 || !strings.Contains(errOut, "--yes") {
		t.Errorf("reset without an answer: got %d %q, want exit 1 naming --yes", code, errOut)
	}
	_, out, _ = f.run(t, "n\n", "progress", "reset", "001")
	mustContain(t, out, "Reset the progress of 001-hello-down? 2 runs stop counting", "progress kept")
	if _, out, _ = f.run(t, "", "list"); !strings.HasSuffix(listRow(out, "001-hello-down"), "solved") {
		t.Fatalf("list after a declined reset:\n%s", out)
	}

	code, out, errOut = f.run(t, "", "progress", "reset", "001", "--yes")
	if code != 0 {
		t.Fatalf("reset: exit %d (%s)", code, errOut)
	}
	mustContain(t, out, "001-hello-down: 2 runs no longer count; stage progress cleared", "undo with: kubedrill progress restore 001-hello-down")
	if learnDone(t, stages, "001-hello-down") {
		t.Error("stage progress still done after the reset")
	}
	_, out, _ = f.run(t, "", "list")
	if row := listRow(out, "001-hello-down"); !strings.HasSuffix(row, "todo") {
		t.Errorf("list row after the reset: got %q, want todo", row)
	}
	_, out, _ = f.run(t, "", "status")
	mustContain(t, out, "solved: 0 of 2 scenarios")
	_, out, _ = f.run(t, "", "journal")
	if n := strings.Count(out, "solved (reset)"); n != 2 {
		t.Errorf("journal: got %d runs marked reset, want 2:\n%s", n, out)
	}
	mustContain(t, out, "(reset): a progress reset no longer counts the run")

	code, out, errOut = f.run(t, "", "progress", "restore", "001")
	if code != 0 {
		t.Fatalf("restore: exit %d (%s)", code, errOut)
	}
	mustContain(t, out, "001-hello-down: 2 runs count again; stage progress restored")
	if !learnDone(t, stages, "001-hello-down") {
		t.Error("stage progress not back after the restore")
	}
	_, out, _ = f.run(t, "", "list")
	if row := listRow(out, "001-hello-down"); !strings.HasSuffix(row, "solved") {
		t.Errorf("list row after the restore: got %q, want solved", row)
	}
	_, out, _ = f.run(t, "", "journal")
	mustNotContain(t, out, "(reset)")

	code, _, errOut = f.run(t, "", "progress", "restore", "001")
	if code != 1 || !strings.Contains(errOut, "no reset of 001-hello-down is recorded; nothing to restore") {
		t.Errorf("second restore: got %d %q", code, errOut)
	}
}

func TestProgressResetAllAndANewRunCounts(t *testing.T) {
	f := newFixture(t)
	solveTwice(t, f)
	code, out, errOut := f.run(t, "", "progress", "reset", "--all", "--yes")
	if code != 0 {
		t.Fatalf("reset --all: exit %d (%s)", code, errOut)
	}
	mustContain(t, out, "every scenario: 2 runs no longer count; no stage progress to clear", "undo with: kubedrill progress restore --all")
	f.grader.grades = []grader.Grade{passGrade()}
	f.run(t, "", "start", "001-hello-down", "--seed", "5")
	f.run(t, "", "check")
	_, out, _ = f.run(t, "", "status")
	mustContain(t, out, "solved: 1 of 2 scenarios")
	_, out, _ = f.run(t, "", "journal")
	if n := strings.Count(out, "solved (reset)"); n != 2 {
		t.Errorf("journal after a new run: got %d marked reset, want 2:\n%s", n, out)
	}
	code, out, _ = f.run(t, "", "progress", "restore", "--all")
	if code != 0 {
		t.Fatalf("restore --all: exit %d", code)
	}
	mustContain(t, out, "every scenario: 2 runs count again; no stage progress to restore")
}

func TestProgressResetRefusesTheActiveRun(t *testing.T) {
	f := newFixture(t)
	solveTwice(t, f)
	f.grader.grades = []grader.Grade{failGrade()}
	f.run(t, "", "start", "001-hello-down", "--seed", "5")
	for _, args := range [][]string{{"001"}, {"--all"}} {
		code, out, errOut := f.run(t, "", append([]string{"progress", "reset", "--yes"}, args...)...)
		if code != 1 || !strings.Contains(errOut, "001-hello-down is active (episode ") || !strings.Contains(errOut, "end the active run first (kubedrill solution, or Give up in the page)") {
			t.Errorf("reset %v while 001 is active: got %d %q %q", args, code, out, errOut)
		}
	}
	if r, _ := f.journal.Resets(); r.All != nil || len(r.Scenarios) != 0 {
		t.Errorf("a refused reset wrote a marker: %+v", r)
	}
	code, out, errOut := f.run(t, "", "progress", "reset", "002", "--yes")
	if code != 0 {
		t.Fatalf("reset of another scenario: exit %d (%s)", code, errOut)
	}
	mustContain(t, out, "002-service-dns: 0 runs no longer count; no stage progress to clear")
}

func TestProgressUsage(t *testing.T) {
	f := newFixture(t)
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"progress", "reset"}, "usage: kubedrill progress reset <ref> | --all [--yes]"},
		{[]string{"progress", "reset", "001", "--all"}, "usage: kubedrill progress reset <ref> | --all [--yes]"},
		{[]string{"progress", "restore"}, "usage: kubedrill progress restore <ref> | --all"},
		{[]string{"progress", "reset", "zzz", "--yes"}, `scenario "zzz" not found`},
		{[]string{"progress", "restore", "--all"}, "no reset of every scenario is recorded; nothing to restore"},
	}
	for _, tt := range tests {
		code, _, errOut := f.run(t, "", tt.args...)
		if code != 1 || !strings.Contains(errOut, tt.want) {
			t.Errorf("%v: got %d %q, want exit 1 with %q", tt.args, code, errOut, tt.want)
		}
	}
	f.run(t, "", "progress", "reset", "001", "--yes")
	_, _, errOut := f.run(t, "", "progress", "restore", "--all")
	mustContain(t, errOut, "scenarios reset one by one: 001-hello-down (kubedrill progress restore <ref>)")
}

func TestWebBackendResetProgress(t *testing.T) {
	f := newFixture(t)
	solveTwice(t, f)
	b := f.webBackend(t)
	ctx := context.Background()
	status := func() string {
		t.Helper()
		c, err := b.Catalog(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return c.Levels[0].Modules[0].Assignments[0].Status + " " + c.Levels[0].Modules[0].Badge.State
	}
	if got := status(); got != "solved earned" {
		t.Fatalf("before: got %q, want solved earned", got)
	}
	var gotID string
	ch, err := b.ResetProgress(ctx, "001", func(id string, prev json.RawMessage) (json.RawMessage, error) {
		gotID = id
		return json.RawMessage(`{"saved":true}`), nil
	})
	if err != nil {
		t.Fatalf("ResetProgress: %v", err)
	}
	if ch.ID != "001-hello-down" || ch.Runs != 2 || gotID != "001-hello-down" {
		t.Errorf("reset: got %+v, stages id %q", ch, gotID)
	}
	if got := status(); got != "todo locked" {
		t.Errorf("after the reset: got %q, want todo locked", got)
	}
	a, err := b.Assignment(ctx, "001")
	if err != nil || a.Mastery.Attempts != 0 {
		t.Errorf("assignment mastery after the reset: %+v %v, want no attempt", a.Mastery, err)
	}
	d, err := b.Debrief(ctx, f.journal.episodes[0].ID)
	if err != nil || !d.Reset || d.Outcome != "solved" {
		t.Errorf("debrief of a reset run: got %+v %v, want it shown and marked reset", d, err)
	}
	ch, err = b.RestoreProgress(ctx, "001")
	if err != nil || ch.Runs != 2 || string(ch.Saved) != `{"saved":true}` {
		t.Errorf("restore: got %+v %v", ch, err)
	}
	if got := status(); got != "solved earned" {
		t.Errorf("after the restore: got %q, want solved earned", got)
	}
	if d, _ := b.Debrief(ctx, f.journal.episodes[0].ID); d.Reset {
		t.Error("debrief after the restore: still marked reset")
	}
	f.grader.grades = []grader.Grade{failGrade()}
	f.run(t, "", "start", "001-hello-down", "--seed", "5")
	if _, err := b.ResetProgress(ctx, "", nil); !errors.Is(err, web.ErrRunActive) {
		t.Errorf("reset everything while active: got %v, want web.ErrRunActive", err)
	}
	if _, err := b.RestoreProgress(ctx, ""); err == nil || !strings.Contains(err.Error(), "no reset of every scenario is recorded") {
		t.Errorf("restore without a marker: got %v", err)
	}
}
