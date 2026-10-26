package watch

import (
	"strings"
	"testing"

	"github.com/cauebraz/kubedrill/internal/grader"
)

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

func spoilerGrade() grader.Grade {
	return grader.Grade{Results: []grader.Result{
		{ID: "image", Description: "image is pullable", Status: grader.StatusFail, Expected: "nginx:1.27", Actual: "nginx:1.2x", Spoiler: true},
	}}
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
