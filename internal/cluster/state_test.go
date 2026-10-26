package cluster

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/yaml"

	kindv1alpha4 "sigs.k8s.io/kind/pkg/apis/config/v1alpha4"
)

func TestStateDir(t *testing.T) {
	xdg := t.TempDir()
	home := t.TempDir()
	tests := []struct {
		name    string
		env     map[string]string
		homeErr error
		want    string
		wantErr string
	}{
		{name: "XDG_STATE_HOME", env: map[string]string{"XDG_STATE_HOME": xdg}, want: filepath.Join(xdg, "kubedrill", "kubedrill")},
		{name: "home default", want: filepath.Join(home, ".local", "state", "kubedrill", "kubedrill")},
		{name: "no home", homeErr: errors.New("no home"), wantErr: "home directory: no home"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := stateDir(func(k string) string { return tc.env[k] }, func() (string, error) { return home, tc.homeErr })
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("stateDir: got %v, want error %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("stateDir: got %q, %v, want %q", got, err, tc.want)
			}
			info, err := os.Stat(got)
			if err != nil || info.Mode().Perm() != 0o700 {
				t.Errorf("state dir mode: got %v, %v, want 0700", info, err)
			}
		})
	}
}

func TestAuditPaths(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_STATE_HOME", xdg)
	dir, err := AuditDir()
	if want := filepath.Join(xdg, "kubedrill", "kubedrill", "audit"); err != nil || dir != want {
		t.Errorf("AuditDir: got %q, %v, want %q", dir, err, want)
	}
	log, err := AuditLogPath()
	if want := filepath.Join(xdg, "kubedrill", "kubedrill", "audit", "audit.log"); err != nil || log != want {
		t.Errorf("AuditLogPath: got %q, %v, want %q", log, err, want)
	}
}

func TestNodeNames(t *testing.T) {
	got := NodeNames()
	if want := []string{"control-plane", "worker", "worker2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("NodeNames: got %v, want %v", got, want)
	}
	got[0] = "x"
	if NodeNames()[0] != "control-plane" {
		t.Errorf("NodeNames returns the package slice; want a copy")
	}
}

func TestEnsureDefaultsAuditDir(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_STATE_HOME", xdg)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "base.yaml"), []byte("audit: {{ printf \"%q\" .AuditDir }}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", filepath.Join(dir, "kubeconfig"))
	m := New(dir)
	m.provider = &fakeProvider{}
	m.run = (&fakeDocker{}).run
	data := testData(t)
	data.AuditDir = ""
	if err := m.Ensure(context.Background(), "base", data); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	want := filepath.Join(xdg, "kubedrill", "kubedrill", "audit")
	info, err := os.Stat(want)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Errorf("audit dir %s: got %v, %v, want a 0700 directory", want, info, err)
	}
}

func TestBaseProfileQuotesPaths(t *testing.T) {
	data := testData(t)
	spaced := filepath.Join(t.TempDir(), "with space: and #hash")
	data.AuditDir = spaced
	raw, err := renderProfile(filepath.Join("..", "..", "cluster", "profiles", "base.yaml"), data)
	if err != nil {
		t.Fatalf("render base profile: %v", err)
	}
	var cfg kindv1alpha4.Cluster
	if err := yaml.UnmarshalStrict(raw, &cfg); err != nil {
		t.Fatalf("parse rendered base profile: %v", err)
	}
	if got := cfg.Nodes[0].ExtraMounts[1].HostPath; got != spaced {
		t.Errorf("audit hostPath: got %q, want %q", got, spaced)
	}
}

func deadlineRecorder(got *[]time.Duration) func(ctx context.Context, name string, args ...string) ([]byte, error) {
	return func(ctx context.Context, name string, args ...string) ([]byte, error) {
		d, ok := ctx.Deadline()
		if !ok {
			*got = append(*got, 0)
			return []byte("img|true"), nil
		}
		*got = append(*got, time.Until(d).Round(time.Second))
		return []byte("other|true"), nil
	}
}

func TestDockerTimeouts(t *testing.T) {
	var got []time.Duration
	m := New(t.TempDir())
	m.run = deadlineRecorder(&got)
	if _, err := m.AuditEnabled(context.Background()); err != nil {
		t.Fatal(err)
	}
	c := &addonContainer{Name: "c", Image: "img@sha256:1"}
	if err := m.ensureContainer(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{5 * time.Second, 60 * time.Second, 60 * time.Second, 5 * time.Minute}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("timeouts (audit, inspect, rm, run): got %v, want %v", got, want)
	}
}

func TestWriteInternalKubeconfigSkipsUnchanged(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)
	m.provider = &fakeProvider{kubeconfig: "apiVersion: v1\nkind: Config\n"}
	m.run = (&fakeDocker{}).run
	dst := filepath.Join(dir, "ws", "kubeconfig")
	if err := m.WriteInternalKubeconfig(context.Background(), dst); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(dst, old, old); err != nil {
		t.Fatal(err)
	}
	if err := m.WriteInternalKubeconfig(context.Background(), dst); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dst)
	if err != nil || !info.ModTime().Equal(old) {
		t.Errorf("unchanged kubeconfig was rewritten: mtime %v, want %v", info.ModTime(), old)
	}
}

func TestStateDirRestrictsExisting(t *testing.T) {
	xdg := t.TempDir()
	dir := filepath.Join(xdg, "kubedrill", "kubedrill")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := stateDir(func(k string) string { return map[string]string{"XDG_STATE_HOME": xdg}[k] }, os.UserHomeDir)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(got); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("existing state dir: got %v, %v, want restricted to 0700", info, err)
	}
}

func TestInternalKubeconfigPath(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_STATE_HOME", xdg)
	got, err := InternalKubeconfigPath()
	if err != nil || got != filepath.Join(xdg, "kubedrill", "kubedrill", "kubeconfig-internal") {
		t.Errorf("InternalKubeconfigPath: got %q, %v", got, err)
	}
}
