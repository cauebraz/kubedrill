package kube

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

type funcResolver func(apiVersion, kind string) (schema.GroupVersionResource, bool, error)

func (f funcResolver) resolve(apiVersion, kind string) (schema.GroupVersionResource, bool, error) {
	return f(apiVersion, kind)
}

var listKinds = map[schema.GroupVersionResource]string{
	namespaceGVR:  "NamespaceList",
	apiServiceGVR: "APIServiceList",
}

func adminClient(objs ...runtime.Object) (*Client, *fake.FakeDynamicClient) {
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds, objs...)
	return &Client{dyn: dyn, resolver: stubResolver{}, resetTimeout: resetTimeout, crdTimeout: 2 * time.Second}, dyn
}

type patchRecord struct {
	resource, namespace, name string
}

func recordPatches(dyn *fake.FakeDynamicClient, fail func(clienttesting.PatchAction) error) (*[]patchRecord, *sync.Mutex) {
	var mu sync.Mutex
	var got []patchRecord
	dyn.PrependReactor("patch", "*", func(a clienttesting.Action) (bool, runtime.Object, error) {
		pa := a.(clienttesting.PatchAction)
		if fail != nil {
			if err := fail(pa); err != nil {
				return true, nil, err
			}
		}
		mu.Lock()
		got = append(got, patchRecord{pa.GetResource().Resource, pa.GetNamespace(), pa.GetName()})
		mu.Unlock()
		return true, &unstructured.Unstructured{Object: map[string]any{}}, nil
	})
	return &got, &mu
}

func TestApplyClusterScopes(t *testing.T) {
	c, dyn := adminClient()
	got, _ := recordPatches(dyn, nil)
	manifests := []string{
		"apiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRole\nmetadata:\n  name: reader\n---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cfg\n  namespace: kube-system\n",
		"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: plain\n",
	}
	if err := c.ApplyCluster(context.Background(), manifests); err != nil {
		t.Fatalf("ApplyCluster: %v", err)
	}
	want := []patchRecord{{"clusterroles", "", "reader"}, {"configmaps", "kube-system", "cfg"}, {"configmaps", "default", "plain"}}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("patches: got %v, want %v", *got, want)
	}
}

func TestApplyClusterWaitsForNewKinds(t *testing.T) {
	c, dyn := adminClient()
	calls := 0
	c.resolver = funcResolver(func(apiVersion, kind string) (schema.GroupVersionResource, bool, error) {
		calls++
		if calls < 3 {
			return schema.GroupVersionResource{}, false, &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: "x.io", Kind: kind}}
		}
		return schema.GroupVersionResource{Group: "x.io", Version: "v1", Resource: "stages"}, false, nil
	})
	notFound := 1
	got, _ := recordPatches(dyn, func(clienttesting.PatchAction) error {
		if notFound > 0 {
			notFound--
			return apierrors.NewNotFound(schema.GroupResource{Group: "x.io", Resource: "stages"}, "fast")
		}
		return nil
	})
	if err := c.ApplyCluster(context.Background(), []string{"apiVersion: x.io/v1\nkind: Stage\nmetadata:\n  name: fast\n"}); err != nil {
		t.Fatalf("ApplyCluster: %v", err)
	}
	if want := []patchRecord{{"stages", "", "fast"}}; !reflect.DeepEqual(*got, want) {
		t.Errorf("patches: got %v, want %v", *got, want)
	}
}

func TestApplyClusterErrors(t *testing.T) {
	boom := errors.New("forbidden")
	tests := []struct {
		name     string
		manifest string
		resolver resolver
		fail     error
		want     string
		wantIs   error
	}{
		{name: "decode error", manifest: "kind: [\n", want: "apply cluster manifest 0: decode document 1"},
		{name: "kind never served", manifest: "apiVersion: x.io/v1\nkind: Stage\nmetadata:\n  name: s\n", resolver: funcResolver(func(string, string) (schema.GroupVersionResource, bool, error) {
			return schema.GroupVersionResource{}, false, &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: "x.io", Kind: "Stage"}}
		}), want: "apply cluster manifest 0: x.io/v1/Stage s: not served after 100ms"},
		{name: "server error is not retried", manifest: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: c\n", fail: boom, want: "apply cluster manifest 0: v1/ConfigMap c: forbidden", wantIs: boom},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, dyn := adminClient()
			c.crdTimeout = 100 * time.Millisecond
			if tc.resolver != nil {
				c.resolver = tc.resolver
			}
			attempts := 0
			recordPatches(dyn, func(clienttesting.PatchAction) error {
				attempts++
				return tc.fail
			})
			err := c.ApplyCluster(context.Background(), []string{tc.manifest})
			if err == nil || !strings.HasPrefix(err.Error(), tc.want) {
				t.Fatalf("ApplyCluster: got %v, want prefix %q", err, tc.want)
			}
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Errorf("ApplyCluster error %v does not wrap %v", err, tc.wantIs)
			}
			if tc.fail != nil && attempts != 1 {
				t.Errorf("patch attempts: got %d, want 1", attempts)
			}
		})
	}
}

func TestCreateNamespaceHelper(t *testing.T) {
	c, dyn := adminClient(namespaceObj("taken", nil))
	if err := c.createNamespace(context.Background(), "lab-abcde", map[string]string{"kubedrill.io/seed": "3"}); err != nil {
		t.Fatalf("createNamespace: %v", err)
	}
	got, err := dyn.Resource(namespaceGVR).Get(context.Background(), "lab-abcde", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get created namespace: %v", err)
	}
	want := map[string]string{managedByLabel: fieldManager, "kubedrill.io/seed": "3"}
	if !reflect.DeepEqual(got.GetLabels(), want) {
		t.Errorf("labels: got %v, want %v", got.GetLabels(), want)
	}
	if err := c.createNamespace(context.Background(), "taken", nil); err == nil || !apierrors.IsAlreadyExists(err) {
		t.Errorf("createNamespace on an existing namespace: got %v, want AlreadyExists", err)
	}
	if err := c.createNamespace(context.Background(), "", nil); err == nil {
		t.Errorf("createNamespace with empty name: got nil error")
	}
}

func TestDeleteNamespaces(t *testing.T) {
	sel := "app.kubernetes.io/managed-by=kubedrill,kubedrill.io/journal=j1"
	mine := map[string]any{managedByLabel: fieldManager, "kubedrill.io/journal": "j1"}
	terminating := namespaceObj("old-term", mine)
	now := metav1.Now()
	terminating.SetDeletionTimestamp(&now)
	tests := []struct {
		name        string
		selector    string
		keep        string
		objs        []runtime.Object
		wantCount   int
		wantDeleted []string
		wantErr     string
	}{
		{
			name:     "deletes matching except keep and terminating",
			selector: sel,
			keep:     "new",
			objs: []runtime.Object{
				namespaceObj("new", mine), namespaceObj("old-a", mine), namespaceObj("old-b", mine), terminating,
				namespaceObj("other-journal", map[string]any{managedByLabel: fieldManager, "kubedrill.io/journal": "j2"}),
				namespaceObj("default", nil),
			},
			wantCount:   2,
			wantDeleted: []string{"old-a", "old-b"},
		},
		{
			name:        "selector without managed-by still spares foreign namespaces",
			selector:    "kubedrill.io/journal=j1",
			objs:        []runtime.Object{namespaceObj("foreign", map[string]any{"kubedrill.io/journal": "j1"}), namespaceObj("old", mine)},
			wantCount:   1,
			wantDeleted: []string{"old"},
		},
		{name: "empty selector is refused", selector: "", wantErr: "delete namespaces: selector is empty"},
		{name: "bad selector", selector: "a in (", wantErr: "delete namespaces: parse selector"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, dyn := adminClient(tc.objs...)
			n, err := c.DeleteNamespaces(context.Background(), tc.selector, tc.keep)
			if tc.wantErr != "" {
				if err == nil || !strings.HasPrefix(err.Error(), tc.wantErr) {
					t.Fatalf("DeleteNamespaces: got %v, want prefix %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("DeleteNamespaces: %v", err)
			}
			if n != tc.wantCount {
				t.Errorf("count: got %d, want %d", n, tc.wantCount)
			}
			var deleted []string
			for _, a := range dyn.Actions() {
				if a.GetVerb() == "delete" {
					deleted = append(deleted, a.(clienttesting.DeleteAction).GetName())
				}
			}
			sort.Strings(deleted)
			if !reflect.DeepEqual(deleted, tc.wantDeleted) {
				t.Errorf("deleted: got %v, want %v", deleted, tc.wantDeleted)
			}
		})
	}
}

func apiService(name string, conds ...map[string]any) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiregistration.k8s.io/v1",
		"kind":       "APIService",
		"metadata":   map[string]any{"name": name},
	}}
	if len(conds) > 0 {
		list := make([]any, len(conds))
		for i, c := range conds {
			list[i] = c
		}
		obj.Object["status"] = map[string]any{"conditions": list}
	}
	return obj
}

func TestUnavailableAPIServices(t *testing.T) {
	available := map[string]any{"type": "Available", "status": "True"}
	down := map[string]any{"type": "Available", "status": "False", "reason": "MissingEndpoints"}
	c, _ := adminClient(
		apiService("v1."),
		apiService("v1beta1.metrics.k8s.io", down),
		apiService("v1.apps", available),
		apiService("v1alpha1.custom.io"),
		apiService("v1.unknown.io", map[string]any{"type": "Available", "status": "Unknown"}),
	)
	got, err := c.UnavailableAPIServices(context.Background())
	if err != nil {
		t.Fatalf("UnavailableAPIServices: %v", err)
	}
	want := []string{"v1.", "v1.unknown.io", "v1alpha1.custom.io", "v1beta1.metrics.k8s.io"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("UnavailableAPIServices: got %v, want %v", got, want)
	}

	c, dyn := adminClient()
	dyn.PrependReactor("list", "apiservices", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("connection refused")
	})
	if _, err := c.UnavailableAPIServices(context.Background()); err == nil || !strings.Contains(err.Error(), "list apiservices: connection refused") {
		t.Errorf("UnavailableAPIServices with list error: got %v", err)
	}
}
