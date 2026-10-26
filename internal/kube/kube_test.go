package kube

import (
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/cauebraz/kubedrill/internal/scenario"
)

func TestDecode(t *testing.T) {
	tests := []struct {
		name      string
		manifest  string
		wantKinds []string
		wantErr   string
	}{
		{
			name:      "single document",
			manifest:  "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n",
			wantKinds: []string{"ConfigMap"},
		},
		{
			name:      "multiple documents",
			manifest:  "---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n---\napiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: b\n",
			wantKinds: []string{"ConfigMap", "Deployment"},
		},
		{
			name:      "empty and comment-only documents are skipped",
			manifest:  "\n---\n# only a comment\n---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: s\n---\n",
			wantKinds: []string{"Secret"},
		},
		{
			name:      "separator with a trailing comment starts a document",
			manifest:  "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n--- # next\napiVersion: v1\nkind: Secret\nmetadata:\n  name: s\n---\t\napiVersion: v1\nkind: Service\nmetadata:\n  name: svc\n",
			wantKinds: []string{"ConfigMap", "Secret", "Service"},
		},
		{
			name:      "empty manifest",
			manifest:  "",
			wantKinds: nil,
		},
		{
			name:     "invalid yaml names the document",
			manifest: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n---\nkind: [unclosed\n",
			wantErr:  "document 2",
		},
		{
			name:     "missing kind",
			manifest: "apiVersion: v1\nmetadata:\n  name: a\n",
			wantErr:  "document 1",
		},
		{
			name:     "missing name",
			manifest: "apiVersion: v1\nkind: ConfigMap\n",
			wantErr:  "document 1: v1/ConfigMap has no metadata.name",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			objs, err := Decode(tc.manifest)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("Decode: got nil error, want error containing %q", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Decode: got error %q, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Decode: got error %v, want nil", err)
			}
			if len(objs) != len(tc.wantKinds) {
				t.Fatalf("Decode: got %d objects, want %d", len(objs), len(tc.wantKinds))
			}
			for i, obj := range objs {
				if got := obj.GetKind(); got != tc.wantKinds[i] {
					t.Errorf("object %d: got kind %q, want %q", i, got, tc.wantKinds[i])
				}
			}
		})
	}
}

func TestMapGVK(t *testing.T) {
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{
		{Version: "v1"},
		{Group: "apps", Version: "v1"},
	})
	mapper.Add(schema.GroupVersionKind{Version: "v1", Kind: "Pod"}, meta.RESTScopeNamespace)
	mapper.Add(schema.GroupVersionKind{Version: "v1", Kind: "Namespace"}, meta.RESTScopeRoot)
	mapper.Add(schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}, meta.RESTScopeNamespace)

	tests := []struct {
		name           string
		apiVersion     string
		kind           string
		wantGVR        schema.GroupVersionResource
		wantNamespaced bool
		wantErr        string
	}{
		{
			name:           "core namespaced",
			apiVersion:     "v1",
			kind:           "Pod",
			wantGVR:        schema.GroupVersionResource{Version: "v1", Resource: "pods"},
			wantNamespaced: true,
		},
		{
			name:           "core cluster scoped",
			apiVersion:     "v1",
			kind:           "Namespace",
			wantGVR:        schema.GroupVersionResource{Version: "v1", Resource: "namespaces"},
			wantNamespaced: false,
		},
		{
			name:           "group kind",
			apiVersion:     "apps/v1",
			kind:           "Deployment",
			wantGVR:        schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"},
			wantNamespaced: true,
		},
		{
			name:       "unknown kind",
			apiVersion: "apps/v1",
			kind:       "Widget",
			wantErr:    "resolve apps/v1/Widget",
		},
		{
			name:       "missing apiVersion",
			apiVersion: "",
			kind:       "Pod",
			wantErr:    "apiVersion and kind are required",
		},
		{
			name:       "malformed apiVersion",
			apiVersion: "a/b/c",
			kind:       "Pod",
			wantErr:    "parse apiVersion",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gvr, namespaced, err := mapGVK(mapper, tc.apiVersion, tc.kind)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("mapGVK: got error %v, want it to contain %q", err, tc.wantErr)
				}
				if !meta.IsNoMatchError(err) && tc.name == "unknown kind" {
					t.Errorf("mapGVK: unknown kind error does not satisfy meta.IsNoMatchError: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("mapGVK: got error %v, want nil", err)
			}
			if gvr != tc.wantGVR {
				t.Errorf("mapGVK: got gvr %v, want %v", gvr, tc.wantGVR)
			}
			if namespaced != tc.wantNamespaced {
				t.Errorf("mapGVK: got namespaced %v, want %v", namespaced, tc.wantNamespaced)
			}
		})
	}
}

func TestProbeTimeout(t *testing.T) {
	tests := []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{raw: "", want: "1m0s"},
		{raw: "90s", want: "1m30s"},
		{raw: "500ms", wantErr: true},
		{raw: "soon", wantErr: true},
		{raw: " 2m ", want: "2m0s"},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			d, err := ProbeTimeout(scenario.Probe{Timeout: tc.raw})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ProbeTimeout(%q): got nil error, want error", tc.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("ProbeTimeout(%q): got error %v, want nil", tc.raw, err)
			}
			if d.String() != tc.want {
				t.Errorf("ProbeTimeout(%q): got %s, want %s", tc.raw, d, tc.want)
			}
		})
	}
}

func TestProbePod(t *testing.T) {
	probe := scenario.Probe{Image: "busybox:1.36", Command: []string{"sh", "-c", "true"}}
	pod := probePod("kubedrill-probe-abcde", probe, 45*time.Second)
	if got := pod.GetName(); got != "kubedrill-probe-abcde" {
		t.Errorf("name: got %q, want kubedrill-probe-abcde", got)
	}
	if got := pod.GetLabels()[managedByLabel]; got != fieldManager {
		t.Errorf("label %s: got %q, want %q", managedByLabel, got, fieldManager)
	}
	spec := pod.Object["spec"].(map[string]any)
	if got := spec["activeDeadlineSeconds"]; got != int64(45) {
		t.Errorf("activeDeadlineSeconds: got %v, want 45", got)
	}
	if got := spec["restartPolicy"]; got != "Never" {
		t.Errorf("restartPolicy: got %v, want Never", got)
	}
	if got := spec["automountServiceAccountToken"]; got != false {
		t.Errorf("automountServiceAccountToken: got %v, want false", got)
	}
	container := spec["containers"].([]any)[0].(map[string]any)
	sc := container["securityContext"].(map[string]any)
	if got := sc["allowPrivilegeEscalation"]; got != false {
		t.Errorf("allowPrivilegeEscalation: got %v, want false", got)
	}
	if got := sc["runAsNonRoot"]; got != true {
		t.Errorf("runAsNonRoot: got %v, want true", got)
	}
	if got := sc["runAsUser"]; got != int64(65532) {
		t.Errorf("runAsUser: got %v, want 65532", got)
	}
	if got := sc["capabilities"].(map[string]any)["drop"].([]any); len(got) != 1 || got[0] != "ALL" {
		t.Errorf("capabilities.drop: got %v, want [ALL]", got)
	}
	if got := sc["seccompProfile"].(map[string]any)["type"]; got != "RuntimeDefault" {
		t.Errorf("seccompProfile.type: got %v, want RuntimeDefault", got)
	}
	res := container["resources"].(map[string]any)
	requests := res["requests"].(map[string]any)
	limits := res["limits"].(map[string]any)
	if requests["cpu"] != "10m" || requests["memory"] != "16Mi" {
		t.Errorf("requests: got %v, want cpu 10m memory 16Mi", requests)
	}
	if limits["memory"] != "64Mi" || limits["cpu"] != nil {
		t.Errorf("limits: got %v, want memory 64Mi and no cpu", limits)
	}
	if got := waitingReason(pod); got != "" {
		t.Errorf("waitingReason without status: got %q, want empty", got)
	}
	pod.Object["status"] = map[string]any{
		"phase": "Pending",
		"containerStatuses": []any{
			map[string]any{"name": probeContainer, "state": map[string]any{"waiting": map[string]any{"reason": "ImagePullBackOff"}}},
		},
	}
	if got := waitingReason(pod); got != "ImagePullBackOff" {
		t.Errorf("waitingReason: got %q, want ImagePullBackOff", got)
	}
	code, ok := terminatedExitCode(pod)
	if ok || code != 0 {
		t.Errorf("terminatedExitCode on pod without status: got (%d, %v), want (0, false)", code, ok)
	}
	pod.Object["status"] = map[string]any{
		"phase": "Failed",
		"containerStatuses": []any{
			map[string]any{"name": probeContainer, "state": map[string]any{"terminated": map[string]any{"exitCode": int64(3)}}},
		},
	}
	code, ok = terminatedExitCode(pod)
	if !ok || code != 3 {
		t.Errorf("terminatedExitCode: got (%d, %v), want (3, true)", code, ok)
	}
}

func TestRandomSuffix(t *testing.T) {
	seen := map[string]bool{}
	for _, n := range []int{0, 1, 5, 40} {
		s := RandomSuffix(n)
		if len(s) != n {
			t.Fatalf("RandomSuffix(%d): got length %d", n, len(s))
		}
		for _, r := range s {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
				t.Errorf("RandomSuffix: got character %q, want [a-z0-9]", r)
			}
		}
		seen[s] = true
	}
	if a, b := RandomSuffix(12), RandomSuffix(12); a == b {
		t.Errorf("RandomSuffix twice: got %q both times", a)
	}
}

func TestQuietClientLogs(t *testing.T) {
	saved := utilruntime.ErrorHandlers
	t.Cleanup(func() { utilruntime.ErrorHandlers = saved })
	QuietClientLogs()
	if len(utilruntime.ErrorHandlers) != 0 {
		t.Errorf("ErrorHandlers: got %d, want none", len(utilruntime.ErrorHandlers))
	}
}

func TestDecodeGenerateName(t *testing.T) {
	tests := []struct {
		name             string
		manifest         string
		wantGenerateName string
		wantErr          string
	}{
		{
			name:             "generateName without name is accepted",
			manifest:         "apiVersion: v1\nkind: Pod\nmetadata:\n  generateName: probe-\n",
			wantGenerateName: "probe-",
		},
		{
			name:     "neither name nor generateName",
			manifest: "apiVersion: v1\nkind: Pod\nmetadata:\n  labels:\n    app: x\n",
			wantErr:  "decode document 1: v1/Pod has no metadata.name",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			objs, err := Decode(tc.manifest)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Decode: got %v, want error containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if len(objs) != 1 {
				t.Fatalf("Decode: got %d objects, want 1", len(objs))
			}
			if got := objs[0].GetGenerateName(); got != tc.wantGenerateName {
				t.Errorf("GenerateName: got %q, want %q", got, tc.wantGenerateName)
			}
			if got := objs[0].GetName(); got != "" {
				t.Errorf("Name: got %q, want empty", got)
			}
		})
	}
}
