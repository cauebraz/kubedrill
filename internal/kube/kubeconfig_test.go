package kube

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/tools/clientcmd"
)

const twoContextKubeconfig = `apiVersion: v1
kind: Config
current-context: prod
clusters:
- name: prod-cluster
  cluster:
    server: https://prod.example.com:6443
- name: kind-kubedrill
  cluster:
    server: https://127.0.0.1:6443
    certificate-authority-data: Y2E=
contexts:
- name: prod
  context:
    cluster: prod-cluster
    user: prod-admin
- name: kind-kubedrill
  context:
    cluster: kind-kubedrill
    user: kind-kubedrill
users:
- name: prod-admin
  user:
    token: prod-secret
- name: kind-kubedrill
  user:
    client-certificate-data: Y2VydA==
    client-key-data: a2V5
`

func TestWriteContextKubeconfig(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "config")
	if err := os.WriteFile(src, []byte(twoContextKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "data", "kubeconfig")

	if err := WriteContextKubeconfig(src, "kind-kubedrill", dst); err != nil {
		t.Fatalf("WriteContextKubeconfig: got error %v, want nil", err)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat %s: %v", dst, err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("file mode: got %o, want 600", got)
	}
	dirInfo, err := os.Stat(filepath.Dir(dst))
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Errorf("dir mode: got %o, want 700", got)
	}
	cfg, err := clientcmd.LoadFromFile(dst)
	if err != nil {
		t.Fatalf("load written kubeconfig: %v", err)
	}
	if cfg.CurrentContext != "kind-kubedrill" {
		t.Errorf("current-context: got %q, want kind-kubedrill", cfg.CurrentContext)
	}
	if len(cfg.Contexts) != 1 || len(cfg.Clusters) != 1 || len(cfg.AuthInfos) != 1 {
		t.Errorf("entries: got %d contexts, %d clusters, %d users; want 1 each", len(cfg.Contexts), len(cfg.Clusters), len(cfg.AuthInfos))
	}
	if _, leaked := cfg.Contexts["prod"]; leaked {
		t.Error("context prod leaked into the pinned kubeconfig")
	}
	if got := cfg.Clusters["kind-kubedrill"].Server; got != "https://127.0.0.1:6443" {
		t.Errorf("server: got %q, want https://127.0.0.1:6443", got)
	}
	if got := string(cfg.AuthInfos["kind-kubedrill"].ClientKeyData); got != "key" {
		t.Errorf("client key: got %q, want key", got)
	}
	raw, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "prod-secret") {
		t.Error("pinned kubeconfig contains the token of another context")
	}

	err = WriteContextKubeconfig(src, "missing", filepath.Join(dir, "other"))
	if err == nil {
		t.Fatal("missing context: got nil error, want error")
	}
	for _, want := range []string{"missing", src, "kubedrill up"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing context error %q does not contain %q", err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "other")); !os.IsNotExist(err) {
		t.Errorf("file written although the context is missing: %v", err)
	}

	if err := WriteContextKubeconfig(filepath.Join(dir, "nope"), "kind-kubedrill", dst); err == nil {
		t.Error("missing source: got nil error, want error")
	}
}

func TestHasContext(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "config")
	if err := os.WriteFile(src, []byte(twoContextKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		path    string
		context string
		want    bool
		wantErr bool
	}{
		{name: "present", path: src, context: "kind-kubedrill", want: true},
		{name: "absent", path: src, context: "staging", want: false},
		{name: "missing file", path: filepath.Join(dir, "nope"), context: "kind-kubedrill", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := HasContext(tc.path, tc.context)
			if (err != nil) != tc.wantErr {
				t.Fatalf("HasContext: got error %v, want error %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("HasContext: got %v, want %v", got, tc.want)
			}
		})
	}
	if err := os.WriteFile(filepath.Join(dir, "bad"), []byte("{not yaml"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := HasContext(filepath.Join(dir, "bad"), "x"); err == nil {
		t.Error("HasContext on malformed file: got nil error, want error")
	}
}

func TestWriteContextKubeconfigSkipsUnchanged(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "config")
	if err := os.WriteFile(src, []byte(twoContextKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "data", "kubeconfig")
	if err := WriteContextKubeconfig(src, "kind-kubedrill", dst); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(dst, old, old); err != nil {
		t.Fatal(err)
	}
	if err := WriteContextKubeconfig(src, "kind-kubedrill", dst); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dst)
	if err != nil || !info.ModTime().Equal(old) {
		t.Errorf("unchanged kubeconfig rewritten: mtime %v, want %v", info.ModTime(), old)
	}
	changed := strings.Replace(twoContextKubeconfig, "127.0.0.1:6443", "127.0.0.1:7443", 1)
	if err := os.WriteFile(src, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteContextKubeconfig(src, "kind-kubedrill", dst); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst)
	if !strings.Contains(string(got), "127.0.0.1:7443") {
		t.Errorf("changed kubeconfig not written: %s", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(dst))
	if len(entries) != 1 {
		t.Errorf("data dir entries: got %d, want 1 (no temp file left)", len(entries))
	}
}
