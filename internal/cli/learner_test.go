package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cauebraz/kubedrill/internal/cluster"
	"github.com/cauebraz/kubedrill/internal/env"
	"github.com/cauebraz/kubedrill/internal/grader"
	"github.com/cauebraz/kubedrill/internal/journal"
	"github.com/cauebraz/kubedrill/internal/render"
	"github.com/cauebraz/kubedrill/internal/scenario"
	"github.com/cauebraz/kubedrill/internal/web"
)

func strPtr(s string) *string { return &s }

func terminal(f *fixture) {
	f.stdoutTTY, f.stdinTTY = true, true
	f.env = map[string]string{"NO_COLOR": "1"}
}

func goodTelemetry() *journal.Telemetry {
	return &journal.Telemetry{Source: "audit+shell", ReadsBeforeFirstWrite: 4, FirstWriteTarget: "apps/deployments/hello", Verdict: "root-cause", Commands: []string{"kubectl get pods", "kubectl describe pod hello-1", "kubectl set image deploy/hello app=nginx:1.27"}}
}

func TestAssertion(t *testing.T) {
	yes, no := true, false
	code := 0
	tests := []struct {
		name  string
		check scenario.Check
		want  string
	}{
		{"equals on a path", scenario.Check{Resource: &scenario.ResourceRef{Kind: "Deployment", Name: "hello"}, Path: "{.status.readyReplicas}", Equals: strPtr("2")}, `Deployment hello: {.status.readyReplicas} equals "2"`},
		{"selector list and namespace", scenario.Check{Resource: &scenario.ResourceRef{Kind: "Pod", Selector: "app=hello", Namespace: "other"}, Path: "{.items[*].status.phase}", Contains: "Running", NotEmpty: true}, `Pod list selected by app=hello in namespace other: {.items[*].status.phase} contains "Running" and is not empty`},
		{"exists", scenario.Check{Resource: &scenario.ResourceRef{Kind: "Service", Name: "hello"}, Exists: &yes}, "Service hello: exists"},
		{"absent", scenario.Check{Resource: &scenario.ResourceRef{Kind: "Secret", Name: "token"}, Exists: &no}, "Secret token: does not exist"},
		{"bounds and regex", scenario.Check{Resource: &scenario.ResourceRef{Kind: "HorizontalPodAutoscaler", Name: "web"}, Path: "{.spec.maxReplicas}", GTE: strPtr("3"), LTE: strPtr("10"), Matches: "^[0-9]+$", NotEquals: strPtr("5")}, `HorizontalPodAutoscaler web: {.spec.maxReplicas} does not equal "5" and matches /^[0-9]+$/ and >= 3 and <= 10`},
		{"probe", scenario.Check{Probe: &scenario.Probe{Image: "busybox:1.37", Command: []string{"wget", "-qO-", "http://hello"}}, ExitCode: &code}, `probe pod running "wget -qO- http://hello" (image busybox:1.37): exits with code 0`},
		{"script", scenario.Check{Script: "checks/dns.sh", Contains: "ok"}, `script "checks/dns.sh": contains "ok"`},
		{"spoiler keeps the expected value back", scenario.Check{Resource: &scenario.ResourceRef{Kind: "Deployment", Name: "hello"}, Path: "{.spec.template.spec.containers[0].image}", Matches: "^nginx:1\\.27$", Spoiler: true}, "Deployment hello: {.spec.template.spec.containers[0].image} has the expected value (" + render.SpoilerHidden + ")"},
		{"no validator", scenario.Check{ID: "x", Resource: &scenario.ResourceRef{Kind: "ConfigMap", Name: "cfg"}}, "ConfigMap cfg: is read without error"},
		{"rollout", scenario.Check{ID: "r", Rollout: &scenario.Rollout{Kind: "Deployment", Name: "api"}}, "Deployment api finished rolling out: every pod runs the current template"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := assertion(tc.check); got != tc.want {
				t.Errorf("assertion: got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCheckExplainGradesNothing(t *testing.T) {
	f := newFixture(t)
	f.catalog.all[0].Checks = []scenario.Check{
		{ID: "ready", Description: "deployment hello has 2 ready replicas", Why: "Only ready pods receive traffic from the service.", Critical: true, Resource: &scenario.ResourceRef{Kind: "Deployment", Name: "hello"}, Path: "{.status.readyReplicas}", Equals: strPtr("2")},
		{ID: "svc", Description: "service hello has endpoints", Why: "A service without endpoints refuses every connection.", DependsOn: []string{"ready"}, Resource: &scenario.ResourceRef{Kind: "Endpoints", Name: "hello"}, NotEmpty: true, Path: "{.subsets}"},
	}
	f.run(t, "", "start", "001", "--seed", "1")
	code, out, errOut := f.run(t, "", "check", "--explain")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	mustContain(t, out,
		"001-hello-down is checked by 2 checks; kubedrill check grades them, this list grades nothing",
		"1. ready: deployment hello has 2 ready replicas\n       asserts: Deployment hello: {.status.readyReplicas} equals \"2\"\n       critical: if it does not pass (failed, errored or skipped) the score is 0\n       why: Only ready pods receive traffic from the service.",
		"2. svc: service hello has endpoints\n       asserts: Endpoints hello: {.subsets} is not empty\n       runs after ready\n       why: A service without endpoints",
	)
	if f.grader.n != 0 {
		t.Errorf("explain graded %d times, want 0", f.grader.n)
	}
}

func TestCheckSolvedDebriefPrompts(t *testing.T) {
	f := newFixture(t)
	terminal(f)
	f.telemetry = goodTelemetry()
	f.grader.grades = []grader.Grade{passGrade()}
	f.run(t, "\n", "start", "001", "--seed", "1")
	f.run(t, "", "diagnose", "the image tag does not exist")
	code, out, errOut := f.run(t, "y\nthe pod events\nkubectl get events\n", "check")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	mustContain(t, out,
		"Solved: Hello app is down in 3.0 min with 0 hints (variant image)",
		"Debrief: 001-hello-down (episode ep-1, solved)",
		"  duration 3.0 min, 0 hints",
		"  API reads before the first write: 4 (telemetry: audit+shell)",
		"  first write: apps/deployments/hello (a fix target)",
		"  verdict: root-cause",
		"  your diagnosis: the image tag does not exist\n  root cause: The deployment image tag does not exist in the registry.\nDid your diagnosis match? [y/n] ",
		"Which observation would have ruled out your first hypothesis earlier? (enter to skip) ",
		"Next time I check first: (enter to skip) ",
		"debrief recorded; kubedrill debrief shows it again",
	)
	ep := f.journal.episodes[0]
	if ep.Debrief == nil || ep.Debrief.RuledOutEarlier != "the pod events" || ep.Debrief.NextTimeFirst != "kubectl get events" {
		t.Fatalf("debrief: got %+v", ep.Debrief)
	}
	if ep.Diagnosis == nil || ep.Diagnosis.Matched == nil || !*ep.Diagnosis.Matched {
		t.Errorf("diagnosis matched: got %+v, want true", ep.Diagnosis)
	}
}

func TestCheckSolvedWithoutTerminalPrintsSummaryOnly(t *testing.T) {
	f := newFixture(t)
	f.telemetry = &journal.Telemetry{Source: "audit", FirstWriteTarget: "core/pods/hello-1", Verdict: "symptom-only"}
	f.grader.grades = []grader.Grade{passGrade()}
	f.run(t, "", "start", "001", "--seed", "1")
	code, out, _ := f.run(t, "y\nx\ny\n", "check")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	mustContain(t, out, "Debrief: 001-hello-down", "first write: core/pods/hello-1 (not a fix target; the fix targets are apps/deployments/hello)", "verdict: symptom-only", "  your diagnosis: none recorded", "  root cause: The deployment image tag", "kubedrill debrief --redo answers the questions later")
	mustNotContain(t, out, "Did your diagnosis match?", "Next time I check first:")
	if f.journal.episodes[0].Debrief != nil {
		t.Errorf("debrief stored without a terminal: %+v", f.journal.episodes[0].Debrief)
	}
}

func TestDebriefCommand(t *testing.T) {
	f := newFixture(t)
	start := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	ep := solvedEpisode("001-a", "001-hello-down", 1, start)
	f.journal.episodes = []journal.Episode{ep}
	code, out, _ := f.run(t, "", "debrief")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	mustContain(t, out, "Debrief: 001-hello-down (episode 001-a, solved)", "telemetry: none recorded")
	mustNotContain(t, out, "Next time I check first:")

	terminal(f)
	code, out, errOut := f.run(t, "\nkubectl get endpoints\n", "debrief", "001")
	if code != 0 {
		t.Fatalf("debrief prompt exit %d: %s", code, errOut)
	}
	mustContain(t, out, "Next time I check first: (enter to skip)", "debrief recorded")
	if d := f.journal.episodes[0].Debrief; d == nil || d.NextTimeFirst != "kubectl get endpoints" || d.RuledOutEarlier != "" {
		t.Fatalf("debrief: got %+v", d)
	}
	_, out, _ = f.run(t, "", "debrief")
	mustContain(t, out, "  next time I check first: kubectl get endpoints")
	mustNotContain(t, out, "(enter to skip)")
	_, out, _ = f.run(t, "a\nb\n", "debrief", "--redo")
	mustContain(t, out, "(enter to skip)")
	if d := f.journal.episodes[0].Debrief; d.NextTimeFirst != "b" {
		t.Errorf("redo: got %+v", d)
	}
	f.stdinTTY = false
	if code, _, errOut := f.run(t, "", "debrief", "--redo"); code != 1 || !strings.Contains(errOut, "needs a terminal") {
		t.Errorf("redo without terminal: exit %d err %q", code, errOut)
	}
	if code, _, errOut := f.run(t, "", "debrief", "zzz"); code != 1 || !strings.Contains(errOut, `episode "zzz" not found`) {
		t.Errorf("unknown episode: exit %d err %q", code, errOut)
	}
}

func TestHintNudgeUnderTenSeconds(t *testing.T) {
	f := newFixture(t)
	f.catalog.all[0].Hints = []string{"one", "two", "three"}
	f.run(t, "", "start", "001", "--seed", "1")
	f.now = time.Date(2026, 10, 25, 12, 1, 0, 0, time.UTC)
	_, _, errOut := f.run(t, "", "hint")
	mustNotContain(t, errOut, "tip:")
	f.now = f.now.Add(5 * time.Second)
	code, out, errOut := f.run(t, "", "hint")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	mustContain(t, errOut, "tip: hints are spaced; try one more observation before the next one")
	mustContain(t, out, "hint 2 of 3 (what to observe)", "two")
	f.now = f.now.Add(30 * time.Second)
	f.run(t, "", "diagnose", "the image tag does not exist")
	_, out, errOut = f.run(t, "", "hint")
	mustNotContain(t, errOut, "tip:")
	mustContain(t, out, "hint 3 of 3 (what it means)")
}

func TestDiagnoseValidation(t *testing.T) {
	f := newFixture(t)
	f.run(t, "", "start", "001", "--seed", "1")
	code, _, errOut := f.run(t, "", "diagnose", "short")
	if code != 1 || !strings.Contains(errOut, "diagnosis must be 10 to 300 characters") {
		t.Errorf("short diagnosis: exit %d err %q", code, errOut)
	}
	code, _, errOut = f.run(t, "", "diagnose")
	if code != 1 || !strings.Contains(errOut, `usage: kubedrill diagnose "<what you think is wrong>"`) {
		t.Errorf("no argument: exit %d err %q", code, errOut)
	}
}

func TestExamRefusalsSubmitAndTicket(t *testing.T) {
	f := newFixture(t)
	f.grader.grades = []grader.Grade{failGrade()}
	out := f.startExam(t, 1, false)
	deadline := time.Date(2026, 10, 25, 12, 15, 0, 0, time.UTC).Local().Format("15:04:05")
	mustContain(t, out, "Exam deadline: "+deadline+" (15 min time box); no hints, no solution, no live checks", "Next: kubedrill submit (grade once, ends the exam)")
	mustNotContain(t, out, "kubedrill hint")
	for _, cmd := range [][]string{{"check"}, {"hint"}, {"solution", "--yes"}} {
		code, _, errOut := f.run(t, "", cmd...)
		if code != 1 {
			t.Errorf("%s in exam: exit %d, want 1", cmd[0], code)
		}
		mustContain(t, errOut, "kubedrill: "+cmd[0]+" is not available in exam mode; kubedrill submit grades once\n")
	}
	if code, out, _ := f.run(t, "", "check", "--explain"); code != 0 || !strings.Contains(out, "is checked by 1 check") {
		t.Errorf("check --explain in exam: exit %d out %q", code, out)
	}
	code, out, _ := f.run(t, "", "submit")
	if code != 2 {
		t.Errorf("failed submit: exit %d, want 2", code)
	}
	mustContain(t, out, "[fail] deployment hello has 2 ready replicas", "exam submitted: failed in 3.0 min")
	if got := f.journal.episodes[0].Outcome; got != journal.OutcomeFailed {
		t.Errorf("outcome after failed submit: %s", got)
	}
	if f.grader.n != 1 {
		t.Errorf("grader runs: got %d, want 1 (submit grades once)", f.grader.n)
	}
}

func TestSubmitPassesAndPracticeRefuses(t *testing.T) {
	f := newFixture(t)
	f.grader.grades = []grader.Grade{passGrade()}
	f.run(t, "", "start", "001", "--seed", "1")
	if code, _, errOut := f.run(t, "", "submit"); code != 1 || !strings.Contains(errOut, "submit grades exam episodes") {
		t.Errorf("submit in practice: exit %d err %q", code, errOut)
	}
	f.startExam(t, 1, true)
	code, out, _ := f.run(t, "", "submit")
	if code != 0 {
		t.Fatalf("passing submit exit %d", code)
	}
	mustContain(t, out, "exam submitted: solved", "Solved: Hello app is down", "Debrief: 001-hello-down")
}

func TestExamExpiryKeepsNamespaceOutputClean(t *testing.T) {
	f := newFixture(t)
	f.grader.grades = []grader.Grade{failGrade()}
	f.startExam(t, 1, false)
	f.now = time.Date(2026, 10, 25, 12, 20, 0, 0, time.UTC)
	_, out, errOut := f.run(t, "", "namespace")
	if out != "" {
		t.Errorf("namespace after the deadline printed %q on stdout, want nothing", out)
	}
	mustContain(t, errOut, "exam time box of 001-hello-down expired")
}

func TestExamExpiresOnAnyCommand(t *testing.T) {
	f := newFixture(t)
	f.grader.grades = []grader.Grade{failGrade()}
	f.startExam(t, 1, false)
	f.now = time.Date(2026, 10, 25, 12, 20, 0, 0, time.UTC)
	code, out, errOut := f.run(t, "", "list")
	if code != 0 {
		t.Fatalf("list after the deadline: exit %d %s", code, errOut)
	}
	at := time.Date(2026, 10, 25, 12, 15, 0, 0, time.UTC).Local().Format("15:04")
	mustContain(t, errOut, "exam time box of 001-hello-down expired at "+at+": outcome timed out (1 of 2 checks passed)")
	mustNotContain(t, out, "exam time box")
	if f.journal.current != nil || f.journal.episodes[0].Outcome != journal.OutcomeTimedOut {
		t.Errorf("after expiry: current %+v outcome %s", f.journal.current, f.journal.episodes[0].Outcome)
	}
	_, out, _ = f.run(t, "", "list")
	mustNotContain(t, out, "expired")
}

func TestListYoursAndLast4(t *testing.T) {
	f := newFixture(t)
	base := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	hinted := solvedEpisode("h", "001-hello-down", 3, base.Add(2*time.Hour))
	hinted.HintsUsed = 1
	long := solvedEpisode("l", "001-hello-down", 4, base.Add(3*time.Hour))
	end := long.StartedAt.Add(9 * time.Minute)
	long.EndedAt = &end
	f.journal.episodes = []journal.Episode{solvedEpisode("a", "001-hello-down", 1, base), solvedEpisode("b", "001-hello-down", 2, base.Add(time.Hour)), hinted, long}
	_, out, _ := f.run(t, "", "list")
	mustContain(t, out, "ID               TITLE                          LEVEL  TYPE       MIN  YOURS    LAST4  STATUS", "clean = solved with 0 hints within MIN")
	var row string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "001-hello-down") {
			row = l
		}
	}
	if fields := strings.Fields(row); len(fields) < 5 || strings.Join(fields[len(fields)-4:], " ") != "3.0 min 3/4 solved" {
		t.Errorf("001 row: got %q, want YOURS 3, LAST4 3/4, STATUS solved", row)
	}
	mustContain(t, out, "002-service-dns  Service name does not resolve  L1     break-fix  20   -        -      todo")
}

func TestTicketFooterShowsYourMedian(t *testing.T) {
	f := newFixture(t)
	f.journal.episodes = []journal.Episode{solvedEpisode("a", "001-hello-down", 1, time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC))}
	_, out, _ := f.run(t, "", "start", "001", "--seed", "2")
	mustContain(t, out, "Checked by 1 check: kubedrill check --explain\nExpected: 15 min (yours: 3.0 min median over 1 solve)\n")
}

func TestStartRemindsCheckFirstAndHookEnv(t *testing.T) {
	f := newFixture(t)
	f.catalog.all[1].Meta.Concepts = []string{"pods"}
	f.catalog.all[1].HookScript = "/scenarios/L1/002-service-dns/setup/hook.sh"
	ep := solvedEpisode("a", "001-hello-down", 1, time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC))
	ep.Debrief = &journal.Debrief{NextTimeFirst: "kubectl get endpoints"}
	f.journal.episodes = []journal.Episode{ep}
	_, out, _ := f.run(t, "", "start", "002", "--seed", "1")
	mustContain(t, out, "Last time you said you would check first: kubectl get endpoints\n")
	if len(f.runs) != 1 || !strings.Contains(strings.Join(f.runs[0], " "), "KUBEDRILL_BIN=/usr/local/bin/kubedrill") {
		t.Errorf("hook env: got %v, want KUBEDRILL_BIN", f.runs)
	}
	f.catalog.all[1].Meta.Concepts = []string{"services"}
	_, out, _ = f.run(t, "", "start", "002", "--seed", "1", "--yes")
	mustNotContain(t, out, "Last time you said")
}

func TestStartTelemetryNotice(t *testing.T) {
	tests := []struct {
		name   string
		audit  bool
		err    error
		notice bool
	}{
		{"audit on", true, nil, false},
		{"audit off", false, nil, true},
		{"unknown", false, errors.New("docker unreachable"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.cluster.audit, f.cluster.auditErr = tc.audit, tc.err
			f.grader.grades = []grader.Grade{passGrade()}
			_, _, errOut := f.run(t, "", "start", "001", "--seed", "1")
			const notice = "telemetry: audit log off; recreate the cluster to enable it (kubedrill down --yes && kubedrill up)\n"
			if got := strings.Count(errOut, notice); got != map[bool]int{true: 1, false: 0}[tc.notice] {
				t.Errorf("notice count: got %d in %q", got, errOut)
			}
			if f.cluster.auditAsks != 1 {
				t.Errorf("audit asked %d times, want once per process", f.cluster.auditAsks)
			}
		})
	}
}

func TestFirstExposureOffer(t *testing.T) {
	tests := []struct {
		name    string
		stdin   string
		example bool
	}{
		{"declined", "\n", false},
		{"accepted", "y\n", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			terminal(f)
			_, out, _ := f.run(t, tc.stdin, "start", "001", "--seed", "1")
			mustContain(t, out, "First time with pods, deployments. See a worked example from another variant before your ticket? [y/N] ", "The hello app returns 503")
			got := strings.Contains(out, "Worked example (variant probe, not yours)")
			if got != tc.example {
				t.Fatalf("worked example shown %v, want %v:\n%s", got, tc.example, out)
			}
			if tc.example {
				mustContain(t, out, "## Symptom\n\nThe hello app returns 503 in worked-example.", "## Candidate causes\n\nThe image tag has a typo (probe).", "Observation, change and verify are yours; the method is in kubedrill learn troubleshooting-loop\n", "End of the worked example; your ticket follows.")
				mustNotContain(t, out, "## Observation that splits them", "## Change", "## Verify", "set image")
				if strings.Index(out, "Worked example") > strings.Index(out, "The hello app returns 503") {
					t.Errorf("worked example printed after the ticket:\n%s", out)
				}
			}
		})
	}
	t.Run("not on later exposure, in exam mode or without a terminal", func(t *testing.T) {
		f := newFixture(t)
		terminal(f)
		f.journal.episodes = []journal.Episode{solvedEpisode("a", "001-hello-down", 2, time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC))}
		_, out, _ := f.run(t, "", "start", "001", "--seed", "1")
		mustNotContain(t, out, "First time with")
		f = newFixture(t)
		terminal(f)
		out = f.startExam(t, 1, false)
		mustNotContain(t, out, "First time with")
		f = newFixture(t)
		_, out, _ = f.run(t, "y\n", "start", "001", "--seed", "1")
		mustNotContain(t, out, "First time with")
	})
}

func solvedFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.telemetry = goodTelemetry()
	f.grader.grades = []grader.Grade{passGrade()}
	f.run(t, "", "start", "001", "--seed", "1")
	f.run(t, "", "check")
	return f
}

func TestSolutionAfterSolvePathVsReference(t *testing.T) {
	f := solvedFixture(t)
	code, out, _ := f.run(t, "", "solution")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	mustContain(t, out,
		"The image tag has a typo (image).",
		"## Observation that splits them\n\nThe pod events name the missing tag.",
		"Your path vs the reference\nYour path (3 kubectl commands, from the shell log):\n   1. kubectl get pods\n   2. kubectl describe pod hello-1\n   3. kubectl set image deploy/hello app=nginx:1.27\n",
		"Reference (## Change and ## Verify above):\n   1. kubectl -n task-hello-abcde set image deploy/hello app=nginx:1.27\n   2. kubectl -n task-hello-abcde rollout status deploy/hello\n",
		"Approaches (kubedrill solution --approach <name> prints one)\n  fix-manifest\n      kubectl edit deploy/hello\n  set-image\n      kubectl set image deploy/hello app=nginx:1.27\n",
	)
	mustNotContain(t, out, "Which principle")
	code, out, _ = f.run(t, "", "solution", "--approach", "set-image")
	if code != 0 || !strings.Contains(out, "Approach set-image (001-hello-down)") || !strings.Contains(out, "Fast, but drifts from git.") || strings.Contains(out, "Your path") {
		t.Errorf("--approach: exit %d out %q", code, out)
	}
	code, _, errOut := f.run(t, "", "solution", "--approach", "helm")
	if code != 1 || !strings.Contains(errOut, `approach "helm" not found for 001-hello-down; valid: fix-manifest, set-image`) {
		t.Errorf("unknown approach: exit %d err %q", code, errOut)
	}
}

func TestSolutionPrincipleOnLaterExposure(t *testing.T) {
	f := newFixture(t)
	f.catalog.all[1].Meta.Concepts = []string{"pods"}
	f.journal.episodes = []journal.Episode{solvedEpisode("dns", "002-service-dns", 1, time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC))}
	f.grader.grades = []grader.Grade{passGrade()}
	f.run(t, "", "start", "001", "--seed", "1")
	f.run(t, "", "check")
	terminal(f)
	code, out, _ := f.run(t, "events record why a container cannot start\n", "solution", "001")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	prompt := "Which principle made this observation necessary? (one line) "
	mustContain(t, out, "## Observation that splits them\n"+prompt, "principle recorded", "The pod events name the missing tag.")
	if strings.Index(out, prompt) > strings.Index(out, "The pod events name the missing tag.") {
		t.Errorf("section revealed before the prompt:\n%s", out)
	}
	ep := f.journal.episodes[len(f.journal.episodes)-1]
	if ep.Debrief == nil || ep.Debrief.Principle != "events record why a container cannot start" {
		t.Fatalf("principle: got %+v", ep.Debrief)
	}
	_, out, _ = f.run(t, "", "solution", "001")
	mustContain(t, out, "Your principle: events record why a container cannot start")
	mustNotContain(t, out, prompt)
}

func TestSolutionApproachWhileActive(t *testing.T) {
	f := newFixture(t)
	f.run(t, "", "start", "001", "--seed", "1")
	if code, _, errOut := f.run(t, "", "solution", "--approach", "set-image"); code != 1 || !strings.Contains(errOut, "open after the episode ends") {
		t.Errorf("approach while active: exit %d err %q", code, errOut)
	}
}

func TestStatsTables(t *testing.T) {
	f := newFixture(t)
	eps := masteredEpisodes()
	eps[0].HintsUsed = 2
	eps[1].Telemetry = &journal.Telemetry{Verdict: "root-cause"}
	end := eps[0].StartedAt.Add(time.Minute)
	eps = append(eps, journal.Episode{ID: "z", ScenarioID: "002-service-dns", StartedAt: eps[0].StartedAt, EndedAt: &end, Outcome: journal.OutcomeAbandoned})
	f.journal.episodes = eps
	code, out, _ := f.run(t, "", "stats")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	mustContain(t, out,
		"Solves by level\nLEVEL  ATTEMPTS  SOLVED\nL0     3         3\nL1     4         3\n",
		"median solve: 3.0 min\nhints per solve: 0.3\n",
		"Outcomes\nOUTCOME    EPISODES\nabandoned  1\nsolved     6\n",
		"Verdicts\nVERDICT",
		"root-cause",
		"Concepts\nCONCEPT      ATTEMPTS  SOLVED  LAST4  SESSIONS  MASTERED\n",
		"pods ",
	)
	mustNotContain(t, out, "streak")
}

func TestFaultAndHeal(t *testing.T) {
	f := newFixture(t)
	if code, _, errOut := f.run(t, "", "heal"); code != 1 || !strings.Contains(errOut, "no fault manager is wired") {
		t.Errorf("heal without manager: exit %d err %q", code, errOut)
	}
	f.faults = &fakeFaults{}
	code, out, errOut := f.run(t, "", "fault", "netem", "worker", "--delay", "200ms", "--loss", "5")
	if code != 0 {
		t.Fatalf("fault exit %d: %s", code, errOut)
	}
	mustContain(t, out, "fault netem applied to worker; kubedrill heal undoes every recorded fault")
	if strings.Join(f.faults.applied, ",") != "netem worker 200ms 5" || f.faults.dir != filepath.Join(f.root, "state", "kubedrill", "kubedrill") {
		t.Errorf("applied %v in %q", f.faults.applied, f.faults.dir)
	}
	if code, _, errOut := f.run(t, "", "fault", "pause", "worker9"); code != 1 || !strings.Contains(errOut, `unknown node "worker9"; valid: control-plane, worker, worker2`) {
		t.Errorf("unknown node: exit %d err %q", code, errOut)
	}
	if code, _, errOut := f.run(t, "", "fault", "pause"); code != 1 || !strings.Contains(errOut, "usage: kubedrill fault <pause|unpause|stop-kubelet|start-kubelet|netem|clear-netem> <control-plane|worker|worker2>") {
		t.Errorf("one argument: exit %d err %q", code, errOut)
	}
	if code, _, errOut := f.run(t, "", "fault", "bogus", "worker"); code != 1 || !strings.Contains(errOut, `kubedrill: unknown action "bogus"; valid: pause, unpause, stop-kubelet, start-kubelet, netem, clear-netem`+"\n") {
		t.Errorf("unknown action: exit %d err %q", code, errOut)
	}
	if code, _, errOut := f.run(t, "", "fault", "netem", "worker", "--loss", "120"); code != 1 || !strings.Contains(errOut, "--loss must be between 0 and 100") {
		t.Errorf("bad loss: exit %d err %q", code, errOut)
	}
	_, out, _ = f.run(t, "", "heal")
	mustContain(t, out, "healed netem on worker")
	_, out, _ = f.run(t, "", "heal")
	mustContain(t, out, "no recorded faults")
	f.run(t, "", "start", "001", "--seed", "1")
	if f.faults.healed != 2 {
		t.Errorf("heals: got %d, want 2 (heal command and start)", f.faults.healed)
	}
}

func TestUpAddons(t *testing.T) {
	f := newFixture(t)
	code, out, _ := f.run(t, "", "up", "--addons", "gateway,kwok,metrics-server")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	mustContain(t, out, "addons: metrics-server, gateway, kwok")
	if len(f.cluster.addons) != 1 || strings.Join(f.cluster.addons[0], ",") != "metrics-server,gateway,kwok" {
		t.Errorf("addons: got %v", f.cluster.addons)
	}
	code, _, errOut := f.run(t, "", "up", "--addons", "mesh,gateway")
	if code != 1 || !strings.Contains(errOut, "unknown addon(s) mesh; valid: gateway, kwok, metrics-server") {
		t.Errorf("unknown addon: exit %d err %q", code, errOut)
	}
}

func TestUpPreloadsImages(t *testing.T) {
	const fixed = "nginx:1.27-alpine@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10"
	f := newFixture(t)
	f.catalog.all[0].Meta.Images = []string{fixed}
	code, out, errOut := f.run(t, "", "up")
	if code != 0 {
		t.Fatalf("exit: got %d, want 0 (%s)", code, errOut)
	}
	if f.cluster.pulls != 1 {
		t.Errorf("preloads: got %d, want 1", f.cluster.pulls)
	}
	if want := []string{cluster.DebugImage, fixed}; !reflect.DeepEqual(f.cluster.pulled, want) {
		t.Errorf("preloaded images: got %v, want %v", f.cluster.pulled, want)
	}
	mustNotContain(t, out+errOut, "warning:")
	f.cluster.pullErr = errors.New("pull busybox on kubedrill-worker: no network")
	code, _, errOut = f.run(t, "", "up")
	if code != 0 {
		t.Fatalf("exit with a failed preload: got %d, want 0 (%s)", code, errOut)
	}
	mustContain(t, errOut, "warning: pull busybox on kubedrill-worker: no network; the nodes pull these images from the registry when a pod first needs them\n")
}

func TestUpWaitsForAPIServices(t *testing.T) {
	f := newFixture(t)
	f.kube.down = [][]string{{"v1beta1.metrics.k8s.io"}, {"v1beta1.metrics.k8s.io"}}
	code, out, _ := f.run(t, "", "up")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	mustContain(t, out, "waiting for API services: v1beta1.metrics.k8s.io")
	mustContain(t, out, "API services available")
	if len(f.kube.down) != 0 {
		t.Errorf("polls left: %v", f.kube.down)
	}
}

func TestReplay(t *testing.T) {
	f := newFixture(t)
	terminal(f)
	withWorkstation(f)
	start := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	ep := solvedEpisode("001-a", "001-hello-down", 1, start)
	ep.Namespace = "task-hello-abcde"
	f.journal.episodes = []journal.Episode{ep}
	code, _, errOut := f.run(t, "", "replay")
	if code != 1 || !strings.Contains(errOut, "episode 001-a (001-hello-down) has no recording") {
		t.Errorf("no recording: exit %d err %q", code, errOut)
	}
	dir := filepath.Join(f.journal.dir, "recordings")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	first := fmt.Sprintf("task-hello-abcde-%d.cast", start.Unix()+1)
	newest := fmt.Sprintf("task-hello-abcde-%d.cast", start.Unix()+100)
	for _, name := range []string{first, newest, "task-hello-abcde-1000.cast", fmt.Sprintf("task-hello-abcde-%d.cast", start.Unix()+3600)} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	code, _, errOut = f.run(t, "", "replay", "001-a")
	if code != 0 {
		t.Fatalf("replay exit %d: %s", code, errOut)
	}
	if strings.Join(f.ws.replays, ",") != newest {
		t.Errorf("replays: got %v, want the newest recording in the episode window", f.ws.replays)
	}
	ep.Telemetry = &journal.Telemetry{Recordings: []string{filepath.Join(dir, first)}}
	f.journal.episodes = []journal.Episode{ep}
	f.run(t, "", "replay")
	if got := f.ws.replays[len(f.ws.replays)-1]; got != first {
		t.Errorf("replay from telemetry: got %q", got)
	}
}

func TestExamTerminalGetsDeadline(t *testing.T) {
	f := newFixture(t)
	withWorkstation(f)
	b := f.webBackend(t)
	if _, err := b.Start(context.Background(), "001", web.StageExam, false, nil); err != nil {
		t.Fatalf("start the exam: %v", err)
	}
	if cur, _ := f.journal.Current(); cur == nil || cur.Mode != journal.ModeExam {
		t.Fatalf("episode: got %+v, want an exam", cur)
	}
	if _, _, err := b.Terminal(context.Background(), "kubedrill-web-exam"); err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 10, 25, 12, 15, 0, 0, time.UTC)
	o := f.ws.shells[len(f.ws.shells)-1]
	if !o.Exam || !o.Deadline.Equal(want) {
		t.Fatalf("exam terminal: got %+v, want Exam and deadline %s", o, want)
	}
}

func TestDiagnosisSentinelsMatch(t *testing.T) {
	err := examRefusal("check")
	if !errors.Is(err, env.ErrExamMode) || err.Error() != "check is not available in exam mode; kubedrill submit grades once" {
		t.Errorf("examRefusal: got %q", err)
	}
	if got := diagnosisNeeded("the last hint").Error(); got != `the last hint needs your diagnosis first: kubedrill diagnose "<what you think is wrong>"` {
		t.Errorf("diagnosisNeeded: got %q", got)
	}
}
