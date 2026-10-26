package kube

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
)

const (
	defaultNamespace = "default"
	crdWaitTimeout   = time.Minute
)

var apiServiceGVR = schema.GroupVersionResource{Group: "apiregistration.k8s.io", Version: "v1", Resource: "apiservices"}

// ApplyCluster decodes each manifest and server-side applies every object
// under the field manager "kubedrill", whatever its scope: cluster-scoped
// objects as they are, namespaced objects in their own namespace or
// "default". A kind that is not served yet (a custom resource whose
// definition was applied earlier in the same call) is retried for up to a
// minute. The error names the manifest index and the object.
func (c *Client) ApplyCluster(ctx context.Context, manifests []string) error {
	force := true
	opts := metav1.PatchOptions{FieldManager: fieldManager, Force: &force}
	for i, m := range manifests {
		objs, err := Decode(m)
		if err != nil {
			return fmt.Errorf("apply cluster manifest %d: %w", i, err)
		}
		for _, obj := range objs {
			if err := c.applyAnyScope(ctx, obj, opts); err != nil {
				return fmt.Errorf("apply cluster manifest %d: %s/%s %s: %w", i, obj.GetAPIVersion(), obj.GetKind(), obj.GetName(), err)
			}
		}
	}
	return nil
}

func (c *Client) applyAnyScope(ctx context.Context, obj *unstructured.Unstructured, opts metav1.PatchOptions) error {
	data, err := obj.MarshalJSON()
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	timeout := c.crdTimeout
	if timeout <= 0 {
		timeout = crdWaitTimeout
	}
	var lastErr error
	attempt := func(ctx context.Context) (bool, error) {
		gvr, namespaced, err := c.resolver.resolve(obj.GetAPIVersion(), obj.GetKind())
		if err != nil {
			if meta.IsNoMatchError(err) {
				lastErr = err
				return false, nil
			}
			return false, err
		}
		var target dynamic.ResourceInterface = c.dyn.Resource(gvr)
		if namespaced {
			ns := obj.GetNamespace()
			if ns == "" {
				ns = defaultNamespace
			}
			target = c.dyn.Resource(gvr).Namespace(ns)
		}
		if _, err := target.Patch(ctx, obj.GetName(), types.ApplyPatchType, data, opts); err != nil {
			if apierrors.IsNotFound(err) {
				lastErr = err
				return false, nil
			}
			return false, err
		}
		return true, nil
	}
	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err = wait.PollUntilContextCancel(wctx, pollInterval, true, attempt)
	if err == nil {
		return nil
	}
	if lastErr != nil && ctx.Err() == nil && errors.Is(wctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("not served after %s: %w", shortDuration(timeout), lastErr)
	}
	return err
}

// createNamespace creates ns with the given labels plus
// app.kubernetes.io/managed-by=kubedrill. A namespace that already exists is
// an error.
func (c *Client) createNamespace(ctx context.Context, ns string, labels map[string]string) error {
	if ns == "" {
		return errors.New("create namespace: name is empty")
	}
	merged := make(map[string]any, len(labels)+1)
	for k, v := range labels {
		merged[k] = v
	}
	merged[managedByLabel] = fieldManager
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata": map[string]any{
			"name":   ns,
			"labels": merged,
		},
	}}
	if _, err := c.dyn.Resource(namespaceGVR).Create(ctx, obj, metav1.CreateOptions{FieldManager: fieldManager}); err != nil {
		return fmt.Errorf("create namespace %s: %w", ns, err)
	}
	return nil
}

// DeleteNamespaces deletes, without waiting, every namespace that matches
// the label selector and carries app.kubernetes.io/managed-by=kubedrill,
// except keep and namespaces already terminating. It returns how many
// deletions it issued. An empty selector is refused.
func (c *Client) DeleteNamespaces(ctx context.Context, selector, keep string) (int, error) {
	if selector == "" {
		return 0, errors.New("delete namespaces: selector is empty")
	}
	sel, err := labels.Parse(selector)
	if err != nil {
		return 0, fmt.Errorf("delete namespaces: parse selector %q: %w", selector, err)
	}
	nsClient := c.dyn.Resource(namespaceGVR)
	list, err := nsClient.List(ctx, metav1.ListOptions{LabelSelector: sel.String()})
	if err != nil {
		return 0, fmt.Errorf("delete namespaces %q: list: %w", selector, err)
	}
	var names []string
	for _, item := range list.Items {
		if item.GetName() == keep || item.GetDeletionTimestamp() != nil {
			continue
		}
		if item.GetLabels()[managedByLabel] != fieldManager || !sel.Matches(labels.Set(item.GetLabels())) {
			continue
		}
		names = append(names, item.GetName())
	}
	sort.Strings(names)
	deleted := 0
	for _, n := range names {
		if err := nsClient.Delete(ctx, n, metav1.DeleteOptions{}); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return deleted, fmt.Errorf("delete namespaces %q: delete %s: %w", selector, n, err)
		}
		deleted++
	}
	return deleted, nil
}

// UnavailableAPIServices returns the names of the aggregated API services
// (apiregistration.k8s.io/v1 APIService) whose Available condition is not
// True, sorted. A missing condition counts as unavailable. Namespace
// deletion stalls while any of them is unavailable.
func (c *Client) UnavailableAPIServices(ctx context.Context) ([]string, error) {
	list, err := c.dyn.Resource(apiServiceGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list apiservices: %w", err)
	}
	var names []string
	for _, item := range list.Items {
		if conditionStatus(item, "Available") != "True" {
			names = append(names, item.GetName())
		}
	}
	sort.Strings(names)
	return names, nil
}

func conditionStatus(obj unstructured.Unstructured, condType string) string {
	conds, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	for _, raw := range conds {
		cond, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := cond["type"].(string); t == condType {
			s, _ := cond["status"].(string)
			return s
		}
	}
	return ""
}
