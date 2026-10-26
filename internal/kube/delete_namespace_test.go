package kube

import (
	"context"
	"errors"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clienttesting "k8s.io/client-go/testing"
)

func TestDeleteNamespaceRefusesForeignNamespaces(t *testing.T) {
	managed := map[string]any{managedByLabel: fieldManager}
	tests := []struct {
		name        string
		existing    *unstructured.Unstructured
		wantErr     string
		wantDeleted bool
	}{
		{name: "missing namespace is no error"},
		{name: "kubedrill namespace is deleted", existing: namespaceObj("lab", managed), wantDeleted: true},
		{name: "foreign namespace is refused", existing: namespaceObj("lab", map[string]any{"team": "x"}), wantErr: "refuse to delete namespace lab: it was not created by kubedrill (missing label app.kubernetes.io/managed-by=kubedrill)"},
		{name: "unlabeled namespace is refused", existing: namespaceObj("lab", nil), wantErr: "refuse to delete namespace lab"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var objs []runtime.Object
			if tc.existing != nil {
				objs = append(objs, tc.existing)
			}
			c, dyn := fakeClient(objs...)
			err := c.DeleteNamespace(context.Background(), "lab")
			deleted := false
			for _, a := range dyn.Actions() {
				if a.GetVerb() == "delete" {
					deleted = true
				}
			}
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("DeleteNamespace: got error %v, want it to contain %q", err, tc.wantErr)
				}
				if !errors.Is(err, ErrNotManaged) {
					t.Errorf("DeleteNamespace error %v does not match ErrNotManaged", err)
				}
			} else if err != nil {
				t.Fatalf("DeleteNamespace: got error %v, want nil", err)
			}
			if deleted != tc.wantDeleted {
				t.Errorf("deleted: got %v, want %v", deleted, tc.wantDeleted)
			}
		})
	}
}

func TestResetNamespaceRefusalMatchesErrNotManaged(t *testing.T) {
	c, _ := fakeClient(namespaceObj("lab", nil))
	err := c.ResetNamespace(context.Background(), "lab", nil)
	if !errors.Is(err, ErrNotManaged) {
		t.Errorf("ResetNamespace on a foreign namespace: got %v, want ErrNotManaged", err)
	}
}

func TestNamespaceDeletesCarryPreconditions(t *testing.T) {
	managed := map[string]any{managedByLabel: fieldManager, "kubedrill.io/journal": "j1"}
	tests := []struct {
		name string
		call func(c *Client) error
	}{
		{name: "DeleteNamespace", call: func(c *Client) error { return c.DeleteNamespace(context.Background(), "lab") }},
		{name: "ResetNamespace", call: func(c *Client) error { return c.ResetNamespace(context.Background(), "lab", nil) }},
		{name: "DeleteNamespaces", call: func(c *Client) error {
			_, err := c.DeleteNamespaces(context.Background(), "kubedrill.io/journal=j1", "")
			return err
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ns := namespaceObj("lab", managed)
			ns.SetUID(types.UID("uid-1"))
			ns.SetResourceVersion("42")
			c, dyn := adminClient(ns)
			var opts []string
			dyn.PrependReactor("delete", "namespaces", func(a clienttesting.Action) (bool, runtime.Object, error) {
				p := a.(clienttesting.DeleteAction).GetDeleteOptions().Preconditions
				if p == nil || p.UID == nil || p.ResourceVersion == nil {
					opts = append(opts, "none")
					return false, nil, nil
				}
				opts = append(opts, string(*p.UID)+"@"+*p.ResourceVersion)
				return false, nil, nil
			})
			if err := tc.call(c); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if len(opts) != 1 || opts[0] != "uid-1@42" {
				t.Errorf("delete preconditions: got %v, want [uid-1@42]", opts)
			}
		})
	}
}

func TestDeleteNamespaceReportsAChangedNamespace(t *testing.T) {
	ns := namespaceObj("lab", map[string]any{managedByLabel: fieldManager})
	ns.SetUID(types.UID("uid-1"))
	ns.SetResourceVersion("42")
	c, dyn := fakeClient(ns)
	dyn.PrependReactor("delete", "namespaces", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewConflict(schema.GroupResource{Resource: "namespaces"}, "lab", errors.New("Precondition failed: UID in precondition: uid-1, UID in object meta: uid-2"))
	})
	err := c.DeleteNamespace(context.Background(), "lab")
	if err == nil || !strings.Contains(err.Error(), "delete namespace lab: changed after the ownership check") {
		t.Errorf("DeleteNamespace with a failed precondition: got %v, want the change named", err)
	}
}
