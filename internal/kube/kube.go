// Package kube is the one place that talks to the Kubernetes API. It hides
// client-go, discovery and server-side apply behind a few methods that take
// unstructured objects and scenario.ResourceRef values.
package kube

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/cauebraz/kubedrill/internal/scenario"
)

const (
	fieldManager   = "kubedrill"
	managedByLabel = "app.kubernetes.io/managed-by"
	defaultQPS     = 50
	defaultBurst   = 100
	defaultTimeout = 30 * time.Second
	pollInterval   = 500 * time.Millisecond
	resetTimeout   = 2 * time.Minute
)

var namespaceGVR = schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}

// ErrNotManaged reports a namespace that does not carry
// app.kubernetes.io/managed-by=kubedrill; ResetNamespace and
// DeleteNamespace leave such a namespace untouched.
var ErrNotManaged = errors.New("not created by kubedrill (missing label " + managedByLabel + "=" + fieldManager + ")")

// ProbeResult is the outcome of a one-shot pod run.
type ProbeResult struct {
	ExitCode int
	Output   string
}

// Client reads and writes objects in one cluster. The zero value is not
// usable; use New.
type Client struct {
	dyn          dynamic.Interface
	core         rest.Interface
	disco        serverVersioner
	resolver     resolver
	resetTimeout time.Duration
	crdTimeout   time.Duration
}

// serverVersioner is the one discovery call Ping makes.
type serverVersioner interface {
	ServerVersionWithContext(ctx context.Context) (*version.Info, error)
}

// New builds a Client from a kubeconfig path and context name. An empty
// context uses the kubeconfig's current context.
func New(kubeconfig, contextName string) (*Client, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		rules.ExplicitPath = kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{CurrentContext: contextName}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig %q context %q: %w", kubeconfig, contextName, err)
	}
	cfg.QPS = defaultQPS
	cfg.Burst = defaultBurst
	cfg.Timeout = defaultTimeout
	cfg.UserAgent = fieldManager
	cfg.WarningHandlerWithContext = rest.NoWarnings{}
	return newFromConfig(cfg)
}

func newFromConfig(cfg *rest.Config) (*Client, error) {
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build dynamic client for %s: %w", cfg.Host, err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build clientset for %s: %w", cfg.Host, err)
	}
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(cs.Discovery()))
	return &Client{
		dyn:          dyn,
		core:         cs.CoreV1().RESTClient(),
		disco:        cs.DiscoveryClient,
		resolver:     &restResolver{mapper: mapper},
		resetTimeout: resetTimeout,
	}, nil
}

// Ping makes one discovery call (the server version) and returns its error,
// so a caller can tell whether the API server behind the kubeconfig answers.
func (c *Client) Ping(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("ping cluster: %w", err)
	}
	if c.disco == nil {
		return errors.New("ping cluster: discovery client is not configured")
	}
	if _, err := c.disco.ServerVersionWithContext(ctx); err != nil {
		return fmt.Errorf("ping cluster: %w", err)
	}
	return nil
}

// NamespaceLabels returns the labels of namespace ns, an empty non-nil map
// when it exists without labels, and nil without an error when it does not
// exist.
func (c *Client) NamespaceLabels(ctx context.Context, ns string) (map[string]string, error) {
	if ns == "" {
		return nil, errors.New("namespace labels: namespace is empty")
	}
	obj, err := c.dyn.Resource(namespaceGVR).Get(ctx, ns, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("namespace labels of %s: %w", ns, err)
	}
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	return labels, nil
}

// Get returns the object a ResourceRef names, or an error satisfying
// apierrors.IsNotFound when it does not exist.
func (c *Client) Get(ctx context.Context, ref scenario.ResourceRef) (*unstructured.Unstructured, error) {
	if ref.Name == "" {
		return nil, fmt.Errorf("get %s %s: name is empty", ref.APIVersion, ref.Kind)
	}
	ri, err := c.resourceFor(ref.APIVersion, ref.Kind, ref.Namespace)
	if err != nil {
		return nil, fmt.Errorf("get %s/%s %s: %w", ref.APIVersion, ref.Kind, ref.Name, err)
	}
	obj, err := ri.Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get %s/%s %s in %q: %w", ref.APIVersion, ref.Kind, ref.Name, ref.Namespace, err)
	}
	return obj, nil
}

// List returns the objects a ResourceRef selects (Selector set, Name empty).
func (c *Client) List(ctx context.Context, ref scenario.ResourceRef) (*unstructured.UnstructuredList, error) {
	if _, err := labels.Parse(ref.Selector); err != nil {
		return nil, fmt.Errorf("list %s/%s: parse selector %q: %w", ref.APIVersion, ref.Kind, ref.Selector, err)
	}
	ri, err := c.resourceFor(ref.APIVersion, ref.Kind, ref.Namespace)
	if err != nil {
		return nil, fmt.Errorf("list %s/%s: %w", ref.APIVersion, ref.Kind, err)
	}
	list, err := ri.List(ctx, metav1.ListOptions{LabelSelector: ref.Selector})
	if err != nil {
		return nil, fmt.Errorf("list %s/%s selector %q in %q: %w", ref.APIVersion, ref.Kind, ref.Selector, ref.Namespace, err)
	}
	return list, nil
}

// Apply creates or updates each object with server-side apply under the
// field manager "kubedrill". Objects without a namespace are applied in ns;
// an object that names another namespace or a cluster-scoped kind is
// refused, because a scenario owns only its namespace.
func (c *Client) Apply(ctx context.Context, ns string, objs []*unstructured.Unstructured) error {
	force := true
	opts := metav1.PatchOptions{FieldManager: fieldManager, Force: &force}
	for i, obj := range objs {
		if obj == nil {
			return fmt.Errorf("apply object %d: nil object", i)
		}
		gvr, namespaced, err := c.resolver.resolve(obj.GetAPIVersion(), obj.GetKind())
		if err != nil {
			return fmt.Errorf("apply %s/%s %s: %w", obj.GetAPIVersion(), obj.GetKind(), obj.GetName(), err)
		}
		if !namespaced {
			return fmt.Errorf("apply object %d: cluster-scoped %s %s is not allowed in a scenario setup", i, obj.GetKind(), obj.GetName())
		}
		if obj.GetNamespace() != "" && obj.GetNamespace() != ns {
			return fmt.Errorf("apply %s %s: object names namespace %q but the scenario namespace is %q", obj.GetKind(), obj.GetName(), obj.GetNamespace(), ns)
		}
		obj.SetNamespace(ns)
		ri := c.dyn.Resource(gvr).Namespace(ns)
		data, err := obj.MarshalJSON()
		if err != nil {
			return fmt.Errorf("apply %s/%s %s: encode: %w", obj.GetAPIVersion(), obj.GetKind(), obj.GetName(), err)
		}
		if _, err := ri.Patch(ctx, obj.GetName(), types.ApplyPatchType, data, opts); err != nil {
			return fmt.Errorf("apply %s/%s %s in %q: %w", obj.GetAPIVersion(), obj.GetKind(), obj.GetName(), obj.GetNamespace(), err)
		}
	}
	return nil
}

// ApplyManifests decodes each manifest and applies every object it holds in
// ns. The error names the manifest index that failed.
func (c *Client) ApplyManifests(ctx context.Context, ns string, manifests []string) error {
	for i, m := range manifests {
		objs, err := Decode(m)
		if err != nil {
			return fmt.Errorf("manifest %d: %w", i, err)
		}
		if err := c.Apply(ctx, ns, objs); err != nil {
			return fmt.Errorf("manifest %d: %w", i, err)
		}
	}
	return nil
}

// ResetNamespace deletes ns if it exists, waits up to two minutes for the
// deletion to finish, and creates it again with the given labels plus
// app.kubernetes.io/managed-by=kubedrill. A namespace without that label was
// not created by kubedrill and is left untouched.
func (c *Client) ResetNamespace(ctx context.Context, ns string, labels map[string]string) error {
	nsClient := c.dyn.Resource(namespaceGVR)
	existing, err := nsClient.Get(ctx, ns, metav1.GetOptions{})
	switch {
	case err == nil:
		if got := existing.GetLabels()[managedByLabel]; got != fieldManager {
			return fmt.Errorf("refuse to reset namespace %s: it was %w", ns, ErrNotManaged)
		}
		if err := deleteExact(ctx, nsClient, existing); err != nil {
			return fmt.Errorf("reset namespace %s: delete: %w", ns, err)
		}
	case !apierrors.IsNotFound(err):
		return fmt.Errorf("reset namespace %s: get: %w", ns, err)
	}
	if err := c.waitNamespaceGone(ctx, ns); err != nil {
		return err
	}
	if err := c.createNamespace(ctx, ns, labels); err != nil {
		return fmt.Errorf("reset namespace %s: %w", ns, err)
	}
	return nil
}

func (c *Client) waitNamespaceGone(ctx context.Context, ns string) error {
	timeout := c.resetTimeout
	if timeout <= 0 {
		timeout = resetTimeout
	}
	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	nsClient := c.dyn.Resource(namespaceGVR)
	err := wait.PollUntilContextCancel(wctx, pollInterval, true, func(ctx context.Context) (bool, error) {
		_, err := nsClient.Get(ctx, ns, metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(err):
			return true, nil
		case err != nil && ctx.Err() == nil:
			return false, fmt.Errorf("get: %w", err)
		}
		return false, nil
	})
	if err == nil {
		return nil
	}
	if ctx.Err() == nil && errors.Is(wctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("namespace %s is still terminating after %s; check finalizers with: kubectl get ns %s -o yaml", ns, shortDuration(timeout), ns)
	}
	return fmt.Errorf("reset namespace %s: wait for deletion: %w", ns, err)
}

func shortDuration(d time.Duration) string {
	return strings.TrimSuffix(strings.TrimSuffix(d.String(), "0s"), "0m")
}

// DeleteNamespace deletes ns and returns without waiting. A missing
// namespace is not an error. A namespace without
// app.kubernetes.io/managed-by=kubedrill was not created by kubedrill and
// is left untouched with an error wrapping ErrNotManaged.
func (c *Client) DeleteNamespace(ctx context.Context, ns string) error {
	nsClient := c.dyn.Resource(namespaceGVR)
	existing, err := nsClient.Get(ctx, ns, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete namespace %s: get: %w", ns, err)
	}
	if existing.GetLabels()[managedByLabel] != fieldManager {
		return fmt.Errorf("refuse to delete namespace %s: it was %w", ns, ErrNotManaged)
	}
	if err := deleteExact(ctx, nsClient, existing); err != nil {
		return fmt.Errorf("delete namespace %s: %w", ns, err)
	}
	return nil
}

// deleteExact deletes obj with its UID and resourceVersion as
// preconditions, so an object replaced or relabeled after the caller's
// ownership check is not deleted. A missing object is not an error.
func deleteExact(ctx context.Context, ri dynamic.ResourceInterface, obj *unstructured.Unstructured) error {
	opts := metav1.DeleteOptions{Preconditions: &metav1.Preconditions{}}
	if uid := obj.GetUID(); uid != "" {
		opts.Preconditions.UID = &uid
	}
	if rv := obj.GetResourceVersion(); rv != "" {
		opts.Preconditions.ResourceVersion = &rv
	}
	err := ri.Delete(ctx, obj.GetName(), opts)
	switch {
	case err == nil, apierrors.IsNotFound(err):
		return nil
	case apierrors.IsConflict(err):
		return fmt.Errorf("changed after the ownership check, run the command again: %w", err)
	}
	return err
}

func (c *Client) resourceFor(apiVersion, kind, namespace string) (dynamic.ResourceInterface, error) {
	gvr, namespaced, err := c.resolver.resolve(apiVersion, kind)
	if err != nil {
		return nil, err
	}
	if !namespaced {
		return c.dyn.Resource(gvr), nil
	}
	if namespace == "" {
		return nil, fmt.Errorf("%s is namespaced and namespace is empty", gvr.Resource)
	}
	return c.dyn.Resource(gvr).Namespace(namespace), nil
}
