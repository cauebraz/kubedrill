package kube

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/restmapper"
)

type resolver interface {
	resolve(apiVersion, kind string) (gvr schema.GroupVersionResource, namespaced bool, err error)
}

type restResolver struct {
	mapper *restmapper.DeferredDiscoveryRESTMapper
}

func (r *restResolver) resolve(apiVersion, kind string) (schema.GroupVersionResource, bool, error) {
	gvr, namespaced, err := mapGVK(r.mapper, apiVersion, kind)
	if err == nil || !meta.IsNoMatchError(err) {
		return gvr, namespaced, err
	}
	r.mapper.Reset()
	return mapGVK(r.mapper, apiVersion, kind)
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
