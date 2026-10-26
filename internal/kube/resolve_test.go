package kube

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type stubMapper struct {
	meta.RESTMapper
	resets  int
	lookups int
}

func (s *stubMapper) RESTMapping(gk schema.GroupKind, _ ...string) (*meta.RESTMapping, error) {
	s.lookups++
	return nil, &meta.NoKindMatchError{GroupKind: gk}
}

func (s *stubMapper) Reset() { s.resets++ }

func TestResolverThrottlesDiscoveryResets(t *testing.T) {
	start := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		offsets    []time.Duration
		wantResets int
	}{
		{name: "first miss resets", offsets: []time.Duration{0}, wantResets: 1},
		{name: "misses within the interval reset once", offsets: []time.Duration{0, 500 * time.Millisecond, time.Second, 2 * time.Second}, wantResets: 1},
		{name: "a miss after the interval resets again", offsets: []time.Duration{0, time.Second, resolverResetInterval, resolverResetInterval + time.Second}, wantResets: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &stubMapper{}
			var now time.Time
			r := &restResolver{mapper: m, now: func() time.Time { return now }}
			for _, off := range tc.offsets {
				now = start.Add(off)
				if _, _, err := r.resolve("x.io/v1", "Stage"); !meta.IsNoMatchError(err) {
					t.Fatalf("resolve: got %v, want a NoMatch error", err)
				}
			}
			if m.resets != tc.wantResets {
				t.Errorf("mapper resets: got %d, want %d", m.resets, tc.wantResets)
			}
			if want := len(tc.offsets) + tc.wantResets; m.lookups != want {
				t.Errorf("mapper lookups: got %d, want %d", m.lookups, want)
			}
		})
	}
}
