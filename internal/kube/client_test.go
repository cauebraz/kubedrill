package kube

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/cauebraz/kubedrill/internal/scenario"
)

type stubResolver struct{}

func (stubResolver) resolve(apiVersion, kind string) (schema.GroupVersionResource, bool, error) {
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return schema.GroupVersionResource{}, false, err
	}
	switch kind {
	case "ConfigMap":
		return gv.WithResource("configmaps"), true, nil
	case "Deployment":
		return gv.WithResource("deployments"), true, nil
	case "Namespace":
		return gv.WithResource("namespaces"), false, nil
	case "ClusterRole":
		return gv.WithResource("clusterroles"), false, nil
	}
	return schema.GroupVersionResource{}, false, fmt.Errorf("resolve %s/%s: unknown kind", apiVersion, kind)
}

func namespaceObj(name string, labels map[string]any) *unstructured.Unstructured {
	meta := map[string]any{"name": name}
	if labels != nil {
		meta["labels"] = labels
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata":   meta,
	}}
}

func fakeClient(objs ...runtime.Object) (*Client, *fake.FakeDynamicClient) {
	dyn := fake.NewSimpleDynamicClient(runtime.NewScheme(), objs...)
	return &Client{dyn: dyn, resolver: stubResolver{}, resetTimeout: resetTimeout}, dyn
}

func TestResetNamespace(t *testing.T) {
	managed := map[string]any{managedByLabel: fieldManager, "kubedrill.io/seed": "1"}
	tests := []struct {
		name        string
		existing    *unstructured.Unstructured
		wantErr     string
		wantDeleted bool
	}{
		{name: "creates a missing namespace"},
		{name: "recreates a kubedrill namespace", existing: namespaceObj("lab", managed), wantDeleted: true},
		{name: "refuses a foreign namespace", existing: namespaceObj("lab", map[string]any{"team": "x"}), wantErr: "refuse to reset namespace lab: it was not created by kubedrill (missing label app.kubernetes.io/managed-by=kubedrill)"},
		{name: "refuses an unlabeled namespace", existing: namespaceObj("lab", nil), wantErr: "refuse to reset namespace lab"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var objs []runtime.Object
			if tc.existing != nil {
				objs = append(objs, tc.existing)
			}
			c, dyn := fakeClient(objs...)
			err := c.ResetNamespace(context.Background(), "lab", map[string]string{"kubedrill.io/seed": "7"})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ResetNamespace: got error %v, want it to contain %q", err, tc.wantErr)
				}
				for _, a := range dyn.Actions() {
					if a.GetVerb() == "delete" || a.GetVerb() == "create" {
						t.Errorf("ResetNamespace: performed %s although it refused", a.GetVerb())
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("ResetNamespace: got error %v, want nil", err)
			}
			deleted := false
			for _, a := range dyn.Actions() {
				if a.GetVerb() == "delete" {
					deleted = true
				}
			}
			if deleted != tc.wantDeleted {
				t.Errorf("deleted: got %v, want %v", deleted, tc.wantDeleted)
			}
			got, err := dyn.Resource(namespaceGVR).Get(context.Background(), "lab", metav1.GetOptions{})
			if err != nil {
				t.Fatalf("namespace after reset: %v", err)
			}
			labels := got.GetLabels()
			if labels[managedByLabel] != fieldManager || labels["kubedrill.io/seed"] != "7" {
				t.Errorf("labels after reset: got %v, want managed-by kubedrill and seed 7", labels)
			}
		})
	}
}

func TestResetNamespaceWaitErrors(t *testing.T) {
	managed := map[string]any{managedByLabel: fieldManager}

	t.Run("still terminating", func(t *testing.T) {
		c, dyn := fakeClient(namespaceObj("lab", managed))
		c.resetTimeout = 50 * time.Millisecond
		dyn.PrependReactor("delete", "namespaces", func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, nil
		})
		start := time.Now()
		err := c.ResetNamespace(context.Background(), "lab", nil)
		if err == nil {
			t.Fatal("ResetNamespace: got nil error, want timeout error")
		}
		want := "namespace lab is still terminating after 50ms; check finalizers with: kubectl get ns lab -o yaml"
		if err.Error() != want {
			t.Errorf("error: got %q, want %q", err, want)
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("elapsed: got %v, want under 5s", elapsed)
		}
	})

	t.Run("get error during wait", func(t *testing.T) {
		c, dyn := fakeClient(namespaceObj("lab", managed))
		deleted := false
		dyn.PrependReactor("delete", "namespaces", func(clienttesting.Action) (bool, runtime.Object, error) {
			deleted = true
			return true, nil, nil
		})
		dyn.PrependReactor("get", "namespaces", func(clienttesting.Action) (bool, runtime.Object, error) {
			if !deleted {
				return false, nil, nil
			}
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "namespaces"}, "lab", errors.New("rbac"))
		})
		err := c.ResetNamespace(context.Background(), "lab", nil)
		if err == nil || !apierrors.IsForbidden(err) {
			t.Fatalf("ResetNamespace: got %v, want wrapped Forbidden", err)
		}
		if !strings.Contains(err.Error(), "wait for deletion") {
			t.Errorf("error %q does not name the wait", err)
		}
	})

	t.Run("parent context cancelled", func(t *testing.T) {
		c, dyn := fakeClient(namespaceObj("lab", managed))
		dyn.PrependReactor("delete", "namespaces", func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, nil
		})
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(20 * time.Millisecond)
			cancel()
		}()
		err := c.ResetNamespace(ctx, "lab", nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ResetNamespace: got %v, want context.Canceled", err)
		}
	})
}

func TestApplyScope(t *testing.T) {
	cm := func(ns string) *unstructured.Unstructured {
		meta := map[string]any{"name": "cfg"}
		if ns != "" {
			meta["namespace"] = ns
		}
		return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": meta}}
	}
	tests := []struct {
		name      string
		obj       *unstructured.Unstructured
		wantErr   string
		wantPatch string
	}{
		{name: "no namespace gets the scenario namespace", obj: cm(""), wantPatch: "lab"},
		{name: "same namespace is applied", obj: cm("lab"), wantPatch: "lab"},
		{name: "other namespace is refused", obj: cm("kube-system"), wantErr: `apply ConfigMap cfg: object names namespace "kube-system" but the scenario namespace is "lab"`},
		{
			name:    "cluster-scoped kind is refused",
			obj:     &unstructured.Unstructured{Object: map[string]any{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRole", "metadata": map[string]any{"name": "admin"}}},
			wantErr: "cluster-scoped ClusterRole admin is not allowed in a scenario setup",
		},
		{
			name:    "namespace kind is refused",
			obj:     namespaceObj("other", nil),
			wantErr: "cluster-scoped Namespace other is not allowed in a scenario setup",
		},
		{
			name:    "unknown kind names the object",
			obj:     &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Widget", "metadata": map[string]any{"name": "w"}}},
			wantErr: "apply v1/Widget w: resolve v1/Widget: unknown kind",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, dyn := fakeClient()
			var patched []clienttesting.PatchAction
			dyn.PrependReactor("patch", "*", func(a clienttesting.Action) (bool, runtime.Object, error) {
				pa := a.(clienttesting.PatchAction)
				patched = append(patched, pa)
				return true, tc.obj, nil
			})
			err := c.Apply(context.Background(), "lab", []*unstructured.Unstructured{tc.obj})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Apply: got error %v, want it to contain %q", err, tc.wantErr)
				}
				if len(patched) != 0 {
					t.Errorf("Apply: sent %d patches although it refused", len(patched))
				}
				return
			}
			if err != nil {
				t.Fatalf("Apply: got error %v, want nil", err)
			}
			if len(patched) != 1 {
				t.Fatalf("patches: got %d, want 1", len(patched))
			}
			if got := patched[0].GetNamespace(); got != tc.wantPatch {
				t.Errorf("patch namespace: got %q, want %q", got, tc.wantPatch)
			}
			if got := patched[0].GetPatchType(); got != "application/apply-patch+yaml" {
				t.Errorf("patch type: got %q, want application/apply-patch+yaml", got)
			}
		})
	}

	c, _ := fakeClient()
	if err := c.Apply(context.Background(), "lab", []*unstructured.Unstructured{nil}); err == nil || !strings.Contains(err.Error(), "nil object") {
		t.Errorf("Apply nil: got %v, want nil object error", err)
	}
}

func TestClientGuards(t *testing.T) {
	cm := func(ns string) *unstructured.Unstructured {
		meta := map[string]any{"name": "cfg"}
		if ns != "" {
			meta["namespace"] = ns
		}
		return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": meta}}
	}
	tests := []struct {
		name string
		call func(c *Client) error
		want string
	}{
		{
			name: "get with empty name",
			call: func(c *Client) error {
				_, err := c.Get(context.Background(), scenario.ResourceRef{APIVersion: "v1", Kind: "ConfigMap", Namespace: "lab"})
				return err
			},
			want: "get v1 ConfigMap: name is empty",
		},
		{
			name: "get namespaced kind with empty namespace",
			call: func(c *Client) error {
				_, err := c.Get(context.Background(), scenario.ResourceRef{APIVersion: "v1", Kind: "ConfigMap", Name: "cfg"})
				return err
			},
			want: "get v1/ConfigMap cfg: configmaps is namespaced and namespace is empty",
		},
		{
			name: "list with invalid selector",
			call: func(c *Client) error {
				_, err := c.List(context.Background(), scenario.ResourceRef{APIVersion: "v1", Kind: "ConfigMap", Namespace: "lab", Selector: "a=b=c"})
				return err
			},
			want: `list v1/ConfigMap: parse selector "a=b=c": found '=', expected: ',' or 'end of string'`,
		},
		{
			name: "list namespaced kind with empty namespace",
			call: func(c *Client) error {
				_, err := c.List(context.Background(), scenario.ResourceRef{APIVersion: "v1", Kind: "ConfigMap", Selector: "app=x"})
				return err
			},
			want: "list v1/ConfigMap: configmaps is namespaced and namespace is empty",
		},
		{
			name: "apply with nil object",
			call: func(c *Client) error {
				return c.Apply(context.Background(), "lab", []*unstructured.Unstructured{cm(""), nil})
			},
			want: "apply object 1: nil object",
		},
		{
			name: "apply refuses cluster-scoped kind",
			call: func(c *Client) error {
				return c.Apply(context.Background(), "lab", []*unstructured.Unstructured{namespaceObj("other", nil)})
			},
			want: "apply object 0: cluster-scoped Namespace other is not allowed in a scenario setup",
		},
		{
			name: "apply refuses foreign namespace",
			call: func(c *Client) error {
				return c.Apply(context.Background(), "lab", []*unstructured.Unstructured{cm("prod")})
			},
			want: `apply ConfigMap cfg: object names namespace "prod" but the scenario namespace is "lab"`,
		},
		{
			name: "apply manifests names the manifest index on decode error",
			call: func(c *Client) error {
				return c.ApplyManifests(context.Background(), "lab", []string{
					"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n",
					"apiVersion: v1\nkind: ConfigMap\n",
				})
			},
			want: "manifest 1: decode document 1: v1/ConfigMap has no metadata.name",
		},
		{
			name: "apply manifests names the manifest index on apply error",
			call: func(c *Client) error {
				return c.ApplyManifests(context.Background(), "lab", []string{
					"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n",
					"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: b\n  namespace: prod\n",
				})
			},
			want: `manifest 1: apply ConfigMap b: object names namespace "prod" but the scenario namespace is "lab"`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, dyn := fakeClient()
			dyn.PrependReactor("patch", "*", func(a clienttesting.Action) (bool, runtime.Object, error) {
				pa := a.(clienttesting.PatchAction)
				return true, &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": pa.GetName()}}}, nil
			})
			err := tc.call(c)
			if err == nil {
				t.Fatalf("got nil error, want %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error: got %q, want it to contain %q", err.Error(), tc.want)
			}
			for _, a := range dyn.Actions() {
				if a.GetVerb() == "patch" && a.GetNamespace() != "lab" {
					t.Errorf("patched outside the scenario namespace: %s %s", a.GetVerb(), a.GetNamespace())
				}
			}
		})
	}
}

func TestApplyManifestsAppliesEveryObject(t *testing.T) {
	c, dyn := fakeClient()
	var names []string
	dyn.PrependReactor("patch", "*", func(a clienttesting.Action) (bool, runtime.Object, error) {
		pa := a.(clienttesting.PatchAction)
		names = append(names, pa.GetNamespace()+"/"+pa.GetName())
		return true, &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": pa.GetName()}}}, nil
	})
	err := c.ApplyManifests(context.Background(), "lab", []string{
		"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n---\napiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: b\n",
		"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: c\n",
	})
	if err != nil {
		t.Fatalf("ApplyManifests: %v", err)
	}
	if got, want := strings.Join(names, ","), "lab/a,lab/b,lab/c"; got != want {
		t.Errorf("patched objects: got %s, want %s", got, want)
	}
}

func TestGetAndListThroughFake(t *testing.T) {
	cfg := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "cfg", "namespace": "lab", "labels": map[string]any{"app": "x"}},
	}}
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		{Version: "v1", Resource: "configmaps"}: "ConfigMapList",
	}, cfg)
	c := &Client{dyn: dyn, resolver: stubResolver{}, resetTimeout: resetTimeout}
	got, err := c.Get(context.Background(), scenario.ResourceRef{APIVersion: "v1", Kind: "ConfigMap", Namespace: "lab", Name: "cfg"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.GetName() != "cfg" {
		t.Errorf("Get name: got %q, want cfg", got.GetName())
	}
	_, err = c.Get(context.Background(), scenario.ResourceRef{APIVersion: "v1", Kind: "ConfigMap", Namespace: "lab", Name: "ghost"})
	if !apierrors.IsNotFound(err) {
		t.Errorf("Get missing: got %v, want NotFound", err)
	}
	if err == nil || !strings.Contains(err.Error(), `get v1/ConfigMap ghost in "lab":`) {
		t.Errorf("Get missing error text: got %v", err)
	}
	list, err := c.List(context.Background(), scenario.ResourceRef{APIVersion: "v1", Kind: "ConfigMap", Namespace: "lab", Selector: "app=x"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list.Items) != 1 {
		t.Errorf("List items: got %d, want 1", len(list.Items))
	}
	_, err = c.Get(context.Background(), scenario.ResourceRef{APIVersion: "v1", Kind: "Widget", Namespace: "lab", Name: "w"})
	if err == nil || !strings.Contains(err.Error(), "get v1/Widget w: resolve v1/Widget: unknown kind") {
		t.Errorf("Get unknown kind: got %v, want resolver error", err)
	}
}

type stubVersioner struct {
	err    error
	gotCtx *context.Context
}

func (s stubVersioner) ServerVersionWithContext(ctx context.Context) (*version.Info, error) {
	if s.gotCtx != nil {
		*s.gotCtx = ctx
	}
	if s.err != nil {
		return nil, s.err
	}
	return &version.Info{GitVersion: "v1.37.1"}, nil
}

type pingKey struct{}

func TestPingPassesContext(t *testing.T) {
	var got context.Context
	c := &Client{disco: stubVersioner{gotCtx: &got}}
	ctx := context.WithValue(context.Background(), pingKey{}, "caller")
	if err := c.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if got == nil || got.Value(pingKey{}) != "caller" {
		t.Errorf("Ping context: got %v, want the caller's context", got)
	}
}

func TestPing(t *testing.T) {
	boom := errors.New("connection refused")
	tests := []struct {
		name    string
		disco   serverVersioner
		ctxDone bool
		wantErr string
	}{
		{name: "answers", disco: stubVersioner{}},
		{name: "server down", disco: stubVersioner{err: boom}, wantErr: "ping cluster: connection refused"},
		{name: "no discovery client", disco: nil, wantErr: "ping cluster: discovery client is not configured"},
		{name: "context canceled", disco: stubVersioner{}, ctxDone: true, wantErr: "ping cluster: context canceled"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{disco: tc.disco}
			ctx := context.Background()
			if tc.ctxDone {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			err := c.Ping(ctx)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Ping: got %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tc.wantErr {
				t.Errorf("Ping: got %v, want %q", err, tc.wantErr)
			}
			if tc.name == "server down" && !errors.Is(err, boom) {
				t.Errorf("Ping: got %v, want wrapped boom", err)
			}
		})
	}
}

func TestNamespaceLabels(t *testing.T) {
	tests := []struct {
		name     string
		existing *unstructured.Unstructured
		ns       string
		want     map[string]string
		wantNil  bool
		wantErr  string
	}{
		{name: "labeled", existing: namespaceObj("lab", map[string]any{"kubedrill.io/seed": "7", "a": "b"}), ns: "lab", want: map[string]string{"kubedrill.io/seed": "7", "a": "b"}},
		{name: "no labels", existing: namespaceObj("lab", nil), ns: "lab", want: map[string]string{}},
		{name: "missing", ns: "lab", wantNil: true},
		{name: "empty name", ns: "", wantErr: "namespace labels: namespace is empty"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var objs []runtime.Object
			if tc.existing != nil {
				objs = append(objs, tc.existing)
			}
			c, _ := fakeClient(objs...)
			got, err := c.NamespaceLabels(context.Background(), tc.ns)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("NamespaceLabels: got %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NamespaceLabels: got error %v, want nil", err)
			}
			if tc.wantNil {
				if got != nil {
					t.Errorf("NamespaceLabels of a missing namespace: got %v, want nil", got)
				}
				return
			}
			if got == nil || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("NamespaceLabels: got %v, want %v", got, tc.want)
			}
		})
	}
	t.Run("api error is wrapped", func(t *testing.T) {
		c, dyn := fakeClient()
		boom := errors.New("api down")
		dyn.PrependReactor("get", "namespaces", func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, boom
		})
		_, err := c.NamespaceLabels(context.Background(), "lab")
		if !errors.Is(err, boom) || !strings.Contains(err.Error(), "namespace labels of lab") {
			t.Errorf("NamespaceLabels: got %v, want wrapped api down naming the namespace", err)
		}
	})
}
