package cli

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/cauebraz/kubedrill/internal/grader"
	"github.com/cauebraz/kubedrill/internal/journal"
	"github.com/cauebraz/kubedrill/internal/scenario"
	"github.com/cauebraz/kubedrill/internal/web"
)

func (f *fixture) webBackend(t *testing.T) *webBackend {
	t.Helper()
	var out, errb strings.Builder
	a := newApp(Options{
		Factories:       f.factories(),
		Stdout:          &out,
		Stderr:          &errb,
		Stdin:           strings.NewReader(""),
		Getenv:          func(k string) string { return f.env[k] },
		Getwd:           func() (string, error) { return f.root, nil },
		Now:             f.nowFunc(),
		Executable:      func() (string, error) { return "/usr/local/bin/kubedrill", nil },
		Rand:            func(n int64) int64 { return 6 % n },
		NamespaceSuffix: func() string { return "abcde" },
	})
	a.rootFlag = f.root
	return &webBackend{a: a}
}

func TestWebBackendCatalogAndAssignment(t *testing.T) {
	f := newFixture(t)
	b := f.webBackend(t)
	ctx := context.Background()

	c, err := b.Catalog(ctx)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if sess, err := b.Session(ctx, true); err != nil || sess.Active != nil {
		t.Errorf("Session without an episode: got %+v %v, want an empty session and no error", sess, err)
	}
	m := c.Levels[0].Modules[0]
	if len(m.Assignments) != 1 || m.Assignments[0].ID != "001-hello-down" || m.Assignments[0].Status != "todo" || m.Planned != 1 {
		t.Errorf("module: got %+v, want 001-hello-down todo and 1 planned", m)
	}

	a, err := b.Assignment(ctx, "001")
	if err != nil {
		t.Fatalf("Assignment: %v", err)
	}
	if a.ID != "001-hello-down" || a.ModuleTitle != "Workloads" || !strings.Contains(a.Story, "task-hello") || a.Simulation != nil {
		t.Errorf("assignment: got %+v", a)
	}
	if _, err := b.Assignment(ctx, "999"); err == nil {
		t.Error("Assignment of an unknown scenario: got nil error")
	}
}

func TestWebBackendEpisodeFlow(t *testing.T) {
	f := newFixture(t)
	withWorkstation(f)
	f.grader.grades = []grader.Grade{
		{Results: []grader.Result{{ID: "ready", Description: "deployment hello has 2 ready replicas", Status: grader.StatusFail, Actual: "secret", Spoiler: true}}},
		{Passed: true, Score: 1, Results: []grader.Result{{ID: "ready", Status: grader.StatusPass}}},
	}
	b := f.webBackend(t)
	ctx := context.Background()

	sess, err := b.Start(ctx, "001", web.StagePractice, false, nil)
	if err != nil {
		t.Fatalf("Start practice: %v", err)
	}
	if sess.Active == nil || sess.Active.ScenarioID != "001-hello-down" || sess.Active.HintsTotal != 2 {
		t.Fatalf("started: got %+v", sess.Active)
	}
	cur, _ := f.journal.Current()
	if cur.Variant != "image" || !cur.Guided {
		t.Errorf("practice episode: got variant %q guided %v, want the first variant, image, guided", cur.Variant, cur.Guided)
	}
	if !strings.Contains(sess.Active.Story, "Point the image at a tag that exists.") {
		t.Errorf("guided practice story: got %q, want the fix section", sess.Active.Story)
	}
	if _, err := b.Start(ctx, "001", web.StageDIY, false, nil); !errors.Is(err, web.ErrActiveElsewhere) {
		t.Errorf("Start over an active episode: got %v, want ErrActiveElsewhere", err)
	}

	live, err := b.Session(ctx, true)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if len(live.Checks) != 1 || live.Checks[0].Actual != "" || live.Checks[0].Status != "fail" {
		t.Errorf("live checks: got %+v, want one failing check without its spoiler value", live.Checks)
	}

	if err := b.Diagnose(ctx, "the image tag does not exist"); err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	h, err := b.Hint(ctx)
	if err != nil || h.Index != 1 || h.Total != 2 || h.HTML != "Look at the pod events." {
		t.Errorf("Hint: got %+v %v", h, err)
	}

	v, err := b.Validate(ctx)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !v.Passed || v.RootCause == "" || v.Diagnosis != "the image tag does not exist" || !strings.Contains(v.Solution, "set image") {
		t.Errorf("validation: got %+v", v)
	}
	if cur, _ := f.journal.Current(); cur != nil {
		t.Errorf("episode still active after a passing validate: %+v", cur)
	}

	if _, err := b.Start(ctx, "001", web.StageDIY, false, nil); err != nil {
		t.Fatalf("Start DIY: %v", err)
	}
	cur, _ = f.journal.Current()
	if cur.Variant != "probe" || cur.Guided {
		t.Errorf("DIY episode: got variant %q guided %v, want another variant than practice's, probe, unguided", cur.Variant, cur.Guided)
	}
	diy, err := b.Session(ctx, false)
	if err != nil {
		t.Fatalf("Session of the DIY run: %v", err)
	}
	asg, err := b.Assignment(ctx, "001")
	if err != nil {
		t.Fatalf("Assignment during the DIY run: %v", err)
	}
	for name, story := range map[string]string{"session": diy.Active.Story, "assignment": asg.Story} {
		if strings.Contains(story, "Point the image at a tag that exists.") || !strings.Contains(story, storyFixClosed) {
			t.Errorf("%s story during the DIY run: got %q, want the fix section closed", name, story)
		}
	}
}

func TestWebBackendTerminal(t *testing.T) {
	f := newFixture(t)
	withWorkstation(f)
	b := f.webBackend(t)
	cmd, cleanup, err := b.Terminal(context.Background(), "kubedrill-web-1234")
	if err != nil {
		t.Fatalf("Terminal: %v", err)
	}
	if cmd == nil || cleanup == nil {
		t.Fatal("Terminal: nil command or cleanup")
	}
	o := f.ws.shells[len(f.ws.shells)-1]
	if o.Name != "kubedrill-web-1234" || o.Exam || !o.Web || o.Kubeconfig == "" {
		t.Errorf("shell options: got %+v", o)
	}
	cleanup()
	if len(f.ws.removed) != 1 || f.ws.removed[0] != "kubedrill-web-1234" {
		t.Errorf("removed containers: got %v", f.ws.removed)
	}
}

func TestStageSeed(t *testing.T) {
	two := &scenario.Scenario{Variants: []scenario.Variant{{Name: "a"}, {Name: "b"}}}
	one := &scenario.Scenario{Variants: []scenario.Variant{{Name: "only"}}}
	seeds := two.VariantSeeds()
	if got, _ := stageSeed(two, web.StagePractice); got != seeds[0] {
		t.Errorf("practice seed: got %d, want the first variant's %d", got, seeds[0])
	}
	for i := 0; i < 10; i++ {
		got, _ := stageSeed(two, web.StageDIY)
		if two.VariantFor(got).Name == "a" {
			t.Fatalf("DIY seed %d maps to the practice variant", got)
		}
	}
	if got, _ := stageSeed(one, web.StageDIY); got != 0 {
		t.Errorf("DIY seed of a single-variant scenario: got %d, want 0 (draw one)", got)
	}
}

func TestWebBackendNextDebriefAndExams(t *testing.T) {
	f := newFixture(t)
	f.curr.Levels[0].Modules[0].Exam = []string{"CKA:workloads"}
	withWorkstation(f)
	f.grader.grades = []grader.Grade{{Passed: true, Score: 1, Results: []grader.Result{{ID: "ready", Status: grader.StatusPass}}}}
	b := f.webBackend(t)
	ctx := context.Background()

	n, err := b.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if n.Active != nil || n.ScenarioID == "" || n.Reason == "" || n.Title == "" {
		t.Errorf("Next without an episode: got %+v, want a recommendation with a reason", n)
	}

	c, err := b.Catalog(ctx)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if len(c.Exams) != 1 || c.Exams[0].ID != "CKA" || len(c.Exams[0].Domains) != 1 || c.Exams[0].Domains[0].Assignments == nil {
		t.Errorf("exams: got %+v", c.Exams)
	}
	if m := c.Levels[0].Modules[0].Assignments[0].Mastery; m.Variants != 2 || m.Mastered {
		t.Errorf("mastery: got %+v, want 2 variants, not mastered", m)
	}

	if _, err := b.Start(ctx, "001", web.StagePractice, false, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	n, _ = b.Next(ctx)
	if n.Active == nil || n.Active.ScenarioID != "001-hello-down" || n.Active.Minutes != 15 || n.Active.StartedAt.IsZero() {
		t.Errorf("Next with an episode: got %+v", n.Active)
	}
	if err := b.Diagnose(ctx, "the image tag does not exist"); err != nil {
		t.Fatal(err)
	}
	cur, _ := f.journal.Current()
	if err := b.RecordDebrief(ctx, cur.ID, web.DebriefAnswers{RuledOutEarlier: "x"}); err == nil {
		t.Error("RecordDebrief of an active episode: got nil error")
	}
	v, err := b.Validate(ctx)
	if err != nil || !v.Passed {
		t.Fatalf("Validate: %+v %v", v, err)
	}
	d, err := b.Debrief(ctx, v.EpisodeID)
	if err != nil {
		t.Fatalf("Debrief: %v", err)
	}
	if d.Outcome != "solved" || d.RootCause == "" || d.Diagnosis != "the image tag does not exist" || d.Minutes != 15 || len(d.FixTargets) != 1 {
		t.Errorf("debrief: got %+v", d)
	}
	yes := true
	if err := b.RecordDebrief(ctx, v.EpisodeID, web.DebriefAnswers{Matched: &yes, RuledOutEarlier: "  the pod events  ", NextTimeFirst: "describe"}); err != nil {
		t.Fatalf("RecordDebrief: %v", err)
	}
	d, _ = b.Debrief(ctx, v.EpisodeID)
	if d.Answers.RuledOutEarlier != "the pod events" || d.Matched == nil || !*d.Matched {
		t.Errorf("recorded debrief: got %+v", d)
	}
	if _, err := b.Debrief(ctx, "nope"); err == nil {
		t.Error("Debrief of an unknown episode: got nil error")
	}

	c, _ = b.Catalog(ctx)
	if got := c.Exams[0].Domains[0]; !slices.Contains(got.Assignments, "001-hello-down") || got.Solved != 0 {
		t.Errorf("exam domain after a guided solve: got %+v, want 001-hello-down listed and 0 solved", got)
	}
	if _, err := b.Start(ctx, "001", web.StageDIY, false, nil); err != nil {
		t.Fatalf("Start DIY: %v", err)
	}
	if v, err := b.Validate(ctx); err != nil || !v.Passed {
		t.Fatalf("Validate DIY: %+v %v", v, err)
	}
	c, _ = b.Catalog(ctx)
	if got := c.Exams[0].Domains[0]; got.Solved != 1 {
		t.Errorf("exam domain after a DIY solve: got %+v, want 1 solved", got)
	}
}

func TestWebBackendPreviewReloadsTheCatalog(t *testing.T) {
	f := newFixture(t)
	b := f.webBackend(t)
	calls := 0
	b.a.f.Catalog = func(string, []string) (Catalog, error) { calls++; return f.catalog, nil }
	b.reload = true
	for i := 0; i < 2; i++ {
		if _, err := b.Assignment(context.Background(), "001"); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Errorf("catalog loads in a preview: got %d, want one per request", calls)
	}
}

func TestWebCommandRefusesBadFlags(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"web", "--preview", "999", "--no-open"}, "999"},
		{[]string{"web", "--static-dir", t.TempDir(), "--no-open"}, "no index.html there"},
		{[]string{"web", "--port", "0"}, "port must be 1 to 65535"},
	}
	for _, tc := range cases {
		code, _, errOut := f.run(t, "", tc.args...)
		if code == 0 || !strings.Contains(errOut, tc.want) {
			t.Errorf("%v: exit %d, stderr %q, want an error containing %q", tc.args, code, errOut, tc.want)
		}
	}
}

func TestDraftGuideYAMLLoads(t *testing.T) {
	out := draftGuideYAML("001-x", []scenario.GuideStep{{Title: `Observe: "quoted" <pod>`, Body: "TODO", Command: "kubectl get pods -o jsonpath='{.items[*].metadata.name}'"}})
	var sim scenario.Simulation
	if err := yaml.UnmarshalStrict([]byte(out), &sim); err != nil {
		t.Fatalf("draft does not load: %v\n%s", err, out)
	}
	if len(sim.Guide) != 1 || sim.Guide[0].Title != `Observe: "quoted" <pod>` || !strings.Contains(sim.Guide[0].Command, "{.items[*]") {
		t.Errorf("round trip: got %+v", sim.Guide)
	}
}

func TestDraftGuideCommand(t *testing.T) {
	f := newFixture(t)
	code, out, errOut := f.run(t, "", "draft-guide", "001")
	if code != 0 || !strings.Contains(out, `command: "kubectl set image deploy/hello app=nginx:1.27"`) {
		t.Errorf("draft-guide: exit %d, out %q, err %q", code, out, errOut)
	}
	code, out, _ = f.run(t, "", "draft-guide", "001", "--check")
	if code != 0 || !strings.Contains(out, "has no simulation.yaml") {
		t.Errorf("draft-guide --check without a simulation: exit %d, out %q", code, out)
	}
}

// TestWebTelemetryOutlivesTheFirstRequest pins that the session the
// workspace caches does not keep the context of the request that created
// it: a later Validate must still ask the cluster whether the audit log is
// on.
func TestWebTelemetryOutlivesTheFirstRequest(t *testing.T) {
	f := newFixture(t)
	withWorkstation(f)
	f.grader.grades = []grader.Grade{{Passed: true, Score: 1, Results: []grader.Result{{ID: "ready", Status: grader.StatusPass}}}}
	b := f.webBackend(t)
	var asked []bool
	b.a.f.Telemetry = func(_ string, auditAvailable func() bool) TelemetryFunc {
		return func(journal.Episode, *scenario.Rendered) journal.Telemetry {
			asked = append(asked, auditAvailable())
			return journal.Telemetry{Source: "audit"}
		}
	}
	if code, _, errOut := f.run(t, "", "start", "001", "--seed", "1"); code != 0 {
		t.Fatalf("start from the terminal: %s", errOut)
	}
	first, cancel := context.WithCancel(context.Background())
	if _, err := b.Session(first, false); err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := b.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(asked) == 0 || !asked[len(asked)-1] {
		t.Errorf("audit available at validate: got %v, want true after the first request's context ended", asked)
	}
}

func TestWebExamSubmitAndExpiry(t *testing.T) {
	ctx := context.Background()
	t.Run("submit before the deadline", func(t *testing.T) {
		f := newFixture(t)
		withWorkstation(f)
		f.grader.grades = []grader.Grade{{Passed: true, Score: 1, Results: []grader.Result{{ID: "ready", Status: grader.StatusPass}}}}
		b := f.webBackend(t)
		sess, err := b.Start(ctx, "001", web.StageExam, false, nil)
		if err != nil {
			t.Fatal(err)
		}
		if a := sess.Active; a == nil || !a.Exam || a.Deadline == nil || a.HintsTotal != 0 || a.Story != "" || len(a.Guide) != 0 {
			t.Fatalf("exam session: got %+v, want exam with a deadline and no aids", sess.Active)
		}
		if _, err := b.Validate(ctx); err == nil {
			t.Error("Validate during an exam: got nil error, want the exam refusal")
		}
		if d, err := b.Debrief(ctx, sess.Active.EpisodeID); err == nil {
			t.Errorf("Debrief of the running exam: got %+v, want a refusal that keeps the walkthrough hidden", d)
		}
		v, err := b.Submit(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !v.Passed || v.Outcome != "solved" || v.RootCause == "" || v.Solution == "" {
			t.Errorf("submit: got %+v", v)
		}
		if _, err := b.Submit(ctx); err == nil {
			t.Error("second submit: got nil error")
		}
		if d, err := b.Debrief(ctx, sess.Active.EpisodeID); err != nil || d.Outcome != "solved" || d.Solution == "" {
			t.Errorf("Debrief after the submit: got %+v, %v", d, err)
		}
	})
	t.Run("past the deadline the exam expires", func(t *testing.T) {
		f := newFixture(t)
		withWorkstation(f)
		f.grader.grades = []grader.Grade{failGrade()}
		b := f.webBackend(t)
		if _, err := b.Start(ctx, "001", web.StageExam, false, nil); err != nil {
			t.Fatal(err)
		}
		f.now = time.Date(2026, 10, 25, 12, 20, 0, 0, time.UTC)
		b = f.webBackend(t)
		v, err := b.Submit(ctx)
		if err != nil {
			t.Fatalf("submit past the deadline: %v", err)
		}
		if v.Passed || v.Outcome != "timed out" || v.RootCause == "" {
			t.Errorf("expired exam: got %+v, want timed out with the root cause", v)
		}
	})
	t.Run("the session expires a due exam", func(t *testing.T) {
		f := newFixture(t)
		withWorkstation(f)
		f.grader.grades = []grader.Grade{failGrade()}
		b := f.webBackend(t)
		if _, err := b.Start(ctx, "001", web.StageExam, false, nil); err != nil {
			t.Fatal(err)
		}
		f.now = time.Date(2026, 10, 25, 12, 20, 0, 0, time.UTC)
		sess, err := f.webBackend(t).Session(ctx, false)
		if err != nil || sess.Active != nil {
			t.Errorf("session after the deadline: got %+v %v, want no active episode", sess.Active, err)
		}
	})
}

func TestMasteryViewCleanVariants(t *testing.T) {
	sc := &scenario.Scenario{Meta: scenario.Meta{ID: "001-hello-down", Minutes: 15}, Variants: []scenario.Variant{{Name: "typo-tag"}, {Name: "wrong-repo"}, {Name: "old-registry"}}}
	t0 := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	solve := func(seed int64, variant string, start time.Duration, hints int) journal.Episode {
		s := t0.Add(start)
		end := s.Add(5 * time.Minute)
		return journal.Episode{ID: strconv.FormatInt(seed, 10), ScenarioID: sc.Meta.ID, Seed: seed, Variant: variant, StartedAt: s, EndedAt: &end, Outcome: journal.OutcomeSolved, HintsUsed: hints}
	}
	eps := []journal.Episode{solve(1, "typo-tag", 0, 0), solve(2, "typo-tag", 24*time.Hour, 0), solve(3, "wrong-repo", 48*time.Hour, 2)}
	got := masteryView(sc, eps)
	if got.CleanVariants != 1 || got.VariantsSolved != 2 || got.Variants != 3 || got.CleanInLast != 2 {
		t.Errorf("mastery view: got %+v, want 1 clean variant, 2 variants solved, 3 variants, 2 clean in last", got)
	}
}
