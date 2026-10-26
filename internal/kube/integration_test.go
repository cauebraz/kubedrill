//go:build integration

package kube

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/cauebraz/kubedrill/internal/scenario"
)

const integrationContext = "kind-kubedrill"

func integrationClient(t *testing.T) *Client {
	t.Helper()
	if os.Getenv("KUBECONFIG") == "" {
		t.Skip("KUBECONFIG is unset")
	}
	c, err := New("", integrationContext)
	if err != nil {
		t.Skipf("cannot build client for context %s: %v", integrationContext, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.Get(ctx, scenario.ResourceRef{APIVersion: "v1", Kind: "Namespace", Name: "default"}); err != nil {
		t.Skipf("API server unreachable: %v", err)
	}
	return c
}

func TestIntegrationLifecycle(t *testing.T) {
	c := integrationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	ns := "kubedrill-it-" + strings.ToLower(t.Name()[len("TestIntegration"):])
	if err := c.ResetNamespace(ctx, ns, map[string]string{"kubedrill.io/test": "true"}); err != nil {
		t.Fatalf("ResetNamespace: got error %v, want nil", err)
	}
	t.Cleanup(func() {
		dctx, dcancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer dcancel()
		if err := c.DeleteNamespace(dctx, ns); err != nil {
			t.Errorf("DeleteNamespace: got error %v, want nil", err)
		}
	})

	nsObj, err := c.Get(ctx, scenario.ResourceRef{APIVersion: "v1", Kind: "Namespace", Name: ns})
	if err != nil {
		t.Fatalf("Get namespace: got error %v, want nil", err)
	}
	if got := nsObj.GetLabels()[managedByLabel]; got != fieldManager {
		t.Errorf("namespace label %s: got %q, want %q", managedByLabel, got, fieldManager)
	}

	manifests := []string{
		"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: probe-config\n  labels:\n    app: probe\ndata:\n  key: value\n",
	}
	if err := c.ApplyManifests(ctx, ns, manifests); err != nil {
		t.Fatalf("ApplyManifests: got error %v, want nil", err)
	}
	if err := c.ApplyManifests(ctx, ns, manifests); err != nil {
		t.Fatalf("ApplyManifests second time: got error %v, want nil", err)
	}

	cm, err := c.Get(ctx, scenario.ResourceRef{APIVersion: "v1", Kind: "ConfigMap", Namespace: ns, Name: "probe-config"})
	if err != nil {
		t.Fatalf("Get configmap: got error %v, want nil", err)
	}
	if got := cm.Object["data"].(map[string]any)["key"]; got != "value" {
		t.Errorf("configmap data.key: got %v, want value", got)
	}

	list, err := c.List(ctx, scenario.ResourceRef{APIVersion: "v1", Kind: "ConfigMap", Namespace: ns, Selector: "app=probe"})
	if err != nil {
		t.Fatalf("List: got error %v, want nil", err)
	}
	if len(list.Items) != 1 {
		t.Errorf("List: got %d items, want 1", len(list.Items))
	}

	_, err = c.Get(ctx, scenario.ResourceRef{APIVersion: "v1", Kind: "ConfigMap", Namespace: ns, Name: "missing"})
	if !apierrors.IsNotFound(err) {
		t.Errorf("Get missing: got error %v, want IsNotFound", err)
	}

	res, err := c.RunProbe(ctx, ns, scenario.Probe{
		Image:   "busybox:1.36",
		Command: []string{"sh", "-c", "echo hello-probe; exit 3"},
		Timeout: "90s",
	})
	if err != nil {
		t.Fatalf("RunProbe: got error %v, want nil", err)
	}
	if res.ExitCode != 3 {
		t.Errorf("RunProbe exit code: got %d, want 3", res.ExitCode)
	}
	if !strings.Contains(res.Output, "hello-probe") {
		t.Errorf("RunProbe output: got %q, want it to contain hello-probe", res.Output)
	}
	pods, err := c.List(ctx, scenario.ResourceRef{APIVersion: "v1", Kind: "Pod", Namespace: ns, Selector: managedByLabel + "=" + fieldManager})
	if err != nil {
		t.Fatalf("List pods: got error %v, want nil", err)
	}
	for _, p := range pods.Items {
		if p.GetDeletionTimestamp() == nil {
			t.Errorf("probe pod %s still present without deletion timestamp", p.GetName())
		}
	}

	if err := c.ResetNamespace(ctx, ns, map[string]string{"kubedrill.io/test": "second"}); err != nil {
		t.Fatalf("ResetNamespace on an existing namespace: got error %v, want nil", err)
	}
	if _, err := c.Get(ctx, scenario.ResourceRef{APIVersion: "v1", Kind: "ConfigMap", Namespace: ns, Name: "probe-config"}); !apierrors.IsNotFound(err) {
		t.Errorf("Get configmap after second reset: got error %v, want IsNotFound", err)
	}
	nsObj, err = c.Get(ctx, scenario.ResourceRef{APIVersion: "v1", Kind: "Namespace", Name: ns})
	if err != nil {
		t.Fatalf("Get namespace after second reset: got error %v, want nil", err)
	}
	if got := nsObj.GetLabels()["kubedrill.io/test"]; got != "second" {
		t.Errorf("namespace label after second reset: got %q, want second", got)
	}
}
