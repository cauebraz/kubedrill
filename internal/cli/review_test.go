package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cauebraz/kubedrill/internal/faults"
	"github.com/cauebraz/kubedrill/internal/grader"
	"github.com/cauebraz/kubedrill/internal/journal"
	"github.com/cauebraz/kubedrill/internal/scenario"
)

func TestWorkedExampleEmptyAnswerSaysSkipped(t *testing.T) {
	f := newFixture(t)
	terminal(f)
	_, out, _ := f.run(t, "", "start", "001", "--seed", "1", "--no-shell")
	mustContain(t, out, "before your ticket? [y/N] worked example skipped\n", "The hello app returns 503")
	mustNotContain(t, out, "Worked example (variant")
	f2 := newFixture(t)
	terminal(f2)
	_, out, _ = f2.run(t, "n\n", "start", "001", "--seed", "1", "--no-shell")
	mustNotContain(t, out, "worked example skipped", "Worked example (variant")
}

func TestWorkedExampleKeepsSymptomAndCandidates(t *testing.T) {
	sol := "## Symptom\n\ns\n\n## Candidate causes\n\nc\n\n## Observation that splits them\n\no\n\n## Change\n\nx\n\n## Verify\n\nv\n"
	got := workedExample(sol)
	want := "## Symptom\n\ns\n\n## Candidate causes\n\nc\n"
	if got != want {
		t.Errorf("workedExample: got %q, want %q", got, want)
	}
	if got := workedExample("## Change\n\nx\n"); got != "" {
		t.Errorf("workedExample without the sections: got %q, want empty", got)
	}
}

func TestExamRefusesResetAndAsksToLeaveTheExam(t *testing.T) {
	f := newFixture(t)
	f.grader.grades = []grader.Grade{failGrade()}
	if code, _, errOut := f.run(t, "", "start", "001", "--seed", "1", "--exam"); code != 0 {
		t.Fatalf("start --exam: exit %d: %s", code, errOut)
	}
	code, _, errOut := f.run(t, "", "reset")
	if code != 1 || !strings.Contains(errOut, "reset is not available in exam mode; kubedrill submit ends the exam") {
		t.Errorf("reset in exam: exit %d err %q", code, errOut)
	}
	if f.journal.current == nil || f.journal.current.Mode != journal.ModeExam {
		t.Fatalf("reset changed the exam episode: %+v", f.journal.current)
	}
	_, out, _ := f.run(t, "n\n", "start", "002")
	mustContain(t, out, "Leave the exam of 001-hello-down? It will be recorded as failed. [y/N]", "kept the episode active")
}

func TestWatchResetKeyRefusedInExam(t *testing.T) {
	w := &fakeWatcher{observe: failGrade()}
	m := newTestModel(w)
	m.examActive = func() bool { return true }
	next, _ := m.Update(key("x"))
	m = next.(watchModel)
	if m.confirmReset {
		t.Error("x armed the reset during an exam")
	}
	mustContain(t, m.View().Content, "reset is not available in exam mode; kubedrill submit ends the exam")
	m.examActive = func() bool { return false }
	next, _ = m.Update(key("x"))
	m = next.(watchModel)
	m.examActive = func() bool { return true }
	next, cmd := m.Update(key("x"))
	m = next.(watchModel)
	if cmd != nil || len(w.starts) != 0 || m.starting {
		t.Errorf("second x during an exam started %v", w.starts)
	}
}

func TestPrincipleAskedAfterAnotherVariantOfTheSameScenario(t *testing.T) {
	f := newFixture(t)
	terminal(f)
	f.grader.grades = []grader.Grade{passGrade()}
	earlier := solvedEpisode("old", "001-hello-down", 2, time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC))
	earlier.Variant = "probe"
	f.journal.episodes = []journal.Episode{earlier}
	f.run(t, "\n", "start", "001", "--seed", "1", "--no-shell")
	f.run(t, "\n\n\n", "check")
	_, out, _ := f.run(t, "the probe port must match\n", "solution", "001")
	mustContain(t, out, "Which principle made this observation necessary? (one line)", "principle recorded")
	last := f.journal.episodes[len(f.journal.episodes)-1]
	if last.Debrief == nil || last.Debrief.Principle != "the probe port must match" {
		t.Errorf("principle: got %+v", last.Debrief)
	}

	same := newFixture(t)
	terminal(same)
	same.grader.grades = []grader.Grade{passGrade()}
	earlier.Variant = "image"
	same.journal.episodes = []journal.Episode{earlier}
	same.run(t, "\n", "start", "001", "--seed", "1", "--no-shell")
	same.run(t, "\n\n\n", "check")
	_, out, _ = same.run(t, "", "solution", "001")
	mustNotContain(t, out, "Which principle made this observation necessary?")
}

func TestMarkdownWrapsAt60WithoutBreakingCode(t *testing.T) {
	f := newFixture(t)
	f.stdoutTTY = true
	f.env = map[string]string{"COLUMNS": "60"}
	a := newApp(Options{Stdout: &strings.Builder{}, IsTerminal: true, Getenv: func(k string) string { return f.env[k] }})
	md := "Run `kubectl -n task-hello-abcde get pods -o wide --show-labels` before you change anything in the deployment.\n\n" +
		"```\nkubectl -n task-hello-abcde set image deploy/hello app=nginx:1.27 --dry-run=server -o yaml\n```\n\n" +
		"> A quoted line that is long enough to wrap around the sixty column limit for sure, yes indeed it is.\n\n" +
		"- A list item that is long enough to wrap around the sixty column limit for sure, yes indeed.\n" +
		"- short\n\n" +
		"1. A numbered item that is long enough to wrap around the sixty column limit for sure.\n"
	plain := stripANSI(a.markdown(md))
	lines := strings.Split(plain, "\n")
	find := func(sub string) int {
		for i, l := range lines {
			if strings.Contains(l, sub) {
				return i
			}
		}
		t.Fatalf("rendered output lacks %q:\n%s", sub, plain)
		return -1
	}
	mustContain(t, plain, "kubectl -n task-hello-abcde get pods -o wide --show-labels", "kubectl -n task-hello-abcde set image deploy/hello app=nginx:1.27 --dry-run=server -o yaml")
	if strings.ContainsRune(plain, codeSpace) {
		t.Errorf("rendered output keeps the code space placeholder:\n%q", plain)
	}
	q := find("| A quoted line")
	if !strings.HasPrefix(strings.TrimLeft(lines[q+1], " "), "| ") {
		t.Errorf("quote continuation lost the bar: %q", lines[q+1])
	}
	li := find("- A list item")
	marker := strings.Index(lines[li], "- A list")
	if cont := lines[li+1]; len(cont)-len(strings.TrimLeft(cont, " ")) != marker+2 {
		t.Errorf("list continuation %q: want a hanging indent of %d", cont, marker+2)
	}
	num := find("1. A numbered")
	nmarker := strings.Index(lines[num], "1. A")
	if cont := lines[num+1]; len(cont)-len(strings.TrimLeft(cont, " ")) != nmarker+3 {
		t.Errorf("numbered continuation %q: want a hanging indent of %d", cont, nmarker+3)
	}
	for _, l := range lines {
		if strings.Contains(l, "kubectl") {
			continue
		}
		if w := len([]rune(strings.TrimRight(l, " "))); w > 60 {
			t.Errorf("line of %d columns over 60: %q", w, l)
		}
	}
}

func TestMarkdownFixedStyleWithNoTermQuery(t *testing.T) {
	render := func(env map[string]string) string {
		a := newApp(Options{Stdout: &strings.Builder{}, IsTerminal: true, Getenv: func(k string) string { return env[k] }})
		return a.markdown("# Title\n\nText with `code`.\n")
	}
	with := render(map[string]string{"KUBEDRILL_NO_TERM_QUERY": "1"})
	without := render(map[string]string{})
	if with != without || !strings.Contains(with, "\x1b[") {
		t.Errorf("styles differ or lack color:\n%q\n%q", with, without)
	}
}

func TestExamHidesPracticeAids(t *testing.T) {
	f := newFixture(t)
	f.catalog.all[1].Meta.Concepts = []string{"pods"}
	start := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	done := solvedEpisode("old", "002-service-dns", 1, start)
	done.Debrief = &journal.Debrief{NextTimeFirst: "the pod events"}
	f.journal.episodes = []journal.Episode{done}
	_, out, _ := f.run(t, "", "start", "001", "--seed", "1", "--exam")
	mustNotContain(t, out, "Fundamentals:", "Last time you said")
	_, out, _ = f.run(t, "", "ticket")
	mustNotContain(t, out, "Fundamentals:")
	_, out, _ = f.run(t, "", "diagnose", "the image tag does not exist")
	mustContain(t, out, "Next: change one thing, then kubedrill submit\n")
	mustNotContain(t, out, "kubedrill hint")
	for _, args := range [][]string{{"learn"}, {"learn", "pods"}, {"watch"}} {
		code, _, errOut := f.run(t, "", args...)
		if code != 1 || !strings.Contains(errOut, args[0]+" is not available in exam mode") {
			t.Errorf("%v in exam without a terminal: exit %d err %q", args, code, errOut)
		}
	}
}

func TestSolutionAfterExamOrFailureAsks(t *testing.T) {
	start := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	exam := solvedEpisode("e", "001-hello-down", 1, start)
	exam.Mode = journal.ModeExam
	failed := solvedEpisode("f", "001-hello-down", 1, start)
	failed.Outcome = journal.OutcomeFailed
	for _, tc := range []struct {
		name string
		ep   journal.Episode
		want string
	}{
		{"exam", exam, "Your latest episode of 001-hello-down was an exam (solved); show the solution? [y/N]"},
		{"failed", failed, "Your latest episode of 001-hello-down ended failed; show the solution anyway? [y/N]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.journal.episodes = []journal.Episode{tc.ep}
			_, out, _ := f.run(t, "n\n", "solution", "001")
			mustContain(t, out, tc.want, "kept the solution of 001-hello-down hidden")
			mustNotContain(t, out, "## Symptom")
		})
	}
	f := newFixture(t)
	f.journal.episodes = []journal.Episode{solvedEpisode("s", "001-hello-down", 1, start)}
	_, out, _ := f.run(t, "", "solution", "001")
	mustNotContain(t, out, "show the solution anyway")
	mustContain(t, out, "## Symptom")
}

func TestWatchTruncatesLongActual(t *testing.T) {
	g := grader.Grade{Results: []grader.Result{{ID: "a", Description: "first", Status: grader.StatusFail, Expected: "1", Actual: strings.Repeat("x", 200)}}}
	m := newTestModel(&fakeWatcher{observe: g})
	m.width = 60
	next, _ := m.Update(pollMsg{grade: g})
	m = next.(watchModel)
	for _, l := range strings.Split(m.View().Content, "\n") {
		if strings.Contains(l, "actual: ") {
			if len(l) != 60 || !strings.HasSuffix(l, "...") {
				t.Errorf("actual line: got %q (%d columns), want 60 ending in ...", l, len(l))
			}
		}
	}
}

func TestLastEndedOfFallsBackToTheRecordedSolve(t *testing.T) {
	f := newFixture(t)
	start := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	f.journal.episodes = []journal.Episode{solvedEpisode("old", "001-hello-down", 1, start.Add(-time.Hour)), solvedEpisode("new", "001-hello-down", 2, start)}
	a := newApp(Options{Factories: f.factories(), Getenv: func(string) string { return "" }})
	if ep := a.lastEndedOf("001-hello-down", start); ep == nil || ep.ID != "new" {
		t.Errorf("lastEndedOf: got %+v, want the episode new", ep)
	}
	if ep := a.lastEndedOf("001-hello-down", start.Add(time.Minute)); ep != nil {
		t.Errorf("lastEndedOf before the episode started: got %+v, want nil", ep)
	}
	if ep := a.lastEndedOf("002-service-dns", start); ep != nil {
		t.Errorf("lastEndedOf of another scenario: got %+v, want nil", ep)
	}
}

func TestStartPrintsLeaveLineBeforeProgress(t *testing.T) {
	f := newFixture(t)
	f.run(t, "", "start", "001", "--seed", "1")
	var both strings.Builder
	code := Main(context.Background(), []string{"--root", f.root, "start", "002", "--yes"}, Options{
		Factories:       f.factories(),
		Stdout:          &both,
		Stderr:          &both,
		Stdin:           strings.NewReader(""),
		Getenv:          func(string) string { return "" },
		Now:             f.nowFunc(),
		Rand:            func(n int64) int64 { return 6 % n },
		NamespaceSuffix: func() string { return "fghij" },
	})
	out := both.String()
	leave := strings.Index(out, "leaving the episode of 001-hello-down (recorded as failed)")
	progress := strings.Index(out, "creating namespace task-dns-fghij... done")
	if code != 0 || leave < 0 || progress < 0 || leave > progress {
		t.Errorf("leave line at %d, progress at %d, exit %d:\n%s", leave, progress, code, out)
	}
}

func TestDoneStatus(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{0, "done"},
		{99 * time.Millisecond, "done"},
		{100 * time.Millisecond, "done (0.1s)"},
		{1234 * time.Millisecond, "done (1.2s)"},
	} {
		if got := doneStatus(tc.d); got != tc.want {
			t.Errorf("doneStatus(%s): got %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestReasonSentence(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"practice: level L0, solved 1 of last 2", "you solved 1 of your last 2 attempts at L0"},
		{"practice: level L0, solved 1 of last 1", "you solved your last attempt at L0"},
		{"practice: level L0, solved 0 of last 1", "you did not solve your last attempt at L0"},
		{"practice: level L1, new scenario", "you have not tried it yet; it is the next one at L1"},
		{"review: concept pods was mastered, last clean solve 4 days ago", "review: you mastered pods 4 days ago"},
		{"review: concept pods was mastered, last clean solve 1 days ago", "review: you mastered pods 1 day ago"},
		{"something else", "something else"},
	} {
		if got := reasonSentence(tc.in); got != tc.want {
			t.Errorf("reasonSentence(%q): got %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSolvedBannerRecommendedLine(t *testing.T) {
	f := newFixture(t)
	f.grader.grades = []grader.Grade{passGrade()}
	f.run(t, "", "start", "001", "--seed", "1")
	_, out, _ := f.run(t, "", "check")
	mustContain(t, out, "Solved: Hello app is down in 3.0 min with 0 hints (variant image)\nRecommended: Hello app is down (001-hello-down, L0, 15 min): you solved your last attempt at L0\n")
}

func TestDebriefVerdictUnknownAndRedoKeepsAnswers(t *testing.T) {
	f := newFixture(t)
	terminal(f)
	f.catalog.all[0].Meta.FixTargets = []string{"Apps/Deployments/hello"}
	ep := solvedEpisode("e1", "001-hello-down", 1, time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC))
	ep.Telemetry = &journal.Telemetry{Source: "shell", FirstWriteTarget: "apps/deployments/hello", Verdict: "unknown"}
	ep.Debrief = &journal.Debrief{RuledOutEarlier: "the events", NextTimeFirst: "kubectl describe"}
	f.journal.episodes = []journal.Episode{ep}
	_, out, _ := f.run(t, "\nkubectl get events\n", "debrief", "--redo")
	mustContain(t, out,
		"  verdict: unknown (telemetry off)\n",
		"  first write: apps/deployments/hello (a fix target)\n",
		"Which observation would have ruled out your first hypothesis earlier? [the events] ",
		"Next time I check first: [kubectl describe] ",
	)
	got := f.journal.episodes[0].Debrief
	if got == nil || got.RuledOutEarlier != "the events" || got.NextTimeFirst != "kubectl get events" {
		t.Errorf("redo: got %+v, want the first answer kept and the second replaced", got)
	}
}

func TestStatsVerdictsCountSolvedOnly(t *testing.T) {
	f := newFixture(t)
	start := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	solved := solvedEpisode("s", "001-hello-down", 1, start)
	solved.Telemetry = &journal.Telemetry{Verdict: "root-cause"}
	failed := solvedEpisode("f", "001-hello-down", 2, start.Add(time.Hour))
	failed.Outcome = journal.OutcomeFailed
	failed.Telemetry = &journal.Telemetry{Verdict: "symptom-only"}
	f.journal.episodes = []journal.Episode{solved, failed}
	_, out, _ := f.run(t, "", "stats")
	mustContain(t, out, "root-cause  1", "SESSIONS: sittings among those clean solves")
	mustNotContain(t, out, "symptom-only")
}

func TestJournalModeAndPathHidesRecommendation(t *testing.T) {
	f := newFixture(t)
	start := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	review := solvedEpisode("r", "001-hello-down", 600_000, start)
	exam := solvedEpisode("e", "002-service-dns", 1, start.Add(time.Hour))
	exam.Mode = journal.ModeExam
	f.journal.episodes = []journal.Episode{review, exam}
	_, out, _ := f.run(t, "", "journal")
	mustContain(t, out, "SEED    MODE", "600000  review", "1       exam")
	_, out, _ = f.run(t, "", "path")
	mustContain(t, out, "\nNext: ")
	f.run(t, "", "start", "001", "--seed", "1")
	_, out, _ = f.run(t, "", "path")
	mustNotContain(t, out, "\nNext: ", "kubedrill start")
	mustContain(t, out, "Active: 001-hello-down (seed 1); the recommendation returns once it ends")
}

type createdCluster struct {
	*fakeCluster
	created time.Time
}

func (c createdCluster) Created(context.Context) (time.Time, error) { return c.created, nil }

func TestTelemetryNoticeOncePerJournalAndCluster(t *testing.T) {
	f := newFixture(t)
	f.cluster.audit = false
	const notice = "telemetry: audit log off"
	_, _, errOut := f.run(t, "", "start", "001", "--seed", "1")
	mustContain(t, errOut, notice)
	_, _, errOut = f.run(t, "", "start", "001", "--seed", "2", "--yes")
	mustNotContain(t, errOut, notice)
	if _, err := os.Stat(filepath.Join(f.journal.dir, telemetryNoticeFile)); err != nil {
		t.Errorf("marker: %v", err)
	}

	g := newFixture(t)
	g.cluster.audit = false
	cc := createdCluster{fakeCluster: g.cluster, created: time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)}
	fs := g.factories()
	fs.Cluster = func(string) Cluster { return cc }
	run := func(seed string) string {
		var out, errb strings.Builder
		Main(context.Background(), []string{"--root", g.root, "start", "001", "--seed", seed, "--yes"}, Options{Factories: fs, Stdout: &out, Stderr: &errb, Stdin: strings.NewReader(""), Getenv: func(string) string { return "" }, Now: g.nowFunc(), NamespaceSuffix: func() string { return "abcde" }})
		return errb.String()
	}
	mustContain(t, run("1"), notice)
	mustNotContain(t, run("2"), notice)
	cc.created = cc.created.Add(24 * time.Hour)
	fs.Cluster = func(string) Cluster { return cc }
	mustContain(t, run("3"), notice)
	g.cluster.audit = true
	cc.created = cc.created.Add(24 * time.Hour)
	fs.Cluster = func(string) Cluster { return cc }
	mustNotContain(t, run("4"), notice)
}

func TestApproachRenderedSnippetAndContent(t *testing.T) {
	f := newFixture(t)
	f.catalog.all[0].Approaches = []scenario.Approach{{Name: "set-image", Snippet: "kubectl -n {{ .Namespace }} set image deploy/hello app=nginx:1.27", Body: "# Set image\n\nFast in {{ .Namespace }}.\n"}}
	ep := solvedEpisode("s", "001-hello-down", 1, time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC))
	ep.Namespace = "task-hello-zzzzz"
	f.journal.episodes = []journal.Episode{ep}
	_, out, _ := f.run(t, "", "solution", "001")
	mustContain(t, out, "  set-image\n      kubectl -n task-hello-zzzzz set image deploy/hello app=nginx:1.27\n")
	code, out, errOut := f.run(t, "", "solution", "001", "--approach", "set-image")
	if code != 0 {
		t.Fatalf("approach exit %d: %s", code, errOut)
	}
	mustContain(t, out, "Approach set-image (001-hello-down)\n\n    kubectl -n task-hello-zzzzz set image deploy/hello app=nginx:1.27\n\n# Set image\n\nFast in task-hello-zzzzz.\n")
}

func TestReplayMapsWorkstationPathToJournal(t *testing.T) {
	f := newFixture(t)
	terminal(f)
	withWorkstation(f)
	ep := solvedEpisode("001-a", "001-hello-down", 1, time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC))
	ep.Telemetry = &journal.Telemetry{Recordings: []string{"/data/recordings/task-hello-abcde-1761382800.cast"}}
	f.journal.episodes = []journal.Episode{ep}
	code, out, errOut := f.run(t, "", "replay")
	if code != 0 {
		t.Fatalf("replay exit %d: %s", code, errOut)
	}
	if got := strings.Join(f.ws.replays, ","); got != "task-hello-abcde-1761382800.cast" {
		t.Errorf("replays: got %q, want the base name under the journal", got)
	}
	mustContain(t, out, "replaying task-hello-abcde-1761382800.cast (space pause, q quit)\n")
	if strings.Count(out, "\n") != 1 {
		t.Errorf("replay printed more than one line:\n%s", out)
	}
}

type failingFaults struct {
	fakeFaults
	err error
}

func (m *failingFaults) Apply(context.Context, faults.Action, string, faults.NetemOptions) error {
	return m.err
}

func TestFaultAlreadyActiveAndUnknownNode(t *testing.T) {
	f := newFixture(t)
	ff := &failingFaults{err: fmt.Errorf("fault pause on worker: %w since 2026-10-25T12:00:00Z", faults.ErrAlreadyActive)}
	fs := f.factories()
	fs.Faults = func(string) Faults { return ff }
	run := func(args ...string) (int, string) {
		var out, errb strings.Builder
		code := Main(context.Background(), append([]string{"--root", f.root}, args...), Options{Factories: fs, Stdout: &out, Stderr: &errb, Stdin: strings.NewReader(""), Getenv: func(string) string { return "" }})
		return code, errb.String()
	}
	if code, errOut := run("fault", "pause", "worker"); code != 1 || errOut != "kubedrill: worker is already paused\n" {
		t.Errorf("already active: exit %d err %q", code, errOut)
	}
	ff.err = fmt.Errorf("%w %q; valid nodes: control-plane, worker, worker2", faults.ErrUnknownNode, "worker")
	if code, errOut := run("fault", "pause", "worker"); code != 1 || errOut != "kubedrill: unknown node \"worker\"; valid: control-plane, worker, worker2\n" {
		t.Errorf("unknown node from the manager: exit %d err %q", code, errOut)
	}
	if !errors.Is(refusal{cause: faults.ErrAlreadyActive}, faults.ErrAlreadyActive) {
		t.Error("refusal does not unwrap to its cause")
	}
}

func TestSmokeSolveGetsBinAndEpisode(t *testing.T) {
	f := newFixture(t)
	f.grader.grades = []grader.Grade{failGrade(), passGrade()}
	f.catalog.all = f.catalog.all[1:]
	if code, out, errOut := f.run(t, "", "smoke"); code != 0 {
		t.Fatalf("smoke exit %d: %s %s", code, out, errOut)
	}
	if len(f.runs) != 1 {
		t.Fatalf("runs: %v", f.runs)
	}
	env := strings.Join(f.runs[0][1:], " ")
	mustContain(t, env, "KUBEDRILL_BIN=/usr/local/bin/kubedrill", "KUBEDRILL_EPISODE=002-service-dns-")
}

func TestRunWatchQuitBeforeSolvePrintsNext(t *testing.T) {
	f := newFixture(t)
	var out strings.Builder
	a := newApp(Options{Factories: f.factories(), Stdout: &out, Stderr: &out, Stdin: strings.NewReader("q"), Getenv: func(string) string { return "" }, Now: f.nowFunc()})
	ep := &journal.Episode{ScenarioID: "001-hello-down", Seed: 3, StartedAt: f.nowFunc()()}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.runWatch(ctx, &fakeWatcher{observe: failGrade()}, ep, "Hello app is down", 2, 2, time.Hour); err != nil {
		t.Fatalf("runWatch: %v", err)
	}
	mustContain(t, out.String(), "Next: kubedrill check | kubedrill hint\n")
}

func TestRunWatchNextRunsDebriefBeforeStarting(t *testing.T) {
	f := newFixture(t)
	r, w := io.Pipe()
	var out lockedBuilder
	a := newApp(Options{Factories: f.factories(), Stdout: &out, Stderr: &out, Stdin: r, Getenv: func(string) string { return "" }, Now: f.nowFunc(), IsTerminal: true})
	a.teaOptions = []tea.ProgramOption{tea.WithWindowSize(50, 24)}
	ep := &journal.Episode{ScenarioID: "001-hello-down", Seed: 3, StartedAt: f.nowFunc()()}
	fw := &fakeWatcher{observe: passGrade()}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer w.Close()
		if !awaitOutput(ctx, out.String, "n: debrief, then start 001-hello-down") {
			return
		}
		w.Write([]byte("n"))
		if !awaitOutput(ctx, out.String, "starting 001-hello-down: ") {
			return
		}
		for {
			select {
			case <-done:
				return
			default:
			}
			if _, err := w.Write([]byte("q")); err != nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	err := a.runWatch(ctx, fw, ep, "Hello app is down", 2, 2, time.Hour)
	close(done)
	r.Close()
	if err != nil {
		t.Fatalf("runWatch: %v", err)
	}
	got := out.String()
	debrief := strings.Index(got, "Debrief: 001-hello-down")
	start := strings.Index(got, "starting 001-hello-down: ")
	if debrief < 0 || start < 0 || debrief > start || len(fw.starts) != 1 {
		t.Fatalf("debrief at %d, start at %d, starts %v:\n%s", debrief, start, fw.starts, got)
	}
	if a.paneWidth != 50 {
		t.Errorf("pane width after watch: got %d, want 50", a.paneWidth)
	}
	for _, l := range strings.Split(stripANSI(got[debrief:start]), "\n") {
		if lipgloss.Width(l) > 50 {
			t.Errorf("debrief line of %d columns after a 50 column pane: %q", lipgloss.Width(l), l)
		}
	}
	mustContain(t, got[debrief:start], "your diagnosis: none recorded")
}

// awaitOutput polls read until it contains want or ctx ends; it reports
// whether want appeared.
func awaitOutput(ctx context.Context, read func() string, want string) bool {
	for {
		if strings.Contains(read(), want) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(10 * time.Millisecond):
		}
	}
}

type lockedBuilder struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuilder) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuilder) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
