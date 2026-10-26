package workstation

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// bashrcFunctions extracts the named top-level functions from
// workstation/bashrc so a non-interactive bash -c can source them.
func bashrcFunctions(t *testing.T, names ...string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "workstation", "bashrc"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	var out strings.Builder
	for _, name := range names {
		start := strings.Index(text, "\n"+name+"() {\n")
		if start < 0 {
			t.Fatalf("bashrc lacks the function %s", name)
		}
		end := strings.Index(text[start:], "\n}\n")
		if end < 0 {
			t.Fatalf("bashrc: function %s has no closing brace", name)
		}
		out.WriteString(text[start : start+end+3])
	}
	return out.String()
}

// stubTools writes kubedrill and kubectl stubs: kubedrill namespace prints
// the content of $STUB_NS_FILE, kubectl appends its arguments to
// $STUB_KUBECTL_LOG.
func stubTools(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	stubs := map[string]string{
		"kubedrill": "#!/bin/bash\n[ \"$1\" = namespace ] || exit 1\ncat \"$STUB_NS_FILE\" 2>/dev/null\n",
		"kubectl":   "#!/bin/bash\nprintf '%s\\n' \"$*\" >>\"$STUB_KUBECTL_LOG\"\n",
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return bin
}

func TestBashrcSyncNamespaceReentersOnChange(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found")
	}
	bin := stubTools(t)
	work := t.TempDir()
	nsFile := filepath.Join(work, "ns")
	log := filepath.Join(work, "kubectl.log")
	kubeconfig := filepath.Join(work, "config")
	writable := filepath.Join(work, "writable")
	if err := os.WriteFile(kubeconfig, []byte("apiVersion: v1\nkind: Config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nsFile, []byte("task-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	functions := bashrcFunctions(t, "__kubedrill_enter_namespace", "__kubedrill_sync_namespace")
	script := functions + `
__kubedrill_ns_cached=
__kubedrill_enter_namespace
echo "cached1=$__kubedrill_ns_cached kubeconfig=$KUBECONFIG"
false
__kubedrill_sync_namespace
echo "status=$?"
echo "cached2=$__kubedrill_ns_cached"
printf 'task-b\n' >"$STUB_NS_FILE"
__kubedrill_sync_namespace
echo "cached3=$__kubedrill_ns_cached"
: >"$STUB_NS_FILE"
__kubedrill_sync_namespace
echo "cached4=$__kubedrill_ns_cached"
`
	cmd := exec.Command(bash, "-c", script)
	cmd.Env = []string{
		"PATH=" + bin + ":/usr/bin:/bin",
		"HOME=" + work,
		"KUBECONFIG=" + kubeconfig,
		"KUBEDRILL_WRITABLE_KUBECONFIG=" + writable,
		"STUB_NS_FILE=" + nsFile,
		"STUB_KUBECTL_LOG=" + log,
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash: %v\n%s", err, out)
	}
	got := string(out)
	for _, want := range []string{
		"cached1=task-a kubeconfig=" + writable + "\n",
		"status=1\n",
		"cached2=task-a\n",
		"cached3=task-b\n",
		"cached4=task-b\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	want := "config set-context --current --namespace=task-a\nconfig set-context --current --namespace=task-b\n"
	if string(calls) != want {
		t.Errorf("kubectl calls: got %q, want %q (one set-context per namespace change, none while unchanged or empty)", calls, want)
	}
	if info, err := os.Stat(writable); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("writable kubeconfig: got %v, %v, want mode 0600", info, err)
	}
}

func TestBashrcSyncNamespaceWithoutKubedrillKeepsStatus(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found")
	}
	empty := t.TempDir()
	functions := bashrcFunctions(t, "__kubedrill_enter_namespace", "__kubedrill_sync_namespace")
	script := functions + "\n__kubedrill_ns_cached=\n(exit 3)\n__kubedrill_sync_namespace\necho \"status=$? cached=$__kubedrill_ns_cached\"\n"
	cmd := exec.Command(bash, "-c", script)
	cmd.Env = []string{"PATH=" + empty + ":/usr/bin:/bin", "HOME=" + empty, "KUBECONFIG=" + filepath.Join(empty, "none")}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "status=3 cached=\n") {
		t.Errorf("output: got %q, want the previous status 3 kept and no namespace cached", out)
	}
}
