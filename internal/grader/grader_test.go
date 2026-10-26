package grader

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/cauebraz/kubedrill/internal/kube"
	"github.com/cauebraz/kubedrill/internal/scenario"
	"github.com/cauebraz/kubedrill/internal/scriptrun"
)

type fakeKube struct {
	objects       map[string]*unstructured.Unstructured
	probes        map[string]kube.ProbeResult
	getErr        error
	block         bool
	probeDeadline time.Time
}

func key(ref scenario.ResourceRef) string {
	return ref.APIVersion + "/" + ref.Kind + "/" + ref.Namespace + "/" + ref.Name
}

func (f *fakeKube) Get(ctx context.Context, ref scenario.ResourceRef) (*unstructured.Unstructured, error) {
	if f.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if f.getErr != nil {
		return nil, f.getErr
	}
	obj, ok := f.objects[key(ref)]
	if !ok {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: strings.ToLower(ref.Kind) + "s"}, ref.Name)
	}
	return obj, nil
}

func (f *fakeKube) List(ctx context.Context, ref scenario.ResourceRef) (*unstructured.UnstructuredList, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	want := map[string]string{}
	for _, kv := range strings.Split(ref.Selector, ",") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			want[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	list := &unstructured.UnstructuredList{Object: map[string]any{"apiVersion": ref.APIVersion, "kind": ref.Kind + "List"}}
	for _, obj := range f.objects {
		if obj.GetAPIVersion() != ref.APIVersion || obj.GetKind() != ref.Kind || obj.GetNamespace() != ref.Namespace {
			continue
		}
		labels := obj.GetLabels()
		match := true
		for k, v := range want {
			if labels[k] != v {
				match = false
			}
		}
		if match {
			list.Items = append(list.Items, *obj)
		}
	}
	return list, nil
}

func (f *fakeKube) RunProbe(ctx context.Context, ns string, probe scenario.Probe) (kube.ProbeResult, error) {
	f.probeDeadline, _ = ctx.Deadline()
	if f.block {
		<-ctx.Done()
		return kube.ProbeResult{}, ctx.Err()
	}
	pr, ok := f.probes[probe.Image]
	if !ok {
		return kube.ProbeResult{}, errors.New("image not found: " + probe.Image)
	}
	return pr, nil
}

func obj(apiVersion, kind, ns, name string, labels map[string]string, spec map[string]any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   map[string]any{"name": name, "namespace": ns},
	}}
	if labels != nil {
		u.SetLabels(labels)
	}
	for k, v := range spec {
		u.Object[k] = v
	}
	return u
}

func endpointSlice(ns, name, service string, ready bool, addrs ...string) *unstructured.Unstructured {
	var list []any
	for _, a := range addrs {
		list = append(list, a)
	}
	return obj("discovery.k8s.io/v1", "EndpointSlice", ns, name,
		map[string]string{"kubernetes.io/service-name": service},
		map[string]any{"endpoints": []any{map[string]any{
			"addresses":  list,
			"conditions": map[string]any{"ready": ready},
		}}})
}

func newFake() *fakeKube {
	deploy := obj("apps/v1", "Deployment", "lab", "hello", map[string]string{"app": "hello"}, map[string]any{
		"spec":   map[string]any{"replicas": int64(2), "template": map[string]any{"spec": map[string]any{"containers": []any{map[string]any{"name": "web", "image": "nginx:1.27-alpine"}}}}},
		"status": map[string]any{"readyReplicas": int64(2), "conditions": []any{map[string]any{"type": "Available", "status": "True"}}},
	})
	f := &fakeKube{objects: map[string]*unstructured.Unstructured{}, probes: map[string]kube.ProbeResult{
		"curl":   {ExitCode: 0, Output: "HTTP/1.1 200 OK\n"},
		"fail":   {ExitCode: 7, Output: "connection refused"},
		"silent": {ExitCode: 0, Output: ""},
	}}
	for _, o := range []*unstructured.Unstructured{
		deploy,
		endpointSlice("lab", "hello-abc", "hello", true, "10.0.0.1", "10.0.0.2"),
		endpointSlice("lab", "hello-def", "hello", false, "10.0.0.3"),
		endpointSlice("lab", "other-xyz", "other", true, "10.0.1.1"),
	} {
		f.objects[key(scenario.ResourceRef{APIVersion: o.GetAPIVersion(), Kind: o.GetKind(), Namespace: o.GetNamespace(), Name: o.GetName()})] = o
	}
	return f
}

func rendered(checks ...scenario.Check) *scenario.Rendered {
	return &scenario.Rendered{Namespace: "lab", Checks: checks}
}

func str(s string) *string { return &s }
func boolp(b bool) *bool   { return &b }
func intp(i int) *int      { return &i }

func deployRef(name string) *scenario.ResourceRef {
	return &scenario.ResourceRef{APIVersion: "apps/v1", Kind: "Deployment", Name: name}
}

func TestGradeSingleChecks(t *testing.T) {
	tests := []struct {
		name         string
		check        scenario.Check
		fake         func(*fakeKube)
		wantStatus   Status
		wantExpected string
		wantActual   string
		wantMessage  string
	}{
		{
			name:         "jsonpath equals passes",
			check:        scenario.Check{ID: "replicas", Resource: deployRef("hello"), Path: "{.spec.replicas}", Equals: str("2")},
			wantStatus:   StatusPass,
			wantExpected: "",
			wantActual:   "2",
		},
		{
			name:         "jsonpath equals fails",
			check:        scenario.Check{ID: "replicas", Resource: deployRef("hello"), Path: "{.status.readyReplicas}", Equals: str("3")},
			wantStatus:   StatusFail,
			wantExpected: `"3"`,
			wantActual:   "2",
		},
		{
			name:       "matches passes",
			check:      scenario.Check{ID: "image", Resource: deployRef("hello"), Path: "{.spec.template.spec.containers[0].image}", Matches: "^nginx:1.27-alpine"},
			wantStatus: StatusPass,
			wantActual: "nginx:1.27-alpine",
		},
		{
			name:         "matches fails",
			check:        scenario.Check{ID: "image", Resource: deployRef("hello"), Path: "{.spec.template.spec.containers[0].image}", Matches: "^httpd"},
			wantStatus:   StatusFail,
			wantExpected: "match /^httpd/",
			wantActual:   "nginx:1.27-alpine",
		},
		{
			name:        "invalid regexp is an error",
			check:       scenario.Check{ID: "image", Resource: deployRef("hello"), Path: "{.spec.template.spec.containers[0].image}", Matches: "("},
			wantStatus:  StatusError,
			wantMessage: "matches (:",
		},
		{
			name:         "not found without exists fails",
			check:        scenario.Check{ID: "missing", Resource: deployRef("ghost"), Path: "{.spec.replicas}", Equals: str("2")},
			wantStatus:   StatusFail,
			wantExpected: "exists",
			wantActual:   "not found",
		},
		{
			name:       "exists false passes on not found",
			check:      scenario.Check{ID: "absent", Resource: deployRef("ghost"), Exists: boolp(false)},
			wantStatus: StatusPass,
		},
		{
			name:         "exists false fails when found",
			check:        scenario.Check{ID: "absent", Resource: deployRef("hello"), Exists: boolp(false)},
			wantStatus:   StatusFail,
			wantExpected: "absent",
			wantActual:   "exists",
		},
		{
			name:       "exists true passes when found",
			check:      scenario.Check{ID: "present", Resource: deployRef("hello"), Exists: boolp(true)},
			wantStatus: StatusPass,
		},
		{
			name: "list filter collects ready addresses",
			check: scenario.Check{ID: "endpoints", Resource: &scenario.ResourceRef{APIVersion: "discovery.k8s.io/v1", Kind: "EndpointSlice", Selector: "kubernetes.io/service-name=hello"},
				Path: "{.items[*].endpoints[?(@.conditions.ready==true)].addresses[*]}", NotEmpty: true, Contains: "10.0.0.2"},
			wantStatus: StatusPass,
			wantActual: "10.0.0.1 10.0.0.2",
		},
		{
			name: "list selector with no match fails exists",
			check: scenario.Check{ID: "endpoints", Resource: &scenario.ResourceRef{APIVersion: "discovery.k8s.io/v1", Kind: "EndpointSlice", Selector: "kubernetes.io/service-name=nothing"},
				Exists: boolp(true)},
			wantStatus:   StatusFail,
			wantExpected: "at least one matching object",
			wantActual:   "0 matching objects",
		},
		{
			name:         "missing key empty fails not_empty",
			check:        scenario.Check{ID: "nokey", Resource: deployRef("hello"), Path: "{.spec.nothing.here}", NotEmpty: true},
			wantStatus:   StatusFail,
			wantExpected: "at least one value",
			wantActual:   "no value at {.spec.nothing.here}",
		},
		{
			name:         "equals on missing key names the path",
			check:        scenario.Check{ID: "nokey", Resource: deployRef("hello"), Path: "{.spec.nothing.here}", Equals: str("x")},
			wantStatus:   StatusFail,
			wantExpected: `"x"`,
			wantActual:   "no value at {.spec.nothing.here}",
		},
		{
			name:        "exists with path is an error",
			check:       scenario.Check{ID: "haspath", Resource: deployRef("hello"), Path: "{.spec.replicas}", Exists: boolp(true)},
			wantStatus:  StatusError,
			wantMessage: "exists applies to a resource without path; use not_empty for a path",
		},
		{
			name:        "exists false with path is an error even when the object is missing",
			check:       scenario.Check{ID: "nopath", Resource: deployRef("ghost"), Path: "{.spec.replicas}", Exists: boolp(false)},
			wantStatus:  StatusError,
			wantMessage: "exists applies to a resource without path; use not_empty for a path",
		},
		{
			name:        "exists on probe is an error",
			check:       scenario.Check{ID: "out", Probe: &scenario.Probe{Image: "silent"}, Exists: boolp(true)},
			wantStatus:  StatusError,
			wantMessage: "exists applies to a resource source only",
		},
		{
			name:        "exists on script is an error",
			check:       scenario.Check{ID: "out", Script: "true", Exists: boolp(false)},
			wantStatus:  StatusError,
			wantMessage: "exists applies to a resource source only",
		},
		{
			name:         "gte passes",
			check:        scenario.Check{ID: "ready", Resource: deployRef("hello"), Path: "{.status.readyReplicas}", GTE: str("2")},
			wantStatus:   StatusPass,
			wantExpected: "",
		},
		{
			name:         "gte fails",
			check:        scenario.Check{ID: "ready", Resource: deployRef("hello"), Path: "{.status.readyReplicas}", GTE: str("3")},
			wantStatus:   StatusFail,
			wantExpected: ">= 3",
			wantActual:   "2",
		},
		{
			name:         "lte fails",
			check:        scenario.Check{ID: "ready", Resource: deployRef("hello"), Path: "{.status.readyReplicas}", LTE: str("1")},
			wantStatus:   StatusFail,
			wantExpected: "<= 1",
			wantActual:   "2",
		},
		{
			name:        "gte parse error is an error",
			check:       scenario.Check{ID: "ready", Resource: deployRef("hello"), Path: "{.spec.template.spec.containers[0].image}", GTE: str("1")},
			wantStatus:  StatusError,
			wantMessage: `gte: value "nginx:1.27-alpine" is not a number`,
		},
		{
			name:         "not_equals fails",
			check:        scenario.Check{ID: "ready", Resource: deployRef("hello"), Path: "{.status.readyReplicas}", NotEquals: str("2")},
			wantStatus:   StatusFail,
			wantExpected: `not "2"`,
			wantActual:   "2",
		},
		{
			name:         "contains fails",
			check:        scenario.Check{ID: "cond", Resource: deployRef("hello"), Path: "{.status.conditions[?(@.type==\"Available\")].status}", Contains: "False"},
			wantStatus:   StatusFail,
			wantExpected: `contain "False"`,
			wantActual:   "True",
		},
		{
			name:       "probe passes on exit code and output",
			check:      scenario.Check{ID: "http", Probe: &scenario.Probe{Image: "curl", Command: []string{"curl"}}, ExitCode: intp(0), Contains: "200"},
			wantStatus: StatusPass,
			wantActual: "HTTP/1.1 200 OK",
		},
		{
			name:         "probe exit code fails",
			check:        scenario.Check{ID: "http", Probe: &scenario.Probe{Image: "fail"}, ExitCode: intp(0)},
			wantStatus:   StatusFail,
			wantExpected: "exit code 0",
			wantActual:   "exit code 7",
		},
		{
			name:        "probe error is an error",
			check:       scenario.Check{ID: "http", Probe: &scenario.Probe{Image: "nope"}, ExitCode: intp(0)},
			wantStatus:  StatusError,
			wantMessage: "probe nope: image not found: nope",
		},
		{
			name:        "kube error other than not found is an error",
			check:       scenario.Check{ID: "replicas", Resource: deployRef("hello"), Path: "{.spec.replicas}", Equals: str("2")},
			fake:        func(f *fakeKube) { f.getErr = errors.New("connection refused") },
			wantStatus:  StatusError,
			wantMessage: "get Deployment lab/hello: connection refused",
		},
		{
			name:        "no source is an error",
			check:       scenario.Check{ID: "empty", Equals: str("x")},
			wantStatus:  StatusError,
			wantMessage: "check has no source: set resource, probe or script",
		},
		{
			name:        "exit_code on a resource check is an error",
			check:       scenario.Check{ID: "ec", Resource: deployRef("hello"), Path: "{.spec.replicas}", ExitCode: intp(0)},
			wantStatus:  StatusError,
			wantMessage: "exit_code applies to probe and script checks only",
		},
		{
			name: "selector with exists false fails when objects match",
			check: scenario.Check{ID: "gone", Resource: &scenario.ResourceRef{APIVersion: "discovery.k8s.io/v1", Kind: "EndpointSlice", Selector: "kubernetes.io/service-name=hello"},
				Exists: boolp(false)},
			wantStatus:   StatusFail,
			wantExpected: "no matching objects",
			wantActual:   "2 matching objects",
		},
		{
			name:        "bad jsonpath is an error",
			check:       scenario.Check{ID: "badpath", Resource: deployRef("hello"), Path: "{.spec.replicas", Equals: str("2")},
			wantStatus:  StatusError,
			wantMessage: "jsonpath {.spec.replicas:",
		},
		{
			name:        "gte bound not a number is an error",
			check:       scenario.Check{ID: "ready", Resource: deployRef("hello"), Path: "{.status.readyReplicas}", GTE: str("abc")},
			wantStatus:  StatusError,
			wantMessage: `gte: bound "abc" is not a number`,
		},
		{
			name:       "equals trims surrounding whitespace",
			check:      scenario.Check{ID: "replicas", Resource: deployRef("hello"), Path: "{.spec.replicas}", Equals: str(" 2 ")},
			wantStatus: StatusPass,
			wantActual: "2",
		},
		{
			name:       "lte passes",
			check:      scenario.Check{ID: "ready", Resource: deployRef("hello"), Path: "{.status.readyReplicas}", LTE: str("2")},
			wantStatus: StatusPass,
			wantActual: "2",
		},
		{
			name: "list error is an error",
			check: scenario.Check{ID: "endpoints", Resource: &scenario.ResourceRef{APIVersion: "discovery.k8s.io/v1", Kind: "EndpointSlice", Selector: "kubernetes.io/service-name=hello"},
				Exists: boolp(true)},
			fake:        func(f *fakeKube) { f.getErr = errors.New("connection refused") },
			wantStatus:  StatusError,
			wantMessage: "list EndpointSlice kubernetes.io/service-name=hello: connection refused",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake()
			if tt.fake != nil {
				tt.fake(f)
			}
			g, err := New(f).Grade(context.Background(), rendered(tt.check))
			if err != nil {
				t.Fatalf("Grade: unexpected error %v", err)
			}
			if len(g.Results) != 1 {
				t.Fatalf("got %d results, want 1", len(g.Results))
			}
			r := g.Results[0]
			if r.Status != tt.wantStatus {
				t.Errorf("status: got %q, want %q (expected=%q actual=%q message=%q)", r.Status, tt.wantStatus, r.Expected, r.Actual, r.Message)
			}
			if tt.wantExpected != "" && r.Expected != tt.wantExpected {
				t.Errorf("expected: got %q, want %q", r.Expected, tt.wantExpected)
			}
			if tt.wantActual != "" && r.Actual != tt.wantActual {
				t.Errorf("actual: got %q, want %q", r.Actual, tt.wantActual)
			}
			if tt.wantMessage != "" && !strings.Contains(r.Message, tt.wantMessage) {
				t.Errorf("message: got %q, want it to contain %q", r.Message, tt.wantMessage)
			}
			if r.ID != tt.check.ID {
				t.Errorf("id: got %q, want %q", r.ID, tt.check.ID)
			}
		})
	}
}

func TestGradeScript(t *testing.T) {
	var gotEnv []string
	runner := func(ctx context.Context, script string, env []string) (int, string, error) {
		gotEnv = env
		switch script {
		case "ok":
			return 0, "fine\n", nil
		case "boom":
			return 0, "", errors.New("bash missing")
		}
		return 3, "nope", nil
	}
	r := New(newFake(), WithScriptRunner(runner), WithEnv([]string{"TASK_NS=lab"}))
	g, err := r.Grade(context.Background(), rendered(
		scenario.Check{ID: "ok", Script: "ok", ExitCode: intp(0), Equals: str("fine")},
		scenario.Check{ID: "bad", Script: "bad", ExitCode: intp(0)},
		scenario.Check{ID: "boom", Script: "boom"},
	))
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	want := []Status{StatusPass, StatusFail, StatusError}
	for i, s := range want {
		if g.Results[i].Status != s {
			t.Errorf("result %s: got %q, want %q (%s)", g.Results[i].ID, g.Results[i].Status, s, g.Results[i].Message)
		}
	}
	if g.Results[1].Actual != "exit code 3" {
		t.Errorf("bad actual: got %q, want %q", g.Results[1].Actual, "exit code 3")
	}
	if len(gotEnv) != 1 || gotEnv[0] != "TASK_NS=lab" {
		t.Errorf("env: got %v, want [TASK_NS=lab]", gotEnv)
	}
}

func TestGradeScriptEnvFunc(t *testing.T) {
	var gotEnv []string
	runner := func(ctx context.Context, script string, env []string) (int, string, error) {
		gotEnv = env
		return 0, "", nil
	}
	r := New(newFake(),
		WithScriptRunner(runner),
		WithEnv([]string{"KUBECONFIG=/tmp/kc"}),
		WithEnvFunc(func(rd *scenario.Rendered) []string { return []string{"TASK_NS=" + rd.Namespace} }),
	)
	if _, err := r.Grade(context.Background(), rendered(scenario.Check{ID: "s", Script: "true", ExitCode: intp(0)})); err != nil {
		t.Fatalf("Grade: %v", err)
	}
	want := []string{"KUBECONFIG=/tmp/kc", "TASK_NS=lab"}
	if strings.Join(gotEnv, ",") != strings.Join(want, ",") {
		t.Errorf("env: got %v, want %v", gotEnv, want)
	}
}

func TestGradeDefaultScriptRunnerDirAndKill(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rd := rendered(
		scenario.Check{ID: "pwd", Script: "pwd -P", Equals: str(dir), ExitCode: intp(0)},
	)
	rd.Scenario = &scenario.Scenario{Dir: dir}
	g, err := New(newFake()).Grade(context.Background(), rd)
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	if res := g.Results[0]; res.Status != StatusPass {
		t.Errorf("pwd: got %q (expected=%q actual=%q message=%q), want pass", res.Status, res.Expected, res.Actual, res.Message)
	}

	start := time.Now()
	g, err = New(newFake(), WithCheckTimeout(300*time.Millisecond)).Grade(context.Background(), rendered(
		scenario.Check{ID: "hang", Script: "sleep 30 & wait", ExitCode: intp(0)},
	))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	if res := g.Results[0]; res.Status != StatusError || !strings.Contains(res.Message, context.DeadlineExceeded.Error()) {
		t.Errorf("hang: got %q %q, want error with deadline exceeded", res.Status, res.Message)
	}
	if elapsed > 3*time.Second {
		t.Errorf("elapsed: got %v, want under 3s (background child kept the pipe open)", elapsed)
	}
}

func TestGradeProbeTimeout(t *testing.T) {
	tests := []struct {
		name         string
		checkTimeout time.Duration
		probeTimeout string
		wantMin      time.Duration
		wantMax      time.Duration
	}{
		{name: "default probe timeout wins over a short check timeout", checkTimeout: 50 * time.Millisecond, probeTimeout: "", wantMin: 89 * time.Second, wantMax: 91 * time.Second},
		{name: "explicit probe timeout plus grace", checkTimeout: 50 * time.Millisecond, probeTimeout: "2m", wantMin: 149 * time.Second, wantMax: 151 * time.Second},
		{name: "longer check timeout wins", checkTimeout: 10 * time.Minute, probeTimeout: "10s", wantMin: 599 * time.Second, wantMax: 601 * time.Second},
		{name: "unparsable probe timeout keeps the check timeout", checkTimeout: 2 * time.Second, probeTimeout: "soon", wantMin: time.Second, wantMax: 3 * time.Second},
		{name: "probe timeout below 1s keeps the check timeout as kube.ProbeTimeout refuses it", checkTimeout: 2 * time.Second, probeTimeout: "500ms", wantMin: time.Second, wantMax: 3 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake()
			start := time.Now()
			_, err := New(f, WithCheckTimeout(tt.checkTimeout)).Grade(context.Background(), rendered(
				scenario.Check{ID: "p", Probe: &scenario.Probe{Image: "curl", Timeout: tt.probeTimeout}, ExitCode: intp(0)},
			))
			if err != nil {
				t.Fatalf("Grade: %v", err)
			}
			if f.probeDeadline.IsZero() {
				t.Fatal("probe context has no deadline")
			}
			budget := f.probeDeadline.Sub(start)
			if budget < tt.wantMin || budget > tt.wantMax {
				t.Errorf("probe budget: got %v, want between %v and %v", budget, tt.wantMin, tt.wantMax)
			}
		})
	}

	f := newFake()
	f.block = true
	start := time.Now()
	g, err := New(f, WithCheckTimeout(50*time.Millisecond)).Grade(context.Background(), rendered(
		scenario.Check{ID: "slow", Resource: deployRef("hello"), Path: "{.spec.replicas}"},
	))
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	if g.Results[0].Status != StatusError || time.Since(start) > 2*time.Second {
		t.Errorf("resource check keeps the check timeout: got %q after %v", g.Results[0].Status, time.Since(start))
	}
}

func TestGradeDefaultScriptRunner(t *testing.T) {
	r := New(newFake(), WithEnv([]string{"GRADER_TEST_VALUE=hello"}))
	g, err := r.Grade(context.Background(), rendered(
		scenario.Check{ID: "echo", Script: "echo $GRADER_TEST_VALUE", Equals: str("hello"), ExitCode: intp(0)},
		scenario.Check{ID: "exit", Script: "exit 4", ExitCode: intp(4)},
	))
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	for _, res := range g.Results {
		if res.Status != StatusPass {
			t.Errorf("%s: got %q, want pass (expected=%q actual=%q message=%q)", res.ID, res.Status, res.Expected, res.Actual, res.Message)
		}
	}
}

func TestGradeScoreAndDependencies(t *testing.T) {
	pass := func(id string, weight float64, deps ...string) scenario.Check {
		return scenario.Check{ID: id, Weight: weight, DependsOn: deps, Resource: deployRef("hello"), Path: "{.spec.replicas}", Equals: str("2")}
	}
	fail := func(id string, weight float64, deps ...string) scenario.Check {
		return scenario.Check{ID: id, Weight: weight, DependsOn: deps, Resource: deployRef("hello"), Path: "{.spec.replicas}", Equals: str("5")}
	}
	tests := []struct {
		name       string
		checks     []scenario.Check
		wantOrder  []string
		wantStatus []Status
		wantScore  float64
		wantPassed bool
		wantMsg    map[string]string
	}{
		{
			name:       "all pass",
			checks:     []scenario.Check{pass("a", 1), pass("b", 3)},
			wantOrder:  []string{"a", "b"},
			wantStatus: []Status{StatusPass, StatusPass},
			wantScore:  1,
			wantPassed: true,
		},
		{
			name:       "weights",
			checks:     []scenario.Check{pass("a", 1), fail("b", 3)},
			wantOrder:  []string{"a", "b"},
			wantStatus: []Status{StatusPass, StatusFail},
			wantScore:  0.25,
		},
		{
			name:       "zero weight counts as one",
			checks:     []scenario.Check{pass("a", 0), fail("b", 0)},
			wantOrder:  []string{"a", "b"},
			wantStatus: []Status{StatusPass, StatusFail},
			wantScore:  0.5,
		},
		{
			name: "critical failure zeroes score",
			checks: []scenario.Check{pass("a", 1), {
				ID: "crit", Weight: 1, Critical: true, Resource: deployRef("hello"), Path: "{.spec.replicas}", Equals: str("9"),
			}},
			wantOrder:  []string{"a", "crit"},
			wantStatus: []Status{StatusPass, StatusFail},
			wantScore:  0,
		},
		{
			name:       "dependency ordering independent of input order",
			checks:     []scenario.Check{pass("c", 1, "b"), pass("b", 1, "a"), pass("a", 1)},
			wantOrder:  []string{"a", "b", "c"},
			wantStatus: []Status{StatusPass, StatusPass, StatusPass},
			wantScore:  1,
			wantPassed: true,
		},
		{
			name:       "stable order among ready checks",
			checks:     []scenario.Check{pass("x", 1), pass("y", 1, "x"), pass("z", 1), pass("w", 1, "x")},
			wantOrder:  []string{"x", "y", "z", "w"},
			wantStatus: []Status{StatusPass, StatusPass, StatusPass, StatusPass},
			wantScore:  1,
			wantPassed: true,
		},
		{
			name:       "skipped when dependency fails",
			checks:     []scenario.Check{fail("a", 1), pass("b", 1, "a"), pass("c", 1, "b")},
			wantOrder:  []string{"a", "b", "c"},
			wantStatus: []Status{StatusFail, StatusSkipped, StatusSkipped},
			wantScore:  0,
			wantMsg:    map[string]string{"b": "skipped: a did not pass", "c": "skipped: b did not pass"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, err := New(newFake()).Grade(context.Background(), rendered(tt.checks...))
			if err != nil {
				t.Fatalf("Grade: %v", err)
			}
			if len(g.Results) != len(tt.wantOrder) {
				t.Fatalf("got %d results, want %d", len(g.Results), len(tt.wantOrder))
			}
			for i, res := range g.Results {
				if res.ID != tt.wantOrder[i] {
					t.Errorf("order[%d]: got %q, want %q", i, res.ID, tt.wantOrder[i])
				}
				if res.Status != tt.wantStatus[i] {
					t.Errorf("%s status: got %q, want %q", res.ID, res.Status, tt.wantStatus[i])
				}
				if want, ok := tt.wantMsg[res.ID]; ok && res.Message != want {
					t.Errorf("%s message: got %q, want %q", res.ID, res.Message, want)
				}
			}
			if g.Score != tt.wantScore {
				t.Errorf("score: got %v, want %v", g.Score, tt.wantScore)
			}
			if g.Passed != tt.wantPassed {
				t.Errorf("passed: got %v, want %v", g.Passed, tt.wantPassed)
			}
			if g.At.IsZero() {
				t.Error("At: got zero time")
			}
		})
	}
}

func TestGradeProgrammerErrors(t *testing.T) {
	r := New(newFake())
	if _, err := r.Grade(context.Background(), nil); err == nil {
		t.Error("nil rendered: got nil error, want error")
	}
	tests := []struct {
		name string
		rd   *scenario.Rendered
		want string
	}{
		{
			name: "cycle",
			rd: rendered(
				scenario.Check{ID: "a", DependsOn: []string{"b"}, Resource: deployRef("hello")},
				scenario.Check{ID: "b", DependsOn: []string{"a"}, Resource: deployRef("hello")},
			),
			want: "grade: check dependencies form a cycle",
		},
		{
			name: "unknown dependency",
			rd:   rendered(scenario.Check{ID: "a", DependsOn: []string{"zz"}, Resource: deployRef("hello")}),
			want: `grade: check "a" depends on unknown check "zz"`,
		},
		{
			name: "duplicate id",
			rd: rendered(
				scenario.Check{ID: "dup", Resource: deployRef("hello"), Exists: boolp(true)},
				scenario.Check{ID: "dup", Resource: deployRef("hello"), Exists: boolp(true)},
			),
			want: `grade: duplicate check id "dup"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := r.Grade(context.Background(), tt.rd)
			if err == nil {
				t.Fatalf("Grade: got nil error, want %q", tt.want)
			}
			if err.Error() != tt.want {
				t.Errorf("error: got %q, want %q", err.Error(), tt.want)
			}
		})
	}
}

func TestGradeEmptyChecks(t *testing.T) {
	g, err := New(newFake()).Grade(context.Background(), rendered())
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	if g.Passed {
		t.Error("Passed: got true, want false for a scenario without checks")
	}
	if g.Score != 0 {
		t.Errorf("Score: got %v, want 0", g.Score)
	}
	if len(g.Results) != 0 {
		t.Errorf("Results: got %d, want 0", len(g.Results))
	}
}

func TestGradeScriptEnvAllowlist(t *testing.T) {
	t.Setenv("AWS_SECRET_ACCESS_KEY", "leak")
	t.Setenv("LANG", "en_US.UTF-8")
	r := New(newFake(), WithEnv([]string{"TASK_NS=lab"}))
	g, err := r.Grade(context.Background(), rendered(
		scenario.Check{ID: "secret", Script: `printf '%s' "${AWS_SECRET_ACCESS_KEY:-none}"`, Equals: str("none")},
		scenario.Check{ID: "kept", Script: `printf '%s|%s|%s' "$LANG" "$TASK_NS" "${PATH:+set}"`, Equals: str("en_US.UTF-8|lab|set")},
	))
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	for _, res := range g.Results {
		if res.Status != StatusPass {
			t.Errorf("%s: got %q (actual %q, message %q), want pass", res.ID, res.Status, res.Actual, res.Message)
		}
	}
	for _, kv := range scriptrun.HostEnv() {
		if strings.HasPrefix(kv, "AWS_") {
			t.Errorf("scriptrun.HostEnv: got %q, want only the allowlist", kv)
		}
	}
}

func TestGradeCopiesSpoiler(t *testing.T) {
	g, err := New(newFake()).Grade(context.Background(), rendered(
		scenario.Check{ID: "image", Spoiler: true, Resource: deployRef("hello"), Path: "{.spec.template.spec.containers[0].image}", Equals: str("nginx:9")},
		scenario.Check{ID: "plain", Resource: deployRef("hello"), Exists: boolp(true)},
	))
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	if !g.Results[0].Spoiler || g.Results[1].Spoiler {
		t.Errorf("Spoiler: got %v and %v, want true and false", g.Results[0].Spoiler, g.Results[1].Spoiler)
	}
	data, err := json.Marshal(g.Results[1])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "spoiler") {
		t.Errorf("json of a plain result: got %s, want spoiler omitted", data)
	}
}

func TestGradeCopiesWhy(t *testing.T) {
	g, err := New(newFake()).Grade(context.Background(), rendered(
		scenario.Check{ID: "pass", Why: "why pass", Resource: deployRef("hello"), Exists: boolp(true)},
		scenario.Check{ID: "fail", Why: "why fail", Resource: deployRef("ghost"), Exists: boolp(true)},
		scenario.Check{ID: "skip", Why: "why skip", DependsOn: []string{"fail"}, Resource: deployRef("hello"), Exists: boolp(true)},
		scenario.Check{ID: "err", Why: "why err", Resource: deployRef("hello"), Path: "{.spec.template.spec.containers[0].image}", Matches: "("},
	))
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	want := map[string]Status{"pass": StatusPass, "fail": StatusFail, "skip": StatusSkipped, "err": StatusError}
	if len(g.Results) != len(want) {
		t.Fatalf("got %d results, want %d", len(g.Results), len(want))
	}
	for _, r := range g.Results {
		if r.Status != want[r.ID] {
			t.Errorf("%s: status %q, want %q", r.ID, r.Status, want[r.ID])
		}
		if got, w := r.Why, "why "+r.ID; got != w {
			t.Errorf("%s: Why = %q, want %q", r.ID, got, w)
		}
	}
}

func TestGradeCriticalWithSkippedDependents(t *testing.T) {
	g, err := New(newFake()).Grade(context.Background(), rendered(
		scenario.Check{ID: "a", Critical: true, Resource: deployRef("ghost"), Exists: boolp(true)},
		scenario.Check{ID: "b", DependsOn: []string{"a"}, Resource: deployRef("hello"), Exists: boolp(true)},
	))
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	want := []Status{StatusFail, StatusSkipped}
	if len(g.Results) != len(want) {
		t.Fatalf("got %d results, want %d", len(g.Results), len(want))
	}
	for i, s := range want {
		if g.Results[i].Status != s {
			t.Errorf("result %s: got %q, want %q", g.Results[i].ID, g.Results[i].Status, s)
		}
	}
	if g.Results[0].Actual != "not found" {
		t.Errorf("a actual: got %q, want %q", g.Results[0].Actual, "not found")
	}
	if g.Results[1].Message != "skipped: a did not pass" {
		t.Errorf("b message: got %q, want %q", g.Results[1].Message, "skipped: a did not pass")
	}
	if g.Passed {
		t.Error("Passed: got true, want false")
	}
	if g.Score != 0 {
		t.Errorf("Score: got %v, want 0", g.Score)
	}
}

func TestGradeTimeout(t *testing.T) {
	f := newFake()
	f.block = true
	r := New(f, WithCheckTimeout(50*time.Millisecond))
	start := time.Now()
	g, err := r.Grade(context.Background(), rendered(
		scenario.Check{ID: "slow", Resource: deployRef("hello"), Path: "{.spec.replicas}", Equals: str("2")},
	))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	res := g.Results[0]
	if res.Status != StatusError {
		t.Errorf("status: got %q, want %q", res.Status, StatusError)
	}
	if !strings.Contains(res.Message, context.DeadlineExceeded.Error()) {
		t.Errorf("message: got %q, want it to contain %q", res.Message, context.DeadlineExceeded.Error())
	}
	if elapsed > 2*time.Second {
		t.Errorf("elapsed: got %v, want under 2s", elapsed)
	}
	if res.Duration < 50*time.Millisecond {
		t.Errorf("duration: got %v, want at least 50ms", res.Duration)
	}
}

func TestGradeTruncatesActual(t *testing.T) {
	f := newFake()
	long := strings.Repeat("x", 500)
	f.probes["long"] = kube.ProbeResult{ExitCode: 0, Output: long}
	g, err := New(f).Grade(context.Background(), rendered(
		scenario.Check{ID: "long", Probe: &scenario.Probe{Image: "long"}, Equals: str("short")},
	))
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	actual := g.Results[0].Actual
	if got := len(actual); got != 200 {
		t.Errorf("actual length: got %d, want 200", got)
	}
	if !strings.HasSuffix(actual, "...") {
		t.Errorf("actual suffix: got %q, want it to end with %q", actual[len(actual)-5:], "...")
	}
	if got, want := g.Results[0].Status, StatusFail; got != want {
		t.Errorf("status: got %q, want %q", got, want)
	}
}

func TestTruncateKeepsRunes(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantLen int
	}{
		{name: "short is unchanged", in: "abc", wantLen: 3},
		{name: "ascii cut at the limit", in: strings.Repeat("x", 500), wantLen: maxTextLen},
		{name: "two-byte runes across the cut", in: "x" + strings.Repeat("\u00e9", 300), wantLen: maxTextLen},
		{name: "four-byte runes across the cut", in: "xy" + strings.Repeat("\U0001F600", 100), wantLen: maxTextLen - 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.in)
			if !utf8.ValidString(got) {
				t.Errorf("truncate: got invalid UTF-8 %q", got)
			}
			if len(got) != tt.wantLen {
				t.Errorf("truncate length: got %d, want %d", len(got), tt.wantLen)
			}
		})
	}
}

func TestParsePairRejectsNonFinite(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		bound   string
		wantErr string
	}{
		{name: "finite", value: "2", bound: "1"},
		{name: "NaN bound", value: "2", bound: "NaN", wantErr: `gte: bound "NaN" is not a finite number`},
		{name: "Inf bound", value: "2", bound: "+Inf", wantErr: `gte: bound "+Inf" is not a finite number`},
		{name: "NaN value", value: "nan", bound: "1", wantErr: `gte: value "nan" is not a finite number`},
		{name: "Inf value", value: "-inf", bound: "1", wantErr: `gte: value "-inf" is not a finite number`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := parsePair(tt.value, tt.bound, "gte")
			got := ""
			if err != nil {
				got = err.Error()
			}
			if got != tt.wantErr {
				t.Errorf("parsePair(%q, %q): got error %q, want %q", tt.value, tt.bound, got, tt.wantErr)
			}
		})
	}
}

func TestGradeDefaultScriptRunnerBackgroundAndCap(t *testing.T) {
	start := time.Now()
	g, err := New(newFake()).Grade(context.Background(), rendered(
		scenario.Check{ID: "bg", Script: "sleep 30 & echo ok", Equals: str("ok"), ExitCode: intp(0)},
		scenario.Check{ID: "flood", Script: "yes | head -c 10000000; exit 3", ExitCode: intp(3)},
	))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	for _, res := range g.Results {
		if res.Status != StatusPass {
			t.Errorf("%s: got %q (expected=%q actual=%q message=%q), want pass", res.ID, res.Status, res.Expected, res.Actual, res.Message)
		}
	}
	if elapsed > 2*time.Second {
		t.Errorf("elapsed: got %v, want under 2s", elapsed)
	}
}

func TestSummary(t *testing.T) {
	tests := []struct {
		name       string
		results    []Result
		wantPassed int
		wantTotal  int
	}{
		{name: "empty", wantPassed: 0, wantTotal: 0},
		{name: "mixed", results: []Result{{Status: StatusPass}, {Status: StatusFail}, {Status: StatusSkipped}, {Status: StatusPass}, {Status: StatusError}}, wantPassed: 2, wantTotal: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			passed, total := Grade{Results: tt.results}.Summary()
			if passed != tt.wantPassed || total != tt.wantTotal {
				t.Errorf("Summary: got (%d, %d), want (%d, %d)", passed, total, tt.wantPassed, tt.wantTotal)
			}
		})
	}
}
