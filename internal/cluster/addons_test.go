package cluster

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

type fakeApplier struct {
	calls [][]string
	err   error
}

func (f *fakeApplier) ApplyCluster(_ context.Context, manifests []string) error {
	f.calls = append(f.calls, manifests)
	return f.err
}

type dockerCall []string

type fakeDocker struct {
	calls   []dockerCall
	respond func(args []string) ([]byte, error)
}

func (f *fakeDocker) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append(dockerCall{name}, args...))
	if f.respond == nil {
		return nil, nil
	}
	return f.respond(args)
}

func writeAddon(t *testing.T, root, name, spec string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, addonFile), []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	for f, content := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const pinnedImage = "example.com/ccm:v1@sha256:0000000000000000000000000000000000000000000000000000000000000001"

func addonFixture(t *testing.T) *Manager {
	t.Helper()
	root := t.TempDir()
	writeAddon(t, root, "alpha", "name: alpha\ndescription: a\nbase: true\nmanifests: [one.yaml, two.yaml]\n", map[string]string{"one.yaml": "kind: A\n", "two.yaml": "kind: B\n"})
	writeAddon(t, root, "beta", "name: beta\ndescription: b\ncontainer:\n  name: ccm\n  image: "+pinnedImage+"\n  network: kind\n  restart: unless-stopped\n  volumes: [/var/run/docker.sock:/var/run/docker.sock]\n", nil)
	if err := os.MkdirAll(filepath.Join(root, "not-an-addon"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeAddon(t, root, "badname", "name: other\nmanifests: [x.yaml]\n", map[string]string{"x.yaml": "kind: X\n"})
	writeAddon(t, root, "unpinned", "name: unpinned\ncontainer:\n  name: u\n  image: example.com/u:v1\n", nil)
	writeAddon(t, root, "escape", "name: escape\nmanifests: [../alpha/one.yaml]\n", nil)
	return New(filepath.Join(t.TempDir(), "profiles"), WithAddonsDir(root))
}

func TestAddonsLists(t *testing.T) {
	m := addonFixture(t)
	want := []string{"alpha", "badname", "beta", "escape", "unpinned"}
	if got := m.Addons(); !reflect.DeepEqual(got, want) {
		t.Errorf("Addons: got %v, want %v", got, want)
	}
	if got := m.BaseAddons(); !reflect.DeepEqual(got, []string{"alpha"}) {
		t.Errorf("BaseAddons: got %v, want [alpha]", got)
	}
	if got := New(filepath.Join(t.TempDir(), "profiles")).Addons(); got != nil {
		t.Errorf("Addons without a directory: got %v, want nil", got)
	}
}

func TestEnsureAddonsValidation(t *testing.T) {
	tests := []struct {
		name  string
		names []string
		want  []string
	}{
		{name: "unknown", names: []string{"nope"}, want: []string{`addon "nope" not found`, "available: alpha, badname, beta, escape, unpinned"}},
		{name: "name mismatch", names: []string{"badname"}, want: []string{`name "other" does not match the directory`}},
		{name: "unpinned image", names: []string{"unpinned"}, want: []string{`container.image "example.com/u:v1" is not pinned by digest`}},
		{name: "path escape", names: []string{"escape"}, want: []string{`manifest "../alpha/one.yaml" must be a file name`}},
		{name: "invalid name", names: []string{"../x"}, want: []string{`addon "../x": name must be`}},
		{name: "every problem at once", names: []string{"nope", "badname", "alpha"}, want: []string{`"nope" not found`, `"other" does not match`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := addonFixture(t)
			fd := &fakeDocker{}
			m.run = fd.run
			k := &fakeApplier{}
			err := m.EnsureAddons(context.Background(), k, tc.names)
			if err == nil {
				t.Fatalf("EnsureAddons: got nil error, want %v", tc.want)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("EnsureAddons error: got %q, want it to contain %q", err, w)
				}
			}
			if len(k.calls) != 0 || len(fd.calls) != 0 {
				t.Errorf("EnsureAddons applied %d manifests and ran %v despite invalid input", len(k.calls), fd.calls)
			}
		})
	}
}

func TestEnsureAddonsAppliesManifests(t *testing.T) {
	m := addonFixture(t)
	k := &fakeApplier{}
	if err := m.EnsureAddons(context.Background(), k, []string{"alpha"}); err != nil {
		t.Fatalf("EnsureAddons: %v", err)
	}
	if want := [][]string{{"kind: A\n", "kind: B\n"}}; !reflect.DeepEqual(k.calls, want) {
		t.Errorf("ApplyCluster calls: got %q, want %q", k.calls, want)
	}
	boom := errors.New("apply failed")
	k = &fakeApplier{err: boom}
	err := m.EnsureAddons(context.Background(), k, []string{"alpha"})
	if !errors.Is(err, boom) || !strings.HasPrefix(err.Error(), "ensure addon alpha: ") {
		t.Errorf("EnsureAddons with failing applier: got %v, want wrapped %v", err, boom)
	}
}

func TestEnsureAddonsContainer(t *testing.T) {
	spec := &addonContainer{Name: "ccm", Image: pinnedImage, Network: "kind", Restart: "unless-stopped", Volumes: []string{"/var/run/docker.sock:/var/run/docker.sock"}}
	hash := spec.specHash()
	runArgs := dockerCall{"docker", "run", "-d", "--name", "ccm", "--label", "app.kubernetes.io/managed-by=kubedrill", "--label", "kubedrill.io/spec-hash=" + hash, "--network", "kind", "--restart", "unless-stopped", "-v", "/var/run/docker.sock:/var/run/docker.sock", pinnedImage}
	inspect := dockerCall{"docker", "inspect", "-f", `{{index .Config.Labels "kubedrill.io/spec-hash"}}|{{.State.Running}}`, "ccm"}
	tests := []struct {
		name    string
		inspect func() ([]byte, error)
		want    []dockerCall
		wantErr string
	}{
		{name: "running with the declared spec is left alone", inspect: func() ([]byte, error) { return []byte(hash + "|true\n"), nil }, want: []dockerCall{inspect}},
		{name: "stopped is started", inspect: func() ([]byte, error) { return []byte(hash + "|false\n"), nil }, want: []dockerCall{inspect, {"docker", "start", "ccm"}}},
		{name: "other spec is replaced", inspect: func() ([]byte, error) { return []byte("0123456789ab|true\n"), nil }, want: []dockerCall{inspect, {"docker", "rm", "-f", "ccm"}, runArgs}},
		{name: "unlabeled container is replaced", inspect: func() ([]byte, error) { return []byte("|true\n"), nil }, want: []dockerCall{inspect, {"docker", "rm", "-f", "ccm"}, runArgs}},
		{name: "missing is created", inspect: func() ([]byte, error) { return nil, exitError(t, "1", "Error: No such object: ccm") }, want: []dockerCall{inspect, runArgs}},
		{name: "daemon error is reported", inspect: func() ([]byte, error) { return nil, exitError(t, "1", "Cannot connect to the Docker daemon") }, want: []dockerCall{inspect}, wantErr: "ensure addon beta: inspect container ccm: "},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := addonFixture(t)
			fd := &fakeDocker{respond: func(args []string) ([]byte, error) {
				if args[0] == "inspect" {
					return tc.inspect()
				}
				return nil, nil
			}}
			m.run = fd.run
			err := m.EnsureAddons(context.Background(), nil, []string{"beta"})
			if tc.wantErr != "" {
				if err == nil || !strings.HasPrefix(err.Error(), tc.wantErr) {
					t.Errorf("EnsureAddons: got %v, want prefix %q", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatalf("EnsureAddons: %v", err)
			}
			if !reflect.DeepEqual(fd.calls, tc.want) {
				t.Errorf("docker calls:\ngot  %v\nwant %v", fd.calls, tc.want)
			}
		})
	}
}

func TestAddonContainerLimits(t *testing.T) {
	root := t.TempDir()
	writeAddon(t, root, "ccm", "name: ccm\ncontainer:\n  name: ccm\n  image: "+pinnedImage+"\n  security_opt: [no-new-privileges]\n  memory: 256m\n  pids_limit: 256\n", nil)
	m := New(filepath.Join(t.TempDir(), "profiles"), WithAddonsDir(root))
	fd := &fakeDocker{respond: func(args []string) ([]byte, error) {
		if args[0] == "inspect" {
			return nil, exitError(t, "1", "Error: No such object: ccm")
		}
		return nil, nil
	}}
	m.run = fd.run
	if err := m.EnsureAddons(context.Background(), nil, []string{"ccm"}); err != nil {
		t.Fatal(err)
	}
	run := strings.Join(fd.calls[len(fd.calls)-1], " ")
	for _, want := range []string{"--security-opt no-new-privileges", "--memory 256m", "--pids-limit 256"} {
		if !strings.Contains(run, want) {
			t.Errorf("docker run: got %q, want %q", run, want)
		}
	}
	a := &addonContainer{Name: "ccm", Image: pinnedImage}
	b := &addonContainer{Name: "ccm", Image: pinnedImage, Memory: "256m"}
	if a.specHash() == b.specHash() {
		t.Errorf("specHash: got the same hash for different limits")
	}
}

func TestEnsureAddonsWaitsForDeployments(t *testing.T) {
	root := t.TempDir()
	manifest := "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: gw\n---\napiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: controller\n  namespace: gw\n--- \napiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: plain\n"
	writeAddon(t, root, "gw", "name: gw\nmanifests: [m.yaml]\n", map[string]string{"m.yaml": manifest})
	var out strings.Builder
	m := New(filepath.Join(t.TempDir(), "profiles"), WithAddonsDir(root), WithOutput(&out))
	fd := &fakeDocker{}
	m.run = fd.run
	if err := m.EnsureAddons(context.Background(), &fakeApplier{}, []string{"gw"}); err != nil {
		t.Fatal(err)
	}
	if len(fd.calls) != 2 {
		t.Fatalf("docker calls: got %v, want one kubectl wait per deployment", fd.calls)
	}
	first := strings.Join(fd.calls[0], " ")
	if !strings.HasPrefix(first, "docker exec kubedrill-control-plane kubectl --kubeconfig /etc/kubernetes/admin.conf wait --for=condition=Available -n gw deployment/controller --timeout=") {
		t.Errorf("wait call: got %q", first)
	}
	if !strings.Contains(strings.Join(fd.calls[1], " "), "-n default deployment/plain") {
		t.Errorf("wait call without namespace: got %v", fd.calls[1])
	}
	got := out.String()
	for _, want := range []string{"waiting for gw: gw/controller...\n", "waiting for gw: default/plain...\n", "gw ready ("} {
		if !strings.Contains(got, want) {
			t.Errorf("output: got %q, want %q", got, want)
		}
	}
	fd.respond = func(args []string) ([]byte, error) {
		return nil, exitError(t, "1", "error: timed out waiting for the condition")
	}
	if err := m.EnsureAddons(context.Background(), &fakeApplier{}, []string{"gw"}); err == nil || !strings.Contains(err.Error(), "deployment gw/controller not available") || !strings.Contains(err.Error(), "timed out waiting") {
		t.Errorf("EnsureAddons with a stuck deployment: got %v", err)
	}
}

var imageLine = regexp.MustCompile(`(?m)^\s*-?\s*image:\s*(\S+)\s*$`)

func TestRepositoryAddonsArePinned(t *testing.T) {
	m := New(filepath.Join("..", "..", "cluster", "profiles"))
	names := m.Addons()
	if want := []string{"gateway", "kwok", "metrics-server"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("Addons: got %v, want %v", names, want)
	}
	if got := m.BaseAddons(); !reflect.DeepEqual(got, []string{"metrics-server"}) {
		t.Errorf("BaseAddons: got %v, want [metrics-server]", got)
	}
	for _, n := range names {
		a, err := m.loadAddon(n)
		if err != nil {
			t.Errorf("load addon %s: %v", n, err)
			continue
		}
		for _, f := range a.Manifests {
			data, err := os.ReadFile(filepath.Join(m.addonsDir, n, f))
			if err != nil {
				t.Errorf("addon %s manifest %s: %v", n, f, err)
				continue
			}
			for _, match := range imageLine.FindAllStringSubmatch(string(data), -1) {
				img := match[1]
				if strings.Contains(img, "{{") {
					continue
				}
				if !strings.Contains(img, "@sha256:") {
					t.Errorf("addon %s manifest %s: image %s is not pinned by digest", n, f, img)
				}
			}
		}
	}
	a, err := m.loadAddon("metrics-server")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(m.addonsDir, "metrics-server", a.Manifests[0]))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "- --kubelet-insecure-tls") {
		t.Errorf("metrics-server manifest lacks --kubelet-insecure-tls")
	}
}
