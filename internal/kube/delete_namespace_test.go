package kube

import (
	"context"
	"errors"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
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
