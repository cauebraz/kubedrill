package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cauebraz/kubedrill/internal/cluster"
	"github.com/cauebraz/kubedrill/internal/concepts"
	"github.com/cauebraz/kubedrill/internal/curriculum"
	"github.com/cauebraz/kubedrill/internal/env"
	"github.com/cauebraz/kubedrill/internal/faults"
	"github.com/cauebraz/kubedrill/internal/grader"
	"github.com/cauebraz/kubedrill/internal/journal"
	"github.com/cauebraz/kubedrill/internal/render"
	"github.com/cauebraz/kubedrill/internal/scenario"
)

type fakeCluster struct {
	tools     []cluster.Tool
	exists    bool
	ensured   []string
	data      []cluster.ProfileData
	deleted   int
	internal  []string
	calls     *[]string
	audit     bool
	auditErr  error
	auditAsks int
	addons    [][]string
	rendered  []byte
	renderErr error
	renders   []string
	stale     bool
	staleErr  error
	staleAsks int
	pulls     int
	pulled    []string
	pullErr   error
}

func (c *fakeCluster) PullImages(_ context.Context, images []string) error {
	c.pulls++
	c.pulled = images
	return c.pullErr
}

func (c *fakeCluster) Doctor(context.Context) ([]cluster.Tool, error) { return c.tools, nil }
func (c *fakeCluster) Exists(context.Context) (bool, error)           { return c.exists, nil }
func (c *fakeCluster) Ensure(_ context.Context, profile string, data cluster.ProfileData) error {
	c.ensured = append(c.ensured, profile)
	c.data = append(c.data, data)
	c.exists = true
	return nil
}
func (c *fakeCluster) AuditEnabled(ctx context.Context) (bool, error) {
	c.auditAsks++
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return c.audit, c.auditErr
}
func (c *fakeCluster) AuditPolicyStale(context.Context) (bool, error) {
	c.staleAsks++
	return c.stale, c.staleErr
}
func (c *fakeCluster) RenderProfile(profile string, data cluster.ProfileData) ([]byte, error) {
	c.renders = append(c.renders, profile+" "+data.AuditPolicy+" "+data.AuditDir)
	if c.renderErr != nil {
		return nil, c.renderErr
	}
	if c.rendered == nil {
		return []byte("kind: Cluster\n"), nil
	}
	return c.rendered, nil
}
func (c *fakeCluster) Addons() []string     { return []string{"gateway", "kwok", "metrics-server"} }
func (c *fakeCluster) BaseAddons() []string { return []string{"metrics-server"} }
func (c *fakeCluster) EnsureAddons(_ context.Context, k cluster.AddonApplier, names []string) error {
	if k == nil {
		return errors.New("no applier")
	}
	c.addons = append(c.addons, names)
	return nil
}
func (c *fakeCluster) Delete(context.Context) error {
	c.deleted++
	c.exists, c.stale = false, false
	return nil
}
func (c *fakeCluster) Kubeconfig() (string, string, error) {
	return "/tmp/kubeconfig", "kind-kubedrill", nil
}
func (c *fakeCluster) WriteInternalKubeconfig(_ context.Context, dst string) error {
	if !c.exists {
		return fmt.Errorf("write internal kubeconfig for kubedrill: cluster missing")
	}
	c.internal = append(c.internal, dst)
	if c.calls != nil {
		*c.calls = append(*c.calls, "WriteInternalKubeconfig "+dst)
	}
	return nil
}

type memJournal struct {
	dir      string
	current  *journal.Episode
	episodes []journal.Episode
	problems []string
	seq      int
	clock    time.Time
}

func (j *memJournal) Dir() string        { return j.dir }
func (j *memJournal) Problems() []string { return j.problems }

func (j *memJournal) Current() (*journal.Episode, error) {
	if j.current == nil {
		return nil, nil
	}
	cp := *j.current
	return &cp, nil
}

func (j *memJournal) Start(e journal.Episode) error {
	if j.current != nil {
		if _, err := j.Finish(journal.OutcomeAbandoned); err != nil {
			return err
		}
	}
	j.seq++
	e.ID = fmt.Sprintf("ep-%d", j.seq)
	if j.clock.IsZero() {
		j.clock = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	}
	j.clock = j.clock.Add(10 * time.Minute)
	e.StartedAt = j.clock
	j.episodes = append(j.episodes, e)
	j.current = &e
	return nil
}

func (j *memJournal) Update(e journal.Episode) error {
	if j.current == nil || j.current.ID != e.ID {
		return journal.ErrNotCurrent
	}
	j.current = &e
	j.episodes[len(j.episodes)-1] = e
	return nil
}

func (j *memJournal) Modify(id string, fn func(*journal.Episode) error) (*journal.Episode, error) {
	if j.current == nil {
		return nil, journal.ErrNoCurrent
	}
	if j.current.ID != id {
		return nil, fmt.Errorf("%w (current is %s)", journal.ErrNotCurrent, j.current.ID)
	}
	e := *j.current
	start := e.StartedAt
	e.HintReveals = slices.Clone(e.HintReveals)
	e.Grades = slices.Clone(e.Grades)
	if err := fn(&e); err != nil {
		return nil, err
	}
	if e.Outcome == "" || e.Outcome == journal.OutcomeActive {
		e.Outcome = journal.OutcomeActive
		e.EndedAt = nil
		j.current = &e
		j.episodes[len(j.episodes)-1] = e
		cp := e
		return &cp, nil
	}
	end := start.Add(3 * time.Minute)
	e.EndedAt = &end
	j.episodes[len(j.episodes)-1] = e
	j.current = nil
	return &e, nil
}

func (j *memJournal) Finish(outcome journal.Outcome) (*journal.Episode, error) {
	if j.current == nil {
		return nil, journal.ErrNoCurrent
	}
	e := *j.current
	e.Outcome = outcome
	now := e.StartedAt.Add(3 * time.Minute)
	e.EndedAt = &now
	j.episodes[len(j.episodes)-1] = e
	j.current = nil
	return &e, nil
}

func (j *memJournal) Episodes(id string) ([]journal.Episode, error) {
	var out []journal.Episode
	for _, e := range j.episodes {
		if id == "" || e.ScenarioID == id {
			out = append(out, e)
		}
	}
	return out, nil
}

func (j *memJournal) ModifyEnded(id string, fn func(*journal.Episode) error) (*journal.Episode, error) {
	for i := range j.episodes {
		if j.episodes[i].ID == id && j.episodes[i].EndedAt != nil {
			e := j.episodes[i]
			if err := fn(&e); err != nil {
				return nil, err
			}
			j.episodes[i] = e
			return &e, nil
		}
	}
	return nil, journal.ErrUnknownEpisode
}

func (j *memJournal) JournalID() string { return "0123abcd" }

type fakeFaults struct {
	dir      string
	applied  []string
	active   []faults.Fault
	healed   int
	forgot   int
	applyErr error
	healErr  error
}

func (m *fakeFaults) Apply(_ context.Context, action faults.Action, node string, n faults.NetemOptions) error {
	if m.applyErr != nil {
		return m.applyErr
	}
	m.applied = append(m.applied, fmt.Sprintf("%s %s %s %g", action, node, n.Delay, n.Loss))
	m.active = append(m.active, faults.Fault{Action: action, Node: node, Netem: n})
	return nil
}

func (m *fakeFaults) Heal(context.Context) error {
	if m.healErr != nil {
		return m.healErr
	}
	m.healed++
	m.active = nil
	return nil
}

func (m *fakeFaults) Active(context.Context) ([]faults.Fault, error) { return m.active, nil }

func (m *fakeFaults) Forget(context.Context) error {
	m.forgot++
	m.active = nil
	return nil
}

type memCatalog struct {
	all []*scenario.Scenario
}

func (c *memCatalog) All() []*scenario.Scenario { return c.all }

func (c *memCatalog) Find(ref string) (*scenario.Scenario, error) {
	var matches []*scenario.Scenario
	for _, sc := range c.all {
		if sc.Meta.ID == ref {
			return sc, nil
		}
		if strings.HasPrefix(sc.Meta.ID, ref) {
			matches = append(matches, sc)
		}
	}
	switch len(matches) {
	case 0:
		return nil, &scenario.NotFoundError{Ref: ref}
	case 1:
		return matches[0], nil
	default:
		var ids []string
		for _, m := range matches {
			ids = append(ids, m.Meta.ID)
		}
		return nil, fmt.Errorf("find scenario %q: %w: matches %s", ref, scenario.ErrAmbiguous, strings.Join(ids, ", "))
	}
}

type fakeKube struct {
	calls   []string
	labels  map[string]string
	gone    map[string]bool
	applied [][]string
	down    [][]string
}

func (k *fakeKube) DeleteNamespaces(_ context.Context, selector, keep string) (int, error) {
	k.calls = append(k.calls, "gc "+selector+" keep "+keep)
	return 0, nil
}

func (k *fakeKube) UnavailableAPIServices(context.Context) ([]string, error) {
	if len(k.down) == 0 {
		return nil, nil
	}
	next := k.down[0]
	k.down = k.down[1:]
	return next, nil
}

func (k *fakeKube) ApplyCluster(_ context.Context, manifests []string) error {
	k.applied = append(k.applied, manifests)
	return nil
}

func (k *fakeKube) NamespaceLabels(_ context.Context, ns string) (map[string]string, error) {
	k.calls = append(k.calls, "labels "+ns)
	if k.gone[ns] {
		return nil, nil
	}
	if k.labels == nil {
		return map[string]string{"app.kubernetes.io/managed-by": "kubedrill"}, nil
	}
	return k.labels, nil
}

func (k *fakeKube) ResetNamespace(_ context.Context, ns string, _ map[string]string) error {
	k.calls = append(k.calls, "reset "+ns)
	return nil
}

func (k *fakeKube) ApplyManifests(_ context.Context, ns string, _ []string) error {
	k.calls = append(k.calls, "apply "+ns)
	return nil
}

func (k *fakeKube) DeleteNamespace(_ context.Context, ns string) error {
	k.calls = append(k.calls, "delete "+ns)
	return nil
}

type fakeGrader struct {
	grades []grader.Grade
	n      int
	err    error
}

func (g *fakeGrader) Grade(context.Context, *scenario.Rendered) (grader.Grade, error) {
	if g.err != nil {
		return grader.Grade{}, g.err
	}
	if len(g.grades) == 0 {
		return grader.Grade{}, errors.New("no grade configured")
	}
	i := g.n
	if i >= len(g.grades) {
		i = len(g.grades) - 1
	}
	g.n++
	return g.grades[i], nil
}

func passGrade() grader.Grade {
	return grader.Grade{Passed: true, Score: 1, Results: []grader.Result{
		{ID: "ready", Description: "deployment hello has 2 ready replicas", Status: grader.StatusPass},
		{ID: "svc", Description: "service hello selects the pods", Status: grader.StatusPass},
	}}
}

func failGrade() grader.Grade {
	return grader.Grade{Passed: false, Score: 0.5, Results: []grader.Result{
		{ID: "ready", Description: "deployment hello has 2 ready replicas", Status: grader.StatusFail, Expected: "2", Actual: "0", Why: "Ready replicas are the pods that passed their readiness probe; the service only routes to those, so 0 ready means every request fails."},
		{ID: "svc", Description: "service hello selects the pods", Status: grader.StatusPass, Why: "A service forwards only to pods matching its selector."},
	}}
}

func scenarios() []*scenario.Scenario {
	return []*scenario.Scenario{
		{
			Meta: scenario.Meta{ID: "001-hello-down", Title: "Hello app is down", Level: "L0", Type: scenario.TypeBreakFix, Minutes: 15, Namespace: "task-hello", Concepts: []string{"pods", "deployments"}, FixTargets: []string{"apps/deployments/hello"}},
			Variants: []scenario.Variant{
				{Name: "image", Vars: map[string]string{"image": "nginx:1.25"}, RootCause: "The deployment image tag does not exist in the registry."},
				{Name: "probe", Vars: map[string]string{"image": "nginx:1.26"}, RootCause: "The readiness probe points at a port nothing listens on."},
			},
			Instruction: "# Ticket\n\nThe hello app returns 503.\n\n## Done when\n\n- 2 ready replicas\n",
			Hints:       []string{"Look at the pod events.", "Compare the image tag with the registry."},
			Solution:    "## Symptom\n\nThe hello app returns 503 in {{ .Namespace }}.\n\n## Candidate causes\n\nThe image tag has a typo ({{ .Variant }}).\n\n## Observation that splits them\n\nThe pod events name the missing tag.\n\n## Change\n\n```\nkubectl -n {{ .Namespace }} set image deploy/hello app=nginx:1.27\n```\n\n## Verify\n\n```\n# wait for the rollout\nkubectl -n {{ .Namespace }} rollout status deploy/hello\n```\n",
			Approaches: []scenario.Approach{
				{Name: "fix-manifest", Snippet: "kubectl edit deploy/hello", Body: "# Fix the manifest\n\nEdit the source of truth.\n"},
				{Name: "set-image", Snippet: "kubectl set image deploy/hello app=nginx:1.27", Body: "# Set image\n\nFast, but drifts from git.\n"},
			},
			Story:       "## What happened\n\nThe hello pods in {{ .Namespace }} never start.\n\n## The fix and why it works\n\nPoint the image at a tag that exists.\n\n## Check your work\n\nWatch the rollout.\n",
			Checks:      []scenario.Check{{ID: "ready", Description: "deployment hello has 2 ready replicas"}},
			SolveScript: "/scenarios/L0/001-hello-down/solution/solve.sh",
		},
		{
			Meta:        scenario.Meta{ID: "002-service-dns", Title: "Service name does not resolve", Level: "L1", Type: scenario.TypeBreakFix, Minutes: 20, Namespace: "task-dns"},
			Variants:    []scenario.Variant{{Name: "selector", RootCause: "The service selector does not match the pod labels."}},
			Instruction: "# Ticket two\n",
			Hints:       []string{"Check the selector."},
			Solution:    "## Root cause two\n",
			SolveScript: "/scenarios/L1/002-service-dns/solution/solve.sh",
		},
	}
}

type fixture struct {
	root      string
	cluster   *fakeCluster
	journal   *memJournal
	catalog   *memCatalog
	kube      *fakeKube
	grader    *fakeGrader
	runs      [][]string
	runOut    string
	runErr    error
	lint      []error
	lintMods  []string
	lintConc  []string
	lintRefs  scenario.Refs
	curr      *curriculum.Curriculum
	currErr   error
	pinned    []string
	stdoutTTY bool
	stdinTTY  bool
	env       map[string]string
	concepts  *memConcepts
	concErr   error
	ws        *fakeWorkstation
	faults    *fakeFaults
	telemetry *journal.Telemetry
	now       time.Time
}

func (f *fixture) nowFunc() func() time.Time {
	return func() time.Time {
		if f.now.IsZero() {
			return time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
		}
		return f.now
	}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	if err := os.WriteFile(filepath.Join(root, "curriculum.yaml"), []byte("levels: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "scenarios"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &fixture{
		root:    root,
		cluster: &fakeCluster{exists: true, audit: true, tools: []cluster.Tool{{Name: "docker", Found: true, Version: "27.1", Detail: "daemon reachable"}, {Name: "kubectl", Found: true, Version: "1.37"}, {Name: "kind", Found: true, Version: "0.33"}}},
		journal: &memJournal{dir: filepath.Join(root, "data")},
		catalog: &memCatalog{all: scenarios()},
		kube:    &fakeKube{},
		grader:  &fakeGrader{},
		concepts: &memConcepts{all: []concepts.Concept{
			{ID: "pods", Title: "Pods", Summary: "The smallest deployable unit: one or more containers sharing a network namespace and storage.", Body: "# Pods\n\nA pod wraps containers.\n"},
			{ID: "deployments", Title: "Deployments", Summary: "Declarative replica management over ReplicaSets.", Body: "# Deployments\n\nA deployment rolls out ReplicaSets.\n"},
			{ID: "services", Title: "Services", Summary: "Stable virtual IP and DNS name in front of a selector.", Body: "# Services\n\nA service selects pods.\n"},
		}},
		curr: &curriculum.Curriculum{
			Levels: []curriculum.Level{{ID: "L0", Title: "First contact", Hours: 6, Modules: []curriculum.Module{
				{ID: "workloads-basics", Title: "Workloads", Concepts: []string{"pods", "deployments"}, Scenarios: []curriculum.PlannedScenario{{ID: "001-hello-down", Ticket: "down"}, {Ticket: "planned only"}}},
			}}},
			Exams: []curriculum.Exam{{ID: "CKA", Title: "CKA", Domains: []curriculum.Domain{{ID: "workloads", Title: "Workloads and scheduling", Weight: 15}}}},
		},
	}
}

func (f *fixture) factories() Factories {
	return Factories{
		Cluster: func(string) Cluster { return f.cluster },
		Journal: func(dir string) (Journal, error) {
			if dir != "" {
				return &memJournal{dir: dir}, nil
			}
			return f.journal, nil
		},
		Catalog: func(string, []string) (Catalog, error) { return f.catalog, nil },
		Connect: func(string, string, string) (Kube, Grader, error) { return f.kube, f.grader, nil },
		ScriptKubeconfig: func(journalDir, kubeconfig, contextName string) (string, error) {
			f.pinned = append(f.pinned, journalDir+" "+kubeconfig+" "+contextName)
			return filepath.Join(journalDir, "kubeconfig"), nil
		},
		Curriculum: func(string) (*curriculum.Curriculum, error) {
			if f.currErr != nil {
				return nil, f.currErr
			}
			c := *f.curr
			c.Levels = append([]curriculum.Level(nil), f.curr.Levels...)
			for i := range c.Levels {
				c.Levels[i].Modules = append([]curriculum.Module(nil), c.Levels[i].Modules...)
			}
			return &c, nil
		},
		Lint: func(_ string, refs scenario.Refs) []error {
			f.lintMods = refs.Modules
			f.lintConc = refs.Concepts
			f.lintRefs = refs
			return f.lint
		},
		Concepts: func(string) (Concepts, error) {
			if f.concErr != nil {
				return nil, f.concErr
			}
			return f.concepts, nil
		},
		Workstation: func(root, dataDir string) Workstation {
			if f.ws == nil {
				return nil
			}
			f.ws.root, f.ws.dataDir = root, dataDir
			return f.ws
		},
		Scaffold: func(root, level, id string) (string, error) { return filepath.Join(root, "scenarios", level, id), nil },
		Run: func(_ context.Context, script string, env []string, out io.Writer) error {
			f.runs = append(f.runs, append([]string{script}, env...))
			if f.runOut != "" {
				fmt.Fprint(out, f.runOut)
			}
			return f.runErr
		},
		TempDir: func() (string, error) { return os.MkdirTemp("", "smoke-test-") },
		Faults: func(stateDir string) Faults {
			if f.faults == nil {
				return nil
			}
			f.faults.dir = stateDir
			return f.faults
		},
		Telemetry: func(dir string, audit func() bool) TelemetryFunc {
			return func(ep journal.Episode, rd *scenario.Rendered) journal.Telemetry {
				if f.telemetry == nil {
					return journal.Telemetry{Source: "none", Verdict: "unknown"}
				}
				return *f.telemetry
			}
		},
	}
}

func (f *fixture) run(t *testing.T, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb strings.Builder
	code = Main(context.Background(), append([]string{"--root", f.root}, args...), Options{
		Factories:       f.factories(),
		Stdout:          &out,
		Stderr:          &errb,
		Stdin:           strings.NewReader(stdin),
		Getenv:          func(k string) string { return f.env[k] },
		Getwd:           func() (string, error) { return f.root, nil },
		IsTerminal:      f.stdoutTTY,
		IsInputTerminal: f.stdinTTY,
		Now:             f.nowFunc(),
		Executable:      func() (string, error) { return "/usr/local/bin/kubedrill", nil },
		Rand:            func(n int64) int64 { return 6 % n },
		NamespaceSuffix: func() string { return "abcde" },
	})
	return code, out.String(), errb.String()
}

// kubeCallsExcept drops the calls starting with prefix, so assertions on the
// write calls stay stable when the session reads namespace labels.
func kubeCallsExcept(calls []string, prefix string) []string {
	var out []string
	for _, c := range calls {
		if !strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

func mustNotContain(t *testing.T, got string, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		if strings.Contains(got, u) {
			t.Errorf("output contains %q; got:\n%s", u, got)
		}
	}
}

func mustContain(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("output lacks %q; got:\n%s", w, got)
		}
	}
}

// solvedEpisode is a clean solve of id: 3 minutes, no hints.
func solvedEpisode(id, scenarioID string, seed int64, start time.Time) journal.Episode {
	end := start.Add(3 * time.Minute)
	variant := "image"
	if seed%2 == 0 {
		variant = "probe"
	}
	return journal.Episode{ID: id, ScenarioID: scenarioID, Seed: seed, Variant: variant, StartedAt: start, EndedAt: &end, Outcome: journal.OutcomeSolved, Mode: journal.ModePractice}
}

// masteredEpisodes masters both catalog scenarios: three clean solves each
// on distinct seeds over two sessions (Oct 19 and Oct 20).
func masteredEpisodes() []journal.Episode {
	d1 := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	return []journal.Episode{
		solvedEpisode("a1", "001-hello-down", 1, d1),
		solvedEpisode("a2", "001-hello-down", 2, d1.Add(10*time.Minute)),
		solvedEpisode("a3", "001-hello-down", 3, d2),
		solvedEpisode("b1", "002-service-dns", 1, d1.Add(20*time.Minute)),
		solvedEpisode("b2", "002-service-dns", 2, d1.Add(30*time.Minute)),
		solvedEpisode("b3", "002-service-dns", 3, d2.Add(10*time.Minute)),
	}
}

func TestListShowsStatusAndNext(t *testing.T) {
	f := newFixture(t)
	code, out, errOut := f.run(t, "", "list")
	if code != 0 {
		t.Fatalf("exit: got %d, want 0 (%s)", code, errOut)
	}
	mustContain(t, out, "001-hello-down", "Hello app is down", "L0", "break-fix", "15", "todo", "kubedrill start 001-hello-down")
	if strings.Contains(out, "mastered") {
		t.Errorf("unexpected mastered status in %s", out)
	}
	if f.cluster.ensured != nil || len(f.kube.calls) != 0 {
		t.Errorf("list touched the cluster: ensured %v, kube %v", f.cluster.ensured, f.kube.calls)
	}
}

func TestListFilterByLevel(t *testing.T) {
	f := newFixture(t)
	_, out, _ := f.run(t, "", "list", "--level", "L1")
	rows := strings.SplitN(out, "MIN: expected", 2)[0]
	if strings.Contains(rows, "001-hello-down") || !strings.Contains(rows, "002-service-dns") {
		t.Errorf("level filter: got\n%s", out)
	}
}

func TestStartResetsAndPrintsTicket(t *testing.T) {
	f := newFixture(t)
	code, out, errOut := f.run(t, "", "start", "001", "--seed", "2")
	if code != 0 {
		t.Fatalf("exit: got %d, want 0 (%s)", code, errOut)
	}
	mustContain(t, out, "started 001-hello-down (seed 2) in namespace task-hello-abcde", "The hello app returns 503", "kubedrill check", "kubedrill hint",
		"Checked by 1 check: kubedrill check --explain\nExpected: 15 min\n")
	if strings.Contains(out, "probe") {
		t.Errorf("start output leaks the variant name:\n%s", out)
	}
	mustContain(t, errOut, "creating namespace task-hello-abcde... done\napplying setup... done\n")
	mustNotContain(t, errOut, "telemetry: audit log off")
	want := []string{"reset task-hello-abcde", "apply task-hello-abcde", "gc app.kubernetes.io/managed-by=kubedrill,kubedrill.io/journal=0123abcd keep task-hello-abcde"}
	if got := strings.Join(kubeCallsExcept(f.kube.calls, "labels"), ","); got != strings.Join(want, ",") {
		t.Errorf("kube calls: got %v, want %v", got, want)
	}
	if f.journal.current == nil || f.journal.current.ScenarioID != "001-hello-down" {
		t.Errorf("journal current: got %+v, want 001-hello-down", f.journal.current)
	}
	_, out, _ = f.run(t, "", "list")
	mustContain(t, out, "active", "Active: 001-hello-down (seed 2)")
}

func TestStartUnknownAndAmbiguous(t *testing.T) {
	f := newFixture(t)
	code, _, errOut := f.run(t, "", "start", "999")
	if code != 1 {
		t.Errorf("unknown: exit got %d, want 1", code)
	}
	mustContain(t, errOut, `kubedrill: scenario "999" not found; see kubedrill list`)
	code, _, errOut = f.run(t, "", "start", "00")
	if code != 1 {
		t.Errorf("ambiguous: exit got %d, want 1", code)
	}
	mustContain(t, errOut, "ambiguous", "001-hello-down, 002-service-dns")
}

func TestCommandsWithoutEpisode(t *testing.T) {
	f := newFixture(t)
	for _, cmd := range []string{"check", "hint", "ticket", "reset", "namespace"} {
		code, _, errOut := f.run(t, "", cmd)
		if code != 1 {
			t.Errorf("%s: exit got %d, want 1", cmd, code)
		}
		mustContain(t, errOut, "no active episode; start one with: kubedrill start <id> (see kubedrill list)")
	}
}

func TestCheckFailThenPass(t *testing.T) {
	f := newFixture(t)
	f.grader.grades = []grader.Grade{failGrade(), passGrade()}
	if code, _, errOut := f.run(t, "", "start", "001-hello-down", "--seed", "1"); code != 0 {
		t.Fatalf("start: %s", errOut)
	}
	code, out, _ := f.run(t, "", "check")
	if code != 2 {
		t.Errorf("failed check exit: got %d, want 2", code)
	}
	mustContain(t, out, "[fail] deployment hello has 2 ready replicas", "       expected: 2  actual: 0", "       why: Ready replicas are the pods that passed their readiness probe; the", "            service only routes to those, so 0 ready means every request\n            fails.", "[pass] service hello selects the pods", "1 of 2 checks passed (weighted score 0.50)")
	if strings.Count(out, "why:") != 1 {
		t.Errorf("why printed %d times, want once (under the failing check only):\n%s", strings.Count(out, "why:"), out)
	}
	if f.journal.current == nil || len(f.journal.current.Grades) != 1 {
		t.Fatalf("grade not recorded: %+v", f.journal.current)
	}
	code, out, errOut := f.run(t, "", "check")
	if code != 0 {
		t.Fatalf("passed check exit: got %d, want 0 (%s)", code, errOut)
	}
	mustContain(t, out, "2 of 2 checks passed (weighted score 1.00)", "Solved: Hello app is down in 3.0 min with 0 hints (variant image)", "Next: kubedrill solution 001-hello-down (compare with the reference) | kubedrill list")
	if f.journal.current != nil {
		t.Errorf("episode still current after solve")
	}
	_, out, _ = f.run(t, "", "list")
	mustContain(t, out, "solved")
}

func TestHintSequence(t *testing.T) {
	f := newFixture(t)
	f.run(t, "", "start", "001-hello-down", "--seed", "1")
	code, out, _ := f.run(t, "", "hint")
	if code != 0 {
		t.Fatalf("hint 1 exit %d", code)
	}
	mustContain(t, out, "hint 1 of 2 (where to look)", "Look at the pod events.")
	code, _, errOut := f.run(t, "", "hint")
	if code != 1 {
		t.Errorf("last hint without diagnosis: exit %d, want 1", code)
	}
	mustContain(t, errOut, `kubedrill: the last hint needs your diagnosis first: kubedrill diagnose "<what you think is wrong>"`)
	if code, out, errOut := f.run(t, "", "diagnose", "the image tag does not exist"); code != 0 {
		t.Fatalf("diagnose exit %d: %s", code, errOut)
	} else {
		mustContain(t, out, "diagnosis recorded for 001-hello-down: the image tag does not exist")
	}
	code, out, errOut = f.run(t, "", "hint")
	if code != 0 {
		t.Fatalf("last hint after diagnosis: exit %d (%s)", code, errOut)
	}
	mustContain(t, out, "hint 2 of 2 (what to observe)")
	code, out, _ = f.run(t, "", "hint")
	if code != 0 {
		t.Errorf("exhausted hint exit: got %d, want 0", code)
	}
	mustContain(t, out, "all 2 hints shown. Next: kubedrill solution (surrender) or keep investigating")
	if f.journal.current.HintsUsed != 2 {
		t.Errorf("hints used: got %d, want 2", f.journal.current.HintsUsed)
	}
}

func TestSolutionSurrenderAndSolved(t *testing.T) {
	f := newFixture(t)
	f.run(t, "", "start", "001-hello-down", "--seed", "1")
	code, _, errOut := f.run(t, "", "solution", "--yes")
	if code != 1 || !strings.Contains(errOut, `the solution needs your diagnosis first (--yes skips only the confirmation): kubedrill diagnose "<cause>" then kubedrill solution --yes`) {
		t.Errorf("surrender without diagnosis: exit %d err %q", code, errOut)
	}
	f.run(t, "", "diagnose", "the image tag does not exist")
	code, out, errOut := f.run(t, "", "solution")
	if code != 1 || !strings.Contains(errOut, "confirmation needed (stdin is not a terminal): rerun with --yes") {
		t.Errorf("solution without input: got exit %d err %q, want 1 naming --yes", code, errOut)
	}
	mustContain(t, out, "Surrender 001-hello-down and show the solution? The episode will be recorded as abandoned. [y/N]")
	code, out, _ = f.run(t, "", "solution", "--yes")
	if code != 0 {
		t.Fatalf("surrender exit %d", code)
	}
	mustContain(t, out, "recorded as abandoned", "The image tag has a typo")
	if got := f.journal.episodes[0].Outcome; got != journal.OutcomeAbandoned {
		t.Errorf("outcome: got %s, want abandoned", got)
	}
	code, out, _ = f.run(t, "", "solution", "001")
	if code != 0 || !strings.Contains(out, "The image tag has a typo") {
		t.Errorf("solution of abandoned scenario: got exit %d out %q, want the walkthrough", code, out)
	}
	code, out, _ = f.run(t, "", "solution")
	if code != 0 || !strings.Contains(out, "The image tag has a typo") {
		t.Errorf("solution without ref after an ended episode: got exit %d out %q", code, out)
	}
	code, out, errOut = f.run(t, "", "solution", "002")
	if code != 1 || !strings.Contains(errOut, "rerun with --yes") || strings.Contains(out, "Root cause two") {
		t.Errorf("solution of an unplayed scenario without input: exit %d out %q err %q", code, out, errOut)
	}
	mustContain(t, out, "Scenario 002-service-dns was never solved or abandoned. Show the solution anyway?")
	code, out, _ = f.run(t, "n\n", "solution", "002")
	if code != 0 || strings.Contains(out, "Root cause two") || !strings.Contains(out, "kept the solution of 002-service-dns hidden") {
		t.Errorf("declined unplayed solution: exit %d out %q", code, out)
	}
	code, out, _ = f.run(t, "y\n", "solution", "002")
	if code != 0 || !strings.Contains(out, "Root cause two") {
		t.Errorf("piped yes: exit %d out %q", code, out)
	}
	f.grader.grades = []grader.Grade{passGrade()}
	f.run(t, "", "start", "001-hello-down", "--seed", "1")
	f.run(t, "", "check")
	code, out, _ = f.run(t, "", "solution", "001")
	if code != 0 || !strings.Contains(out, "Candidate causes") {
		t.Errorf("solution of solved scenario: got exit %d out %q", code, out)
	}
}

func TestResetDrawsNewSeedOrKeepsIt(t *testing.T) {
	f := newFixture(t)
	f.run(t, "", "start", "001-hello-down", "--seed", "2")
	code, out, _ := f.run(t, "", "reset", "--same-seed")
	if code != 0 {
		t.Fatalf("reset exit %d", code)
	}
	mustContain(t, out, "reset 001-hello-down: seed 2, namespace task-hello-abcde (hints reset to 0) (same seed)")
	if strings.Contains(out, "probe") {
		t.Errorf("reset output leaks the variant:\n%s", out)
	}
	if len(f.journal.episodes) != 2 || f.journal.episodes[0].Outcome != journal.OutcomeFailed {
		t.Errorf("episodes after reset: %+v", f.journal.episodes)
	}
	code, out, _ = f.run(t, "", "reset")
	if code != 0 || !strings.Contains(out, "reset 001-hello-down: seed ") || strings.Contains(out, "(same seed)") {
		t.Errorf("reset with new seed: exit %d out %q", code, out)
	}
}

func TestStatusAndJournal(t *testing.T) {
	f := newFixture(t)
	f.cluster.exists = false
	code, out, _ := f.run(t, "", "status")
	if code != 0 {
		t.Fatalf("status exit %d", code)
	}
	mustContain(t, out, "cluster kubedrill: missing; create it with: kubedrill up", "episode: none", "solved: 0 of 2 scenarios (mastered: 0)", "journal: "+f.journal.dir)
	f.cluster.exists = true
	f.grader.grades = []grader.Grade{passGrade()}
	f.run(t, "", "start", "001-hello-down", "--seed", "1")
	_, out, _ = f.run(t, "", "status")
	mustContain(t, out, "cluster kubedrill: present", "episode: 001-hello-down (seed 1, practice), elapsed")
	if strings.Contains(out, "variant") {
		t.Errorf("status leaks the variant:\n%s", out)
	}
	_, out, _ = f.run(t, "", "journal")
	mustContain(t, out, "001-hello-down  1     practice  -        active", "- : hidden while the episode is active")
	f.run(t, "", "check")
	f.run(t, "", "start", "001-hello-down", "--seed", "3")
	f.run(t, "", "check")
	_, out, _ = f.run(t, "", "status")
	mustContain(t, out, "solved: 1 of 2 scenarios (mastered: 0)")
	_, out, _ = f.run(t, "", "journal")
	mustContain(t, out, "STARTED", "001-hello-down", "solved", "3.0 min", "image", "probe")
	mustNotContain(t, out, "hidden while the episode is active")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.Contains(lines[1], "probe") || !strings.Contains(lines[2], "image") {
		t.Errorf("journal order (want newest first):\n%s", out)
	}
	_, out, _ = f.run(t, "", "journal", "--last", "1")
	if lines := strings.Split(strings.TrimSpace(out), "\n"); len(lines) != 2 || !strings.Contains(lines[1], "probe") {
		t.Errorf("journal --last 1:\n%s", out)
	}
	_, out, _ = f.run(t, "", "journal", "--scenario", "002")
	mustContain(t, out, "no episodes for 002-service-dns")
	_, out, _ = f.run(t, "", "journal", "--scenario", "001")
	mustContain(t, out, "001-hello-down")
	code, _, errOut := f.run(t, "", "journal", "--scenario", "zzz")
	if code != 1 {
		t.Errorf("journal unknown ref exit %d", code)
	}
	mustContain(t, errOut, `scenario "zzz" not found; see kubedrill list`)
	f.journal.problems = []string{"episodes.jsonl line 3: invalid character"}
	_, _, errOut = f.run(t, "", "journal")
	mustContain(t, errOut, "warning: journal: episodes.jsonl line 3: invalid character")
}

func TestPathAndExam(t *testing.T) {
	f := newFixture(t)
	f.journal.episodes = []journal.Episode{solvedEpisode("x", "001-hello-down", 1, time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC))}
	code, out, _ := f.run(t, "", "path")
	if code != 0 {
		t.Fatalf("path exit %d", code)
	}
	mustContain(t, out, "L0  First contact  (6 h)", "workloads-basics", "(6 h)  badge: earned\n", "2 planned, 1 existing, 1 solved; badge: earned", "          concepts: pods 1/1 clean, deployments 1/1 clean", "\nNext: Hello app is down (001-hello-down, L0, 15 min): you solved your last attempt at L0\n", "  kubedrill start 001-hello-down\n")
	f.journal.episodes = masteredEpisodes()
	_, out, _ = f.run(t, "", "path")
	mustContain(t, out, "concepts: pods mastered, deployments mastered", "(6 h)  badge: mastered\n", "1 solved; badge: mastered")
	code, out, errOut := f.run(t, "", "exam", "cka")
	if code != 0 {
		t.Fatalf("exam exit %d: %s", code, errOut)
	}
	mustContain(t, out, "DOMAIN", "WEIGHT", "DONE/TOTAL", "Workloads and scheduling", "15%", "  0/0")
	code, _, errOut = f.run(t, "", "exam", "foo")
	if code != 1 {
		t.Errorf("unknown exam exit %d", code)
	}
	mustContain(t, errOut, `unknown exam "FOO"; valid: CKA`)
	code, _, errOut = f.run(t, "", "exam")
	if code != 1 {
		t.Errorf("exam without argument exit %d", code)
	}
	mustContain(t, errOut, "usage: kubedrill exam <CKA|CKAD|CKS>")
}

func TestLint(t *testing.T) {
	f := newFixture(t)
	code, out, _ := f.run(t, "", "lint")
	if code != 0 || !strings.Contains(out, "lint: ok") {
		t.Errorf("clean lint: exit %d out %q", code, out)
	}
	if strings.Join(f.lintMods, ",") != "workloads-basics" {
		t.Errorf("lint module ids: got %v, want [workloads-basics]", f.lintMods)
	}
	if strings.Join(f.lintConc, ",") != "pods,deployments,services" {
		t.Errorf("lint concept ids: got %v, want [pods deployments services]", f.lintConc)
	}
	if got := strings.Join(f.lintRefs.ExamDomains, ","); got != "CKA:workloads" {
		t.Errorf("lint exam domains: got %q, want CKA:workloads", got)
	}
	if want := map[string]string{"001-hello-down": "workloads-basics"}; !reflect.DeepEqual(f.lintRefs.Planned, want) {
		t.Errorf("lint planned scenarios: got %v, want %v", f.lintRefs.Planned, want)
	}
	f.lint = []error{errors.New("scenarios/L0/001: hints.md missing")}
	f.currErr = errors.New("load curriculum: module x references unknown exam")
	code, out, errOut := f.run(t, "", "lint")
	if code != 1 {
		t.Errorf("failing lint exit: got %d, want 1", code)
	}
	mustContain(t, out, "hints.md missing", "unknown exam")
	mustContain(t, errOut, "2 problem(s)")
}

func TestLintConceptProblems(t *testing.T) {
	t.Run("curriculum concept without a page", func(t *testing.T) {
		f := newFixture(t)
		f.curr.Levels[0].Modules[0].Concepts = []string{"pods", "operators"}
		code, out, errOut := f.run(t, "", "lint")
		if code != 1 {
			t.Errorf("exit: got %d, want 1", code)
		}
		mustContain(t, out, `curriculum.yaml: concept "operators" has no page in the concept library`)
		mustContain(t, errOut, "1 problem(s)")
	})
	t.Run("library errors are lint problems", func(t *testing.T) {
		f := newFixture(t)
		f.concErr = errors.New("concepts/pods.md: missing section \"## Why it matters\"")
		code, out, _ := f.run(t, "", "lint")
		if code != 1 {
			t.Errorf("exit: got %d, want 1", code)
		}
		mustContain(t, out, "concepts/pods.md: missing section")
		if f.lintConc != nil {
			t.Errorf("concept ids passed to Lint after a load failure: %v", f.lintConc)
		}
		if strings.Contains(out, "has no page") {
			t.Errorf("curriculum concepts reported against a library that failed to load:\n%s", out)
		}
	})
}

func TestNew(t *testing.T) {
	f := newFixture(t)
	code, out, _ := f.run(t, "", "new", "L0", "011-my-slug")
	if code != 0 {
		t.Fatalf("new exit %d", code)
	}
	mustContain(t, out, filepath.Join("scenarios", "L0", "011-my-slug"), "kubedrill lint", "kubedrill smoke 011-my-slug")
}

func TestDoctorAndUpDown(t *testing.T) {
	f := newFixture(t)
	code, out, _ := f.run(t, "", "doctor")
	if code != 0 {
		t.Fatalf("doctor exit %d", code)
	}
	mustContain(t, out, "TOOL", "docker", "yes", "27.1", "daemon reachable", "kubectl  yes    1.37     -")
	f.faults = &fakeFaults{}
	code, out, _ = f.run(t, "", "up")
	if code != 0 || len(f.cluster.ensured) != 1 || !strings.Contains(out, "cluster kubedrill: present (profile base)") || !strings.Contains(out, "addons: metrics-server") {
		t.Errorf("up with cluster present: exit %d ensured %v out %q", code, f.cluster.ensured, out)
	}
	if f.faults.forgot != 0 {
		t.Errorf("up on an existing cluster forgot the faults %d times, want 0", f.faults.forgot)
	}
	f.cluster.exists = false
	code, out, _ = f.run(t, "", "up")
	if code != 0 || len(f.cluster.ensured) != 2 || f.cluster.ensured[1] != "base" {
		t.Errorf("up: exit %d ensured %v out %q", code, f.cluster.ensured, out)
	}
	if f.faults.forgot != 1 {
		t.Errorf("up creating the cluster forgot the faults %d times, want 1", f.faults.forgot)
	}
	mustContain(t, out, "creating cluster kubedrill (profile base), about 2 minutes...", "cluster kubedrill is ready (profile base)")
	audit := filepath.Join(f.root, "state", "kubedrill", "kubedrill", "audit")
	if d := f.cluster.data[0]; d.AuditDir != audit || d.AuditPolicy != filepath.Join(f.root, "cluster", "audit-policy.yaml") {
		t.Errorf("profile data: got %+v, want audit dir %s and the repository audit policy", d, audit)
	}
	code, _, errOut := f.run(t, "", "down")
	if code != 1 || !strings.Contains(errOut, "--yes") || f.cluster.deleted != 0 {
		t.Errorf("down without confirmation: exit %d err %q deleted %d", code, errOut, f.cluster.deleted)
	}
	code, out, _ = f.run(t, "", "down", "--yes")
	if code != 0 || f.cluster.deleted != 1 || !strings.Contains(out, "deleted") {
		t.Errorf("down --yes: exit %d deleted %d out %q", code, f.cluster.deleted, out)
	}
	if f.faults.forgot != 2 {
		t.Errorf("down forgot the faults %d times in total, want 2", f.faults.forgot)
	}
	f.cluster.tools[0] = cluster.Tool{Name: "docker", Found: false, Install: "https://docs.docker.com/get-docker/"}
	code, out, errOut = f.run(t, "", "doctor")
	if code != 1 {
		t.Errorf("doctor with missing docker: exit %d", code)
	}
	mustContain(t, out, "install: https://docs.docker.com/get-docker/")
	mustContain(t, errOut, "docker is missing")
}

func TestSmoke(t *testing.T) {
	f := newFixture(t)
	f.grader.grades = []grader.Grade{failGrade(), passGrade(), failGrade(), passGrade(), failGrade(), passGrade()}
	code, out, errOut := f.run(t, "", "smoke", "001")
	if code != 0 {
		t.Fatalf("smoke exit %d: %s\n%s", code, errOut, out)
	}
	mustContain(t, out, "ok   001-hello-down seed 1 (variant image)", "ok   001-hello-down seed 3 (variant probe)", "smoke: 2 runs passed")
	if len(f.runs) != 2 || f.runs[0][0] != "/scenarios/L0/001-hello-down/solution/solve.sh" {
		t.Fatalf("solve runs: got %v", f.runs)
	}
	mustContain(t, strings.Join(f.runs[1], " "), "TASK_NS=task-hello-abcde", "SEED=3", "VARIANT=probe", "VAR_IMAGE=nginx:1.26", "KUBECONFIG=")
	if len(f.pinned) == 0 || !strings.HasSuffix(f.pinned[0], " /tmp/kubeconfig kind-kubedrill") {
		t.Errorf("script kubeconfig not pinned: %v", f.pinned)
	}
	mustContain(t, errOut, "running scenario scripts from "+f.root)
	if last := f.kube.calls[len(f.kube.calls)-2]; last != "delete task-hello-abcde" {
		t.Errorf("last namespace delete: got %s, want delete task-hello-abcde", last)
	}
	if len(f.journal.episodes) != 0 || f.journal.current != nil {
		t.Errorf("learner journal touched by smoke: %+v", f.journal.episodes)
	}
}

func TestSmokeFailures(t *testing.T) {
	t.Run("passes after setup", func(t *testing.T) {
		f := newFixture(t)
		f.grader.grades = []grader.Grade{passGrade()}
		code, out, _ := f.run(t, "", "smoke", "002")
		if code != 1 {
			t.Errorf("exit: got %d, want 1", code)
		}
		mustContain(t, out, "FAIL 002-service-dns seed 1 (variant selector): checks pass right after setup")
	})
	t.Run("still failing after solve", func(t *testing.T) {
		f := newFixture(t)
		f.grader.grades = []grader.Grade{failGrade()}
		code, out, _ := f.run(t, "", "smoke", "002")
		if code != 1 {
			t.Errorf("exit: got %d, want 1", code)
		}
		mustContain(t, out, "checks still failing after solve.sh: [fail] ready (expected 2, actual 0)")
	})
	t.Run("solve script fails", func(t *testing.T) {
		f := newFixture(t)
		f.grader.grades = []grader.Grade{failGrade()}
		f.runErr = errors.New("exit status 1")
		code, out, _ := f.run(t, "", "smoke", "002")
		if code != 1 || !strings.Contains(out, "solve.sh: exit status 1") {
			t.Errorf("exit %d out %q", code, out)
		}
	})
}

func TestRepoRootResolution(t *testing.T) {
	f := newFixture(t)
	nested := filepath.Join(f.root, "scenarios", "L0")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	code := Main(context.Background(), []string{"list"}, Options{
		Factories: f.factories(),
		Stdout:    &out,
		Stderr:    &errb,
		Getenv:    func(string) string { return "" },
		Getwd:     func() (string, error) { return nested, nil },
	})
	if code != 0 || !strings.Contains(out.String(), "001-hello-down") {
		t.Errorf("walk up: exit %d err %q", code, errb.String())
	}
	errb.Reset()
	code = Main(context.Background(), []string{"list"}, Options{
		Factories: f.factories(),
		Stdout:    &out,
		Stderr:    &errb,
		Getenv:    func(string) string { return "" },
		Getwd:     func() (string, error) { return t.TempDir(), nil },
	})
	if code != 1 || !strings.Contains(errb.String(), "repository root not found") || !strings.Contains(errb.String(), "KUBEDRILL_ROOT") {
		t.Errorf("outside repo: exit %d err %q", code, errb.String())
	}
	errb.Reset()
	code = Main(context.Background(), []string{"list"}, Options{
		Factories: f.factories(),
		Stdout:    &out,
		Stderr:    &errb,
		Getenv: func(k string) string {
			if k == "KUBEDRILL_ROOT" {
				return f.root
			}
			return ""
		},
		Getwd: func() (string, error) { return "/", nil },
	})
	if code != 0 {
		t.Errorf("KUBEDRILL_ROOT: exit %d err %q", code, errb.String())
	}
}

func TestVersionLine(t *testing.T) {
	got := versionLine(func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{GoVersion: "go1.27.2", Main: debug.Module{Version: "v0.1.0"}, Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "0123456789abcdef"}, {Key: "vcs.time", Value: "2026-10-25T10:00:00Z"}, {Key: "vcs.modified", Value: "true"},
		}}, true
	})
	want := "kubedrill v0.1.0 rev 0123456789ab (modified) 2026-10-25T10:00:00Z go1.27.2"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	got = versionLine(func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{GoVersion: "go1.27.2"}, true
	})
	if want := "kubedrill (devel) revision unknown (built from an unversioned tree) go1.27.2"; got != want {
		t.Errorf("no vcs: got %q, want %q", got, want)
	}
	if got := versionLine(func() (*debug.BuildInfo, bool) { return nil, false }); got != "kubedrill (no build info)" {
		t.Errorf("no build info: got %q", got)
	}
}

func TestStartWhileActiveAsksToAbandon(t *testing.T) {
	f := newFixture(t)
	f.run(t, "", "start", "001-hello-down", "--seed", "1")
	code, out, errOut := f.run(t, "", "start", "002")
	if code != 1 || !strings.Contains(errOut, "rerun with --yes") {
		t.Errorf("start over active without input: exit %d err %q", code, errOut)
	}
	mustContain(t, out, "Leave the active episode of 001-hello-down? It will be recorded as failed. [y/N]")
	code, out, _ = f.run(t, "n\n", "start", "002")
	if code != 0 || !strings.Contains(out, "kept the episode active") || f.journal.current.ScenarioID != "001-hello-down" {
		t.Errorf("declined abandon: exit %d out %q current %+v", code, out, f.journal.current)
	}
	code, out, _ = f.run(t, "", "start", "002", "--yes")
	if code != 0 {
		t.Fatalf("start --yes exit %d", code)
	}
	mustContain(t, out, "leaving the episode of 001-hello-down (recorded as failed)", "started 002-service-dns")
	if f.journal.episodes[0].Outcome != journal.OutcomeFailed || f.journal.current.ScenarioID != "002-service-dns" {
		t.Errorf("episodes after start --yes: %+v", f.journal.episodes)
	}
}

func TestStartNeedsClusterAndArgument(t *testing.T) {
	f := newFixture(t)
	f.cluster.exists = false
	code, _, errOut := f.run(t, "", "start", "001")
	if code != 1 || len(f.kube.calls) != 0 {
		t.Errorf("start without cluster: exit %d kube calls %v", code, f.kube.calls)
	}
	mustContain(t, errOut, "cluster kubedrill: missing; create it with: kubedrill up")
	code, _, errOut = f.run(t, "", "start")
	if code != 1 {
		t.Errorf("start without ref: exit %d", code)
	}
	mustContain(t, errOut, "usage: kubedrill start <ref>; see kubedrill list")
	_, out, _ := f.run(t, "", "start", "--help")
	mustContain(t, out, "seed for the variant (0 or omitted: random)")
}

func TestListAllSolved(t *testing.T) {
	f := newFixture(t)
	f.journal.episodes = masteredEpisodes()
	f.now = time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	_, out, _ := f.run(t, "", "list")
	mustContain(t, out, "every existing scenario is mastered and no review is due; curriculum.yaml lists the planned ones, CONTRIBUTING.md explains how to add one")
}

func TestNewHelpExample(t *testing.T) {
	f := newFixture(t)
	_, out, _ := f.run(t, "", "new", "--help")
	mustContain(t, out, "new L0 001-my-slug")
}

func TestSmokeGuardsActiveEpisode(t *testing.T) {
	f := newFixture(t)
	f.grader.grades = []grader.Grade{failGrade()}
	f.run(t, "", "start", "001-hello-down", "--seed", "1")
	before := len(f.kube.calls)
	code, _, errOut := f.run(t, "", "smoke", "001")
	if code != 1 || len(f.kube.calls) != before {
		t.Errorf("smoke over active episode: exit %d kube calls %v", code, f.kube.calls[before:])
	}
	mustContain(t, errOut, "an active episode of 001-hello-down runs in namespace task-hello-abcde; setup hooks may change cluster-wide state, so finish it (kubedrill reset, or kubedrill diagnose \"<cause>\" then kubedrill solution --yes) or pass --force")
	code, out, _ := f.run(t, "", "smoke", "002")
	if code != 1 || !strings.Contains(out, "FAIL 002-service-dns") {
		t.Errorf("smoke of another namespace blocked: exit %d out %q", code, out)
	}
	f.grader.grades = []grader.Grade{failGrade(), passGrade(), failGrade(), passGrade()}
	f.grader.n = 0
	code, out, _ = f.run(t, "", "smoke", "001", "--force")
	if code != 0 {
		t.Errorf("smoke --force: exit %d out %q", code, out)
	}
	if f.journal.current == nil || f.journal.current.ScenarioID != "001-hello-down" {
		t.Errorf("learner episode changed by smoke: %+v", f.journal.current)
	}
}

func TestSmokeOutputOnlyOnFailureOrVerbose(t *testing.T) {
	f := newFixture(t)
	f.runOut = "deployment.apps/hello configured\n"
	f.grader.grades = []grader.Grade{failGrade(), passGrade()}
	_, out, _ := f.run(t, "", "smoke", "002")
	if strings.Contains(out, "hello configured") {
		t.Errorf("passing run printed solve.sh output:\n%s", out)
	}
	f.grader.n = 0
	_, out, _ = f.run(t, "", "smoke", "002", "-v")
	mustContain(t, out, "     deployment.apps/hello configured")
	f.grader.grades = []grader.Grade{failGrade()}
	f.grader.n = 0
	f.runErr = errors.New("exit status 1")
	_, out, _ = f.run(t, "", "smoke", "002")
	mustContain(t, out, "FAIL 002-service-dns", "     deployment.apps/hello configured")
}

func TestSmokeDeletesEveryNamespaceAtTheEnd(t *testing.T) {
	f := newFixture(t)
	f.grader.grades = []grader.Grade{failGrade(), passGrade(), failGrade(), passGrade(), failGrade(), passGrade()}
	code, _, errOut := f.run(t, "", "smoke")
	if code != 0 {
		t.Fatalf("smoke exit %d: %s", code, errOut)
	}
	n := len(f.kube.calls)
	if n < 3 || f.kube.calls[n-3] != "delete task-dns-abcde" || f.kube.calls[n-2] != "delete task-hello-abcde" || f.kube.calls[n-1] != "gc app.kubernetes.io/managed-by=kubedrill,kubedrill.io/journal=0123abcd keep " {
		t.Errorf("last kube calls: %v", f.kube.calls)
	}
	for i, c := range f.kube.calls[:n-3] {
		ns, ok := strings.CutPrefix(c, "delete ")
		if !ok {
			continue
		}
		if i == 0 || f.kube.calls[i-1] != "labels "+ns {
			t.Errorf("namespace %s deleted before the end other than by the solved episode's end: %v", ns, f.kube.calls)
		}
	}
}

func TestConfirmOnTerminalWithoutInput(t *testing.T) {
	f := newFixture(t)
	f.stdoutTTY = true
	f.run(t, "", "start", "001-hello-down", "--seed", "1")
	f.run(t, "", "diagnose", "the image tag does not exist")
	code, out, errOut := f.run(t, "", "solution")
	if code != 1 || !strings.Contains(errOut, "rerun with --yes") {
		t.Errorf("stdout terminal, stdin empty reader: exit %d out %q err %q", code, out, errOut)
	}
}

func TestStartNegativeSeed(t *testing.T) {
	f := newFixture(t)
	code, _, errOut := f.run(t, "", "start", "001", "--seed", "-1")
	if code != 1 {
		t.Errorf("exit: got %d, want 1", code)
	}
	mustContain(t, errOut, "kubedrill: seed must be >= 1 (0 draws one), got -1")
	if len(f.kube.calls) != 0 {
		t.Errorf("kube calls after rejected seed: got %v, want none", f.kube.calls)
	}
	if f.journal.current != nil {
		t.Errorf("episode started with a rejected seed: %+v", f.journal.current)
	}
}

func TestListEmptyLevel(t *testing.T) {
	f := newFixture(t)
	code, out, errOut := f.run(t, "", "list", "--level", "L9")
	if code != 0 {
		t.Fatalf("exit: got %d, want 0 (%s)", code, errOut)
	}
	if got, want := strings.TrimSpace(out), "no scenarios found"; got != want {
		t.Errorf("stdout: got %q, want %q", got, want)
	}
}

func TestListMastered(t *testing.T) {
	f := newFixture(t)
	day1 := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	hinted := solvedEpisode("c", "002-service-dns", 1, day1)
	hinted.HintsUsed = 1
	f.journal.episodes = append(masteredEpisodes()[:3], hinted)
	code, out, errOut := f.run(t, "", "list")
	if code != 0 {
		t.Fatalf("exit: got %d, want 0 (%s)", code, errOut)
	}
	var hello, dns string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "001-hello-down"):
			hello = line
		case strings.HasPrefix(line, "002-service-dns"):
			dns = line
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(hello), "mastered") {
		t.Errorf("001 row: got %q, want status mastered", hello)
	}
	if !strings.HasSuffix(strings.TrimSpace(dns), "solved") {
		t.Errorf("002 row: got %q, want status solved (hint used)", dns)
	}
}

func TestRepoRootInvalid(t *testing.T) {
	f := newFixture(t)
	empty := t.TempDir()
	var out, errb strings.Builder
	code := Main(context.Background(), []string{"--root", empty, "list"}, Options{
		Factories: f.factories(),
		Stdout:    &out,
		Stderr:    &errb,
		Getenv:    func(string) string { return "" },
		Getwd:     func() (string, error) { return f.root, nil },
	})
	if code != 1 {
		t.Errorf("exit: got %d, want 1", code)
	}
	mustContain(t, errb.String(), "kubedrill: root "+empty+" (from --root) lacks curriculum.yaml or scenarios/; point --root at the kubedrill checkout")
	if out.Len() != 0 {
		t.Errorf("stdout: got %q, want empty", out.String())
	}
	errb.Reset()
	code = Main(context.Background(), []string{"check"}, Options{
		Factories: f.factories(),
		Stdout:    &out,
		Stderr:    &errb,
		Getenv: func(k string) string {
			if k == "KUBEDRILL_ROOT" {
				return empty
			}
			return ""
		},
		Getwd: func() (string, error) { return f.root, nil },
	})
	if code != 1 {
		t.Errorf("KUBEDRILL_ROOT exit: got %d, want 1", code)
	}
	mustContain(t, errb.String(), "kubedrill: root "+empty+" (from KUBEDRILL_ROOT) lacks curriculum.yaml or scenarios/; point KUBEDRILL_ROOT at the kubedrill checkout")
}

func TestSmokeArgs(t *testing.T) {
	t.Run("unknown ref", func(t *testing.T) {
		f := newFixture(t)
		code, _, errOut := f.run(t, "", "smoke", "999")
		if code != 1 {
			t.Errorf("exit: got %d, want 1", code)
		}
		mustContain(t, errOut, `kubedrill: scenario "999" not found; see kubedrill list`)
		if len(f.kube.calls) != 0 || len(f.runs) != 0 {
			t.Errorf("smoke with unknown ref touched the cluster: kube %v runs %v", f.kube.calls, f.runs)
		}
	})
	t.Run("no args runs every scenario", func(t *testing.T) {
		f := newFixture(t)
		f.grader.grades = []grader.Grade{failGrade(), passGrade(), failGrade(), passGrade(), failGrade(), passGrade()}
		code, out, errOut := f.run(t, "", "smoke")
		if code != 0 {
			t.Fatalf("exit: got %d, want 0 (%s)", code, errOut)
		}
		mustContain(t, out, "ok   001-hello-down seed 1 (variant image)", "ok   001-hello-down seed 3 (variant probe)", "ok   002-service-dns seed 1 (variant selector)", "smoke: 3 runs passed")
		if len(f.runs) != 3 {
			t.Errorf("solve runs: got %d, want 3", len(f.runs))
		}
	})
	t.Run("missing solve.sh", func(t *testing.T) {
		f := newFixture(t)
		f.catalog.all[1].SolveScript = ""
		f.grader.grades = []grader.Grade{failGrade()}
		code, out, _ := f.run(t, "", "smoke", "002")
		if code != 1 {
			t.Errorf("exit: got %d, want 1", code)
		}
		mustContain(t, out, "FAIL 002-service-dns seed 1 (variant selector): solution/solve.sh is missing")
		if len(f.runs) != 0 {
			t.Errorf("solve runs without a script: got %v, want none", f.runs)
		}
	})
}

func TestSolutionConfirmFlow(t *testing.T) {
	f := newFixture(t)
	f.run(t, "", "start", "001-hello-down", "--seed", "1")
	f.run(t, "", "diagnose", "the image tag does not exist")
	code, out, _ := f.run(t, "n\n", "solution", "001")
	if code != 0 {
		t.Errorf("declined: exit got %d, want 0", code)
	}
	mustContain(t, out, "Surrender 001-hello-down and show the solution? The episode will be recorded as abandoned. [y/N]", "kept the episode active")
	if strings.Contains(out, "The image tag has a typo") {
		t.Errorf("declined surrender printed the walkthrough:\n%s", out)
	}
	if f.journal.current == nil || f.journal.current.ScenarioID != "001-hello-down" {
		t.Fatalf("current after declined surrender: got %+v, want 001-hello-down active", f.journal.current)
	}
	code, out, _ = f.run(t, "y\n", "solution", "001")
	if code != 0 {
		t.Errorf("accepted: exit got %d, want 0", code)
	}
	mustContain(t, out, "episode of 001-hello-down recorded as abandoned", "The image tag has a typo")
	if f.journal.current != nil {
		t.Errorf("current after accepted surrender: got %+v, want nil", f.journal.current)
	}
	if got := f.journal.episodes[0].Outcome; got != journal.OutcomeAbandoned {
		t.Errorf("outcome: got %s, want abandoned", got)
	}
}

func TestVersionThroughMain(t *testing.T) {
	f := newFixture(t)
	code, out, errOut := f.run(t, "", "version")
	if code != 0 {
		t.Fatalf("exit: got %d, want 0 (%s)", code, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "kubedrill ") || lines[1] != "source: "+repoURL {
		t.Errorf("stdout: got %q, want the build line and the source line", out)
	}
	if errOut != "" {
		t.Errorf("stderr: got %q, want empty", errOut)
	}
}

func TestNamespaceCommand(t *testing.T) {
	f := newFixture(t)
	f.run(t, "", "start", "001", "--seed", "1")
	code, out, errOut := f.run(t, "", "namespace")
	if code != 0 {
		t.Fatalf("exit: got %d, want 0 (%s)", code, errOut)
	}
	if out != "task-hello-abcde\n" {
		t.Errorf("stdout: got %q, want \"task-hello-abcde\\n\"", out)
	}
}

func TestStatusInsideWorkstation(t *testing.T) {
	f := newFixture(t)
	f.env = map[string]string{"KUBEDRILL_WORKSTATION": "1"}
	_, out, _ := f.run(t, "", "status")
	mustContain(t, out, "cluster kubedrill: present (inside the workstation)\n")
	f.cluster.exists = false
	_, out, _ = f.run(t, "", "status")
	mustContain(t, out, "cluster kubedrill: missing; create it with: kubedrill up")
	mustNotContain(t, out, "inside the workstation")
}

func TestCheckNamespaceErrorsExitOne(t *testing.T) {
	for _, sentinel := range []error{env.ErrNamespaceMissing, env.ErrNamespaceForeign} {
		f := newFixture(t)
		f.run(t, "", "start", "001", "--seed", "1")
		f.grader.err = fmt.Errorf("namespace task-hello: %w; run kubedrill reset", sentinel)
		code, out, errOut := f.run(t, "", "check")
		if code != 1 {
			t.Errorf("%v: exit got %d, want 1", sentinel, code)
		}
		if strings.Count(errOut, "\n") != 1 || !strings.Contains(errOut, sentinel.Error()) || !strings.Contains(errOut, "kubedrill reset") {
			t.Errorf("%v: stderr got %q, want one line with the error text", sentinel, errOut)
		}
		if strings.Contains(out, "checks passed") {
			t.Errorf("%v: a grade was printed:\n%s", sentinel, out)
		}
	}
}

func TestProgressLineClosedOnError(t *testing.T) {
	f := newFixture(t)
	f.runErr = errors.New("hook exit status 1")
	f.catalog.all[0].HookScript = "/scenarios/L0/001-hello-down/setup/hook.sh"
	code, _, errOut := f.run(t, "", "start", "001", "--seed", "1")
	if code != 1 {
		t.Errorf("exit: got %d, want 1", code)
	}
	if !strings.Contains(errOut, "applying setup... \n") || !strings.HasSuffix(errOut, "\n") {
		t.Errorf("stderr: %q", errOut)
	}
	for _, line := range strings.Split(strings.TrimRight(errOut, "\n"), "\n") {
		if strings.HasPrefix(line, "kubedrill:") && strings.Contains(line, "... ") {
			t.Errorf("error printed on the progress line: %q", line)
		}
	}
}

func TestSetupHookOutputStaysOffTheTerminal(t *testing.T) {
	f := newFixture(t)
	f.catalog.all[0].HookScript = "/scenarios/L0/001-hello-down/setup/hook.sh"
	f.runOut = "deployment \"hello\" successfully rolled out\n"
	code, out, errOut := f.run(t, "", "start", "001", "--seed", "1")
	if code != 0 {
		t.Fatalf("start: exit %d (%s)", code, errOut)
	}
	mustNotContain(t, out+errOut, "successfully rolled out")

	f = newFixture(t)
	f.catalog.all[0].HookScript = "/scenarios/L0/001-hello-down/setup/hook.sh"
	f.runOut = "error: timed out waiting for the condition\n"
	f.runErr = errors.New("exit status 1")
	code, _, errOut = f.run(t, "", "start", "001", "--seed", "1")
	if code != 1 {
		t.Fatalf("start with a failing hook: exit %d, want 1", code)
	}
	mustContain(t, errOut, "its output ends with:", "error: timed out waiting for the condition")
}

func TestTailBufferKeepsTheEnd(t *testing.T) {
	var b tailBuffer
	fmt.Fprint(&b, strings.Repeat("a", hookOutputLimit))
	fmt.Fprint(&b, "end")
	got := b.String()
	if !strings.HasPrefix(got, "...") || !strings.HasSuffix(got, "end") || len(got) != hookOutputLimit+3 {
		t.Errorf("tail: got %d bytes, prefix %q, suffix %q; want ... then the last %d bytes", len(got), got[:3], got[len(got)-3:], hookOutputLimit)
	}
}

func TestMarkdownWrapsToColumns(t *testing.T) {
	long := strings.Repeat("word ", 30)
	for _, tc := range []struct {
		columns string
		max     int
	}{{"", 80}, {"60", 60}} {
		a := newApp(Options{Stdout: io.Discard, Stderr: io.Discard, IsTerminal: true, Getenv: func(k string) string {
			if k == "COLUMNS" {
				return tc.columns
			}
			return ""
		}})
		out := render.StripANSI(a.printer().Markdown(long))
		for _, line := range strings.Split(out, "\n") {
			if len(line) > tc.max {
				t.Errorf("COLUMNS=%q: line of %d columns: %q", tc.columns, len(line), line)
			}
		}
		mustContain(t, out, "word word")
	}
}

func TestMarkdownRendersASCII(t *testing.T) {
	a := newApp(Options{Stdout: io.Discard, Stderr: io.Discard, IsTerminal: true, Getenv: func(string) string { return "" }})
	md := "# Ticket\n\nRun `kubectl get pods -n hello` first.\n\n> The app returns 503.\n\n- one item\n- another item\n\n## Done when\n\n```\nkubectl rollout status deploy/hello\n```\n"
	out := a.printer().Markdown(md)
	for _, r := range out {
		if r > 0x7F {
			t.Errorf("non-ASCII rune %U in rendered output:\n%q", r, out)
			break
		}
	}
	if strings.Contains(out, "\u00a0") {
		t.Errorf("rendered output contains a non-breaking space:\n%q", out)
	}
	plain := render.StripANSI(out)
	mustContain(t, plain, "# Ticket", "## Done when", "| The app returns 503.", "- one item", "- another item", "kubectl get pods -n hello", "kubectl rollout status deploy/hello")
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("rendered output lost its colors:\n%q", out)
	}
}
