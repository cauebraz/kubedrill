package grader

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/cauebraz/kubedrill/internal/scenario"
)

type rolloutState struct {
	generation int64
	observed   int64
	want       int64
	replicas   int64
	updated    int64
	available  int64
}

func (s rolloutState) done() bool {
	return s.observed >= s.generation &&
		s.updated == s.want &&
		s.available == s.want &&
		s.replicas == s.want
}

func (s rolloutState) expected() string {
	return fmt.Sprintf("%d updated and available replicas of generation %d, %d in total", s.want, s.generation, s.want)
}

func (s rolloutState) actual() string {
	return fmt.Sprintf("%d updated, %d available, %d in total, observed generation %d", s.updated, s.available, s.replicas, s.observed)
}

func (r *Runner) checkRollout(ctx context.Context, rd *scenario.Rendered, c scenario.Check, res *Result) {
	if hasValidator(c) {
		setError(res, "a rollout check is its own assertion and takes no validator")
		return
	}
	ro := *c.Rollout
	if ro.Kind != "Deployment" && ro.Kind != "StatefulSet" {
		setError(res, fmt.Sprintf("rollout.kind must be Deployment or StatefulSet, got %q", ro.Kind))
		return
	}
	ref := scenario.ResourceRef{APIVersion: "apps/v1", Kind: ro.Kind, Namespace: ro.Namespace, Name: ro.Name}
	if ref.Namespace == "" {
		ref.Namespace = rd.Namespace
	}
	obj, err := r.k.Get(ctx, ref)
	switch {
	case apierrors.IsNotFound(err):
		setFail(res, "exists", "not found")
		return
	case err != nil:
		setError(res, fmt.Sprintf("get %s %s/%s: %v", ref.Kind, ref.Namespace, ref.Name, truncate(err.Error())))
		return
	case obj == nil:
		setError(res, fmt.Sprintf("get %s %s/%s: nil object", ref.Kind, ref.Namespace, ref.Name))
		return
	}
	s := readRollout(obj)
	if !s.done() {
		setFail(res, s.expected(), s.actual())
		return
	}
	res.Status = StatusPass
	res.Expected = s.expected()
	res.Actual = s.actual()
}

func readRollout(obj *unstructured.Unstructured) rolloutState {
	want, ok := intField(obj.Object, "spec", "replicas")
	if !ok {
		want = 1
	}
	s := rolloutState{generation: obj.GetGeneration(), want: want}
	s.observed, _ = intField(obj.Object, "status", "observedGeneration")
	s.replicas, _ = intField(obj.Object, "status", "replicas")
	s.updated, _ = intField(obj.Object, "status", "updatedReplicas")
	s.available, _ = intField(obj.Object, "status", "availableReplicas")
	return s
}

func intField(obj map[string]any, fields ...string) (int64, bool) {
	v, ok, err := unstructured.NestedFieldNoCopy(obj, fields...)
	if !ok || err != nil {
		return 0, false
	}
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	case float64:
		return int64(n), true
	}
	return 0, false
}

func hasValidator(c scenario.Check) bool {
	return c.Exists != nil || c.Equals != nil || c.NotEquals != nil || c.Matches != "" || c.Contains != "" ||
		c.GTE != nil || c.LTE != nil || c.NotEmpty || c.ExitCode != nil
}
