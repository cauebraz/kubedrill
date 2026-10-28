package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/cauebraz/kubedrill/internal/grader"
	"github.com/cauebraz/kubedrill/internal/scenario"
)

const restartScript = "/scenarios/L1/002-service-dns/shortcuts/restart.sh"

func withShortcuts(f *fixture, id string, shortcuts ...scenario.Shortcut) {
	for _, sc := range f.catalog.all {
		if sc.Meta.ID == id {
			sc.Shortcuts = shortcuts
		}
	}
}

func scriptsRun(f *fixture) []string {
	var out []string
	for _, r := range f.runs {
		out = append(out, r[0])
	}
	return out
}

func TestSmokeShortcutLeavesChecksFailing(t *testing.T) {
	f := newFixture(t)
	withShortcuts(f, "002-service-dns", scenario.Shortcut{Name: "restart", Script: restartScript, Why: "a restart keeps the broken selector"})
	f.grader.grades = []grader.Grade{failGrade(), passGrade(), failGrade()}
	code, out, errOut := f.run(t, "", "smoke", "002")
	if code != 0 {
		t.Fatalf("smoke exit %d: %s\n%s", code, errOut, out)
	}
	mustContain(t, out, "ok   002-service-dns seed 1 (variant selector) (0s)", "ok   002-service-dns seed 1 (variant selector) shortcut restart: still failing ready (0s)", "smoke: 2 runs passed in 0s")
	if got, want := strings.Join(scriptsRun(f), " "), "/scenarios/L1/002-service-dns/solution/solve.sh "+restartScript; got != want {
		t.Errorf("scripts run: got %q, want %q", got, want)
	}
	mustContain(t, strings.Join(f.runs[1], " "), "TASK_NS=task-dns-abcde", "SEED=1", "VARIANT=selector", "KUBECONFIG=")
	if f.grader.n != 3 {
		t.Errorf("grades: got %d, want 3 (setup, solve, shortcut)", f.grader.n)
	}
}

func TestSmokeShortcutThatPassesFails(t *testing.T) {
	f := newFixture(t)
	withShortcuts(f, "002-service-dns", scenario.Shortcut{Name: "restart", Script: restartScript, Why: "a restart keeps the broken selector"})
	f.grader.grades = []grader.Grade{failGrade(), passGrade(), passGrade()}
	code, out, errOut := f.run(t, "", "smoke", "002")
	if code != 1 {
		t.Errorf("exit: got %d, want 1", code)
	}
	mustContain(t, out, "FAIL 002-service-dns seed 1 (variant selector) shortcut restart: shortcut restart passed every check: a restart keeps the broken selector")
	mustContain(t, errOut, "smoke: 1 of 2 runs failed")
}

func TestSmokeNoShortcuts(t *testing.T) {
	f := newFixture(t)
	withShortcuts(f, "002-service-dns", scenario.Shortcut{Name: "restart", Script: restartScript, Why: "a restart keeps the broken selector"})
	f.grader.grades = []grader.Grade{failGrade(), passGrade(), passGrade()}
	code, out, errOut := f.run(t, "", "smoke", "--no-shortcuts", "002")
	if code != 0 {
		t.Fatalf("smoke exit %d: %s\n%s", code, errOut, out)
	}
	mustContain(t, out, "smoke: 1 runs passed")
	if strings.Contains(out, "shortcut") || len(f.runs) != 1 {
		t.Errorf("--no-shortcuts ran a shortcut: out %q runs %v", out, scriptsRun(f))
	}
}

func TestSmokeShortcutVariants(t *testing.T) {
	f := newFixture(t)
	probeOnly := "/scenarios/L0/001-hello-down/shortcuts/probe-only.sh"
	every := "/scenarios/L0/001-hello-down/shortcuts/every.sh"
	withShortcuts(f, "001-hello-down",
		scenario.Shortcut{Name: "every", Script: every, Why: "applies to every variant"},
		scenario.Shortcut{Name: "probe-only", Script: probeOnly, Why: "applies to the probe variant", Variants: []string{"probe"}},
	)
	f.grader.grades = []grader.Grade{failGrade(), passGrade(), failGrade(), failGrade(), passGrade(), failGrade(), failGrade()}
	code, out, errOut := f.run(t, "", "smoke", "001")
	if code != 0 {
		t.Fatalf("smoke exit %d: %s\n%s", code, errOut, out)
	}
	solve := "/scenarios/L0/001-hello-down/solution/solve.sh"
	want := strings.Join([]string{solve, every, solve, every, probeOnly}, " ")
	if got := strings.Join(scriptsRun(f), " "); got != want {
		t.Errorf("scripts run: got %q, want %q", got, want)
	}
	mustContain(t, out, "ok   001-hello-down seed 3 (variant probe) shortcut probe-only", "smoke: 5 runs passed")
	if strings.Contains(out, "(variant image) shortcut probe-only") {
		t.Errorf("probe-only ran on variant image:\n%s", out)
	}
}

func TestSmokeShortcutScriptFails(t *testing.T) {
	f := newFixture(t)
	withShortcuts(f, "002-service-dns", scenario.Shortcut{Name: "restart", Script: restartScript, Why: "a restart keeps the broken selector"})
	f.grader.grades = []grader.Grade{failGrade(), passGrade(), failGrade()}
	fs := f.factories()
	fs.Run = func(_ context.Context, script string, _ []string, out io.Writer) error {
		if script == restartScript {
			io.WriteString(out, "error: deployments.apps \"web\" not found\n")
			return errors.New("exit status 1")
		}
		return nil
	}
	var out strings.Builder
	a := newApp(Options{Factories: fs, Stdout: &out, Stderr: &out, Getenv: func(string) string { return "" }, Getwd: func() (string, error) { return f.root, nil }, NamespaceSuffix: func() string { return "abcde" }})
	err := a.runSmoke(context.Background(), []string{"002"}, smokeOptions{})
	if err == nil || !strings.Contains(err.Error(), "1 of 2 runs failed") {
		t.Errorf("runSmoke: got %v, want 1 of 2 runs failed", err)
	}
	mustContain(t, out.String(), "FAIL 002-service-dns seed 1 (variant selector) shortcut restart: shortcut restart: exit status 1", "     error: deployments.apps \"web\" not found")
	if f.grader.n != 2 {
		t.Errorf("grades: got %d, want 2 (no grade after a failed shortcut)", f.grader.n)
	}
}

func TestSmokePlan(t *testing.T) {
	sc := scenarios()[0]
	sc.Shortcuts = []scenario.Shortcut{{Name: "a"}, {Name: "b", Variants: []string{"image"}}}
	tests := []struct {
		shortcuts bool
		want      []string
	}{
		{shortcuts: true, want: []string{"image solve", "image a", "image b", "probe solve", "probe a"}},
		{shortcuts: false, want: []string{"image solve", "probe solve"}},
	}
	for _, tt := range tests {
		var got []string
		for _, r := range smokePlan([]*scenario.Scenario{sc}, tt.shortcuts) {
			name := "solve"
			if r.shortcut != nil {
				name = r.shortcut.Name
			}
			got = append(got, r.variant+" "+name)
		}
		if strings.Join(got, "|") != strings.Join(tt.want, "|") {
			t.Errorf("smokePlan(shortcuts=%v): got %q, want %q", tt.shortcuts, got, tt.want)
		}
	}
}
