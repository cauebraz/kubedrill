package cluster

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	kindcluster "sigs.k8s.io/kind/pkg/cluster"
	kindlog "sigs.k8s.io/kind/pkg/log"
)

type fakeProvider struct {
	clusters      []string
	created       []string
	deleted       []string
	exported      []string
	kubeconfig    string
	kubeconfigErr error
	listErr       error
	createErr     error
	deleteErr     error
	onCreate      func()
	onDelete      func()
}

func (f *fakeProvider) List() ([]string, error) { return f.clusters, f.listErr }

func (f *fakeProvider) Create(name string, _ ...kindcluster.CreateOption) error {
	f.created = append(f.created, name)
	if f.onCreate != nil {
		f.onCreate()
	}
	if f.createErr != nil {
		return f.createErr
	}
	f.clusters = append(f.clusters, name)
	return nil
}

func (f *fakeProvider) Delete(name, _ string) error {
	f.deleted = append(f.deleted, name)
	if f.onDelete != nil {
		f.onDelete()
	}
	return f.deleteErr
}

func (f *fakeProvider) ExportKubeConfig(name, _ string, _ bool) error {
	f.exported = append(f.exported, name)
	return nil
}

func (f *fakeProvider) KubeConfig(_ string, internal bool) (string, error) {
	if f.kubeconfigErr != nil {
		return "", f.kubeconfigErr
	}
	if !internal {
		return "", errors.New("fake: expected internal=true")
	}
	return f.kubeconfig, nil
}

// dockerFor returns a fakeDocker whose docker ps answers from fp: a line
// when fp.clusters holds Name, fp.listErr as the error; every other command
// goes to respond (nil output when respond is nil).
func dockerFor(fp *fakeProvider, respond func(args []string) ([]byte, error)) *fakeDocker {
	return &fakeDocker{respond: func(args []string) ([]byte, error) {
		if args[0] == "ps" && strings.HasPrefix(args[len(args)-1], "label="+kindClusterLabel+"=") {
			if fp.listErr != nil {
				return nil, fp.listErr
			}
			for _, n := range fp.clusters {
				if n == Name {
					return []byte("node\n"), nil
				}
			}
			return nil, nil
		}
		if respond == nil {
			return nil, nil
		}
		return respond(args)
	}}
}

func writeProfile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("kind: Cluster\napiVersion: kind.x-k8s.io/v1alpha4\nname: kubedrill\n"), 0o644); err != nil {
		t.Fatalf("write profile %s: %v", name, err)
	}
}

func TestProfiles(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "base.yaml")
	writeProfile(t, dir, "storage.yaml")
	writeProfile(t, dir, "README.md")
	if err := os.Mkdir(filepath.Join(dir, "ignored.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := New(dir).Profiles()
	if err != nil {
		t.Fatalf("Profiles: got error %v, want nil", err)
	}
	want := []string{"base", "storage"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Profiles: got %v, want %v", got, want)
	}

	if _, err := New(filepath.Join(dir, "missing")).Profiles(); err == nil {
		t.Errorf("Profiles on missing dir: got nil error, want error")
	}
}

func TestEnsureMissingProfile(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "base.yaml")
	m := New(dir, WithStateDir(testStateDir(t)))
	m.provider = &fakeProvider{}
	m.run = (&fakeDocker{}).run

	err := m.Ensure(context.Background(), "nope", testData(t))
	if err == nil {
		t.Fatalf("Ensure: got nil error, want error")
	}
	for _, want := range []string{`profile "nope" not found`, "available: base"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Ensure error %q does not contain %q", err, want)
		}
	}
}

func TestEnsureCreatesOnce(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "base.yaml")
	kubeconfig := filepath.Join(dir, "kubeconfig")
	t.Setenv("KUBECONFIG", kubeconfig)

	fp := &fakeProvider{}
	m := New(dir, WithStateDir(testStateDir(t)))
	m.provider = fp
	m.run = dockerFor(fp, nil).run

	if err := m.Ensure(context.Background(), "", testData(t)); err != nil {
		t.Fatalf("Ensure: got error %v, want nil", err)
	}
	if len(fp.created) != 1 || fp.created[0] != Name {
		t.Fatalf("Ensure: created %v, want [%s]", fp.created, Name)
	}

	kc := "apiVersion: v1\nkind: Config\ncontexts:\n- name: kind-kubedrill\n  context:\n    cluster: kind-kubedrill\n    user: kind-kubedrill\nclusters:\n- name: kind-kubedrill\n  cluster:\n    server: https://127.0.0.1:6443\nusers:\n- name: kind-kubedrill\n  user: {}\ncurrent-context: kind-kubedrill\n"
	if err := os.WriteFile(kubeconfig, []byte(kc), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.Ensure(context.Background(), "base", testData(t)); err != nil {
		t.Fatalf("Ensure second call: got error %v, want nil", err)
	}
	if len(fp.created) != 1 {
		t.Errorf("Ensure second call: created %d times, want 1", len(fp.created))
	}
	if len(fp.exported) != 0 {
		t.Errorf("Ensure with context present: exported %d times, want 0", len(fp.exported))
	}

	if err := os.Remove(kubeconfig); err != nil {
		t.Fatal(err)
	}
	if err := m.Ensure(context.Background(), "base", testData(t)); err != nil {
		t.Fatalf("Ensure without context: got error %v, want nil", err)
	}
	if len(fp.exported) != 1 {
		t.Errorf("Ensure without context: exported %d times, want 1", len(fp.exported))
	}
}

func TestExistsAndDelete(t *testing.T) {
	fp := &fakeProvider{clusters: []string{"other"}}
	m := New(t.TempDir(), WithStateDir(testStateDir(t)))
	m.provider = fp
	var nodes string
	var psErr error
	fd := &fakeDocker{respond: func(args []string) ([]byte, error) {
		if args[0] == "ps" && len(args) > 2 && strings.HasPrefix(args[len(args)-1], "label="+kindClusterLabel+"=") {
			return []byte(nodes), psErr
		}
		return nil, nil
	}}
	m.run = fd.run
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "kubeconfig"))

	ok, err := m.Exists(context.Background())
	if err != nil || ok {
		t.Errorf("Exists: got (%v, %v), want (false, nil)", ok, err)
	}
	nodes = "0123abcd\n"
	ok, err = m.Exists(context.Background())
	if err != nil || !ok {
		t.Errorf("Exists: got (%v, %v), want (true, nil)", ok, err)
	}
	psErr = errors.New("docker down")
	if _, err := m.Exists(context.Background()); err == nil {
		t.Errorf("Exists with docker error: got nil, want error")
	}
	psErr = nil
	if err := m.Delete(context.Background()); err != nil {
		t.Errorf("Delete: got error %v, want nil", err)
	}
	if len(fp.deleted) != 1 || fp.deleted[0] != Name {
		t.Errorf("Delete: deleted %v, want [%s]", fp.deleted, Name)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Exists(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Exists with cancelled ctx: got %v, want context.Canceled", err)
	}
}

func TestKubeconfigPath(t *testing.T) {
	home := func() (string, error) { return "/home/learner", nil }
	tests := []struct {
		name string
		env  string
		want string
	}{
		{name: "env single", env: "/tmp/kc", want: "/tmp/kc"},
		{name: "env list takes first", env: "/tmp/a" + string(os.PathListSeparator) + "/tmp/b", want: "/tmp/a"},
		{name: "env empty falls back to home", env: "", want: filepath.Join("/home/learner", ".kube", "config")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path, ctxName, err := kubeconfigPath(tc.env, home)
			if err != nil {
				t.Fatalf("kubeconfigPath: got error %v, want nil", err)
			}
			if path != tc.want {
				t.Errorf("path: got %q, want %q", path, tc.want)
			}
			if ctxName != "kind-kubedrill" {
				t.Errorf("context: got %q, want kind-kubedrill", ctxName)
			}
		})
	}
	if _, _, err := kubeconfigPath("", func() (string, error) { return "", errors.New("no home") }); err == nil {
		t.Errorf("kubeconfigPath without home: got nil error, want error")
	}
}

func TestParseKubectlVersion(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{
			name: "fixture",
			in:   `{"clientVersion":{"major":"1","minor":"34","gitVersion":"v1.34.1","gitCommit":"abc","buildDate":"2025-09-09T00:00:00Z","goVersion":"go1.24.6","compiler":"gc","platform":"darwin/arm64"},"kustomizeVersion":"v5.7.1"}`,
			want: "v1.34.1",
		},
		{name: "empty version", in: `{"clientVersion":{}}`, wantErr: true},
		{name: "not json", in: `Client Version: v1.34.1`, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseKubectlVersion([]byte(tc.in))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseKubectlVersion: got nil error, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseKubectlVersion: got error %v, want nil", err)
			}
			if got != tc.want {
				t.Errorf("parseKubectlVersion: got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseDockerVersion(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "28.3.2\n", want: "28.3.2"},
		{in: "", want: ""},
		{in: "<no value>\n", want: ""},
	}
	for _, tc := range tests {
		if got := parseDockerVersion([]byte(tc.in)); got != tc.want {
			t.Errorf("parseDockerVersion(%q): got %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDoctor(t *testing.T) {
	tests := []struct {
		name        string
		lookPath    func(string) (string, error)
		run         runner
		wantDocker  Tool
		wantKubectl Tool
	}{
		{
			name:     "everything available",
			lookPath: func(f string) (string, error) { return "/usr/local/bin/" + f, nil },
			run: func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name == "docker" {
					return []byte("28.3.2\n"), nil
				}
				return []byte(`{"clientVersion":{"gitVersion":"v1.34.1"}}`), nil
			},
			wantDocker:  Tool{Name: "docker", Found: true, Version: "28.3.2", Install: installDocker},
			wantKubectl: Tool{Name: "kubectl", Found: true, Version: "v1.34.1", Install: installKubectl, Optional: true},
		},
		{
			name:     "docker daemon down",
			lookPath: func(f string) (string, error) { return "/usr/local/bin/" + f, nil },
			run: func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name == "docker" {
					return nil, errors.New("exit status 1")
				}
				return []byte(`{"clientVersion":{"gitVersion":"v1.34.1"}}`), nil
			},
			wantDocker:  Tool{Name: "docker", Found: true, Detail: "daemon not running", Install: installDocker},
			wantKubectl: Tool{Name: "kubectl", Found: true, Version: "v1.34.1", Install: installKubectl, Optional: true},
		},
		{
			name:     "binaries missing",
			lookPath: func(f string) (string, error) { return "", errors.New("not found") },
			run: func(_ context.Context, name string, args ...string) ([]byte, error) {
				t.Errorf("run called for %s although lookPath failed", name)
				return nil, nil
			},
			wantDocker:  Tool{Name: "docker", Detail: "binary not found on PATH", Install: installDocker},
			wantKubectl: Tool{Name: "kubectl", Detail: "binary not found on PATH", Install: installKubectl, Optional: true},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := New(t.TempDir(), WithStateDir(testStateDir(t)))
			m.lookPath = tc.lookPath
			m.run = tc.run
			m.goos = "darwin"
			tools, err := m.Doctor(context.Background())
			if err != nil {
				t.Fatalf("Doctor: got error %v, want nil", err)
			}
			if len(tools) != 3 {
				t.Fatalf("Doctor: got %d tools, want 3", len(tools))
			}
			if tools[0] != tc.wantDocker {
				t.Errorf("docker: got %+v, want %+v", tools[0], tc.wantDocker)
			}
			if tools[1] != tc.wantKubectl {
				t.Errorf("kubectl: got %+v, want %+v", tools[1], tc.wantKubectl)
			}
			if tools[2].Name != "kind" || !tools[2].Found || tools[2].Version != "v0.33.0" {
				t.Errorf("kind: got %+v, want Found with version v0.33.0", tools[2])
			}
		})
	}
}

func TestDoctorDockerMemory(t *testing.T) {
	tests := []struct {
		name       string
		memTotal   string
		wantDetail string
	}{
		{name: "two GiB", memTotal: "2147483648", wantDetail: "2.0 GiB of memory, below the 3 GiB the cluster needs; raise it in Docker Desktop, Settings > Resources"},
		{name: "eight GiB", memTotal: "8589934592"},
		{name: "unreadable", memTotal: "<no value>"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := New(t.TempDir(), WithStateDir(testStateDir(t)))
			m.goos = "darwin"
			m.lookPath = func(f string) (string, error) { return "/usr/local/bin/" + f, nil }
			m.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
				switch {
				case name == "docker" && args[0] == "info":
					return []byte(tc.memTotal + "\n"), nil
				case name == "docker":
					return []byte("28.3.2\n"), nil
				}
				return []byte(`{"clientVersion":{"gitVersion":"v1.34.1"}}`), nil
			}
			tools, err := m.Doctor(context.Background())
			if err != nil {
				t.Fatalf("Doctor: %v", err)
			}
			if got := tools[0]; !got.Found || got.Version != "28.3.2" || got.Detail != tc.wantDetail {
				t.Errorf("docker: got %+v, want found 28.3.2 with detail %q", got, tc.wantDetail)
			}
		})
	}
}

func TestDoctorInotify(t *testing.T) {
	tests := []struct {
		name      string
		goos      string
		files     map[string]string
		wantRow   bool
		wantWarn  bool
		wantValue string
	}{
		{name: "linux defaults too low", goos: "linux", files: map[string]string{"max_user_instances": "128\n", "max_user_watches": "8192\n"}, wantRow: true, wantWarn: true, wantValue: "128 instances, 8192 watches"},
		{name: "linux raised limits", goos: "linux", files: map[string]string{"max_user_instances": "512\n", "max_user_watches": "524288\n"}, wantRow: true, wantValue: "512 instances, 524288 watches"},
		{name: "linux unreadable", goos: "linux", files: map[string]string{}},
		{name: "macOS has no row", goos: "darwin", files: map[string]string{"max_user_instances": "1\n", "max_user_watches": "1\n"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := New(t.TempDir(), WithStateDir(testStateDir(t)))
			m.goos = tc.goos
			m.lookPath = func(f string) (string, error) { return "/usr/local/bin/" + f, nil }
			m.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name == "docker" {
					return []byte("28.3.2\n"), nil
				}
				return []byte(`{"clientVersion":{"gitVersion":"v1.34.1"}}`), nil
			}
			m.readFile = func(name string) ([]byte, error) {
				if v, ok := tc.files[strings.TrimPrefix(name, inotifyDir+"/")]; ok {
					return []byte(v), nil
				}
				return nil, fs.ErrNotExist
			}
			tools, err := m.Doctor(context.Background())
			if err != nil {
				t.Fatalf("Doctor: %v", err)
			}
			var row *Tool
			for i := range tools {
				if tools[i].Name == "inotify" {
					row = &tools[i]
				}
			}
			if (row != nil) != tc.wantRow {
				t.Fatalf("inotify row: got %+v, want present %v", row, tc.wantRow)
			}
			if row == nil {
				return
			}
			if row.Version != tc.wantValue {
				t.Errorf("inotify version: got %q, want %q", row.Version, tc.wantValue)
			}
			if warn := strings.Contains(row.Detail, "sudo sysctl fs.inotify.max_user_instances=512 fs.inotify.max_user_watches=524288"); warn != tc.wantWarn {
				t.Errorf("inotify detail: got %q, want the sysctl remedy %v", row.Detail, tc.wantWarn)
			}
		})
	}
}

func TestFilterLine(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "Creating cluster \"kubedrill\" ...", want: "Creating cluster \"kubedrill\" ..."},
		{name: "emoji and double space", in: " \u2713 Ensuring node image (kindest/node:v1.37.0) \U0001F5BC ", want: "Ensuring node image (kindest/node:v1.37.0)"},
		{name: "bullet", in: " \u2022 Preparing nodes \U0001F4E6 \U0001F4E6  ", want: "Preparing nodes"},
		{name: "blank", in: "   ", want: ""},
		{name: "only emoji", in: "\U0001F389", want: ""},
		{name: "multiline keeps one line per phase", in: "\u2713 Writing configuration \U0001F4DC\n\n\u2713 Starting control-plane \U0001F579\uFE0F\n", want: "Writing configuration\nStarting control-plane"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := filterLine(tc.in); got != tc.want {
				t.Errorf("filterLine(%q): got %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestWriterLogger(t *testing.T) {
	var out strings.Builder
	l := newWriterLogger(&out)
	l.V(0).Info(" \u2713 Ensuring node image \U0001F5BC ")
	l.V(0).Infof("Set kubectl context to %q", "kind-kubedrill")
	l.V(1).Info("verbose line")
	l.Warn("")
	l.Warnf("docker %s", "slow \u26A0")
	l.Error("boom \U0001F4A5")
	want := "Ensuring node image\nSet kubectl context to \"kind-kubedrill\"\nwarning: docker slow\nerror: boom\n"
	if out.String() != want {
		t.Errorf("logger output:\ngot  %q\nwant %q", out.String(), want)
	}
	if _, ok := newWriterLogger(nil).(kindlog.NoopLogger); !ok {
		t.Error("newWriterLogger(nil): got a writer logger, want NoopLogger")
	}
}

func TestNewOptions(t *testing.T) {
	var out strings.Builder
	m := New(t.TempDir(), WithOutput(&out), WithGetenv(func(k string) string {
		if k == "KUBECONFIG" {
			return "/tmp/pinned"
		}
		return ""
	}))
	path, name, err := m.Kubeconfig()
	if err != nil {
		t.Fatalf("Kubeconfig: %v", err)
	}
	if path != "/tmp/pinned" || name != "kind-kubedrill" {
		t.Errorf("Kubeconfig: got (%q, %q), want (/tmp/pinned, kind-kubedrill)", path, name)
	}
	if m.out != &out {
		t.Error("WithOutput: manager writes elsewhere")
	}
	d := New(t.TempDir(), WithStateDir(testStateDir(t)))
	if d.out != os.Stderr || d.getenv == nil {
		t.Error("New without options: want os.Stderr and os.Getenv defaults")
	}
}

func TestDoctorBoundsProbes(t *testing.T) {
	m := New(t.TempDir(), WithStateDir(testStateDir(t)))
	m.lookPath = func(f string) (string, error) { return "/usr/local/bin/" + f, nil }
	m.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Errorf("%s: probe context has no deadline", name)
		} else if remaining := time.Until(deadline); remaining > toolTimeout || remaining < toolTimeout-5*time.Second {
			t.Errorf("%s: deadline in %v, want about %v", name, remaining, toolTimeout)
		}
		if name == "docker" {
			return []byte("28.3.2\n"), nil
		}
		return []byte(`{"clientVersion":{"gitVersion":"v1.34.1"}}`), nil
	}
	if _, err := m.Doctor(context.Background()); err != nil {
		t.Fatalf("Doctor: %v", err)
	}
}

func TestProfilePathRejectsSeparators(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "base.yaml")
	m := New(dir, WithStateDir(testStateDir(t)))
	m.provider = &fakeProvider{}
	m.run = (&fakeDocker{}).run
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "kubeconfig"))
	tests := []string{"../base", "sub/base", "/etc/passwd", "a\\b"}
	for _, profile := range tests {
		t.Run(profile, func(t *testing.T) {
			_, err := m.profilePath(profile)
			if err == nil {
				t.Fatalf("profilePath(%q): got nil error, want rejection", profile)
			}
			if !strings.Contains(err.Error(), "name must not contain path separators") {
				t.Errorf("profilePath(%q) error: got %q, want it to name the separator rule", profile, err)
			}
			err = m.Ensure(context.Background(), profile, testData(t))
			if err == nil || !strings.Contains(err.Error(), "ensure cluster kubedrill: profile ") {
				t.Errorf("Ensure(%q): got %v, want wrapped profile error", profile, err)
			}
		})
	}
	path, err := m.profilePath("base")
	if err != nil || path != filepath.Join(dir, "base.yaml") {
		t.Errorf("profilePath(base): got (%q, %v), want (%q, nil)", path, err, filepath.Join(dir, "base.yaml"))
	}
}

func TestEnsurePropagatesProviderErrors(t *testing.T) {
	boom := errors.New("docker down")
	tests := []struct {
		name     string
		arrange  func(t *testing.T, fp *fakeProvider, kubeconfig string)
		call     func(m *Manager) error
		want     string
		wantBoom bool
	}{
		{
			name:     "create error",
			arrange:  func(t *testing.T, fp *fakeProvider, _ string) { fp.createErr = boom },
			call:     func(m *Manager) error { return m.Ensure(context.Background(), "base", testData(t)) },
			want:     "ensure cluster kubedrill: create from profile ",
			wantBoom: true,
		},
		{
			name:     "delete error",
			arrange:  func(t *testing.T, fp *fakeProvider, _ string) { fp.deleteErr = boom },
			call:     func(m *Manager) error { return m.Delete(context.Background()) },
			want:     "delete cluster kubedrill: docker down",
			wantBoom: true,
		},
		{
			name: "malformed kubeconfig",
			arrange: func(t *testing.T, fp *fakeProvider, kubeconfig string) {
				fp.clusters = []string{Name}
				if err := os.WriteFile(kubeconfig, []byte("contexts: [unclosed\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			call: func(m *Manager) error { return m.Ensure(context.Background(), "base", testData(t)) },
			want: "ensure cluster kubedrill: load kubeconfig ",
		},
		{
			name:     "list error",
			arrange:  func(t *testing.T, fp *fakeProvider, _ string) { fp.listErr = boom },
			call:     func(m *Manager) error { return m.Ensure(context.Background(), "base", testData(t)) },
			want:     "ensure cluster kubedrill: list kind clusters: docker ps: docker down",
			wantBoom: true,
		},
		{
			name: "cancelled context before create",
			call: func(m *Manager) error {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return m.Ensure(ctx, "base", testData(t))
			},
			want: "ensure cluster kubedrill: list kind clusters: context canceled",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeProfile(t, dir, "base.yaml")
			kubeconfig := filepath.Join(dir, "kubeconfig")
			t.Setenv("KUBECONFIG", kubeconfig)
			fp := &fakeProvider{}
			m := New(dir, WithStateDir(testStateDir(t)))
			m.provider = fp
			m.run = dockerFor(fp, nil).run
			if tc.arrange != nil {
				tc.arrange(t, fp, kubeconfig)
			}
			err := tc.call(m)
			if err == nil {
				t.Fatalf("got nil error, want %q", tc.want)
			}
			if !strings.HasPrefix(err.Error(), tc.want) {
				t.Errorf("error: got %q, want prefix %q", err.Error(), tc.want)
			}
			if tc.wantBoom && !errors.Is(err, boom) {
				t.Errorf("error %v does not wrap the provider error", err)
			}
			if tc.name == "malformed kubeconfig" && (len(fp.created) != 0 || len(fp.exported) != 0) {
				t.Errorf("malformed kubeconfig: created %v exported %v, want no provider writes", fp.created, fp.exported)
			}
		})
	}
}

func TestWriteInternalKubeconfig(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "base.yaml")
	m := New(dir, WithStateDir(testStateDir(t)))
	m.provider = &fakeProvider{kubeconfig: "apiVersion: v1\nkind: Config\nclusters:\n- name: kind-kubedrill\n  cluster:\n    server: https://kubedrill-control-plane:6443\n"}
	m.run = (&fakeDocker{}).run

	dst := filepath.Join(dir, "ws", "kubeconfig")
	if err := m.WriteInternalKubeconfig(context.Background(), dst); err != nil {
		t.Fatalf("WriteInternalKubeconfig: got error %v, want nil", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read %s: %v", dst, err)
	}
	if !strings.Contains(string(got), "kubedrill-control-plane:6443") {
		t.Errorf("kubeconfig content: got %q, want the internal server address", got)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode: got %o, want 600", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(dst))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("dir mode: got %o, want 700", dirInfo.Mode().Perm())
	}

	m.provider = &fakeProvider{kubeconfigErr: errors.New("boom")}
	m.run = (&fakeDocker{}).run
	if err := m.WriteInternalKubeconfig(context.Background(), dst); err == nil {
		t.Errorf("WriteInternalKubeconfig with provider error: got nil, want error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.provider = &fakeProvider{kubeconfig: "x"}
	m.run = (&fakeDocker{}).run
	if err := m.WriteInternalKubeconfig(ctx, dst); !errors.Is(err, context.Canceled) {
		t.Errorf("WriteInternalKubeconfig with canceled ctx: got %v, want context.Canceled", err)
	}
}

func TestWriteInternalKubeconfigErrors(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "base.yaml")
	t.Run("provider error", func(t *testing.T) {
		boom := errors.New("boom")
		m := New(dir, WithStateDir(testStateDir(t)))
		m.provider = &fakeProvider{kubeconfigErr: boom}
		m.run = (&fakeDocker{}).run
		dst := filepath.Join(dir, "provider", "kubeconfig")
		err := m.WriteInternalKubeconfig(context.Background(), dst)
		if want := "write internal kubeconfig for kubedrill: boom"; err == nil || err.Error() != want {
			t.Errorf("error: got %v, want %q", err, want)
		}
		if !errors.Is(err, boom) {
			t.Errorf("error %v does not wrap the provider error", err)
		}
		if _, statErr := os.Stat(filepath.Dir(dst)); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("directory %s after a provider error: stat err %v, want not exist", filepath.Dir(dst), statErr)
		}
	})
	t.Run("destination under a regular file", func(t *testing.T) {
		blocker := filepath.Join(dir, "file")
		if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		m := New(dir, WithStateDir(testStateDir(t)))
		m.provider = &fakeProvider{kubeconfig: "apiVersion: v1\n"}
		m.run = (&fakeDocker{}).run
		dst := filepath.Join(blocker, "kubeconfig")
		err := m.WriteInternalKubeconfig(context.Background(), dst)
		if err == nil {
			t.Fatal("WriteInternalKubeconfig under a regular file: got nil, want error")
		}
		if want := "write internal kubeconfig for kubedrill: "; !strings.HasPrefix(err.Error(), want) {
			t.Errorf("error: got %q, want prefix %q", err, want)
		}
		if _, statErr := os.Stat(dst); statErr == nil {
			t.Errorf("%s exists, want nothing created", dst)
		}
		got, readErr := os.ReadFile(blocker)
		if readErr != nil || string(got) != "x" {
			t.Errorf("blocking file: got %q err %v, want unchanged content x", got, readErr)
		}
	})
}

func TestWriterLoggerErrorfAndEnabled(t *testing.T) {
	var out strings.Builder
	l := newWriterLogger(&out)
	l.Errorf("create %s: %d nodes \U0001F4A5 failed", "kubedrill", 2)
	l.Errorf("   ")
	if want := "error: create kubedrill: 2 nodes failed\n"; out.String() != want {
		t.Errorf("Errorf output: got %q, want %q", out.String(), want)
	}
	if !l.V(0).Enabled() {
		t.Error("V(0).Enabled(): got false, want true")
	}
	if l.V(1).Enabled() {
		t.Error("V(1).Enabled(): got true, want false (NoopInfoLogger)")
	}
	if _, ok := l.V(1).(kindlog.NoopInfoLogger); !ok {
		t.Errorf("V(1): got %T, want kindlog.NoopInfoLogger", l.V(1))
	}
}

func TestCreated(t *testing.T) {
	m := New(t.TempDir(), WithStateDir(testStateDir(t)))
	fd := &fakeDocker{respond: func([]string) ([]byte, error) { return []byte("2026-10-20T08:15:30.123456789Z\n"), nil }}
	m.run = fd.run
	got, err := m.Created(context.Background())
	if err != nil || !got.Equal(time.Date(2026, 10, 20, 8, 15, 30, 123456789, time.UTC)) {
		t.Errorf("Created: got %v, %v", got, err)
	}
	if want := (dockerCall{"docker", "inspect", "-f", "{{.Created}}", "kubedrill-control-plane"}); !reflect.DeepEqual(fd.calls[0], want) {
		t.Errorf("docker call: got %v, want %v", fd.calls[0], want)
	}
	fd.respond = func([]string) ([]byte, error) { return []byte("yesterday"), nil }
	if _, err := m.Created(context.Background()); err == nil {
		t.Errorf("Created with garbage: got nil error")
	}
	fd.respond = func([]string) ([]byte, error) { return nil, errors.New("no such object") }
	if _, err := m.Created(context.Background()); err == nil || !strings.Contains(err.Error(), "inspect kubedrill-control-plane") {
		t.Errorf("Created without a cluster: got %v", err)
	}
}

func TestDeleteRemovesAddonContainers(t *testing.T) {
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "kubeconfig"))
	m := New(t.TempDir(), WithStateDir(testStateDir(t)))
	m.provider = &fakeProvider{}
	fd := &fakeDocker{respond: func(args []string) ([]byte, error) {
		if args[0] == "ps" {
			return []byte("abc123\ndef456\n"), nil
		}
		return nil, nil
	}}
	m.run = fd.run
	if err := m.Delete(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []dockerCall{
		{"docker", "ps", "-aq", "--filter", "label=app.kubernetes.io/managed-by=kubedrill"},
		{"docker", "rm", "-f", "abc123", "def456"},
	}
	if !reflect.DeepEqual(fd.calls, want) {
		t.Errorf("docker calls: got %v, want %v", fd.calls, want)
	}
	fd.calls = nil
	fd.respond = nil
	if err := m.Delete(context.Background()); err != nil || len(fd.calls) != 1 {
		t.Errorf("Delete without addon containers: got %v, calls %v, want only the listing", err, fd.calls)
	}
}

func TestDeleteRemovesClusterRecords(t *testing.T) {
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "kubeconfig"))
	state := testStateDir(t)
	m := New(t.TempDir(), WithStateDir(state))
	m.provider = &fakeProvider{}
	m.run = (&fakeDocker{}).run
	records := []string{filepath.Join(state, auditPolicyHashName), InternalKubeconfigPathIn(state), ScriptKubeconfigPathIn(state)}
	kept := filepath.Join(state, "faults.json")
	for _, f := range append(records, kept) {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Delete(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, f := range records {
		if _, err := os.Stat(f); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s after Delete: err %v, want removed", filepath.Base(f), err)
		}
	}
	if _, err := os.Stat(kept); err != nil {
		t.Errorf("faults.json after Delete: %v, want kept for the fault manager", err)
	}
}

func TestEnsureTreatsConcurrentCreateAsExisting(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "base.yaml")
	t.Setenv("KUBECONFIG", filepath.Join(dir, "kubeconfig"))
	fp := &fakeProvider{createErr: errors.New(`node(s) already exist for a cluster with the name "kubedrill"`)}
	m := New(dir, WithStateDir(testStateDir(t)))
	m.provider = fp
	fd := dockerFor(fp, nil)
	m.run = fd.run
	if err := m.Ensure(context.Background(), "base", testData(t)); err != nil {
		t.Fatalf("Ensure losing the create race: got %v, want nil", err)
	}
	if len(fp.exported) != 1 {
		t.Errorf("Ensure losing the race: exported %d times, want 1 (context missing)", len(fp.exported))
	}
	var chmods []string
	for _, c := range fd.calls {
		chmods = append(chmods, strings.Join(c, " "))
	}
	joined := strings.Join(chmods, "\n")
	if !strings.Contains(joined, "docker exec kubedrill-control-plane chmod 0755 /var/log/kubernetes/audit") || !strings.Contains(joined, "chmod 0644 /var/log/kubernetes/audit/audit.log") {
		t.Errorf("audit log chmod: got %q", joined)
	}
	fp.createErr = errors.New("boom")
	fp.clusters = nil
	if err := m.Ensure(context.Background(), "base", testData(t)); err == nil {
		t.Errorf("Ensure with another create error: got nil")
	}
}

func TestEnsureIgnoresChmodFailure(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "base.yaml")
	t.Setenv("KUBECONFIG", filepath.Join(dir, "kubeconfig"))
	m := New(dir, WithStateDir(testStateDir(t)))
	m.provider = &fakeProvider{}
	m.run = dockerFor(&fakeProvider{}, func([]string) ([]byte, error) { return nil, errors.New("exec failed") }).run
	if err := m.Ensure(context.Background(), "base", testData(t)); err != nil {
		t.Errorf("Ensure with a failing chmod: got %v, want nil (best effort)", err)
	}
}

func countNodeQueries(calls []dockerCall) int {
	n := 0
	for _, c := range calls {
		if isNodesQuery(c[1:]) {
			n++
		}
	}
	return n
}

func TestEnsureHonorsContext(t *testing.T) {
	tests := []struct {
		name        string
		createErr   error
		cancelOn    string
		nodes       func(ctx context.Context) ([]byte, error)
		wantPrefix  string
		wantQueries int
		wantRecord  bool
	}{
		{name: "cancel while kind creates", cancelOn: "create", wantPrefix: "ensure cluster kubedrill: context canceled", wantRecord: true},
		{name: "cancel during a lost create race", createErr: errors.New(`node(s) already exist for a cluster with the name "kubedrill"`), cancelOn: "create", wantPrefix: "ensure cluster kubedrill: context canceled"},
		{name: "cancel during the readiness wait", cancelOn: "nodes", nodes: func(ctx context.Context) ([]byte, error) {
			return []byte("kubedrill-control-plane=False\n"), nil
		}, wantPrefix: "ensure cluster kubedrill: wait for nodes: context canceled", wantQueries: 1, wantRecord: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeProfile(t, dir, "base.yaml")
			t.Setenv("KUBECONFIG", filepath.Join(dir, "kubeconfig"))
			state := t.TempDir()
			t.Setenv("XDG_STATE_HOME", state)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fp := &fakeProvider{createErr: tc.createErr}
			if tc.cancelOn == "create" {
				fp.onCreate = cancel
			}
			m := New(dir, WithStateDir(testStateDir(t)))
			m.provider = fp
			fd := dockerFor(fp, nil)
			if tc.nodes != nil {
				fd.nodes = func(qctx context.Context) ([]byte, error) {
					if tc.cancelOn == "nodes" {
						cancel()
					}
					return tc.nodes(qctx)
				}
			}
			m.run = fd.run
			m.readyPoll = time.Millisecond
			err := m.Ensure(ctx, "base", testData(t))
			if err == nil || !strings.HasPrefix(err.Error(), tc.wantPrefix) {
				t.Fatalf("Ensure: got %v, want prefix %q", err, tc.wantPrefix)
			}
			if !errors.Is(err, context.Canceled) {
				t.Errorf("Ensure error %v does not wrap context.Canceled", err)
			}
			if got := countNodeQueries(fd.calls); got != tc.wantQueries {
				t.Errorf("readiness queries: got %d, want %d", got, tc.wantQueries)
			}
			_, statErr := os.Stat(filepath.Join(state, "kubedrill", Name, auditPolicyHashName))
			if got := statErr == nil; got != tc.wantRecord {
				t.Errorf("audit policy hash recorded: got %v, want %v (a created cluster is recorded even when cancelled)", got, tc.wantRecord)
			}
		})
	}
}

func TestEnsureWaitsForNodesAfterLosingTheCreateRace(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "base.yaml")
	t.Setenv("KUBECONFIG", filepath.Join(dir, "kubeconfig"))
	tests := []struct {
		name      string
		answers   []string
		answerErr error
		timeout   time.Duration
		wantErr   string
		wantMin   int
	}{
		{name: "nodes join over time", answers: []string{"", "kubedrill-control-plane=True\n", "kubedrill-control-plane=True\nkubedrill-worker=True\nkubedrill-worker2=True\n"}, timeout: time.Minute, wantMin: 3},
		{name: "a node never becomes ready", answers: []string{"kubedrill-control-plane=True\nkubedrill-worker=True\nkubedrill-worker2=False\n"}, timeout: 50 * time.Millisecond, wantErr: "ensure cluster kubedrill: wait for nodes: not ready after 50ms: kubedrill-worker2=False", wantMin: 1},
		{name: "the control plane never answers", answerErr: exitErrorWith(t, "Error response from daemon: No such container: kubedrill-control-plane"), timeout: 50 * time.Millisecond, wantErr: "ensure cluster kubedrill: wait for nodes: not ready after 50ms: docker exec kubedrill-control-plane: Error response from daemon: No such container", wantMin: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fp := &fakeProvider{createErr: errors.New(`node(s) already exist for a cluster with the name "kubedrill"`)}
			m := New(dir, WithStateDir(testStateDir(t)))
			m.provider = fp
			fd := dockerFor(fp, nil)
			i := 0
			fd.nodes = func(context.Context) ([]byte, error) {
				if tc.answerErr != nil {
					return nil, tc.answerErr
				}
				out := tc.answers[min(i, len(tc.answers)-1)]
				i++
				return []byte(out), nil
			}
			m.run = fd.run
			m.readyPoll = time.Millisecond
			m.readyTimeout = tc.timeout
			err := m.Ensure(context.Background(), "base", testData(t))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Ensure: got %v, want nil", err)
				}
			} else if err == nil || !strings.HasPrefix(err.Error(), tc.wantErr) {
				t.Fatalf("Ensure: got %v, want prefix %q", err, tc.wantErr)
			}
			if got := countNodeQueries(fd.calls); got < tc.wantMin {
				t.Errorf("readiness queries: got %d, want at least %d", got, tc.wantMin)
			}
		})
	}
}

func TestDeleteHonorsContextAfterKind(t *testing.T) {
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "kubeconfig"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := New(t.TempDir(), WithStateDir(testStateDir(t)))
	m.provider = &fakeProvider{onDelete: cancel}
	fd := &fakeDocker{}
	m.run = fd.run
	err := m.Delete(ctx)
	if !errors.Is(err, context.Canceled) || !strings.HasPrefix(err.Error(), "delete cluster kubedrill: context canceled") {
		t.Fatalf("Delete cancelled during kind delete: got %v, want context.Canceled", err)
	}
	if len(fd.calls) != 0 {
		t.Errorf("docker calls after cancel: got %v, want none", fd.calls)
	}
}

func TestEnsureAddonsHonorsContext(t *testing.T) {
	m := addonFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	k := &fakeApplier{}
	err := m.EnsureAddons(ctx, k, []string{"alpha"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("EnsureAddons with a cancelled context: got %v, want context.Canceled", err)
	}
	if len(k.calls) != 0 {
		t.Errorf("applies after cancel: got %d, want none", len(k.calls))
	}
}

func TestDockerError(t *testing.T) {
	plain := errors.New("signal: killed")
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "stderr is quoted", err: exitErrorWith(t, "  Error response from daemon: boom\n"), want: "docker ps: Error response from daemon: boom: exit status 1"},
		{name: "empty stderr", err: exitErrorWith(t, " \n"), want: "docker ps: exit status 1"},
		{name: "not an exit error", err: plain, want: "docker ps: signal: killed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := dockerError("docker ps", tc.err)
			if got.Error() != tc.want {
				t.Errorf("dockerError: got %q, want %q", got, tc.want)
			}
			if !errors.Is(got, tc.err) {
				t.Errorf("dockerError: got %v, want it to wrap %v", got, tc.err)
			}
		})
	}
}

func exitErrorWith(t *testing.T, stderr string) error {
	t.Helper()
	return exitError(t, "1", stderr)
}

func TestEnsureExistingClusterWaitsForNodes(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "base.yaml")
	t.Setenv("KUBECONFIG", filepath.Join(dir, "kubeconfig"))
	allReady := "kubedrill-control-plane=True\nkubedrill-worker=True\nkubedrill-worker2=True\n"
	tests := []struct {
		name        string
		inspect     string
		inspectErr  error
		answers     []string
		timeout     time.Duration
		wantErr     string
		wantQueries int
		atLeast     bool
	}{
		{name: "nodes still joining", inspect: "/kubedrill-control-plane=true\n/kubedrill-worker=true\n/kubedrill-worker2=true\n", answers: []string{"", "kubedrill-control-plane=True\n", allReady}, timeout: time.Minute, wantQueries: 3},
		{name: "a node never becomes ready", answers: []string{"kubedrill-control-plane=True\nkubedrill-worker=False\nkubedrill-worker2=True\n"}, timeout: 50 * time.Millisecond, wantErr: "ensure cluster kubedrill: wait for nodes: not ready after 50ms: kubedrill-worker=False", wantQueries: 1, atLeast: true},
		{name: "stopped node containers fail fast", inspect: "/kubedrill-control-plane=false\n/kubedrill-worker=true\n/kubedrill-worker2=false\n", answers: []string{allReady}, timeout: time.Minute, wantErr: "ensure cluster kubedrill: node containers stopped: kubedrill-control-plane, kubedrill-worker2; start them with docker start kubedrill-control-plane kubedrill-worker2, or recreate the cluster with kubedrill down --yes && kubedrill up", wantQueries: 0},
		{name: "a removed node container fails fast", inspect: "/kubedrill-control-plane=true\n/kubedrill-worker2=true\n", inspectErr: exitErrorWith(t, "Error: No such object: kubedrill-worker"), answers: []string{allReady}, timeout: time.Minute, wantErr: "ensure cluster kubedrill: node containers missing: kubedrill-worker; the cluster is incomplete (a docker rm removed them), recreate it with kubedrill down --yes && kubedrill up", wantQueries: 0},
		{name: "a failing inspect leaves the verdict to the wait", inspectErr: errors.New("inspect failed"), answers: []string{allReady}, timeout: time.Minute, wantQueries: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fp := &fakeProvider{clusters: []string{Name}}
			m := New(dir, WithStateDir(testStateDir(t)))
			m.provider = fp
			fd := dockerFor(fp, func(args []string) ([]byte, error) {
				if args[0] == "inspect" {
					return []byte(tc.inspect), tc.inspectErr
				}
				return nil, nil
			})
			i := 0
			fd.nodes = func(context.Context) ([]byte, error) {
				out := tc.answers[min(i, len(tc.answers)-1)]
				i++
				return []byte(out), nil
			}
			m.run = fd.run
			m.readyPoll = time.Millisecond
			m.readyTimeout = tc.timeout
			err := m.Ensure(context.Background(), "base", testData(t))
			gotErr := ""
			if err != nil {
				gotErr = err.Error()
			}
			if tc.wantErr == "" && gotErr != "" || tc.wantErr != "" && !strings.HasPrefix(gotErr, tc.wantErr) {
				t.Fatalf("Ensure: got %q, want prefix %q", gotErr, tc.wantErr)
			}
			if len(fp.created) != 0 {
				t.Errorf("Ensure on an existing cluster: created %v, want none", fp.created)
			}
			if got := countNodeQueries(fd.calls); got != tc.wantQueries && !(tc.atLeast && got > tc.wantQueries) {
				t.Errorf("readiness queries: got %d, want %d (at least: %v)", got, tc.wantQueries, tc.atLeast)
			}
		})
	}
}

func TestPullImages(t *testing.T) {
	const plain = "registry.k8s.io/pause:3.10"
	const digestRef = "docker.io/library/busybox@sha256:bdf57e528e45e4433820e045b29b4597825a1c9e38353532d90a01445013f82e"
	const tagRef = "docker.io/library/busybox:1.37.0"
	const tagCmd = " ctr --namespace k8s.io images tag --force " + digestRef + " " + tagRef
	tests := []struct {
		name    string
		fail    string
		listErr bool
		have    string
		wantErr string
		want    func(node string) []string
	}{
		{name: "every node pulls and tags", want: func(node string) []string {
			return []string{"docker exec " + node + " crictl pull " + DebugImage, "docker exec " + node + tagCmd, "docker exec " + node + " crictl pull " + plain}
		}},
		{name: "one node fails", fail: "kubedrill-worker2", wantErr: "pull " + DebugImage + " on kubedrill-worker2: docker exec kubedrill-worker2: no route to registry", want: func(node string) []string {
			if node == "kubedrill-worker2" {
				return []string{"docker exec " + node + " crictl pull " + DebugImage, "docker exec " + node + " crictl pull " + plain}
			}
			return []string{"docker exec " + node + " crictl pull " + DebugImage, "docker exec " + node + tagCmd, "docker exec " + node + " crictl pull " + plain}
		}},
		{name: "images already present are skipped", have: digestRef + "\n" + tagRef + "\n" + plain + "\n", want: func(string) []string { return nil }},
		{name: "a digest without its tag is only tagged", have: digestRef + "\n", want: func(node string) []string {
			return []string{"docker exec " + node + tagCmd, "docker exec " + node + " crictl pull " + plain}
		}},
		{name: "a failed listing pulls everything", listErr: true, have: digestRef + "\n" + tagRef + "\n" + plain + "\n", want: func(node string) []string {
			return []string{"docker exec " + node + " crictl pull " + DebugImage, "docker exec " + node + tagCmd, "docker exec " + node + " crictl pull " + plain}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := New(t.TempDir(), WithStateDir(testStateDir(t)))
			fd := &fakeDocker{respond: func(args []string) ([]byte, error) {
				if len(args) > 6 && args[2] == "ctr" && args[6] == "ls" {
					if tc.listErr {
						return nil, exitErrorWith(t, "ctr: not found")
					}
					return []byte(tc.have), nil
				}
				if args[1] == tc.fail && args[2] == "crictl" && args[4] == DebugImage {
					return nil, exitErrorWith(t, "no route to registry")
				}
				return nil, nil
			}}
			m.run = fd.run
			err := m.PullImages(context.Background(), []string{DebugImage, plain})
			gotErr := ""
			if err != nil {
				gotErr = err.Error()
			}
			if tc.wantErr == "" && gotErr != "" || tc.wantErr != "" && !strings.HasPrefix(gotErr, tc.wantErr) {
				t.Fatalf("PullImages: got %q, want prefix %q", gotErr, tc.wantErr)
			}
			var got []string
			for _, c := range fd.calls {
				got = append(got, strings.Join(c, " "))
			}
			var want []string
			for _, node := range []string{"kubedrill-control-plane", "kubedrill-worker", "kubedrill-worker2"} {
				want = append(want, "docker exec "+node+" ctr --namespace k8s.io images ls --quiet")
				want = append(want, tc.want(node)...)
			}
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("calls:\n got %q\nwant %q", got, want)
			}
		})
	}
}

func TestStoredRef(t *testing.T) {
	const d = "@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10"
	tests := []struct {
		image, want string
		ok          bool
	}{
		{"nginx:1.27", "docker.io/library/nginx:1.27", true},
		{"registry.k8s.io/pause:3.10", "registry.k8s.io/pause:3.10", true},
		{"localhost:5000/app:v1", "localhost:5000/app:v1", true},
		{"nginx" + d, "docker.io/library/nginx" + d, true},
		{"nginx", "", false},
		{"localhost:5000/app", "", false},
	}
	for _, tc := range tests {
		got, ok := storedRef(tc.image)
		if got != tc.want || ok != tc.ok {
			t.Errorf("storedRef(%q) = %q, %v, want %q, %v", tc.image, got, ok, tc.want, tc.ok)
		}
	}
}

func TestLocalRefs(t *testing.T) {
	const d = "@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10"
	tests := []struct {
		image, wantDigest, wantTag string
		ok                         bool
	}{
		{"nginx:1.27-alpine" + d, "docker.io/library/nginx" + d, "docker.io/library/nginx:1.27-alpine", true},
		{"curlimages/curl:8.14.1" + d, "docker.io/curlimages/curl" + d, "docker.io/curlimages/curl:8.14.1", true},
		{"index.docker.io/nginx:1" + d, "docker.io/library/nginx" + d, "docker.io/library/nginx:1", true},
		{"registry.k8s.io/pause:3.10" + d, "registry.k8s.io/pause" + d, "registry.k8s.io/pause:3.10", true},
		{"localhost:5000/app:v1" + d, "localhost:5000/app" + d, "localhost:5000/app:v1", true},
		{"localhost:5000/app" + d, "", "", false},
		{"nginx:1.27-alpine", "", "", false},
		{"nginx" + d, "", "", false},
	}
	for _, tc := range tests {
		gotDigest, gotTag, ok := localRefs(tc.image)
		if gotDigest != tc.wantDigest || gotTag != tc.wantTag || ok != tc.ok {
			t.Errorf("localRefs(%q) = %q, %q, %v, want %q, %q, %v", tc.image, gotDigest, gotTag, ok, tc.wantDigest, tc.wantTag, tc.ok)
		}
	}
}
