package kube

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/cauebraz/kubedrill/internal/scenario"
)

func waitingPod(reason, message string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata":   map[string]any{"name": "p", "namespace": "lab"},
		"status": map[string]any{
			"phase": "Pending",
			"containerStatuses": []any{map[string]any{
				"name":  probeContainer,
				"state": map[string]any{"waiting": map[string]any{"reason": reason, "message": message}},
			}},
		},
	}}
}

func TestRunProbeWaitFailures(t *testing.T) {
	podsGR := schema.GroupResource{Resource: "pods"}
	serverErr := apierrors.NewInternalError(errors.New("etcd leader changed"))
	tests := []struct {
		name     string
		get      func() (runtime.Object, error)
		ctxLimit time.Duration
		want     []string
		wantIs   func(error) bool
		maxTime  time.Duration
	}{
		{name: "invalid image name fails fast", get: func() (runtime.Object, error) {
			return waitingPod("InvalidImageName", `couldn't parse image name "Bad:Image"`), nil
		}, want: []string{"container probe cannot start: InvalidImageName: couldn't parse image name"}, maxTime: 3 * time.Second},
		{name: "image never pulled fails fast", get: func() (runtime.Object, error) {
			return waitingPod("ErrImageNeverPull", "image is not present"), nil
		}, want: []string{"ErrImageNeverPull"}, maxTime: 3 * time.Second},
		{name: "container config error fails fast", get: func() (runtime.Object, error) {
			return waitingPod("CreateContainerConfigError", `secret "x" not found`), nil
		}, want: []string{"CreateContainerConfigError: secret \"x\" not found"}, maxTime: 3 * time.Second},
		{name: "forbidden fails fast", get: func() (runtime.Object, error) {
			return nil, apierrors.NewForbidden(podsGR, "p", errors.New("rbac"))
		}, want: []string{"get pod", "forbidden"}, wantIs: apierrors.IsForbidden, maxTime: 3 * time.Second},
		{name: "unauthorized fails fast", get: func() (runtime.Object, error) {
			return nil, apierrors.NewUnauthorized("token expired")
		}, want: []string{"token expired"}, wantIs: apierrors.IsUnauthorized, maxTime: 3 * time.Second},
		{name: "transient error is kept for the timeout", get: func() (runtime.Object, error) {
			return nil, serverErr
		}, ctxLimit: 1500 * time.Millisecond, want: []string{"wait for completion", "last error: ", "etcd leader changed"}, maxTime: 10 * time.Second},
		{name: "pull backoff keeps polling", get: func() (runtime.Object, error) {
			return waitingPod("ErrImagePull", "registry timeout"), nil
		}, ctxLimit: 1500 * time.Millisecond, want: []string{"wait for completion", "container waiting: ErrImagePull"}, maxTime: 10 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dyn := fake.NewSimpleDynamicClient(runtime.NewScheme())
			dyn.PrependReactor("get", "pods", func(clienttesting.Action) (bool, runtime.Object, error) {
				obj, err := tc.get()
				return true, obj, err
			})
			c := &Client{dyn: dyn, resolver: stubResolver{}}
			ctx := context.Background()
			if tc.ctxLimit > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tc.ctxLimit)
				defer cancel()
			}
			start := time.Now()
			_, err := c.RunProbe(ctx, "lab", scenario.Probe{Image: "busybox:1.37", Timeout: "1s"})
			if elapsed := time.Since(start); elapsed > tc.maxTime {
				t.Errorf("RunProbe took %s, want at most %s", elapsed, tc.maxTime)
			}
			if err == nil {
				t.Fatalf("RunProbe: got nil error, want %v", tc.want)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("RunProbe error: got %q, want it to contain %q", err, w)
				}
			}
			if tc.wantIs != nil && !tc.wantIs(err) {
				t.Errorf("RunProbe error %v does not keep the API status", err)
			}
			deleted := false
			for _, a := range dyn.Actions() {
				if a.GetVerb() == "delete" {
					deleted = true
				}
			}
			if !deleted {
				t.Errorf("probe pod: got no delete, want it deleted on every path")
			}
		})
	}
}
