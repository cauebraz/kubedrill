package kube

import (
	"fmt"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type resolver interface {
	resolve(apiVersion, kind string) (gvr schema.GroupVersionResource, namespaced bool, err error)
}

// resolverResetInterval is the least time between two discovery cache
// resets: a kind that is not served yet is retried every pollInterval, and
// each reset refetches every API group.
const resolverResetInterval = 3 * time.Second

// resettableMapper is the RESTMapper whose discovery cache can be dropped;
// restmapper.DeferredDiscoveryRESTMapper satisfies it.
type resettableMapper interface {
	meta.RESTMapper
	Reset()
}

// restResolver maps a kind through discovery. On a NoMatch it drops the
// discovery cache and retries once, at most every resolverResetInterval.
type restResolver struct {
	mapper    resettableMapper
	now       func() time.Time
	mu        sync.Mutex
	lastReset time.Time
}

func (r *restResolver) resolve(apiVersion, kind string) (schema.GroupVersionResource, bool, error) {
	gvr, namespaced, err := mapGVK(r.mapper, apiVersion, kind)
	if err == nil || !meta.IsNoMatchError(err) || !r.mayReset() {
		return gvr, namespaced, err
	}
	r.mapper.Reset()
	return mapGVK(r.mapper, apiVersion, kind)
}

func (r *restResolver) mayReset() bool {
	now := time.Now
	if r.now != nil {
		now = r.now
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t := now()
	if !r.lastReset.IsZero() && t.Sub(r.lastReset) < resolverResetInterval {
		return false
	}
	r.lastReset = t
	return true
}

func mapGVK(mapper meta.RESTMapper, apiVersion, kind string) (schema.GroupVersionResource, bool, error) {
	if apiVersion == "" || kind == "" {
		return schema.GroupVersionResource{}, false, fmt.Errorf("resolve %q/%q: apiVersion and kind are required", apiVersion, kind)
	}
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return schema.GroupVersionResource{}, false, fmt.Errorf("resolve %s/%s: parse apiVersion: %w", apiVersion, kind, err)
	}
	mapping, err := mapper.RESTMapping(schema.GroupKind{Group: gv.Group, Kind: kind}, gv.Version)
	if err != nil {
		return schema.GroupVersionResource{}, false, fmt.Errorf("resolve %s/%s: %w", apiVersion, kind, err)
	}
	return mapping.Resource, mapping.Scope.Name() == meta.RESTScopeNameNamespace, nil
}
