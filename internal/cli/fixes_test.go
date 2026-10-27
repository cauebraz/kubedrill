package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/cauebraz/kubedrill/internal/cluster"
	"github.com/cauebraz/kubedrill/internal/env"
	"github.com/cauebraz/kubedrill/internal/faults"
	"github.com/cauebraz/kubedrill/internal/grader"
	"github.com/cauebraz/kubedrill/internal/journal"
	"github.com/cauebraz/kubedrill/internal/render"
	"github.com/cauebraz/kubedrill/internal/scenario"
)

func examEpisode(deadline time.Time) *journal.Episode {
	return &journal.Episode{ID: "ex-1", ScenarioID: "001-hello-down", Seed: 1, Mode: journal.ModeExam, Namespace: "task-hello-abcde", StartedAt: deadline.Add(-15 * time.Minute), Deadline: &deadline, Outcome: journal.OutcomeActive}
}

func TestExamExpiresAtExactlyTheDeadline(t *testing.T) {
	deadline := time.Date(2026, 10, 25, 12, 15, 0, 0, time.UTC)
	for _, tc := range []struct {
		now     time.Time
		running bool
	}{
		{deadline.Add(-time.Second), true},
		{deadline, false},
		{deadline.Add(time.Second), false},
	} {
		f := newFixture(t)
		f.now = tc.now
		ep := examEpisode(deadline)
		f.journal.current = ep
		f.journal.episodes = []journal.Episode{*ep}
		a := newApp(Options{Factories: f.factories(), Getenv: func(string) string { return "" }, Now: f.nowFunc()})
		if got := a.examRunning(ep); got != tc.running {
			t.Errorf("examRunning at %s: got %v, want %v", tc.now.Format("15:04:05"), got, tc.running)
		}
		f.grader.grades = []grader.Grade{failGrade()}
		_, _, out := f.run(t, "", "status")
		if expired := strings.Contains(out, "exam time box of 001-hello-down expired"); expired == tc.running {
			t.Errorf("status at %s: expired %v, want %v:\n%s", tc.now.Format("15:04:05"), expired, !tc.running, out)
		}
	}
}

func TestSmokeGuardLegacyEpisodeWithoutNamespace(t *testing.T) {
	f := newFixture(t)
	start := time.Date(2026, 10, 24, 9, 30, 0, 0, time.UTC)
	f.journal.current = &journal.Episode{ID: "old", ScenarioID: "001-hello-down", StartedAt: start}
	code, _, errOut := f.run(t, "", "smoke", "001")
	if code != 1 {
		t.Fatalf("exit: got %d, want 1", code)
	}
	mustContain(t, errOut, "an active episode of 001-hello-down runs since "+start.Local().Format("2006-01-02 15:04")+"; setup hooks may change cluster-wide state")
}

func TestStartKeepsEpisodeAfterFailedStart(t *testing.T) {
	f := newFixture(t)
	if code, _, errOut := f.run(t, "", "start", "001", "--seed", "1"); code != 0 {
		t.Fatalf("first start: %s", errOut)
	}
	f.faults = &fakeFaults{active: []faults.Fault{{Action: faults.Pause, Node: "worker2"}}, healErr: errors.New("docker unreachable")}
	code, out, errOut := f.run(t, "", "start", "002", "--yes")
	if code != 1 {
		t.Fatalf("exit: got %d, want 1", code)
	}
	mustContain(t, out, "kept the episode of 001-hello-down active\n")
	mustContain(t, errOut, "heal injected faults: docker unreachable")
	mustNotContain(t, out, "leaving the episode")
	if f.journal.current == nil || f.journal.current.ScenarioID != "001-hello-down" {
		t.Errorf("current: got %+v, want the first episode", f.journal.current)
	}
}

func TestStartHealsBeforeLeaving(t *testing.T) {
	f := newFixture(t)
	if code, _, errOut := f.run(t, "", "start", "001", "--seed", "1"); code != 0 {
		t.Fatalf("first start: %s", errOut)
	}
	f.faults = &fakeFaults{active: []faults.Fault{{Action: faults.Pause, Node: "worker2"}}}
	var b strings.Builder
	code := Main(context.Background(), []string{"--root", f.root, "start", "002", "--yes"}, Options{
		Factories: f.factories(), Stdout: &b, Stderr: &b, Stdin: strings.NewReader(""),
		Getenv: func(string) string { return "" }, Getwd: func() (string, error) { return f.root, nil },
		Now: f.nowFunc(), NamespaceSuffix: func() string { return "abcde" },
	})
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, b.String())
	}
	got := b.String()
	healed := strings.Index(got, "healed pause on worker2")
	leaving := strings.Index(got, "leaving the episode of 001-hello-down")
	if healed < 0 || leaving < 0 || healed > leaving || strings.Count(got, "healed pause") != 1 {
		t.Errorf("healed at %d, leaving at %d; want one heal line first:\n%s", healed, leaving, got)
	}
}

func TestDebriefAndReplayEpisodeIDs(t *testing.T) {
	f := newFixture(t)
	terminal(f)
	withWorkstation(f)
	start := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	active := journal.Episode{ID: "ep-b1", ScenarioID: "001-hello-down", Seed: 4, StartedAt: start.Add(time.Hour), Outcome: journal.OutcomeActive}
	f.journal.episodes = []journal.Episode{
		solvedEpisode("ep-a1", "001-hello-down", 1, start),
		solvedEpisode("ep-a2", "001-hello-down", 2, start.Add(10*time.Minute)),
		active,
	}
	f.journal.current = &active
	for _, cmd := range []string{"debrief", "replay"} {
		if code, _, errOut := f.run(t, "", cmd, "ep-a"); code != 1 || !strings.Contains(errOut, `episode "ep-a" is ambiguous (2 matches); kubedrill journal lists them`) {
			t.Errorf("%s ambiguous: exit %d err %q", cmd, code, errOut)
		}
	}
	if code, _, errOut := f.run(t, "", "debrief", "ep-b1"); code != 1 || !strings.Contains(errOut, "episode ep-b1 is still active") {
		t.Errorf("debrief active: exit %d err %q", code, errOut)
	}
	if code, _, errOut := f.run(t, "", "replay", "ep-b1"); code != 1 || !strings.Contains(errOut, "episode ep-b1 (001-hello-down) has no recording") {
		t.Errorf("replay active: exit %d err %q", code, errOut)
	}
}

func TestNegativeDelayAndLast(t *testing.T) {
	f := newFixture(t)
	f.faults = &fakeFaults{}
	if code, _, errOut := f.run(t, "", "fault", "netem", "worker", "--delay", "-1s"); code != 1 || !strings.Contains(errOut, "--delay must be >= 0, got -1s") {
		t.Errorf("negative delay: exit %d err %q", code, errOut)
	}
	if code, _, errOut := f.run(t, "", "journal", "--last", "-1"); code != 1 || !strings.Contains(errOut, "--last must be >= 0, got -1") {
		t.Errorf("negative last: exit %d err %q", code, errOut)
	}
	if len(f.faults.applied) != 0 {
		t.Errorf("applied: %v", f.faults.applied)
	}
}

func TestFaultUnknownNodeFromManager(t *testing.T) {
	f := newFixture(t)
	f.faults = &fakeFaults{applyErr: fmt.Errorf("apply pause: %w", faults.ErrUnknownNode)}
	code, _, errOut := f.run(t, "", "fault", "pause", "worker2")
	if code != 1 || !strings.Contains(errOut, `kubedrill: unknown node "worker2"; valid: control-plane, worker, worker2`) {
		t.Errorf("exit %d err %q", code, errOut)
	}
}

func spoilerGrade() grader.Grade {
	return grader.Grade{Results: []grader.Result{
		{ID: "image", Description: "image is pullable", Status: grader.StatusFail, Expected: "nginx:1.27", Actual: "nginx:1.2x", Spoiler: true},
	}}
}

func TestCheckHidesSpoilerUntilDiagnosis(t *testing.T) {
	f := newFixture(t)
	f.grader.grades = []grader.Grade{spoilerGrade()}
	if code, _, errOut := f.run(t, "", "start", "001", "--seed", "1"); code != 0 {
		t.Fatalf("start: %s", errOut)
	}
	_, out, _ := f.run(t, "", "check")
	mustContain(t, out, "expected: nginx:1.27  actual: "+render.SpoilerHidden)
	mustNotContain(t, out, "nginx:1.2x")
	if code, _, errOut := f.run(t, "", "diagnose", "the image tag is wrong"); code != 0 {
		t.Fatalf("diagnose: %s", errOut)
	}
	_, out, _ = f.run(t, "", "check")
	mustContain(t, out, "expected: nginx:1.27  actual: nginx:1.2x")
}

func TestExplainErrors(t *testing.T) {
	a := newApp(Options{Getenv: func(string) string { return "" }})
	if got := a.explain(fmt.Errorf("start: %w", &scenario.NotFoundError{Ref: "abc"})); got != `scenario "abc" not found; see kubedrill list` {
		t.Errorf("not found: got %q", got)
	}
	missing := fmt.Errorf("namespace task-x is missing; it was deleted or belongs to another episode; run: kubedrill reset: %w", env.ErrNamespaceMissing)
	if got := a.explain(missing); got != "namespace task-x is missing; it was deleted or belongs to another episode; run: kubedrill reset" {
		t.Errorf("namespace missing: got %q", got)
	}
	foreign := fmt.Errorf("namespace task-x belongs to another episode (seed 4); run: kubedrill reset: %w", env.ErrNamespaceForeign)
	if got := a.explain(foreign); strings.Contains(got, ": "+env.ErrNamespaceForeign.Error()) {
		t.Errorf("namespace foreign: got %q", got)
	}
	if got, want := a.explain(env.ErrNoActiveEpisode), "no active episode; start one with: kubedrill start <id> (see kubedrill list)"; got != want {
		t.Errorf("bare no active episode: got %q, want %q", got, want)
	}
	solution := fmt.Errorf("%w; kubedrill solution <id> shows the walkthrough of a finished scenario", env.ErrNoActiveEpisode)
	if got, want := a.explain(solution), "no active episode; kubedrill solution <id> shows the walkthrough of a finished scenario"; got != want {
		t.Errorf("wrapped no active episode: got %q, want %q", got, want)
	}
}

func TestRefusalsKeepTheirCause(t *testing.T) {
	for _, tc := range []struct {
		err   error
		cause error
	}{
		{diagnosisNeeded("the last hint"), env.ErrDiagnosisRequired},
		{errSurrenderDiagnosis, env.ErrDiagnosisRequired},
		{errSubmitPractice, env.ErrNotExam},
		{examRefusal("check"), env.ErrExamMode},
	} {
		if !errors.Is(tc.err, tc.cause) {
			t.Errorf("%q: does not match %v", tc.err, tc.cause)
		}
	}
	f := newFixture(t)
	f.run(t, "", "start", "001")
	code, _, errOut := f.run(t, "", "submit")
	if code != 1 || !strings.Contains(errOut, "submit grades exam episodes") {
		t.Errorf("submit practice: exit %d err %q", code, errOut)
	}
}

func writeProfile(t *testing.T, root, body string) {
	t.Helper()
	dir := filepath.Join(root, "cluster", "profiles")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "base.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cluster", "audit-policy.yaml"), []byte("rules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestProfileRenderUsesClusterRendererAndRefusesPaths(t *testing.T) {
	f := newFixture(t)
	writeProfile(t, f.root, "kind: Cluster\n")
	f.cluster.rendered = []byte("kind: Cluster\nname: rendered\n")
	out := filepath.Join(t.TempDir(), "kind.yaml")
	code, stdout, errOut := f.run(t, "", "profile", "render", "--out", out)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(f.cluster.rendered) {
		t.Errorf("written file:\n%s\nwant the cluster renderer's bytes", raw)
	}
	if st, err := os.Stat(out); err != nil || st.Mode().Perm() != 0o644 {
		t.Errorf("mode of %s: %v %v, want 0644", out, st.Mode(), err)
	}
	auditDir := filepath.Join(f.root, "state", "kubedrill", "kubedrill", "audit")
	if want := []string{"base " + filepath.Join(f.root, "cluster", "audit-policy.yaml") + " " + auditDir}; !slices.Equal(f.cluster.renders, want) {
		t.Errorf("renders: got %v, want %v", f.cluster.renders, want)
	}
	if st, err := os.Stat(auditDir); err != nil || !st.IsDir() {
		t.Errorf("audit directory not created: %v", err)
	}
	mustContain(t, stdout, "rendered profile base to "+out)

	f.cluster.renderErr = errors.New("render profile base: template: base.yaml:1: map has no entry for key \"Missing\"")
	if code, _, errOut := f.run(t, "", "profile", "render", "--out", out); code != 1 || !strings.Contains(errOut, "no entry for key") {
		t.Errorf("renderer error: exit %d err %q", code, errOut)
	}
	f.cluster.renderErr = nil
	f.cluster.rendered = []byte("x: {{ .Left }}\n")
	if code, _, errOut := f.run(t, "", "profile", "render", "--out", out); code != 1 || !strings.Contains(errOut, "still holds a template action") {
		t.Errorf("leftover action: exit %d err %q", code, errOut)
	}
	if code, _, errOut := f.run(t, "", "profile", "render"); code != 1 || !strings.Contains(errOut, "usage: kubedrill profile render [--profile base] --out <file>") {
		t.Errorf("missing --out: exit %d err %q", code, errOut)
	}
	refused := filepath.Join(t.TempDir(), "refused.yaml")
	renders := len(f.cluster.renders)
	for _, profile := range []string{"../base", ".x", "cluster/base", ""} {
		code, _, errOut := f.run(t, "", "profile", "render", "--profile", profile, "--out", refused)
		if code != 1 || !strings.Contains(errOut, "want a file name under cluster/profiles") {
			t.Errorf("--profile %q: exit %d err %q", profile, code, errOut)
		}
	}
	if _, err := os.Stat(refused); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused profile wrote %s: %v", refused, err)
	}
	if len(f.cluster.renders) != renders {
		t.Errorf("a refused profile reached the renderer: %v", f.cluster.renders[renders:])
	}
}

func TestRenderRealBaseProfile(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	profiles := filepath.Join(repo, "cluster", "profiles")
	if _, err := os.Stat(filepath.Join(profiles, "base.yaml")); err != nil {
		t.Skip("no base profile in this checkout")
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	a := newApp(Options{Getenv: func(string) string { return "" }, Factories: Factories{Cluster: func(dir string) Cluster { return cluster.New(dir) }}})
	a.rootFlag = repo
	raw, err := a.renderProfile("base")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "{{") || !strings.Contains(string(raw), "hostPath: \""+filepath.Join(repo, "cluster", "audit-policy.yaml")+"\"") {
		t.Errorf("rendered base profile:\n%s", raw)
	}
}

func TestSolutionPathWithoutShellLog(t *testing.T) {
	f := newFixture(t)
	f.journal.episodes = []journal.Episode{solvedEpisode("s1", "001-hello-down", 1, time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC))}
	_, out, _ := f.run(t, "", "solution", "001")
	mustContain(t, out, "Your path: no kubectl commands recorded (telemetry: none)\n  commands typed outside the workstation are not logged; the terminal of kubedrill web records them\n")
}

func TestLegendWrapsToTerminalWidth(t *testing.T) {
	f := newFixture(t)
	f.stdoutTTY = true
	f.env = map[string]string{"COLUMNS": "60", "NO_COLOR": "1"}
	_, out, _ := f.run(t, "", "list")
	i := strings.Index(out, "MIN: expected")
	if i < 0 {
		t.Fatalf("no legend:\n%s", out)
	}
	legend := out[i : strings.Index(out[i:], "\n\n")+i]
	for _, l := range strings.Split(legend, "\n") {
		if lipgloss.Width(l) > 60 {
			t.Errorf("legend line of %d columns: %q", lipgloss.Width(l), l)
		}
	}
	if got := strings.Join(strings.Fields(legend), " "); got != listLegend {
		t.Errorf("legend text changed: %q", got)
	}
}

func TestMarkdownWithoutColorWrapsOnTerminal(t *testing.T) {
	a := newApp(Options{IsTerminal: true, Getenv: func(k string) string {
		switch k {
		case "NO_COLOR":
			return "1"
		case "COLUMNS":
			return "40"
		}
		return ""
	}})
	out := a.printer().Markdown("- " + strings.Repeat("word ", 20) + "\n\n```\n" + strings.Repeat("x", 60) + "\n```\n")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 3 || !strings.HasPrefix(lines[1], "  word") {
		t.Fatalf("no hanging indent:\n%s", out)
	}
	for _, l := range lines {
		if len(l) > 40 && l != strings.Repeat("x", 60) {
			t.Errorf("line of %d columns: %q", len(l), l)
		}
	}
	pipe := newApp(Options{Getenv: func(string) string { return "" }})
	if got := pipe.printer().Markdown("- " + strings.Repeat("word ", 20)); strings.Count(got, "\n") != 1 {
		t.Errorf("pipe output wrapped: %q", got)
	}
}

func TestDebriefRedoShowsMatchedAsLetter(t *testing.T) {
	f := newFixture(t)
	terminal(f)
	ep := solvedEpisode("e1", "001-hello-down", 1, time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC))
	ep.Diagnosis = &journal.Diagnosis{Text: "the image tag is wrong", Matched: boolPtr(true)}
	f.journal.episodes = []journal.Episode{ep}
	_, out, _ := f.run(t, "\n\n\n", "debrief", "--redo")
	mustContain(t, out, "Did your diagnosis match? [y/n] [y] ", debriefLater)
	mustNotContain(t, out, "debrief recorded")
	_, out, _ = f.run(t, "n\nthe events\n\n", "debrief", "--redo")
	mustContain(t, out, "debrief recorded; kubedrill debrief shows it again")
	mustNotContain(t, out, debriefLater)
	_, out, _ = f.run(t, "\n\n\n", "debrief", "--redo")
	mustContain(t, out, "Did your diagnosis match? [y/n] [n] ")
}

func TestCheckExplainInExam(t *testing.T) {
	f := newFixture(t)
	f.startExam(t, 0, false)
	_, out, _ := f.run(t, "", "check", "--explain")
	mustContain(t, out, "001-hello-down is checked by 1 check; kubedrill submit grades them once, this list grades nothing\n")
}

func TestReasonSentenceSingleAttempt(t *testing.T) {
	if got := reasonSentence("practice: level L0, solved 0 of last 1"); got != "you did not solve your last attempt at L0" {
		t.Errorf("got %q", got)
	}
}

var _ clusterCreated = (*cluster.Manager)(nil)

func TestFaultsUseTheInjectedStateDir(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "elsewhere"))
	cause := errors.New("no home")
	tests := []struct {
		name    string
		dir     string
		err     error
		wantDir string
	}{
		{"resolved directory", "/state/kubedrill", nil, "/state/kubedrill"},
		{"resolution error", "", cause, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			a := newApp(Options{Getenv: func(string) string { return "" }, Factories: Factories{
				StateDir: func() (string, error) { return tc.dir, tc.err },
				Faults: func(dir string) Faults {
					got = dir
					return &fakeFaults{dir: dir}
				},
			}})
			_, err := a.getFaults()
			if !errors.Is(err, tc.err) {
				t.Fatalf("getFaults: got %v, want %v", err, tc.err)
			}
			if got != tc.wantDir {
				t.Errorf("Faults dir: got %q, want %q", got, tc.wantDir)
			}
		})
	}
}
