package cluster

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoLayout writes a cluster directory with profiles/base.yaml and
// audit-policy.yaml and returns a Manager over it plus the policy path.
func repoLayout(t *testing.T) (*Manager, string) {
	t.Helper()
	root := t.TempDir()
	profiles := filepath.Join(root, "cluster", "profiles")
	if err := os.MkdirAll(profiles, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProfile(t, profiles, "base.yaml")
	policy := filepath.Join(root, "cluster", "audit-policy.yaml")
	if err := os.WriteFile(policy, []byte("apiVersion: audit.k8s.io/v1\nkind: Policy\nrules:\n- level: Metadata\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return New(profiles), policy
}

func sha(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestExistsUsesBoundedDockerPs(t *testing.T) {
	fd := &fakeDocker{}
	m := New(t.TempDir())
	m.provider = &fakeProvider{}
	m.run = fd.run
	var deadlineSet bool
	m.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		_, deadlineSet = ctx.Deadline()
		return fd.run(ctx, name, args...)
	}
	ok, err := m.Exists(context.Background())
	if err != nil || ok {
		t.Errorf("Exists with no container: got (%v, %v), want (false, nil)", ok, err)
	}
	if !deadlineSet {
		t.Errorf("docker ps ran without a deadline")
	}
	want := dockerCall{"docker", "ps", "-aq", "--filter", "label=io.x-k8s.kind.cluster=kubedrill"}
	if len(fd.calls) != 1 || strings.Join(fd.calls[0], " ") != strings.Join(want, " ") {
		t.Errorf("docker calls: got %v, want %v", fd.calls, want)
	}
	fd.respond = func([]string) ([]byte, error) { return []byte("abc123\ndef456\n"), nil }
	if ok, err := m.Exists(context.Background()); err != nil || !ok {
		t.Errorf("Exists with node containers: got (%v, %v), want (true, nil)", ok, err)
	}
	fd.respond = func([]string) ([]byte, error) { return nil, exitError(t, "1", "Cannot connect to the Docker daemon") }
	if _, err := m.Exists(context.Background()); err == nil || !strings.Contains(err.Error(), "Cannot connect to the Docker daemon") {
		t.Errorf("Exists with docker down: got %v, want the daemon error", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Exists(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Exists with a cancelled context: got %v, want context.Canceled", err)
	}
}

func TestScriptKubeconfigPath(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	got, err := ScriptKubeconfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(state, "kubedrill", Name, "kubeconfig-scripts"); got != want {
		t.Errorf("ScriptKubeconfigPath: got %q, want %q", got, want)
	}
	internal, _ := InternalKubeconfigPath()
	if filepath.Dir(internal) != filepath.Dir(got) {
		t.Errorf("script kubeconfig %q is not next to the internal kubeconfig %q", got, internal)
	}
}

func TestRenderProfileExported(t *testing.T) {
	m, policy := repoLayout(t)
	data := ProfileData{AuditDir: filepath.Join(t.TempDir(), "audit"), AuditPolicy: policy}
	got, err := m.RenderProfile("base", data)
	if err != nil {
		t.Fatalf("RenderProfile: %v", err)
	}
	want, err := renderProfile(filepath.Join(m.profilesDir, "base.yaml"), data)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("RenderProfile: got %q, want the renderProfile output %q", got, want)
	}
	if _, err := m.RenderProfile("nope", data); err == nil || !strings.Contains(err.Error(), `profile "nope" not found`) || !strings.Contains(err.Error(), "available: base") {
		t.Errorf("RenderProfile of an unknown profile: got %v, want not found with the available list", err)
	}
	if _, err := m.RenderProfile("../base", data); err == nil || !strings.Contains(err.Error(), "path separators") {
		t.Errorf("RenderProfile with a path: got %v, want a refusal", err)
	}
	if _, err := m.RenderProfile("base", ProfileData{AuditDir: "relative", AuditPolicy: policy}); err == nil || !strings.Contains(err.Error(), "is not an absolute path") {
		t.Errorf("RenderProfile with a relative AuditDir: got %v, want the validation error", err)
	}
}

func TestEnsureRecordsAuditPolicyHash(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "kubeconfig"))
	m, policy := repoLayout(t)
	fp := &fakeProvider{}
	fd := &fakeDocker{}
	m.provider = fp
	m.run = fd.run
	data := ProfileData{AuditDir: filepath.Join(t.TempDir(), "audit"), AuditPolicy: policy}

	stale, err := m.AuditPolicyStale(context.Background())
	if err != nil || stale {
		t.Errorf("AuditPolicyStale without a cluster or a record: got (%v, %v), want (false, nil)", stale, err)
	}

	if err := m.Ensure(context.Background(), "", data); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	record := filepath.Join(state, "kubedrill", Name, "audit-policy.sha256")
	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("record after Ensure: %v", err)
	}
	if strings.TrimSpace(string(got)) != sha(t, policy) {
		t.Errorf("record: got %q, want the sha256 of %s", got, policy)
	}
	if info, _ := os.Stat(record); info.Mode().Perm() != 0o600 {
		t.Errorf("record mode: got %o, want 0600", info.Mode().Perm())
	}
	fd.respond = func([]string) ([]byte, error) { return []byte("node\n"), nil }
	if stale, err := m.AuditPolicyStale(context.Background()); err != nil || stale {
		t.Errorf("AuditPolicyStale right after creation: got (%v, %v), want (false, nil)", stale, err)
	}

	if err := os.WriteFile(policy, []byte("apiVersion: audit.k8s.io/v1\nkind: Policy\nrules:\n- level: RequestResponse\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if stale, err := m.AuditPolicyStale(context.Background()); err != nil || !stale {
		t.Errorf("AuditPolicyStale after editing the repository file: got (%v, %v), want (true, nil)", stale, err)
	}

	fd.calls = nil
	if err := m.Ensure(context.Background(), "", data); err != nil {
		t.Fatalf("Ensure on an existing cluster: %v", err)
	}
	if again, _ := os.ReadFile(record); string(again) != string(got) {
		t.Errorf("record rewritten by an Ensure that created nothing: got %q, want %q", again, got)
	}

	if err := m.Delete(context.Background()); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(record); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("record after Delete: got %v, want it removed", err)
	}
}

func TestAuditPolicyStaleWithoutRecordAsksTheNode(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	m, policy := repoLayout(t)
	inNode := "apiVersion: audit.k8s.io/v1\nkind: Policy\nrules:\n- level: Metadata\n"
	fd := &fakeDocker{respond: func(args []string) ([]byte, error) {
		switch args[0] {
		case "ps":
			return []byte("node\n"), nil
		case "exec":
			return []byte(inNode), nil
		}
		return nil, nil
	}}
	m.provider = &fakeProvider{}
	m.run = fd.run

	stale, err := m.AuditPolicyStale(context.Background())
	if err != nil || stale {
		t.Errorf("same policy in the node: got (%v, %v), want (false, nil)", stale, err)
	}
	want := dockerCall{"docker", "exec", controlPlane, "cat", auditPolicyInNode}
	var sawCat bool
	for _, c := range fd.calls {
		if strings.Join(c, " ") == strings.Join(want, " ") {
			sawCat = true
		}
	}
	if !sawCat {
		t.Errorf("docker calls %v lack %v", fd.calls, want)
	}

	inNode = "apiVersion: audit.k8s.io/v1\nkind: Policy\nrules:\n- level: None\n"
	if stale, err := m.AuditPolicyStale(context.Background()); err != nil || !stale {
		t.Errorf("different policy in the node: got (%v, %v), want (true, nil)", stale, err)
	}

	fd.respond = func(args []string) ([]byte, error) {
		if args[0] == "exec" {
			return nil, exitError(t, "1", "cat: /etc/kubernetes/audit-policy.yaml: No such file or directory")
		}
		return []byte("node\n"), nil
	}
	if _, err := m.AuditPolicyStale(context.Background()); err == nil || !strings.Contains(err.Error(), "No such file or directory") {
		t.Errorf("node without the policy file: got %v, want the cat error", err)
	}

	if err := os.Remove(policy); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AuditPolicyStale(context.Background()); err == nil || !strings.Contains(err.Error(), "check audit policy") {
		t.Errorf("missing repository file: got %v, want an error", err)
	}
}

func TestDeploymentsInSplitsOnCommentedSeparators(t *testing.T) {
	manifest := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: a\n  namespace: x\n--- # second\napiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: b\n---\t\napiVersion: v1\nkind: Service\nmetadata:\n  name: c\n"
	got := deploymentsIn([]string{manifest})
	if want := "x/a,default/b"; strings.Join(got, ",") != want {
		t.Errorf("deploymentsIn: got %v, want %s", got, want)
	}
}

func TestEnsureAddonsWithoutDeploymentsPrintsNoWaitLines(t *testing.T) {
	root := t.TempDir()
	writeAddon(t, root, "crds", "name: crds\nmanifests: [m.yaml]\n", map[string]string{"m.yaml": "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: crds\n---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: c\n  namespace: crds\n"})
	var out strings.Builder
	m := New(filepath.Join(t.TempDir(), "profiles"), WithAddonsDir(root), WithOutput(&out))
	fd := &fakeDocker{}
	m.run = fd.run
	if err := m.EnsureAddons(context.Background(), &fakeApplier{}, []string{"crds"}); err != nil {
		t.Fatal(err)
	}
	if len(fd.calls) != 0 {
		t.Errorf("docker calls: got %v, want none (no Deployment to wait for)", fd.calls)
	}
	if out.Len() != 0 {
		t.Errorf("output: got %q, want no waiting or ready lines", out.String())
	}
}
