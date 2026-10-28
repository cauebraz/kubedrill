package grader

import (
	"context"
	"errors"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/cauebraz/kubedrill/internal/scenario"
)

func workload(kind, name string, generation int64, spec, status map[string]any) *unstructured.Unstructured {
	u := obj("apps/v1", kind, "lab", name, nil, map[string]any{"spec": spec, "status": status})
	u.SetGeneration(generation)
	return u
}

func TestGradeRollout(t *testing.T) {
	tests := []struct {
		name       string
		obj        *unstructured.Unstructured
		rollout    scenario.Rollout
		check      func(*scenario.Check)
		getErr     error
		wantStatus Status
		wantExp    string
		wantAct    string
		wantMsg    string
	}{
		{
			name: "finished deployment",
			obj: workload("Deployment", "api", 3, map[string]any{"replicas": int64(2)},
				map[string]any{"observedGeneration": int64(3), "replicas": int64(2), "updatedReplicas": int64(2), "availableReplicas": int64(2), "readyReplicas": int64(2)}),
			rollout:    scenario.Rollout{Kind: "Deployment", Name: "api"},
			wantStatus: StatusPass,
			wantExp:    "2 updated and available replicas of generation 3, 2 in total",
			wantAct:    "2 updated, 2 available, 2 in total, observed generation 3",
		},
		{
			name: "broken new template while old pods stay ready",
			obj: workload("Deployment", "api", 3, map[string]any{"replicas": int64(2)},
				map[string]any{"observedGeneration": int64(3), "replicas": int64(3), "updatedReplicas": int64(1), "availableReplicas": int64(2), "readyReplicas": int64(2)}),
			rollout:    scenario.Rollout{Kind: "Deployment", Name: "api"},
			wantStatus: StatusFail,
			wantExp:    "2 updated and available replicas of generation 3, 2 in total",
			wantAct:    "1 updated, 2 available, 3 in total, observed generation 3",
		},
		{
			name: "controller has not observed the latest generation",
			obj: workload("Deployment", "api", 4, map[string]any{"replicas": int64(2)},
				map[string]any{"observedGeneration": int64(3), "replicas": int64(2), "updatedReplicas": int64(2), "availableReplicas": int64(2)}),
			rollout:    scenario.Rollout{Kind: "Deployment", Name: "api"},
			wantStatus: StatusFail,
			wantAct:    "2 updated, 2 available, 2 in total, observed generation 3",
		},
		{
			name: "old pod left after the update",
			obj: workload("Deployment", "api", 2, map[string]any{"replicas": int64(1)},
				map[string]any{"observedGeneration": int64(2), "replicas": int64(2), "updatedReplicas": int64(1), "availableReplicas": int64(1)}),
			rollout:    scenario.Rollout{Kind: "Deployment", Name: "api"},
			wantStatus: StatusFail,
			wantAct:    "1 updated, 1 available, 2 in total, observed generation 2",
		},
		{
			name: "updated but not available",
			obj: workload("Deployment", "api", 2, map[string]any{"replicas": int64(1)},
				map[string]any{"observedGeneration": int64(2), "replicas": int64(1), "updatedReplicas": int64(1), "unavailableReplicas": int64(1)}),
			rollout:    scenario.Rollout{Kind: "Deployment", Name: "api"},
			wantStatus: StatusFail,
			wantAct:    "1 updated, 0 available, 1 in total, observed generation 2",
		},
		{
			name: "missing spec.replicas defaults to 1",
			obj: workload("Deployment", "api", 1, map[string]any{},
				map[string]any{"observedGeneration": int64(1), "replicas": int64(1), "updatedReplicas": int64(1), "availableReplicas": int64(1)}),
			rollout:    scenario.Rollout{Kind: "Deployment", Name: "api"},
			wantStatus: StatusPass,
		},
		{
			name: "scaled to zero is rolled out",
			obj: workload("Deployment", "api", 5, map[string]any{"replicas": int64(0)},
				map[string]any{"observedGeneration": int64(5)}),
			rollout:    scenario.Rollout{Kind: "Deployment", Name: "api"},
			wantStatus: StatusPass,
		},
		{
			name: "finished statefulset in an explicit namespace",
			obj: workload("StatefulSet", "db", 2, map[string]any{"replicas": int64(3)},
				map[string]any{"observedGeneration": int64(2), "replicas": int64(3), "updatedReplicas": int64(3), "availableReplicas": int64(3)}),
			rollout:    scenario.Rollout{Kind: "StatefulSet", Namespace: "lab", Name: "db"},
			wantStatus: StatusPass,
		},
		{
			name: "statefulset with a pod on the old revision",
			obj: workload("StatefulSet", "db", 2, map[string]any{"replicas": int64(3)},
				map[string]any{"observedGeneration": int64(2), "replicas": int64(3), "updatedReplicas": int64(2), "availableReplicas": int64(3)}),
			rollout:    scenario.Rollout{Kind: "StatefulSet", Name: "db"},
			wantStatus: StatusFail,
			wantAct:    "2 updated, 3 available, 3 in total, observed generation 2",
		},
		{
			name:       "workload not found",
			rollout:    scenario.Rollout{Kind: "Deployment", Name: "api"},
			wantStatus: StatusFail,
			wantExp:    "exists",
			wantAct:    "not found",
		},
		{
			name:       "api error",
			rollout:    scenario.Rollout{Kind: "Deployment", Name: "api"},
			getErr:     errors.New("connection refused"),
			wantStatus: StatusError,
			wantMsg:    "get Deployment lab/api: connection refused",
		},
		{
			name:       "unsupported kind",
			rollout:    scenario.Rollout{Kind: "DaemonSet", Name: "agent"},
			wantStatus: StatusError,
			wantMsg:    `rollout.kind must be Deployment or StatefulSet, got "DaemonSet"`,
		},
		{
			name:       "validator on a rollout check",
			rollout:    scenario.Rollout{Kind: "Deployment", Name: "api"},
			check:      func(c *scenario.Check) { c.Equals = str("2") },
			wantStatus: StatusError,
			wantMsg:    "a rollout check is its own assertion and takes no validator",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeKube{objects: map[string]*unstructured.Unstructured{}, getErr: tt.getErr}
			if tt.obj != nil {
				f.objects[key(scenario.ResourceRef{APIVersion: "apps/v1", Kind: tt.obj.GetKind(), Namespace: "lab", Name: tt.obj.GetName()})] = tt.obj
			}
			ro := tt.rollout
			c := scenario.Check{ID: "rolled-out", Rollout: &ro}
			if tt.check != nil {
				tt.check(&c)
			}
			g, err := New(f).Grade(context.Background(), rendered(c))
			if err != nil {
				t.Fatalf("Grade: %v", err)
			}
			res := g.Results[0]
			if res.Status != tt.wantStatus {
				t.Fatalf("status: got %q, want %q (%+v)", res.Status, tt.wantStatus, res)
			}
			if tt.wantExp != "" && res.Expected != tt.wantExp {
				t.Errorf("expected: got %q, want %q", res.Expected, tt.wantExp)
			}
			if tt.wantAct != "" && res.Actual != tt.wantAct {
				t.Errorf("actual: got %q, want %q", res.Actual, tt.wantAct)
			}
			if res.Message != tt.wantMsg {
				t.Errorf("message: got %q, want %q", res.Message, tt.wantMsg)
			}
		})
	}
}

func TestScriptRunsAgainAfterTheSettle(t *testing.T) {
	type attempt struct {
		code int
		out  string
		err  error
	}
	cases := []struct {
		name      string
		attempts  []attempt
		check     scenario.Check
		settle    time.Duration
		wantCalls int
		wantPass  bool
	}{
		{name: "a pass runs once", attempts: []attempt{{0, "ok", nil}}, check: scenario.Check{Contains: "ok"}, settle: time.Millisecond, wantCalls: 1, wantPass: true},
		{name: "a failure then a pass", attempts: []attempt{{1, "refused", nil}, {0, "ok", nil}}, check: scenario.Check{Contains: "ok"}, settle: time.Millisecond, wantCalls: 2, wantPass: true},
		{name: "a failure that stays", attempts: []attempt{{1, "refused", nil}, {1, "refused", nil}}, check: scenario.Check{Contains: "ok"}, settle: time.Millisecond, wantCalls: 2, wantPass: false},
		{name: "a runner error on the retry keeps the first result", attempts: []attempt{{1, "refused", nil}, {-1, "", errors.New("killed")}}, check: scenario.Check{Contains: "refused"}, settle: time.Millisecond, wantCalls: 2, wantPass: true},
		{name: "a runner error is not retried", attempts: []attempt{{-1, "", errors.New("bash missing")}}, check: scenario.Check{ExitCode: intp(0)}, settle: time.Millisecond, wantCalls: 1, wantPass: false},
		{name: "an expected non-zero exit is not retried", attempts: []attempt{{0, "", nil}, {3, "", nil}}, check: scenario.Check{ExitCode: intp(3)}, settle: time.Millisecond, wantCalls: 1, wantPass: false},
		{name: "exit 0 with the wrong output is not retried", attempts: []attempt{{0, "nope", nil}, {0, "ok", nil}}, check: scenario.Check{Contains: "ok"}, settle: time.Millisecond, wantCalls: 1, wantPass: false},
		{name: "no retry without a settle", attempts: []attempt{{1, "", nil}, {0, "ok", nil}}, check: scenario.Check{Contains: "ok"}, settle: 0, wantCalls: 1, wantPass: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			runner := func(ctx context.Context, script string, env []string) (int, string, error) {
				a := tc.attempts[min(calls, len(tc.attempts)-1)]
				calls++
				return a.code, a.out, a.err
			}
			c := tc.check
			c.ID, c.Script = "s", "curl"
			g, err := New(newFake(), WithScriptRunner(runner), WithSettle(tc.settle)).Grade(context.Background(), rendered(c))
			if err != nil {
				t.Fatal(err)
			}
			if calls != tc.wantCalls || g.Passed != tc.wantPass {
				t.Errorf("calls %d passed %v, want %d %v (%+v)", calls, g.Passed, tc.wantCalls, tc.wantPass, g.Results)
			}
		})
	}
}

func TestScriptRetryNeedsTheBudget(t *testing.T) {
	calls := 0
	runner := func(ctx context.Context, script string, env []string) (int, string, error) {
		calls++
		return 1, "", nil
	}
	start := time.Now()
	_, err := New(newFake(), WithScriptRunner(runner), WithSettle(time.Second), WithCheckTimeout(1500*time.Millisecond)).
		Grade(context.Background(), rendered(scenario.Check{ID: "s", Script: "curl", ExitCode: intp(0)}))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || time.Since(start) > 500*time.Millisecond {
		t.Errorf("calls %d after %v, want 1 without a pause", calls, time.Since(start))
	}
}
