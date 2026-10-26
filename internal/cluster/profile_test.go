package cluster

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	kindv1alpha4 "sigs.k8s.io/kind/pkg/apis/config/v1alpha4"
	"sigs.k8s.io/yaml"
)

func testData(t *testing.T) ProfileData {
	t.Helper()
	dir := t.TempDir()
	policy := filepath.Join(dir, "audit-policy.yaml")
	if err := os.WriteFile(policy, []byte("apiVersion: audit.k8s.io/v1\nkind: Policy\nrules:\n- level: Metadata\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return ProfileData{AuditDir: filepath.Join(dir, "audit"), AuditPolicy: policy}
}

func TestRenderProfile(t *testing.T) {
	data := testData(t)
	tests := []struct {
		name    string
		src     string
		data    ProfileData
		want    string
		wantErr []string
	}{
		{name: "substitutes both paths", src: "a: {{ .AuditDir }}\nb: {{ .AuditPolicy }}\n", data: data, want: "a: " + data.AuditDir + "\nb: " + data.AuditPolicy + "\n"},
		{name: "relative paths are refused", src: "x: 1\n", data: ProfileData{AuditDir: "audit", AuditPolicy: "policy.yaml"}, wantErr: []string{`AuditDir "audit" is not an absolute path`, `AuditPolicy "policy.yaml" is not an absolute path`}},
		{name: "missing policy file", src: "x: 1\n", data: ProfileData{AuditDir: data.AuditDir, AuditPolicy: filepath.Join(t.TempDir(), "nope.yaml")}, wantErr: []string{"audit policy: stat "}},
		{name: "policy is a directory", src: "x: 1\n", data: ProfileData{AuditDir: data.AuditDir, AuditPolicy: t.TempDir()}, wantErr: []string{"is a directory"}},
		{name: "unknown field", src: "x: {{ .Nope }}\n", data: data, wantErr: []string{"can't evaluate field Nope"}},
		{name: "parse error", src: "x: {{ .AuditDir \n", data: data, wantErr: []string{"render profile "}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "p.yaml")
			if err := os.WriteFile(path, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := renderProfile(path, tc.data)
			if len(tc.wantErr) > 0 {
				if err == nil {
					t.Fatalf("renderProfile: got nil error, want %q", tc.wantErr)
				}
				for _, w := range tc.wantErr {
					if !strings.Contains(err.Error(), w) {
						t.Errorf("renderProfile error: got %q, want it to contain %q", err, w)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("renderProfile: got error %v, want nil", err)
			}
			if string(got) != tc.want {
				t.Errorf("renderProfile: got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBaseProfileRendersAuditConfiguration(t *testing.T) {
	data := testData(t)
	raw, err := renderProfile(filepath.Join("..", "..", "cluster", "profiles", "base.yaml"), data)
	if err != nil {
		t.Fatalf("render base profile: %v", err)
	}
	var cfg kindv1alpha4.Cluster
	if err := yaml.UnmarshalStrict(raw, &cfg); err != nil {
		t.Fatalf("parse rendered base profile: %v", err)
	}
	if len(cfg.Nodes) != 3 || cfg.Nodes[0].Role != kindv1alpha4.ControlPlaneRole {
		t.Fatalf("nodes: got %d with first role %q, want 3 with a control plane first", len(cfg.Nodes), cfg.Nodes[0].Role)
	}
	cp := cfg.Nodes[0]
	var mounts []string
	for _, m := range cp.ExtraMounts {
		mounts = append(mounts, m.HostPath+"->"+m.ContainerPath)
		if !m.SelinuxRelabel {
			t.Errorf("extraMount %s: selinuxRelabel false, want true so SELinux hosts let the API server use it", m.ContainerPath)
		}
	}
	wantMounts := []string{data.AuditPolicy + "->/etc/kubernetes/audit-policy.yaml", data.AuditDir + "->/var/log/kubernetes/audit"}
	if !reflect.DeepEqual(mounts, wantMounts) {
		t.Errorf("control plane extraMounts: got %v, want %v", mounts, wantMounts)
	}
	if len(cp.KubeadmConfigPatches) != 1 {
		t.Fatalf("control plane kubeadmConfigPatches: got %d, want 1", len(cp.KubeadmConfigPatches))
	}
	var patch struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		APIServer  struct {
			ExtraArgs []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"extraArgs"`
			ExtraVolumes []struct {
				Name      string `json:"name"`
				HostPath  string `json:"hostPath"`
				MountPath string `json:"mountPath"`
				ReadOnly  bool   `json:"readOnly"`
				PathType  string `json:"pathType"`
			} `json:"extraVolumes"`
		} `json:"apiServer"`
	}
	if err := yaml.UnmarshalStrict([]byte(cp.KubeadmConfigPatches[0]), &patch); err != nil {
		t.Fatalf("parse kubeadm patch: %v", err)
	}
	if patch.APIVersion != "kubeadm.k8s.io/v1beta4" || patch.Kind != "ClusterConfiguration" {
		t.Errorf("kubeadm patch type: got %s %s, want kubeadm.k8s.io/v1beta4 ClusterConfiguration", patch.APIVersion, patch.Kind)
	}
	args := map[string]string{}
	for _, a := range patch.APIServer.ExtraArgs {
		args[a.Name] = a.Value
	}
	wantArgs := map[string]string{
		"audit-log-path":      "/var/log/kubernetes/audit/audit.log",
		"audit-policy-file":   "/etc/kubernetes/audit-policy.yaml",
		"audit-log-maxsize":   "50",
		"audit-log-maxbackup": "5",
	}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Errorf("apiServer extraArgs: got %v, want %v", args, wantArgs)
	}
	if len(patch.APIServer.ExtraVolumes) != 2 {
		t.Fatalf("apiServer extraVolumes: got %d, want 2", len(patch.APIServer.ExtraVolumes))
	}
	for _, n := range cfg.Nodes {
		if !strings.Contains(n.Image, "@sha256:") {
			t.Errorf("node %s image %q is not pinned by digest", n.Role, n.Image)
		}
	}
}

func TestAuditPolicyParses(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "cluster", "audit-policy.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		APIVersion string   `json:"apiVersion"`
		Kind       string   `json:"kind"`
		OmitStages []string `json:"omitStages"`
		Rules      []struct {
			Level string `json:"level"`
		} `json:"rules"`
	}
	if err := yaml.Unmarshal(data, &p); err != nil {
		t.Fatalf("parse audit policy: %v", err)
	}
	if p.APIVersion != "audit.k8s.io/v1" || p.Kind != "Policy" {
		t.Errorf("audit policy type: got %s %s", p.APIVersion, p.Kind)
	}
	if !reflect.DeepEqual(p.OmitStages, []string{"RequestReceived"}) {
		t.Errorf("omitStages: got %v, want [RequestReceived]", p.OmitStages)
	}
	if n := len(p.Rules); n == 0 || p.Rules[n-1].Level != "Metadata" {
		t.Errorf("last rule: got %v, want a Metadata catch-all", p.Rules)
	}
}

func TestEnsureCreatesAuditDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "base.yaml"), []byte("name: kubedrill\naudit: {{ .AuditDir }}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", filepath.Join(dir, "kubeconfig"))
	fp := &fakeProvider{}
	m := New(dir, WithStateDir(testStateDir(t)))
	m.provider = fp
	m.run = (&fakeDocker{}).run
	data := testData(t)
	if err := m.Ensure(context.Background(), "base", data); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if info, err := os.Stat(data.AuditDir); err != nil || !info.IsDir() {
		t.Errorf("audit dir %s: got %v, want a directory", data.AuditDir, err)
	}
	if len(fp.created) != 1 {
		t.Errorf("created: got %v, want one cluster", fp.created)
	}
	fp2 := &fakeProvider{}
	m.provider = fp2
	m.run = (&fakeDocker{}).run
	err := m.Ensure(context.Background(), "base", ProfileData{AuditDir: "rel", AuditPolicy: data.AuditPolicy})
	if err == nil || !strings.Contains(err.Error(), `AuditDir "rel" is not an absolute path`) {
		t.Errorf("Ensure with relative AuditDir: got %v, want absolute path error", err)
	}
	if len(fp2.created) != 0 {
		t.Errorf("Ensure with invalid data created %v, want nothing", fp2.created)
	}
}

func exitError(t *testing.T, code string, stderr string) error {
	t.Helper()
	_, err := exec.Command("sh", "-c", "exit "+code).Output()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("exit %s: got %v, want an ExitError", code, err)
	}
	exitErr.Stderr = []byte(stderr)
	return exitErr
}

func TestAuditEnabled(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		want    bool
		wantErr string
	}{
		{name: "policy present", want: true},
		{name: "policy missing", err: exitError(t, "1", ""), want: false},
		{name: "node missing", err: exitError(t, "1", "Error response from daemon: No such container: kubedrill-control-plane\n"), wantErr: "check audit policy on kubedrill-control-plane: Error response from daemon: No such container"},
		{name: "docker missing", err: exec.ErrNotFound, wantErr: "check audit policy on kubedrill-control-plane: "},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := New(t.TempDir(), WithStateDir(testStateDir(t)))
			var got []string
			m.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
				got = append([]string{name}, args...)
				return nil, tc.err
			}
			ok, err := m.AuditEnabled(context.Background())
			want := []string{"docker", "exec", "kubedrill-control-plane", "test", "-f", "/etc/kubernetes/audit-policy.yaml"}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("command: got %v, want %v", got, want)
			}
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("AuditEnabled: got error %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || ok != tc.want {
				t.Errorf("AuditEnabled: got (%v, %v), want (%v, nil)", ok, err, tc.want)
			}
		})
	}
}

func TestAuditPolicyLevels(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "cluster", "audit-policy.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	type groupResources struct {
		Group     string   `json:"group"`
		Resources []string `json:"resources"`
	}
	var p struct {
		Rules []struct {
			Level           string           `json:"level"`
			Users           []string         `json:"users"`
			UserGroups      []string         `json:"userGroups"`
			Verbs           []string         `json:"verbs"`
			Resources       []groupResources `json:"resources"`
			NonResourceURLs []string         `json:"nonResourceURLs"`
		} `json:"rules"`
	}
	if err := yaml.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	level := func(user string, groups []string, verb, group, resource string) string {
		for _, r := range p.Rules {
			if len(r.Users) > 0 && !slices.Contains(r.Users, user) {
				continue
			}
			if len(r.UserGroups) > 0 && !slices.ContainsFunc(groups, func(g string) bool { return slices.Contains(r.UserGroups, g) }) {
				continue
			}
			if len(r.Verbs) > 0 && !slices.Contains(r.Verbs, verb) {
				continue
			}
			if len(r.NonResourceURLs) > 0 {
				continue
			}
			if len(r.Resources) > 0 && !slices.ContainsFunc(r.Resources, func(gr groupResources) bool {
				return gr.Group == group && slices.Contains(gr.Resources, resource)
			}) {
				continue
			}
			return r.Level
		}
		return "None"
	}
	learner := []string{"kubeadm:cluster-admins", "system:authenticated"}
	tests := []struct {
		verb, group, resource string
		want                  string
	}{
		{"get", "", "events", "Metadata"},
		{"list", "", "events", "Metadata"},
		{"list", "events.k8s.io", "events", "Metadata"},
		{"watch", "", "events", "None"},
		{"create", "", "events", "None"},
		{"patch", "events.k8s.io", "events", "None"},
		{"get", "", "pods", "Metadata"},
		{"patch", "apps", "deployments", "Metadata"},
		{"get", "coordination.k8s.io", "leases", "None"},
	}
	for _, tc := range tests {
		if got := level("kubernetes-admin", learner, tc.verb, tc.group, tc.resource); got != tc.want {
			t.Errorf("learner %s %s/%s: got level %s, want %s", tc.verb, tc.group, tc.resource, got, tc.want)
		}
	}
}
