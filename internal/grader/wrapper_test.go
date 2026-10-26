package grader

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cauebraz/kubedrill/internal/scenario"
)

func fakeKubectl(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "real bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found")
	}
	if err := os.Symlink(bash, filepath.Join(dir, "kubectl")); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestWriteKubectlWrapper(t *testing.T) {
	realDir := fakeKubectl(t)
	t.Setenv("PATH", realDir+string(os.PathListSeparator)+"/usr/bin:/bin")
	dir := filepath.Join(t.TempDir(), "state", "bin")
	path, err := WriteKubectlWrapper(dir)
	if err != nil {
		t.Fatalf("WriteKubectlWrapper: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("wrapper mode: got %v, %v, want 0755", info, err)
	}
	if dinfo, err := os.Stat(dir); err != nil || dinfo.Mode().Perm() != 0o700 {
		t.Errorf("wrapper dir mode: got %v, %v, want 0700", dinfo, err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteKubectlWrapper(dir); err != nil {
		t.Fatal(err)
	}
	if dinfo, err := os.Stat(dir); err != nil || dinfo.Mode().Perm() != 0o700 {
		t.Errorf("existing 0755 wrapper dir: got %v, %v, want restricted to 0700", dinfo, err)
	}
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteKubectlWrapper(dir); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); !info.ModTime().Equal(old) {
		t.Errorf("identical wrapper rewritten: mtime %v, want %v", info.ModTime(), old)
	}

	r := New(nil, WithPathPrefix(dir), WithEnv([]string{"PATH=" + realDir + ":/usr/bin:/bin"}))
	rd := &scenario.Rendered{}
	code, out, err := r.runScript(context.Background(), rd, `kubectl -c 'ps -o args= -p $$; true' get pods`)
	if err != nil || code != 0 {
		t.Fatalf("script: code %d, err %v, out %q", code, err, out)
	}
	if got := strings.TrimSpace(out); !strings.HasPrefix(got, CheckUserAgent+" ") {
		t.Errorf("wrapped kubectl argv: got %q, want it to start with %q", got, CheckUserAgent)
	}

	t.Setenv("PATH", dir+":/usr/bin:/bin")
	if _, err := WriteKubectlWrapper(dir); err == nil || !strings.Contains(err.Error(), "wrapper itself") {
		t.Errorf("WriteKubectlWrapper with only the wrapper on PATH: got %v, want a self-reference error", err)
	}
}

func TestPrefixPath(t *testing.T) {
	t.Setenv("PATH", "/proc/bin")
	tests := []struct {
		env  []string
		want string
	}{
		{nil, "PATH=/w:/proc/bin"},
		{[]string{"A=1", "PATH=/a", "PATH=/b"}, "PATH=/w:/b"},
	}
	for _, tt := range tests {
		got := prefixPath(tt.env, "/w")
		if got[len(got)-1] != tt.want {
			t.Errorf("prefixPath(%q): last pair %q, want %q", tt.env, got[len(got)-1], tt.want)
		}
	}
}
