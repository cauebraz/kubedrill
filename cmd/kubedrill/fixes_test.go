package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cauebraz/kubedrill/internal/workstation"
)

func TestScriptKubeconfigLivesInTheStateDir(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	dir := t.TempDir()
	learner := filepath.Join(dir, "config")
	if err := os.WriteFile(learner, []byte(unreachableKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	journalDir := t.TempDir()
	stale := filepath.Join(journalDir, "kubeconfig")
	if err := os.WriteFile(stale, []byte("old pinned copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := factories(io.Discard, func(string) string { return "" }, time.Now, resolveStateDir())
	got, err := f.ScriptKubeconfig(journalDir, learner, "kind-kubedrill")
	if err != nil {
		t.Fatalf("ScriptKubeconfig: %v", err)
	}
	want := filepath.Join(state, "kubedrill", "kubedrill", "kubeconfig-scripts")
	if got != want {
		t.Errorf("ScriptKubeconfig path: got %q, want %q", got, want)
	}
	info, err := os.Stat(got)
	if err != nil {
		t.Fatalf("pinned kubeconfig: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("pinned kubeconfig mode: got %o, want 0600", info.Mode().Perm())
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale <journal>/kubeconfig after the first run: stat err %v, want not exist", err)
	}
	entries, err := os.ReadDir(journalDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("journal directory after ScriptKubeconfig: got %d entries, want none script-related", len(entries))
	}
	if again, err := f.ScriptKubeconfig(journalDir, learner, "kind-kubedrill"); err != nil || again != want {
		t.Errorf("second run without a stale file: got (%q, %v), want (%q, nil)", again, err, want)
	}
}

func TestFaultsFactoryUsesTheGivenStateDir(t *testing.T) {
	f := factories(io.Discard, func(string) string { return "" }, time.Now, resolveStateDir())
	dir := filepath.Join(t.TempDir(), "state")
	fm := f.Faults(dir)
	if fm == nil {
		t.Fatal("Faults on the host: got nil")
	}
	if active, err := fm.Active(context.Background()); err != nil || len(active) != 0 {
		t.Fatalf("Active: got (%v, %v), want none", active, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "faults.lock")); err != nil {
		t.Errorf("lock file under the given state dir: %v, want it created there", err)
	}
	var out testWriter
	if fm := factories(&out, func(string) string { return "" }, time.Now, resolveStateDir()).Faults(""); fm != nil {
		t.Errorf("Faults with an empty state dir: got %T, want nil and a warning", fm)
	}
	if out.String() == "" {
		t.Errorf("Faults with an empty state dir printed no warning")
	}
}

type testWriter struct{ buf []byte }

func (w *testWriter) Write(p []byte) (int, error) { w.buf = append(w.buf, p...); return len(p), nil }
func (w *testWriter) String() string              { return string(w.buf) }

func TestWorkstationFactoryUsesTheClusterStateDir(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	root := t.TempDir()
	for rel, body := range map[string]string{
		"go.mod":                "module example.com/kubedrillfixture\n\ngo 1.21\n",
		"cmd/kubedrill/main.go": "package main\n\nfunc main() {}\n",
	} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sum := sha256.Sum256([]byte(root))
	binary := filepath.Join(state, "kubedrill", "kubedrill", "bin", "kubedrill-linux-"+runtime.GOARCH+"-"+hex.EncodeToString(sum[:])[:8])
	f := factories(io.Discard, func(string) string { return "" }, time.Now, resolveStateDir())
	ws, ok := f.Workstation(root, t.TempDir()).(*workstation.Manager)
	if !ok {
		t.Fatalf("Workstation factory: got %T, want *workstation.Manager", f.Workstation(root, t.TempDir()))
	}
	got, err := ws.EnsureBinary(context.Background())
	if err != nil {
		t.Fatalf("EnsureBinary: %v", err)
	}
	if got != binary {
		t.Errorf("EnsureBinary path: got %q, want %q (WithStateDir pointing at the cluster state dir)", got, binary)
	}
	if _, err := os.Stat(binary); err != nil {
		t.Errorf("built binary: %v", err)
	}
}

func TestWorkstationFactoryReportsTheStateDirError(t *testing.T) {
	cause := errors.New("cluster state directory: home directory: $HOME is not defined")
	f := factories(io.Discard, func(string) string { return "" }, time.Now, stateDir{err: cause})
	ws := f.Workstation(t.TempDir(), t.TempDir())
	want := "workstation: " + cause.Error()
	for name, err := range map[string]error{
		"DockerAvailable": ws.DockerAvailable(context.Background()),
		"Shell":           ws.Shell(context.Background(), workstation.ShellOptions{}),
		"NodeShell":       ws.NodeShell(context.Background(), "kubedrill", "worker"),
		"Replay":          ws.Replay(context.Background(), "x.cast"),
	} {
		if err == nil || err.Error() != want || !errors.Is(err, cause) {
			t.Errorf("%s: got %v, want %q wrapping the cause", name, err, want)
		}
	}
	if _, err := checkBinDir(stateDir{err: cause}); !errors.Is(err, cause) {
		t.Errorf("checkBinDir: got %v, want the state dir error", err)
	}
}

func TestConnectOnTheHostNeverExportsTheLearnerKubeconfig(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	dir := t.TempDir()
	learner := filepath.Join(dir, "config")
	if err := os.WriteFile(learner, []byte(unreachableKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	f := factories(io.Discard, func(string) string { return "" }, time.Now, resolveStateDir())
	if _, _, err := f.Connect(learner, "kind-other", ""); err == nil || !strings.Contains(err.Error(), "write script kubeconfig: context kind-other not found") {
		t.Errorf("Connect without a pinned kubeconfig and without the context: got %v, want the pin error", err)
	}
	if _, g, err := f.Connect(learner, "kind-kubedrill", ""); err != nil || g == nil {
		t.Fatalf("Connect without a pinned kubeconfig: got (%v, %v), want a grader", g, err)
	}
	pinned := filepath.Join(state, "kubedrill", "kubedrill", "kubeconfig-scripts")
	if _, err := os.Stat(pinned); err != nil {
		t.Errorf("Connect without a pinned kubeconfig: %s not written: %v", pinned, err)
	}
}
