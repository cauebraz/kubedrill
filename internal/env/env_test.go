package env

import (
	"context"
	"errors"
	"io"
	"math"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cauebraz/kubedrill/internal/faults"
	"github.com/cauebraz/kubedrill/internal/grader"
	"github.com/cauebraz/kubedrill/internal/journal"
	"github.com/cauebraz/kubedrill/internal/scenario"
)

const testNS = "task-hello-abcde"

type fakeKube struct {
	calls     *[]string
	labels    map[string]string
	nsLabels  map[string]string
	nsMissing bool
	labelsErr error
	ns        string
	applyNS   string
	n         int
	err       error
	applyErr  error
	down      []string
	downErr   error
	gcErr     error
	deleteErr error
	gc        [][2]string
}

func (k *fakeKube) ResetNamespace(_ context.Context, ns string, labels map[string]string) error {
	*k.calls = append(*k.calls, "create:"+ns)
	k.ns, k.labels = ns, labels
	return k.err
}

func (k *fakeKube) NamespaceLabels(_ context.Context, ns string) (map[string]string, error) {
	*k.calls = append(*k.calls, "labels")
	if k.labelsErr != nil {
		return nil, k.labelsErr
	}
	if k.nsLabels != nil {
		return k.nsLabels, nil
	}
	if k.nsMissing || ns != k.ns {
		return nil, nil
	}
	return k.labels, nil
}

func (k *fakeKube) ApplyManifests(_ context.Context, ns string, manifests []string) error {
	*k.calls = append(*k.calls, "apply")
	k.n, k.applyNS = len(manifests), ns
	return k.applyErr
}

func (k *fakeKube) DeleteNamespaces(_ context.Context, selector, keep string) (int, error) {
	*k.calls = append(*k.calls, "gc")
	k.gc = append(k.gc, [2]string{selector, keep})
	return 2, k.gcErr
}

func (k *fakeKube) DeleteNamespace(_ context.Context, ns string) error {
	*k.calls = append(*k.calls, "delete:"+ns)
	return k.deleteErr
}

func (k *fakeKube) UnavailableAPIServices(context.Context) ([]string, error) {
	*k.calls = append(*k.calls, "apiservices")
	return k.down, k.downErr
}

type fakeHealer struct {
	calls     *[]string
	err       error
	active    []faults.Fault
	stuck     map[string]bool
	healed    bool
	listAfter error
}

func (h *fakeHealer) Heal(context.Context) error {
	*h.calls = append(*h.calls, "heal")
	h.healed = true
	var left []faults.Fault
	for _, f := range h.active {
		if h.stuck[f.Node] {
			left = append(left, f)
		}
	}
	h.active = left
	return h.err
}

func (h *fakeHealer) Active(_ context.Context) ([]faults.Fault, error) {
	if h.healed && h.listAfter != nil {
		return nil, h.listAfter
	}
	return append([]faults.Fault(nil), h.active...), nil
}

type fakeGrader struct {
	grade  grader.Grade
	err    error
	n      int
	during func()
}

func (g *fakeGrader) Grade(context.Context, *scenario.Rendered) (grader.Grade, error) {
	g.n++
	if g.during != nil {
		g.during()
	}
	return g.grade, g.err
}

type fakeJournal struct {
	current    *journal.Episode
	ended      []journal.Episode
	calls      *[]string
	finished   []journal.Outcome
	updates    []journal.Episode
	startErr   error
	now        func() time.Time
	afterStart func()
	modifyErr  error
}

func (j *fakeJournal) Current() (*journal.Episode, error) {
	if j.current == nil {
		return nil, nil
	}
	cp := *j.current
	return &cp, nil
}

func (j *fakeJournal) Start(e journal.Episode) error {
	*j.calls = append(*j.calls, "start")
	if j.startErr != nil {
		return j.startErr
	}
	j.current = &e
	if j.afterStart != nil {
		j.afterStart()
	}
	return nil
}

func (j *fakeJournal) Modify(id string, fn func(*journal.Episode) error) (*journal.Episode, error) {
	if j.modifyErr != nil {
		return nil, j.modifyErr
	}
	if j.current == nil {
		return nil, journal.ErrNoCurrent
	}
	if j.current.ID != id {
		return nil, journal.ErrNotCurrent
	}
	e := *j.current
	e.Grades = slices.Clone(e.Grades)
	e.HintReveals = slices.Clone(e.HintReveals)
	if err := fn(&e); err != nil {
		return nil, err
	}
	if e.Outcome == "" || e.Outcome == journal.OutcomeActive {
		j.updates = append(j.updates, e)
		j.current = &e
		cp := e
		return &cp, nil
	}
	*j.calls = append(*j.calls, "finish:"+string(e.Outcome))
	j.finished = append(j.finished, e.Outcome)
	if e.EndedAt == nil {
		now := j.now()
		e.EndedAt = &now
	}
	j.current = nil
	j.ended = append(j.ended, e)
	cp := e
	return &cp, nil
}

func (j *fakeJournal) UpdateEnded(e journal.Episode) error {
	*j.calls = append(*j.calls, "update-ended:"+e.ID)
	for i := range j.ended {
		if j.ended[i].ID == e.ID {
			j.ended[i] = e
			return nil
		}
	}
	return journal.ErrUnknownEpisode
}

func (j *fakeJournal) Episodes(string) ([]journal.Episode, error) {
	out := append([]journal.Episode(nil), j.ended...)
	if j.current != nil {
		out = append(out, *j.current)
	}
	return out, nil
}

func (j *fakeJournal) JournalID() string { return "0123abcd" }

type fakeCatalog struct {
	sc *scenario.Scenario
}

func (c *fakeCatalog) Find(ref string) (*scenario.Scenario, error) {
	if c.sc == nil || (ref != c.sc.Meta.ID && !strings.HasPrefix(c.sc.Meta.ID, ref)) {
		return nil, scenario.ErrNotFound
	}
	return c.sc, nil
}

func testScenario() *scenario.Scenario {
	return &scenario.Scenario{
		Dir:  "/scenarios/L0/001-hello",
		Meta: scenario.Meta{ID: "001-hello", Title: "Hello", Namespace: "task-hello", Minutes: 10},
		Variants: []scenario.Variant{
			{Name: "image", Vars: map[string]string{"image": "nginx:1.25", "replicas": "2"}},
			{Name: "probe", Vars: map[string]string{"image": "nginx:1.26", "replicas": "3"}},
		},
		Instruction: "Fix hello in {{ .Namespace }}",
		Hints:       []string{"look at events in {{ .Namespace }}", "check the image", "set the image to {{ .Vars.image }}"},
		Solution:    "## Symptom\nhello in {{ .Namespace }} is down",
		Checks:      []scenario.Check{{ID: "ready", Description: "ready"}},
	}
}

type harness struct {
	calls     []string
	progress  []string
	kube      *fakeKube
	healer    *fakeHealer
	grader    *fakeGrader
	journal   *fakeJournal
	catalog   *fakeCatalog
	hooks     [][]string
	hookErr   error
	telemetry []string
	windows   []time.Time
	suffix    string
	now       time.Time
	out       strings.Builder
	s         *Session
}

var t0 = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

func newHarness(t *testing.T, sc *scenario.Scenario, hook string, seed int64) *harness {
	t.Helper()
	h := &harness{now: t0}
	if sc != nil {
		sc.HookScript = hook
	}
	clock := func() time.Time { return h.now }
	h.kube = &fakeKube{calls: &h.calls}
	h.healer = &fakeHealer{calls: &h.calls}
	h.grader = &fakeGrader{}
	h.journal = &fakeJournal{calls: &h.calls, now: clock}
	h.catalog = &fakeCatalog{sc: sc}
	h.s = New(Deps{
		Kube:    h.kube,
		Grader:  h.grader,
		Journal: h.journal,
		Catalog: h.catalog,
		Healer:  h.healer,
		Out:     &h.out,
		Hook: func(_ context.Context, script string, env []string) error {
			h.calls = append(h.calls, "hook")
			h.hooks = append(h.hooks, append([]string{script}, env...))
			return h.hookErr
		},
		Rand: func() int64 { return seed },
		Suffix: func() string {
			if h.suffix != "" {
				return h.suffix
			}
			return "abcde"
		},
		Now:        clock,
		Kubeconfig: "/tmp/kubeconfig",
		Progress: func(msg string) {
			h.calls = append(h.calls, "progress:"+msg)
			h.progress = append(h.progress, msg)
		},
		Telemetry: func(ep journal.Episode, rd *scenario.Rendered) journal.Telemetry {
			h.calls = append(h.calls, "telemetry:"+ep.ID)
			h.telemetry = append(h.telemetry, ep.ID+"@"+rd.Namespace)
			if ep.EndedAt != nil {
				h.windows = append(h.windows, *ep.EndedAt)
			}
			return journal.Telemetry{Source: "audit", Verdict: "root-cause", Reads: 3, Writes: 1, ReadsBeforeFirstWrite: 3}
		},
	})
	return h
}

func (h *harness) start(t *testing.T, mode journal.Mode, seed int64) *journal.Episode {
	t.Helper()
	ep, _, err := h.s.Start(context.Background(), "001-hello", StartOptions{Seed: seed, Mode: mode})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	h.calls = nil
	return ep
}

func TestStartHappyPath(t *testing.T) {
	h := newHarness(t, testScenario(), "", 0)
	ep, rd, err := h.s.Reset(context.Background(), "001", 2)
	if err != nil {
		t.Fatalf("Reset: got err %v, want nil", err)
	}
	wantCalls := []string{"heal", "apiservices", "progress:creating namespace " + testNS, "create:" + testNS, "progress:applying setup", "apply", "start", "gc"}
	if !reflect.DeepEqual(h.calls, wantCalls) {
		t.Errorf("call order:\ngot  %v\nwant %v", h.calls, wantCalls)
	}
	wantLabels := map[string]string{
		"app.kubernetes.io/managed-by": "kubedrill",
		"kubedrill.io/scenario":        "001-hello",
		"kubedrill.io/seed":            "2",
		"kubedrill.io/journal":         "0123abcd",
	}
	if !reflect.DeepEqual(h.kube.labels, wantLabels) {
		t.Errorf("labels: got %v, want %v", h.kube.labels, wantLabels)
	}
	wantGC := [][2]string{{"app.kubernetes.io/managed-by=kubedrill,kubedrill.io/journal=0123abcd", testNS}}
	if !reflect.DeepEqual(h.kube.gc, wantGC) {
		t.Errorf("gc: got %v, want %v", h.kube.gc, wantGC)
	}
	if h.kube.applyNS != testNS {
		t.Errorf("apply namespace: got %q, want %q", h.kube.applyNS, testNS)
	}
	want := journal.Episode{
		ID: "001-hello-" + strconv.FormatInt(t0.Unix(), 10) + "-abcde", ScenarioID: "001-hello", Seed: 2, Variant: "probe",
		StartedAt: t0, Outcome: journal.OutcomeActive, Namespace: testNS, Mode: journal.ModePractice,
	}
	if !reflect.DeepEqual(*ep, want) {
		t.Errorf("episode:\ngot  %+v\nwant %+v", *ep, want)
	}
	if rd.Namespace != testNS || rd.Instruction != "Fix hello in "+testNS || rd.Hints[0] != "look at events in "+testNS {
		t.Errorf("rendered: got ns %q instruction %q hint %q, want the episode namespace everywhere", rd.Namespace, rd.Instruction, rd.Hints[0])
	}
	if h.out.Len() != 0 {
		t.Errorf("output: got %q, want none", h.out.String())
	}
}

func TestStartOrderWithActiveEpisode(t *testing.T) {
	h := newHarness(t, testScenario(), "/scenarios/L0/001-hello/setup/hook.sh", 0)
	first := h.start(t, journal.ModePractice, 1)
	h.now = t0.Add(time.Minute)
	if _, _, err := h.s.Start(context.Background(), "001-hello", StartOptions{Seed: 3}); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	wantCalls := []string{
		"heal", "apiservices", "progress:creating namespace " + testNS, "create:" + testNS, "progress:applying setup", "apply", "hook",
		"telemetry:" + first.ID, "finish:failed", "start", "gc",
	}
	if !reflect.DeepEqual(h.calls, wantCalls) {
		t.Errorf("call order:\ngot  %v\nwant %v", h.calls, wantCalls)
	}
	if len(h.journal.ended) != 1 || h.journal.ended[0].Telemetry == nil || h.journal.ended[0].Outcome != journal.OutcomeFailed {
		t.Errorf("finished episode: got %+v, want failed with telemetry", h.journal.ended)
	}
	wantTele := []string{first.ID + "@" + testNS}
	if !reflect.DeepEqual(h.telemetry, wantTele) {
		t.Errorf("telemetry calls: got %v, want %v", h.telemetry, wantTele)
	}
}

func TestStartRunsHookWithEnv(t *testing.T) {
	h := newHarness(t, testScenario(), "/scenarios/L0/001-hello/setup/hook.sh", 0)
	ep := h.start(t, journal.ModePractice, 1)
	want := []string{
		"/scenarios/L0/001-hello/setup/hook.sh",
		"TASK_NS=" + testNS,
		"SEED=1",
		"VARIANT=image",
		"VAR_IMAGE=nginx:1.25",
		"VAR_REPLICAS=2",
		"KUBECONFIG=/tmp/kubeconfig",
		"KUBEDRILL_EPISODE=" + ep.ID,
	}
	if len(h.hooks) != 1 || !reflect.DeepEqual(h.hooks[0], want) {
		t.Errorf("hook env:\ngot  %v\nwant %v", h.hooks, want)
	}
}

func TestStartDrawsSeed(t *testing.T) {
	tests := []struct {
		rand int64
		want int64
	}{
		{rand: 41, want: 41},
		{rand: -5, want: 6},
		{rand: MaxSeed + 1, want: 2},
		{rand: MaxSeed, want: MaxSeed},
		{rand: 0, want: 1},
	}
	for _, tc := range tests {
		h := newHarness(t, testScenario(), "", tc.rand)
		ep, _, err := h.s.Start(context.Background(), "001-hello", StartOptions{})
		if err != nil {
			t.Fatalf("Start with Rand=%d: %v", tc.rand, err)
		}
		if ep.Seed != tc.want {
			t.Errorf("Rand=%d: seed got %d, want %d", tc.rand, ep.Seed, tc.want)
		}
	}
	h := newHarness(t, testScenario(), "", math.MinInt64)
	ep, _, err := h.s.Start(context.Background(), "001-hello", StartOptions{})
	if err != nil || ep.Seed < 1 || ep.Seed > MaxSeed {
		t.Errorf("Rand=MinInt64: got seed %v err %v, want a seed in range", ep, err)
	}
	d := New(Deps{})
	for i := 0; i < 100; i++ {
		if s := d.d.Rand(); s < 1 || s > 499_999 {
			t.Fatalf("default Rand: got %d, want a practice seed 1..499999", s)
		}
		if s := d.d.Suffix(); len(s) != 5 || strings.Trim(s, "abcdefghijklmnopqrstuvwxyz0123456789") != "" {
			t.Fatalf("default Suffix: got %q, want 5 lowercase alphanumerics", s)
		}
	}
}

func TestStartRefusals(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name      string
		ref       string
		opts      StartOptions
		arrange   func(h *harness)
		wantIs    error
		wantText  string
		wantCalls []string
	}{
		{
			name: "unknown scenario", ref: "999", opts: StartOptions{Seed: 1},
			wantIs: scenario.ErrNotFound, wantCalls: nil,
		},
		{
			name: "negative seed", ref: "001-hello", opts: StartOptions{Seed: -3},
			wantText: "seed must be >= 1, got -3", wantCalls: nil,
		},
		{
			name: "unknown mode", ref: "001-hello", opts: StartOptions{Seed: 1, Mode: "race"},
			wantText: `unknown mode "race"`, wantCalls: nil,
		},
		{
			name: "exam without minutes", ref: "001-hello", opts: StartOptions{Seed: 1, Mode: journal.ModeExam},
			arrange:  func(h *harness) { h.catalog.sc.Meta.Minutes = 0 },
			wantText: "scenario has no time box", wantCalls: nil,
		},
		{
			name: "heal fails", ref: "001-hello", opts: StartOptions{Seed: 1},
			arrange: func(h *harness) { h.healer.err = boom },
			wantIs:  boom, wantText: "heal injected faults: boom", wantCalls: []string{"heal"},
		},
		{
			name: "API services unavailable", ref: "001-hello", opts: StartOptions{Seed: 1},
			arrange:   func(h *harness) { h.kube.down = []string{"v1beta1.metrics.k8s.io", "v1.example.io"} },
			wantIs:    ErrAPIServicesUnavailable,
			wantText:  "API services unavailable: v1beta1.metrics.k8s.io, v1.example.io; namespaces cannot be deleted until they recover; run: kubedrill up",
			wantCalls: []string{"heal", "apiservices"},
		},
		{
			name: "API service check fails", ref: "001-hello", opts: StartOptions{Seed: 1},
			arrange: func(h *harness) { h.kube.downErr = boom },
			wantIs:  boom, wantText: "check API services: boom", wantCalls: []string{"heal", "apiservices"},
		},
		{
			name: "namespace create fails", ref: "001-hello", opts: StartOptions{Seed: 1},
			arrange: func(h *harness) { h.kube.err = boom },
			wantIs:  boom, wantText: "create namespace " + testNS + ": boom",
			wantCalls: []string{"heal", "apiservices", "progress:creating namespace " + testNS, "create:" + testNS},
		},
		{
			name: "apply fails", ref: "001-hello", opts: StartOptions{Seed: 1},
			arrange: func(h *harness) { h.kube.applyErr = boom },
			wantIs:  boom, wantText: "apply setup of 001-hello to namespace " + testNS + ": boom",
			wantCalls: []string{"heal", "apiservices", "progress:creating namespace " + testNS, "create:" + testNS, "progress:applying setup", "apply"},
		},
		{
			name: "hook fails", ref: "001-hello", opts: StartOptions{Seed: 1},
			arrange: func(h *harness) { h.hookErr = boom },
			wantIs:  boom, wantText: "setup hook of 001-hello: boom",
			wantCalls: []string{"heal", "apiservices", "progress:creating namespace " + testNS, "create:" + testNS, "progress:applying setup", "apply", "hook"},
		},
		{
			name: "journal start fails", ref: "001-hello", opts: StartOptions{Seed: 1},
			arrange: func(h *harness) { h.journal.startErr = boom },
			wantIs:  boom, wantText: "record episode of 001-hello: boom",
			wantCalls: []string{"heal", "apiservices", "progress:creating namespace " + testNS, "create:" + testNS, "progress:applying setup", "apply", "hook", "start"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, testScenario(), "/scenarios/L0/001-hello/setup/hook.sh", 0)
			if tc.arrange != nil {
				tc.arrange(h)
			}
			ep, rd, err := h.s.Start(context.Background(), tc.ref, tc.opts)
			if err == nil {
				t.Fatal("Start: got nil error")
			}
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Errorf("Start: got %v, want %v", err, tc.wantIs)
			}
			if tc.wantText != "" && !strings.Contains(err.Error(), tc.wantText) {
				t.Errorf("error text: got %q, want it to contain %q", err.Error(), tc.wantText)
			}
			if ep != nil || rd != nil {
				t.Errorf("Start returned %v and %v on error, want nil", ep, rd)
			}
			if !reflect.DeepEqual(h.calls, tc.wantCalls) {
				t.Errorf("calls:\ngot  %v\nwant %v", h.calls, tc.wantCalls)
			}
			if h.journal.current != nil {
				t.Errorf("journal current after failed start: %+v", h.journal.current)
			}
		})
	}
}

func TestStartKeepsActiveEpisodeWhenClusterRefuses(t *testing.T) {
	h := newHarness(t, testScenario(), "", 0)
	first := h.start(t, journal.ModePractice, 1)
	h.kube.down = []string{"v1beta1.metrics.k8s.io"}
	if _, _, err := h.s.Reset(context.Background(), "001-hello", 2); !errors.Is(err, ErrAPIServicesUnavailable) {
		t.Fatalf("Reset: got %v, want ErrAPIServicesUnavailable", err)
	}
	if h.journal.current == nil || h.journal.current.ID != first.ID || len(h.journal.finished) != 0 {
		t.Errorf("active episode after refusal: got %+v finished %v, want the first one untouched", h.journal.current, h.journal.finished)
	}
}

func TestStartGCFailureWarns(t *testing.T) {
	h := newHarness(t, testScenario(), "", 0)
	h.kube.gcErr = errors.New("forbidden")
	ep, _, err := h.s.Reset(context.Background(), "001-hello", 1)
	if err != nil || ep == nil {
		t.Fatalf("Reset: got %v, %v; want the episode despite a cleanup failure", ep, err)
	}
	if got := h.out.String(); got != "warning: delete old episode namespaces: forbidden\n" {
		t.Errorf("output: got %q", got)
	}
}

func TestStartExam(t *testing.T) {
	h := newHarness(t, testScenario(), "", 0)
	ep := h.start(t, journal.ModeExam, 1)
	if ep.Mode != journal.ModeExam || ep.Deadline == nil || !ep.Deadline.Equal(t0.Add(10*time.Minute)) {
		t.Errorf("exam episode: got mode %q deadline %v, want exam and %v", ep.Mode, ep.Deadline, t0.Add(10*time.Minute))
	}
	h.now = t0.Add(11 * time.Minute)
	if _, _, err := h.s.Reset(context.Background(), "001-hello", 2); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(h.journal.finished, []journal.Outcome{journal.OutcomeTimedOut}) {
		t.Errorf("exam left past its deadline: got %v, want [timed_out]", h.journal.finished)
	}
}

func TestCurrentRerendersWithStoredNamespace(t *testing.T) {
	h := newHarness(t, testScenario(), "", 0)
	_, started, err := h.s.Reset(context.Background(), "001-hello", 4)
	if err != nil {
		t.Fatal(err)
	}
	h.s.d.Suffix = func() string { return "zzzzz" }
	ep, rd, err := h.s.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if ep.Seed != 4 || rd.Variant.Name != "probe" || rd.Namespace != testNS {
		t.Errorf("got seed %d variant %q ns %q, want 4 probe %s", ep.Seed, rd.Variant.Name, rd.Namespace, testNS)
	}
	if rd.Instruction != started.Instruction || !reflect.DeepEqual(rd.Hints, started.Hints) || rd.Solution != started.Solution {
		t.Errorf("Current renders differently from Start:\ngot  %q %q\nwant %q %q", rd.Instruction, rd.Hints, started.Instruction, started.Hints)
	}
}

func TestCurrentLegacyEpisodeWithoutNamespace(t *testing.T) {
	h := newHarness(t, testScenario(), "", 0)
	h.journal.current = &journal.Episode{ID: "old", ScenarioID: "001-hello", Seed: 1, Outcome: journal.OutcomeActive}
	_, rd, err := h.s.Current()
	if err != nil || rd.Namespace != "task-hello" {
		t.Errorf("Current of a pre-upgrade episode: got %v, %v; want the scenario namespace task-hello", rd, err)
	}
}

func TestCurrentWithoutEpisode(t *testing.T) {
	h := newHarness(t, testScenario(), "", 0)
	ctx := context.Background()
	if _, _, err := h.s.Current(); !errors.Is(err, ErrNoActiveEpisode) {
		t.Errorf("Current: got %v", err)
	}
	if _, err := h.s.Observe(ctx); !errors.Is(err, ErrNoActiveEpisode) {
		t.Errorf("Observe: got %v", err)
	}
	if _, _, _, err := h.s.Hint(); !errors.Is(err, ErrNoActiveEpisode) {
		t.Errorf("Hint: got %v", err)
	}
	if _, err := h.s.Surrender(ctx); !errors.Is(err, ErrNoActiveEpisode) {
		t.Errorf("Surrender: got %v", err)
	}
	if _, err := h.s.Diagnose("the image tag is wrong"); !errors.Is(err, ErrNoActiveEpisode) {
		t.Errorf("Diagnose: got %v", err)
	}
	if _, _, err := h.s.Submit(ctx); !errors.Is(err, ErrNoActiveEpisode) {
		t.Errorf("Submit: got %v", err)
	}
	if _, err := h.s.Expire(ctx); !errors.Is(err, ErrNoActiveEpisode) {
		t.Errorf("Expire: got %v", err)
	}
}

func TestGradeSolvedRecordsTelemetry(t *testing.T) {
	h := newHarness(t, testScenario(), "", 0)
	started := h.start(t, journal.ModePractice, 1)
	h.grader.grade = grader.Grade{Passed: true, Score: 1, Results: []grader.Result{{ID: "ready", Status: grader.StatusPass}}}
	h.now = t0.Add(4 * time.Minute)
	g, ep, err := h.s.Grade(context.Background())
	if err != nil || !g.Passed {
		t.Fatalf("Grade: got %+v, %v", g, err)
	}
	if ep.Outcome != journal.OutcomeSolved || ep.EndedAt == nil || !ep.EndedAt.Equal(h.now) || len(ep.Grades) != 1 {
		t.Errorf("episode: got %+v, want solved now with one grade", ep)
	}
	if ep.Telemetry == nil || ep.Telemetry.Verdict != "root-cause" {
		t.Errorf("telemetry: got %+v, want the hook result", ep.Telemetry)
	}
	want := []string{"labels", "telemetry:" + started.ID, "finish:solved"}
	if !reflect.DeepEqual(h.calls, want) {
		t.Errorf("calls: got %v, want %v", h.calls, want)
	}
}

func TestGradeNotSolvedUpdates(t *testing.T) {
	h := newHarness(t, testScenario(), "", 0)
	h.start(t, journal.ModePractice, 1)
	h.grader.grade = grader.Grade{Passed: false, Score: 0.5}
	g, ep, err := h.s.Grade(context.Background())
	if err != nil || g.Passed {
		t.Fatalf("Grade: got %+v, %v", g, err)
	}
	if ep.Outcome != journal.OutcomeActive || len(ep.Grades) != 1 || len(h.journal.finished) != 0 || len(h.telemetry) != 0 {
		t.Errorf("episode: got %+v finished %v telemetry %v, want active, one grade, no telemetry", ep, h.journal.finished, h.telemetry)
	}
}

func TestGradeErrorIsWrapped(t *testing.T) {
	h := newHarness(t, testScenario(), "", 0)
	h.start(t, journal.ModePractice, 1)
	boom := errors.New("api down")
	h.grader.err = boom
	_, _, err := h.s.Grade(context.Background())
	if !errors.Is(err, boom) || err.Error() != "grade 001-hello: api down" {
		t.Errorf("got %v, want wrapped api down naming the scenario", err)
	}
	if len(h.journal.updates) != 0 {
		t.Errorf("updates after grader error: got %d, want 0", len(h.journal.updates))
	}
}

func TestDiagnose(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		wantErr string
	}{
		{"too short", "  image   ", "diagnosis must be 10 to 300 characters, got 5"},
		{"too long", strings.Repeat("x", 301), "got 301"},
		{"minimum", "0123456789", ""},
		{"exactly 300", strings.Repeat("x", 300), ""},
		{"300 multibyte runes", strings.Repeat("\u00e9", 300), ""},
		{"301 multibyte runes", strings.Repeat("\u00e9", 301), "got 301"},
		{"trimmed", "  the image tag nginx:1.99 does not exist  ", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, testScenario(), "", 0)
			h.start(t, journal.ModePractice, 1)
			h.now = t0.Add(time.Minute)
			ep, err := h.s.Diagnose(tc.text)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Diagnose: got %v, want %q", err, tc.wantErr)
				}
				if h.journal.current.Diagnosis != nil {
					t.Errorf("diagnosis stored after a rejected text")
				}
				return
			}
			if err != nil {
				t.Fatalf("Diagnose: %v", err)
			}
			want := &journal.Diagnosis{Text: strings.TrimSpace(tc.text), At: h.now}
			if !reflect.DeepEqual(ep.Diagnosis, want) || !reflect.DeepEqual(h.journal.current.Diagnosis, want) {
				t.Errorf("diagnosis: got %+v stored %+v, want %+v", ep.Diagnosis, h.journal.current.Diagnosis, want)
			}
		})
	}
}

func TestHintLadderAndDiagnosisGate(t *testing.T) {
	h := newHarness(t, testScenario(), "", 0)
	h.start(t, journal.ModePractice, 1)
	steps := []struct {
		diagnose string
		hint     string
		index    int
		err      error
	}{
		{hint: "look at events in " + testNS, index: 1},
		{hint: "check the image", index: 2},
		{index: 2, err: ErrDiagnosisRequired},
		{diagnose: "the image tag does not exist", hint: "set the image to nginx:1.25", index: 3},
		{index: 3, err: ErrNoMoreHints},
	}
	for i, st := range steps {
		h.now = t0.Add(time.Duration(i+1) * time.Minute)
		if st.diagnose != "" {
			if _, err := h.s.Diagnose(st.diagnose); err != nil {
				t.Fatal(err)
			}
		}
		hint, index, total, err := h.s.Hint()
		if !errors.Is(err, st.err) {
			t.Fatalf("step %d: got err %v, want %v", i, err, st.err)
		}
		if hint != st.hint || index != st.index || total != 3 {
			t.Errorf("step %d: got (%q, %d, %d), want (%q, %d, 3)", i, hint, index, total, st.hint, st.index)
		}
	}
	cur := h.journal.current
	want := []time.Time{t0.Add(time.Minute), t0.Add(2 * time.Minute), t0.Add(4 * time.Minute)}
	if cur.HintsUsed != 3 || !reflect.DeepEqual(cur.HintReveals, want) {
		t.Errorf("hints: got used %d reveals %v, want 3 and %v", cur.HintsUsed, cur.HintReveals, want)
	}
}

func TestSurrender(t *testing.T) {
	h := newHarness(t, testScenario(), "", 0)
	started := h.start(t, journal.ModePractice, 1)
	if _, err := h.s.Surrender(context.Background()); !errors.Is(err, ErrDiagnosisRequired) {
		t.Fatalf("Surrender without diagnosis: got %v, want ErrDiagnosisRequired", err)
	}
	if h.journal.current == nil {
		t.Fatal("episode ended by a refused surrender")
	}
	if _, err := h.s.Diagnose("the image tag does not exist"); err != nil {
		t.Fatal(err)
	}
	h.calls = nil
	sol, err := h.s.Surrender(context.Background())
	if err != nil {
		t.Fatalf("Surrender: %v", err)
	}
	if sol != "## Symptom\nhello in "+testNS+" is down" {
		t.Errorf("solution: got %q, want the rendered walkthrough", sol)
	}
	if want := []string{"telemetry:" + started.ID, "finish:abandoned", "delete:" + testNS}; !reflect.DeepEqual(h.calls, want) {
		t.Errorf("calls: got %v, want %v", h.calls, want)
	}
	if h.journal.ended[0].Telemetry == nil {
		t.Errorf("abandoned episode without telemetry")
	}
	if _, _, _, err := h.s.Hint(); !errors.Is(err, ErrNoActiveEpisode) {
		t.Errorf("Hint after surrender: got %v", err)
	}
}

func TestExamRules(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, testScenario(), "", 0)
	h.start(t, journal.ModeExam, 1)
	h.grader.grade = grader.Grade{Passed: false}
	if _, _, _, err := h.s.Hint(); !errors.Is(err, ErrExamMode) {
		t.Errorf("Hint in exam: got %v, want ErrExamMode", err)
	}
	if _, err := h.s.Diagnose("the image tag does not exist"); err != nil {
		t.Errorf("Diagnose in exam: got %v, want nil", err)
	}
	if _, err := h.s.Surrender(ctx); !errors.Is(err, ErrExamMode) {
		t.Errorf("Surrender in exam: got %v, want ErrExamMode", err)
	}
	if _, _, err := h.s.Grade(ctx); !errors.Is(err, ErrExamMode) {
		t.Errorf("Grade in exam: got %v, want ErrExamMode", err)
	}
	if _, err := h.s.Observe(ctx); !errors.Is(err, ErrExamMode) {
		t.Errorf("Observe in exam before the deadline: got %v, want ErrExamMode", err)
	}
	if _, err := h.s.Expire(ctx); !errors.Is(err, ErrNotExpired) {
		t.Errorf("Expire before the deadline: got %v, want ErrNotExpired", err)
	}
	if h.grader.n != 0 || len(h.journal.finished) != 0 {
		t.Errorf("grader runs %d finishes %v, want none and none", h.grader.n, h.journal.finished)
	}

	h.now = t0.Add(10 * time.Minute)
	for name, call := range map[string]func() error{
		"Observe":   func() error { _, err := h.s.Observe(ctx); return err },
		"Grade":     func() error { _, _, err := h.s.Grade(ctx); return err },
		"Hint":      func() error { _, _, _, err := h.s.Hint(); return err },
		"Diagnose":  func() error { _, err := h.s.Diagnose("another cause entirely"); return err },
		"Surrender": func() error { _, err := h.s.Surrender(ctx); return err },
		"Submit":    func() error { _, _, err := h.s.Submit(ctx); return err },
	} {
		if err := call(); !errors.Is(err, ErrTimedOut) {
			t.Errorf("%s after the deadline: got %v, want ErrTimedOut", name, err)
		}
	}
}

func TestPracticeRefusesExamActions(t *testing.T) {
	h := newHarness(t, testScenario(), "", 0)
	h.start(t, journal.ModePractice, 1)
	if _, _, err := h.s.Submit(context.Background()); !errors.Is(err, ErrNotExam) {
		t.Errorf("Submit in practice: got %v, want ErrNotExam", err)
	}
	if _, err := h.s.Expire(context.Background()); !errors.Is(err, ErrNotExam) {
		t.Errorf("Expire in practice: got %v, want ErrNotExam", err)
	}
}

func TestSubmit(t *testing.T) {
	tests := []struct {
		name    string
		passed  bool
		outcome journal.Outcome
	}{
		{"solved", true, journal.OutcomeSolved},
		{"failed", false, journal.OutcomeFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, testScenario(), "", 0)
			started := h.start(t, journal.ModeExam, 1)
			h.grader.grade = grader.Grade{Passed: tc.passed}
			h.now = t0.Add(9 * time.Minute)
			g, ep, err := h.s.Submit(context.Background())
			if err != nil || g.Passed != tc.passed {
				t.Fatalf("Submit: got %+v, %v", g, err)
			}
			if ep.Outcome != tc.outcome || len(ep.Grades) != 1 || ep.Telemetry == nil {
				t.Errorf("episode: got outcome %s grades %d telemetry %v, want %s, 1, set", ep.Outcome, len(ep.Grades), ep.Telemetry, tc.outcome)
			}
			want := []string{"labels", "telemetry:" + started.ID, "finish:" + string(tc.outcome)}
			if tc.outcome != journal.OutcomeSolved {
				want = append(want, "delete:"+testNS)
			}
			if !reflect.DeepEqual(h.calls, want) {
				t.Errorf("calls: got %v, want %v", h.calls, want)
			}
			if _, _, err := h.s.Submit(context.Background()); !errors.Is(err, ErrNoActiveEpisode) {
				t.Errorf("second Submit: got %v, want ErrNoActiveEpisode", err)
			}
		})
	}
}

func TestExpire(t *testing.T) {
	boom := errors.New("api down")
	tests := []struct {
		name     string
		grade    grader.Grade
		gradeErr error
		after    time.Duration
		outcome  journal.Outcome
		grades   int
	}{
		{name: "fails the final grade", grade: grader.Grade{Passed: false}, after: 2 * time.Minute, outcome: journal.OutcomeTimedOut, grades: 1},
		{name: "passes late: still timed out", grade: grader.Grade{Passed: true}, after: 2 * time.Minute, outcome: journal.OutcomeTimedOut, grades: 1},
		{name: "passes 31s after the deadline: timed out", grade: grader.Grade{Passed: true}, after: 31 * time.Second, outcome: journal.OutcomeTimedOut, grades: 1},
		{name: "passes 30s after the deadline: solved", grade: grader.Grade{Passed: true}, after: 30 * time.Second, outcome: journal.OutcomeSolved, grades: 1},
		{name: "passes at the deadline: solved", grade: grader.Grade{Passed: true}, outcome: journal.OutcomeSolved, grades: 1},
		{name: "grader error still times out", gradeErr: boom, after: 2 * time.Minute, outcome: journal.OutcomeTimedOut, grades: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, testScenario(), "", 0)
			h.start(t, journal.ModeExam, 1)
			h.grader.grade, h.grader.err = tc.grade, tc.gradeErr
			h.now = t0.Add(10*time.Minute + tc.after)
			ep, err := h.s.Expire(context.Background())
			if tc.gradeErr != nil {
				if !errors.Is(err, boom) || !strings.Contains(err.Error(), "final grade of 001-hello") {
					t.Errorf("Expire: got %v, want the wrapped grader error", err)
				}
			} else if err != nil {
				t.Fatalf("Expire: %v", err)
			}
			if ep == nil || ep.Outcome != tc.outcome || len(ep.Grades) != tc.grades || ep.Telemetry == nil {
				t.Errorf("episode: got %+v, want outcome %s with %d grades and telemetry", ep, tc.outcome, tc.grades)
			}
			if h.journal.current != nil {
				t.Errorf("episode still active after Expire")
			}
		})
	}
}

func TestRecordDebrief(t *testing.T) {
	yes := true
	t.Run("after a solve", func(t *testing.T) {
		h := newHarness(t, testScenario(), "", 0)
		started := h.start(t, journal.ModePractice, 1)
		if _, err := h.s.Diagnose("the image tag does not exist"); err != nil {
			t.Fatal(err)
		}
		h.grader.grade = grader.Grade{Passed: true}
		if _, _, err := h.s.Grade(context.Background()); err != nil {
			t.Fatal(err)
		}
		h.now = t0.Add(20 * time.Minute)
		h.calls = nil
		d := journal.Debrief{RuledOutEarlier: "kubectl describe pod", NextTimeFirst: "events", Principle: "observe before changing"}
		if err := h.s.RecordDebrief(started.ID, d, &yes); err != nil {
			t.Fatalf("RecordDebrief: %v", err)
		}
		if want := []string{"update-ended:" + started.ID}; !reflect.DeepEqual(h.calls, want) {
			t.Errorf("calls: got %v, want %v", h.calls, want)
		}
		got := h.journal.ended[0]
		d.At = h.now
		if !reflect.DeepEqual(got.Debrief, &d) {
			t.Errorf("debrief: got %+v, want %+v", got.Debrief, d)
		}
		if got.Diagnosis == nil || got.Diagnosis.Matched == nil || !*got.Diagnosis.Matched {
			t.Errorf("diagnosis matched: got %+v, want true", got.Diagnosis)
		}
	})
	t.Run("matched without a diagnosis is ignored", func(t *testing.T) {
		h := newHarness(t, testScenario(), "", 0)
		started := h.start(t, journal.ModePractice, 1)
		h.grader.grade = grader.Grade{Passed: true}
		if _, _, err := h.s.Grade(context.Background()); err != nil {
			t.Fatal(err)
		}
		at := t0.Add(time.Hour)
		if err := h.s.RecordDebrief(started.ID, journal.Debrief{At: at, NextTimeFirst: "x"}, &yes); err != nil {
			t.Fatal(err)
		}
		if got := h.journal.ended[0]; got.Diagnosis != nil || !got.Debrief.At.Equal(at) {
			t.Errorf("episode: got %+v, want debrief at %v and no diagnosis", got, at)
		}
	})
	t.Run("active episode", func(t *testing.T) {
		h := newHarness(t, testScenario(), "", 0)
		started := h.start(t, journal.ModePractice, 1)
		if err := h.s.RecordDebrief(started.ID, journal.Debrief{NextTimeFirst: "x"}, nil); err != nil {
			t.Fatal(err)
		}
		if h.journal.current.Debrief == nil {
			t.Errorf("active episode without the debrief")
		}
	})
	t.Run("unknown id", func(t *testing.T) {
		h := newHarness(t, testScenario(), "", 0)
		if err := h.s.RecordDebrief("nope", journal.Debrief{}, nil); !errors.Is(err, journal.ErrUnknownEpisode) {
			t.Errorf("RecordDebrief: got %v, want ErrUnknownEpisode", err)
		}
	})
}

func TestObserve(t *testing.T) {
	h := newHarness(t, testScenario(), "", 0)
	h.start(t, journal.ModePractice, 1)
	h.grader.grade = grader.Grade{Passed: false, Score: 0.5, Results: []grader.Result{{ID: "ready", Status: grader.StatusFail}}}
	g, err := h.s.Observe(context.Background())
	if err != nil || g.Score != 0.5 {
		t.Fatalf("Observe: got %+v, %v", g, err)
	}
	if len(h.journal.updates) != 0 || len(h.journal.finished) != 0 {
		t.Errorf("Observe wrote to the journal")
	}
}

func TestNamespaceOwnership(t *testing.T) {
	boom := errors.New("api down")
	tests := []struct {
		name     string
		arrange  func(k *fakeKube)
		wantIs   error
		wantText string
	}{
		{
			name:     "namespace missing",
			arrange:  func(k *fakeKube) { k.nsMissing = true },
			wantIs:   ErrNamespaceMissing,
			wantText: "namespace " + testNS + " is missing; it was deleted or belongs to another episode; run: kubedrill reset: namespace missing",
		},
		{
			name:     "namespace of another episode",
			arrange:  func(k *fakeKube) { k.nsLabels = map[string]string{"kubedrill.io/seed": "9"} },
			wantIs:   ErrNamespaceForeign,
			wantText: "namespace " + testNS + " belongs to another episode (seed 9); run: kubedrill reset: namespace belongs to another episode",
		},
		{
			name:     "label read fails",
			arrange:  func(k *fakeKube) { k.labelsErr = boom },
			wantIs:   boom,
			wantText: "check namespace " + testNS + ": api down",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, testScenario(), "", 0)
			h.start(t, journal.ModePractice, 1)
			tc.arrange(h.kube)
			h.grader.grade = grader.Grade{Passed: true}
			_, err := h.s.Observe(context.Background())
			if !errors.Is(err, tc.wantIs) || err.Error() != tc.wantText {
				t.Errorf("Observe: got %v, want %q", err, tc.wantText)
			}
			_, ep, err := h.s.Grade(context.Background())
			if !errors.Is(err, tc.wantIs) || ep != nil {
				t.Errorf("Grade: got %v, %v", ep, err)
			}
			if h.grader.n != 0 || len(h.journal.updates) != 0 || len(h.journal.finished) != 0 {
				t.Errorf("graded or journaled after a failed ownership check")
			}
		})
	}
}

func TestScriptEnv(t *testing.T) {
	rd := &scenario.Rendered{Namespace: "ns-abcde", Seed: 7, Variant: scenario.Variant{Name: "v"}, Vars: map[string]string{"b": "2", "a": "1"}}
	tests := []struct {
		name       string
		kubeconfig string
		episode    string
		want       []string
	}{
		{"bare", "", "", []string{"TASK_NS=ns-abcde", "SEED=7", "VARIANT=v", "VAR_A=1", "VAR_B=2"}},
		{"kubeconfig only", "/k", "", []string{"TASK_NS=ns-abcde", "SEED=7", "VARIANT=v", "VAR_A=1", "VAR_B=2", "KUBECONFIG=/k"}},
		{"all", "/k", "001-x-1-abcde", []string{"TASK_NS=ns-abcde", "SEED=7", "VARIANT=v", "VAR_A=1", "VAR_B=2", "KUBECONFIG=/k", "KUBEDRILL_EPISODE=001-x-1-abcde"}},
	}
	for _, tc := range tests {
		if got := ScriptEnv(rd, tc.kubeconfig, tc.episode); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRunScriptKillsProcessGroup(t *testing.T) {
	dir := t.TempDir()
	script := dir + "/hang.sh"
	if err := writeFile(script, "#!/bin/bash\nsleep 30 &\nwait\n"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := RunScript(io.Discard)(ctx, script, nil)
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("run: got %v, want context.DeadlineExceeded", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("elapsed: got %v, want under 3s", elapsed)
	}
}

func TestRunScript(t *testing.T) {
	dir := t.TempDir()
	script := dir + "/hook.sh"
	if err := writeFile(script, "#!/bin/bash\necho \"ns=$TASK_NS\"\ntest \"$SEED\" = 3\n"); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	run := RunScript(&out)
	if err := run(context.Background(), script, []string{"TASK_NS=demo", "SEED=3"}); err != nil {
		t.Fatalf("run: got err %v, want nil", err)
	}
	if got := strings.TrimSpace(out.String()); got != "ns=demo" {
		t.Errorf("output: got %q, want ns=demo", got)
	}
	if err := run(context.Background(), script, []string{"TASK_NS=demo", "SEED=4"}); err == nil || !strings.Contains(err.Error(), script) {
		t.Errorf("failing script: got %v, want error naming %s", err, script)
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o755)
}

func TestStartReportsHealedFaults(t *testing.T) {
	h := newHarness(t, testScenario(), "", 0)
	h.healer.active = []faults.Fault{{Action: faults.Pause, Node: "worker2"}, {Action: faults.Netem, Node: "worker"}}
	h.healer.stuck = map[string]bool{"worker": true}
	if _, _, err := h.s.Start(context.Background(), "001-hello", StartOptions{Seed: 1}); err != nil {
		t.Fatal(err)
	}
	var healed []string
	for _, p := range h.progress {
		if strings.HasPrefix(p, "healed ") {
			healed = append(healed, p)
		}
	}
	if want := []string{"healed pause on worker2"}; !reflect.DeepEqual(healed, want) {
		t.Errorf("healed lines: got %v, want %v", healed, want)
	}
}

func TestFailedSetupKeepsPreviousEpisode(t *testing.T) {
	h := newHarness(t, testScenario(), "/hook.sh", 0)
	first := h.start(t, journal.ModePractice, 1)
	h.suffix = "zzzzz"
	h.hookErr = errors.New("hook exploded")
	if _, _, err := h.s.Start(context.Background(), "001-hello", StartOptions{Seed: 2}); err == nil {
		t.Fatal("Start with a failing hook: got nil error")
	}
	if h.journal.current == nil || h.journal.current.ID != first.ID {
		t.Errorf("current after a failed start: got %+v, want %s still active", h.journal.current, first.ID)
	}
	if len(h.journal.finished) != 0 {
		t.Errorf("finished: got %v, want none", h.journal.finished)
	}
	for _, c := range h.calls {
		if strings.HasPrefix(c, "delete:") || c == "gc" || c == "start" {
			t.Errorf("failed start called %s", c)
		}
	}
}

func TestStartDeletesPreviousNamespace(t *testing.T) {
	h := newHarness(t, testScenario(), "", 0)
	h.start(t, journal.ModePractice, 1)
	h.suffix = "zzzzz"
	if _, _, err := h.s.Start(context.Background(), "001-hello", StartOptions{Seed: 2}); err != nil {
		t.Fatal(err)
	}
	want := []string{"finish:failed", "delete:" + testNS, "start", "gc"}
	if got := h.calls[len(h.calls)-4:]; !reflect.DeepEqual(got, want) {
		t.Errorf("tail of calls: got %v, want %v", got, want)
	}
}

func TestStartSkipsGCWhenAnotherStartWon(t *testing.T) {
	h := newHarness(t, testScenario(), "", 0)
	h.journal.afterStart = func() {
		other := *h.journal.current
		other.ID, other.Namespace = "other", "task-hello-other"
		h.journal.current = &other
	}
	if _, _, err := h.s.Start(context.Background(), "001-hello", StartOptions{Seed: 1}); err != nil {
		t.Fatal(err)
	}
	if len(h.kube.gc) != 0 {
		t.Errorf("gc: got %v, want none while another episode is current", h.kube.gc)
	}
}

func TestGradeMergesConcurrentHint(t *testing.T) {
	for _, pass := range []bool{false, true} {
		h := newHarness(t, testScenario(), "", 0)
		h.start(t, journal.ModePractice, 1)
		reveal := t0.Add(3 * time.Minute)
		diag := &journal.Diagnosis{Text: "the image tag is wrong", At: reveal}
		h.grader.grade = grader.Grade{Passed: pass}
		h.grader.during = func() {
			cur := *h.journal.current
			cur.HintsUsed = 1
			cur.HintReveals = []time.Time{reveal}
			cur.Diagnosis = diag
			h.journal.current = &cur
		}
		_, ep, err := h.s.Grade(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if ep.HintsUsed != 1 || len(ep.HintReveals) != 1 || !ep.HintReveals[0].Equal(reveal) || ep.Diagnosis == nil {
			t.Errorf("pass %v: episode got hints %d reveals %v diagnosis %v, want the concurrent hint and diagnosis kept", pass, ep.HintsUsed, ep.HintReveals, ep.Diagnosis)
		}
	}
}

func TestTelemetryWindowUsesSessionClock(t *testing.T) {
	h := newHarness(t, testScenario(), "", 0)
	h.start(t, journal.ModePractice, 1)
	h.now = t0.Add(7 * time.Minute)
	h.grader.grade = grader.Grade{Passed: true}
	if _, _, err := h.s.Grade(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h.windows) != 1 || !h.windows[0].Equal(h.now) {
		t.Errorf("telemetry window ends: got %v, want [%v]", h.windows, h.now)
	}
}

type cancelGrader struct {
	cancel context.CancelFunc
}

func (g *cancelGrader) Grade(ctx context.Context, _ *scenario.Rendered) (grader.Grade, error) {
	g.cancel()
	return grader.Grade{Results: []grader.Result{{ID: "ready", Status: grader.StatusPass}, {ID: "rest", Status: grader.StatusError, Message: ctx.Err().Error()}}}, nil
}

func TestCancelledGradeRecordsNothing(t *testing.T) {
	for _, mode := range []journal.Mode{journal.ModePractice, journal.ModeExam} {
		h := newHarness(t, testScenario(), "", 0)
		h.start(t, mode, 1)
		ctx, cancel := context.WithCancel(context.Background())
		h.s.d.Grader = &cancelGrader{cancel: cancel}
		var err error
		if mode == journal.ModeExam {
			_, _, err = h.s.Submit(ctx)
		} else {
			_, _, err = h.s.Grade(ctx)
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("%s: got %v, want context.Canceled", mode, err)
		}
		if h.journal.current == nil || len(h.journal.current.Grades) != 0 || len(h.journal.finished) != 0 {
			t.Errorf("%s: got current %+v finished %v, want the episode active with no grade", mode, h.journal.current, h.journal.finished)
		}
	}
}

func TestGradeReturnsEpisodeEndedElsewhere(t *testing.T) {
	h := newHarness(t, testScenario(), "", 0)
	ep := h.start(t, journal.ModePractice, 1)
	h.grader.grade = grader.Grade{Passed: false}
	h.grader.during = func() {
		e := *h.journal.current
		e.Outcome = journal.OutcomeAbandoned
		end := t0.Add(time.Minute)
		e.EndedAt = &end
		h.journal.ended = append(h.journal.ended, e)
		h.journal.current = nil
	}
	_, got, err := h.s.Grade(context.Background())
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	if got == nil || got.ID != ep.ID || got.Outcome != journal.OutcomeAbandoned {
		t.Errorf("Grade after another process ended the episode: got %+v, want it abandoned", got)
	}
	h.journal.ended = nil
	h.start(t, journal.ModePractice, 1)
	h.grader.during = func() { h.journal.current = nil }
	if _, _, err := h.s.Grade(context.Background()); !errors.Is(err, journal.ErrNoCurrent) {
		t.Errorf("Grade with the episode gone and not recorded: got %v, want ErrNoCurrent", err)
	}
}

func TestGradeStampsSessionClock(t *testing.T) {
	h := newHarness(t, testScenario(), "", 0)
	h.start(t, journal.ModePractice, 1)
	h.now = t0.Add(4 * time.Minute)
	h.grader.grade = grader.Grade{At: time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)}
	g, _, err := h.s.Grade(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !g.At.Equal(h.now) || !h.journal.current.Grades[0].At.Equal(h.now) {
		t.Errorf("grade At: got %v stored %v, want %v", g.At, h.journal.current.Grades[0].At, h.now)
	}
}

func TestStartRedrawsRepeatedVariant(t *testing.T) {
	h := newHarness(t, testScenario(), "", 0)
	draws := []int64{1, 3, 5, 2}
	n := 0
	h.s.d.Rand = func() int64 { d := draws[n%len(draws)]; n++; return d }
	h.journal.ended = []journal.Episode{{ID: "old", ScenarioID: "001-hello", Variant: "image", Outcome: journal.OutcomeSolved}}
	ep, _, err := h.s.Start(context.Background(), "001-hello", StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if ep.Seed != 2 || ep.Variant != "probe" || n != 4 {
		t.Errorf("redraw: got seed %d variant %s after %d draws, want seed 2 variant probe after 4", ep.Seed, ep.Variant, n)
	}

	h = newHarness(t, testScenario(), "", 1)
	h.journal.ended = []journal.Episode{{ID: "old", ScenarioID: "001-hello", Variant: "image", Outcome: journal.OutcomeSolved}}
	calls := 0
	h.s.d.Rand = func() int64 { calls++; return 1 }
	if _, _, err := h.s.Start(context.Background(), "001-hello", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if calls != seedDraws {
		t.Errorf("redraw cap: got %d draws, want %d", calls, seedDraws)
	}

	one := testScenario()
	one.Variants = one.Variants[:1]
	h = newHarness(t, one, "", 1)
	h.journal.ended = []journal.Episode{{ID: "old", ScenarioID: "001-hello", Variant: "image", Outcome: journal.OutcomeSolved}}
	calls = 0
	h.s.d.Rand = func() int64 { calls++; return 1 }
	if _, _, err := h.s.Start(context.Background(), "001-hello", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("single variant: got %d draws, want 1", calls)
	}
	h = newHarness(t, testScenario(), "", 1)
	h.journal.ended = []journal.Episode{{ID: "old", ScenarioID: "001-hello", Variant: "image", Outcome: journal.OutcomeSolved}}
	calls = 0
	h.s.d.Rand = func() int64 { calls++; return 1 }
	if ep, _, err := h.s.Start(context.Background(), "001-hello", StartOptions{Seed: 1}); err != nil || ep.Seed != 1 || calls != 0 {
		t.Errorf("explicit seed: got %v, %v after %d draws, want seed 1 and no draw", ep, err, calls)
	}
}

func TestHealReportsNothingWhenListingFailsAfterHeal(t *testing.T) {
	h := newHarness(t, testScenario(), "", 0)
	h.healer.active = []faults.Fault{{Action: faults.Pause, Node: "worker"}}
	h.healer.listAfter = errors.New("faults.json unreadable")
	if _, _, err := h.s.Start(context.Background(), "001-hello", StartOptions{Seed: 1}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for _, p := range h.progress {
		if strings.HasPrefix(p, "healed") {
			t.Errorf("progress: got %q, want no healed line when the faults cannot be listed after Heal", p)
		}
	}
	h = newHarness(t, testScenario(), "", 0)
	h.healer.active = []faults.Fault{{Action: faults.Pause, Node: "worker"}}
	h.healer.listAfter = errors.New("faults.json unreadable")
	h.healer.err = errors.New("docker down")
	if _, _, err := h.s.Start(context.Background(), "001-hello", StartOptions{Seed: 1}); err == nil || !strings.Contains(err.Error(), "docker down") {
		t.Errorf("Start with a failed heal: got %v, want the heal error", err)
	}
}

func TestDeleteNamespace(t *testing.T) {
	h := newHarness(t, testScenario(), "", 0)
	h.s.deleteNamespace(context.Background(), "")
	if len(h.calls) != 0 || h.out.Len() != 0 {
		t.Errorf("empty namespace: got calls %v output %q, want nothing", h.calls, h.out.String())
	}
	h.kube.deleteErr = errors.New("forbidden")
	h.s.deleteNamespace(context.Background(), "task-x")
	if got := h.out.String(); !strings.Contains(got, "warning: delete namespace task-x: forbidden") {
		t.Errorf("delete failure: got output %q, want a warning", got)
	}
}

func TestRunScriptEnvAllowlist(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "leak")
	dir := t.TempDir()
	script := dir + "/env.sh"
	if err := writeFile(script, "#!/bin/bash\nprintf '%s|%s|%s' \"${GITHUB_TOKEN:-none}\" \"$TASK_NS\" \"${PATH:+set}\"\n"); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := RunScript(&out)(context.Background(), script, []string{"TASK_NS=lab"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "none|lab|set" {
		t.Errorf("script env: got %q, want none|lab|set", got)
	}
}
