package workstation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

type call struct {
	name string
	args []string
	cmd  *exec.Cmd
}

// fakeRunner records every command and runs, in place of the real program,
// the shell script registered for the longest matching argv prefix (default
// "true"; "false" to fail).
type fakeRunner struct {
	calls   []call
	scripts map[string]string
}

func (f *fakeRunner) run(ctx context.Context, name string, args ...string) *exec.Cmd {
	key := name + " " + strings.Join(args, " ")
	script := "true"
	best := -1
	for prefix, s := range f.scripts {
		if strings.HasPrefix(key, prefix) && len(prefix) > best {
			script, best = s, len(prefix)
		}
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", script)
	f.calls = append(f.calls, call{name: name, args: args, cmd: cmd})
	return cmd
}

func (f *fakeRunner) joined() []string {
	var out []string
	for _, c := range f.calls {
		out = append(out, c.name+" "+strings.Join(c.args, " "))
	}
	return out
}

func failing(prefixes ...string) map[string]string {
	m := map[string]string{}
	for _, p := range prefixes {
		m[p] = "false"
	}
	return m
}

func writeRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	ws := filepath.Join(root, "workstation")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range imageFiles {
		if err := os.WriteFile(filepath.Join(ws, name), []byte(name+" v1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{"cmd/kubedrill", "internal/cli"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func wantBinary(data, root, goarch string) string {
	sum := sha256.Sum256([]byte(root))
	return filepath.Join(data, "bin", "kubedrill-linux-"+goarch+"-"+hex.EncodeToString(sum[:])[:8])
}

func TestImageTag(t *testing.T) {
	root := writeRepo(t)
	m := New(root, t.TempDir())
	first, err := m.ImageTag()
	if err != nil {
		t.Fatalf("ImageTag: got error %v, want nil", err)
	}
	if !strings.HasPrefix(first, "kubedrill-workstation:") || len(first) != len("kubedrill-workstation:")+12 {
		t.Errorf("ImageTag: got %q, want kubedrill-workstation:<12 hex>", first)
	}
	again, err := m.ImageTag()
	if err != nil {
		t.Fatal(err)
	}
	if again != first {
		t.Errorf("ImageTag stability: got %q then %q, want equal", first, again)
	}
	if err := os.WriteFile(filepath.Join(root, "workstation", "versions.env"), []byte("KUBECTL_VERSION=v1.38.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := m.ImageTag()
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Errorf("ImageTag after versions.env change: got %q, want a different tag", changed)
	}
	if err := os.Remove(filepath.Join(root, "workstation", "bashrc")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ImageTag(); err == nil {
		t.Errorf("ImageTag with missing bashrc: got nil error, want error")
	}
}

func TestEnsureImage(t *testing.T) {
	root := writeRepo(t)
	tests := []struct {
		name      string
		scripts   map[string]string
		wantBuild bool
		wantErr   string
		wantMsg   string
	}{
		{name: "present", wantBuild: false},
		{name: "missing, first time", scripts: failing("docker image inspect"), wantBuild: true, wantMsg: "(first time only, a few minutes)..."},
		{name: "missing, older tag", scripts: map[string]string{"docker image inspect": "false", "docker image ls": "printf '0123456789ab\\t%s\\n' '" + time.Now().Add(-30*24*time.Hour).Format(createdAtLayout) + "'"}, wantBuild: true, wantMsg: "(sources changed)..."},
		{name: "build fails", scripts: map[string]string{"docker image inspect": "false", "docker build": "echo 'ERROR: failed to solve' >&2; false"}, wantBuild: true, wantErr: "ERROR: failed to solve"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fr := &fakeRunner{scripts: tt.scripts}
			var out bytes.Buffer
			m := New(root, t.TempDir(), WithRunner(fr.run), WithOutput(&out))
			wantTag, _ := m.ImageTag()
			tag, err := m.EnsureImage(context.Background())
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("EnsureImage: got %v, want error with %q", err, tt.wantErr)
				}
			} else if err != nil || tag != wantTag {
				t.Fatalf("EnsureImage: got %q, %v, want %q", tag, err, wantTag)
			}
			calls := fr.joined()
			if calls[0] != "docker image inspect "+wantTag {
				t.Errorf("first call: got %q, want docker image inspect", calls[0])
			}
			built := slices.Contains(calls, "docker build --progress quiet -t "+wantTag+" "+filepath.Join(root, "workstation"))
			if built != tt.wantBuild {
				t.Errorf("build call: got %v (%v), want %v", built, calls, tt.wantBuild)
			}
			if tt.wantMsg != "" && !strings.Contains(out.String(), wantTag+" "+tt.wantMsg) {
				t.Errorf("build message: got %q, want %q", out.String(), tt.wantMsg)
			}
			if tt.wantBuild && tt.wantErr == "" && !strings.Contains(out.String(), "workstation image "+wantTag+" built in ") {
				t.Errorf("done line: got %q", out.String())
			}
			if strings.Count(out.String(), "\n") > 3 {
				t.Errorf("output: got %q, want at most three lines", out.String())
			}
			for _, c := range fr.calls {
				if !slices.Contains(c.cmd.Env, "DOCKER_CLI_HINTS=false") {
					t.Errorf("%s %v: env lacks DOCKER_CLI_HINTS=false", c.name, c.args)
				}
			}
		})
	}
}

func TestShellAuditMountAndDataDirs(t *testing.T) {
	m, fr, _, data, kubeconfig := shellHarness(t, nil)
	if err := os.MkdirAll(filepath.Join(data, "shell"), 0o755); err != nil {
		t.Fatal(err)
	}
	audit := t.TempDir()
	m.auditDir = audit
	if err := m.Shell(context.Background(), ShellOptions{Kubeconfig: kubeconfig}); err != nil {
		t.Fatal(err)
	}
	run, _ := dockerRun(fr)
	argv := " " + strings.Join(run.args, " ") + " "
	for _, want := range []string{
		" --mount type=bind,source=" + resolved(t, audit) + ",target=/audit,readonly ",
		" -e KUBEDRILL_AUDIT_LOG=/audit/audit.log ",
		" -e KUBEDRILL_NO_TERM_QUERY=1 ",
	} {
		if !strings.Contains(argv, want) {
			t.Errorf("docker run argv lacks %q:\n%s", strings.TrimSpace(want), argv)
		}
	}
	for _, d := range []string{"shell", "recordings"} {
		info, err := os.Stat(filepath.Join(data, d))
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Errorf("%s dir: got %v, %v, want mode 0700", d, info, err)
		}
	}

	fr.calls = nil
	m.auditDir = filepath.Join(audit, "missing")
	if err := m.Shell(context.Background(), ShellOptions{Kubeconfig: kubeconfig}); err != nil {
		t.Fatal(err)
	}
	run, _ = dockerRun(fr)
	if argv := strings.Join(run.args, " "); strings.Contains(argv, "/audit") {
		t.Errorf("docker run with a missing audit dir mounts it: %s", argv)
	}
}

func TestEnsureBinary(t *testing.T) {
	root := writeRepo(t)
	future := time.Now().Add(24 * time.Hour)
	tests := []struct {
		name   string
		binary bool
		remove string
	}{
		{name: "missing binary"},
		{name: "binary newer than every source", binary: true},
		{name: "binary newer than every source after a source file was deleted", binary: true, remove: filepath.Join("internal", "cli", "main.go")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.remove != "" {
				if err := os.Remove(filepath.Join(root, tt.remove)); err != nil {
					t.Fatal(err)
				}
			}
			data := t.TempDir()
			fr := &fakeRunner{}
			m := New(root, t.TempDir(), WithStateDir(data), WithRunner(fr.run), WithOutput(&bytes.Buffer{}))
			want := wantBinary(data, root, m.goarch)
			if tt.binary {
				if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(want, []byte("bin"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(want, future, future); err != nil {
					t.Fatal(err)
				}
			}
			got, err := m.EnsureBinary(context.Background())
			if err != nil {
				t.Fatalf("EnsureBinary: got error %v, want nil", err)
			}
			if got != want {
				t.Errorf("EnsureBinary path: got %q, want %q", got, want)
			}
			if len(fr.calls) != 1 {
				t.Fatalf("go build calls: got %v, want exactly one (the build cache decides what is stale)", fr.joined())
			}
			c := fr.calls[0]
			wantArgs := "build -buildvcs=false -trimpath -o " + want + " ./cmd/kubedrill"
			if c.name != "go" || strings.Join(c.args, " ") != wantArgs {
				t.Errorf("go build argv: got %s %v, want go %s", c.name, c.args, wantArgs)
			}
			for _, e := range []string{"GOOS=linux", "GOARCH=" + m.goarch, "CGO_ENABLED=0"} {
				if !slices.Contains(c.cmd.Env, e) {
					t.Errorf("go build env lacks %s", e)
				}
			}
		})
	}
	t.Run("two roots yield two binaries", func(t *testing.T) {
		data := t.TempDir()
		a, _ := New(root, data, WithStateDir(data)).binaryPath()
		b, _ := New(writeRepo(t), data, WithStateDir(data)).binaryPath()
		if a == b {
			t.Errorf("binaryPath: both roots map to %q, want distinct names", a)
		}
		if !strings.HasPrefix(filepath.Base(a), "kubedrill-linux-"+runtime.GOARCH+"-") || len(filepath.Base(a)) != len("kubedrill-linux-"+runtime.GOARCH+"-")+8 {
			t.Errorf("binaryPath: got %q, want kubedrill-linux-<arch>-<8 hex>", filepath.Base(a))
		}
	})
}

func TestStateDirRequired(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	data := t.TempDir()
	m := New(writeRepo(t), data)
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"binaryPath", func() error { _, err := m.binaryPath(); return err }},
		{"checkJournalOverlap", func() error {
			return m.checkJournalOverlap(data, filepath.Join(t.TempDir(), "kubeconfig"), filepath.Join(t.TempDir(), "bin"))
		}},
	} {
		if err := tc.run(); !errors.Is(err, errNoStateDir) {
			t.Errorf("%s without WithStateDir: got %v, want %v", tc.name, err, errNoStateDir)
		}
	}
}

func ptr(t time.Time) *time.Time { return &t }

func shellHarness(t *testing.T, scripts map[string]string) (*Manager, *fakeRunner, string, string, string) {
	t.Helper()
	root := writeRepo(t)
	data := t.TempDir()
	state := t.TempDir()
	kubeconfig := filepath.Join(state, "kubeconfig-internal")
	if err := os.WriteFile(kubeconfig, []byte("apiVersion: v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRunner{scripts: scripts}
	m := New(root, data, WithStateDir(state), WithRunner(fr.run), WithOutput(&bytes.Buffer{}))
	m.stdin, m.stdout, m.stderr = strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}
	return m, fr, root, data, kubeconfig
}

func resolved(t *testing.T, p string) string {
	t.Helper()
	got, err := hostPath(p)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func dockerRun(fr *fakeRunner) (call, bool) {
	for _, c := range fr.calls {
		if c.name == "docker" && len(c.args) > 0 && c.args[0] == "run" {
			return c, true
		}
	}
	return call{}, false
}

func TestShellArgv(t *testing.T) {
	m, fr, root, data, kubeconfig := shellHarness(t, nil)
	m.goos = "darwin"
	err := m.Shell(context.Background(), ShellOptions{Kubeconfig: kubeconfig, ClusterName: "kubedrill"})
	if err != nil {
		t.Fatalf("Shell: got error %v, want nil", err)
	}
	run, ok := dockerRun(fr)
	if !ok {
		t.Fatalf("no docker run call in %v", fr.joined())
	}
	calls := fr.joined()
	netIdx := slices.Index(calls, "docker network inspect kind")
	if netIdx < 0 || netIdx > len(calls)-2 {
		t.Errorf("docker network inspect kind must run before docker run, got %v", calls)
	}
	argv := " " + strings.Join(run.args, " ") + " "
	tag, _ := m.ImageTag()
	binary := wantBinary(m.stateDir, root, m.goarch)
	for _, want := range []string{
		" run --rm -it ",
		" --network kind ",
		" --hostname kubedrill ",
		" --security-opt no-new-privileges --cap-drop ALL --pids-limit 512 --memory 2g --init ",
		" --mount type=bind,source=" + resolved(t, root) + ",target=/workspace/repo,readonly ",
		" --mount type=bind,source=" + resolved(t, data) + ",target=/data ",
		" --mount type=bind,source=" + resolved(t, kubeconfig) + ",target=/home/learner/.kube/config,readonly ",
		" --mount type=bind,source=" + filepath.Join(resolved(t, m.stateDir), "bin", filepath.Base(binary)) + ",target=/usr/local/bin/kubedrill,readonly ",
		" -e KUBEDRILL_ROOT=/workspace/repo ",
		" -e KUBEDRILL_DATA=/data ",
		" -e KUBECONFIG=/home/learner/.kube/config ",
		" -e KUBEDRILL_WORKSTATION=1 ",
		" -e KUBEDRILL_CLUSTER=kubedrill ",
		" -e TERM ",
		" -w /workspace ",
		" " + tag + " tmux ",
	} {
		if !strings.Contains(argv, want) {
			t.Errorf("docker run argv lacks %q:\n%s", strings.TrimSpace(want), argv)
		}
	}
	for _, reject := range []string{" -v ", " --user ", " HOME="} {
		if strings.Contains(argv, reject) {
			t.Errorf("docker run argv on darwin must not contain %q:\n%s", strings.TrimSpace(reject), argv)
		}
	}
	if !strings.HasSuffix(argv, " "+tag+" tmux ") {
		t.Errorf("argv must end with the tag and the layout, got %s", argv)
	}
	if !slices.Contains(run.cmd.Env, "DOCKER_CLI_HINTS=false") {
		t.Errorf("docker run env lacks DOCKER_CLI_HINTS=false")
	}
	if run.cmd.WaitDelay != 5*time.Second {
		t.Errorf("WaitDelay: got %v, want 5s", run.cmd.WaitDelay)
	}
	if run.cmd.Cancel == nil {
		t.Errorf("Cancel: got nil, want a SIGTERM sender")
	}

	fr.calls = nil
	err = m.Shell(context.Background(), ShellOptions{Kubeconfig: kubeconfig, Layout: LayoutShell, Command: []string{"kubectl", "get", "nodes"}})
	if err != nil {
		t.Fatal(err)
	}
	last := fr.calls[len(fr.calls)-1]
	if got := strings.Join(last.args, " "); !strings.HasSuffix(got, " "+tag+" kubectl get nodes") {
		t.Errorf("Command replaces layout: got %q", got)
	}

	if err := m.Shell(context.Background(), ShellOptions{Kubeconfig: kubeconfig, Layout: "screen"}); err == nil {
		t.Errorf("Shell with unknown layout: got nil, want error")
	}
	if err := m.Shell(context.Background(), ShellOptions{}); err == nil {
		t.Errorf("Shell with empty kubeconfig: got nil, want error")
	}
}

func TestShellLinuxUser(t *testing.T) {
	m, fr, _, _, kubeconfig := shellHarness(t, nil)
	m.goos, m.uid, m.gid = "linux", 1234, 5678
	if err := m.Shell(context.Background(), ShellOptions{Kubeconfig: kubeconfig}); err != nil {
		t.Fatalf("Shell: %v", err)
	}
	run, _ := dockerRun(fr)
	argv := " " + strings.Join(run.args, " ") + " "
	for _, want := range []string{" --user 1234:5678 ", " -e HOME=/home/learner "} {
		if !strings.Contains(argv, want) {
			t.Errorf("docker run argv on linux lacks %q:\n%s", strings.TrimSpace(want), argv)
		}
	}
}

func TestShellKindNetworkMissing(t *testing.T) {
	m, fr, _, _, kubeconfig := shellHarness(t, failing("docker network inspect kind"))
	err := m.Shell(context.Background(), ShellOptions{Kubeconfig: kubeconfig})
	want := "start workstation: kind docker network not found; was the cluster created with docker? (kubedrill up)"
	if err == nil || err.Error() != want {
		t.Errorf("Shell without kind network: got %v, want %q", err, want)
	}
	if _, ok := dockerRun(fr); ok {
		t.Errorf("docker run was called after the network preflight failed: %v", fr.joined())
	}
}

func TestShellRejectsMountCharacters(t *testing.T) {
	for name, bad := range map[string]string{"comma": "a,b", "colon": "a:b"} {
		t.Run(name, func(t *testing.T) {
			m, fr, _, data, _ := shellHarness(t, nil)
			kubeconfig := filepath.Join(data, bad)
			if err := os.WriteFile(kubeconfig, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			err := m.Shell(context.Background(), ShellOptions{Kubeconfig: kubeconfig})
			if err == nil || !strings.Contains(err.Error(), resolved(t, data)+"/"+bad) {
				t.Errorf("Shell with %q in the path: got %v, want an error naming the path", bad, err)
			}
			if _, ok := dockerRun(fr); ok {
				t.Errorf("docker run was called with a rejected mount source")
			}
		})
	}
}

func TestShellExitCodes(t *testing.T) {
	tests := []struct {
		name    string
		script  string
		wantErr string
	}{
		{name: "shell exit 0", script: "exit 0"},
		{name: "shell exit 1 is a normal end of session", script: "exit 1"},
		{name: "shell exit 130 is a normal end of session", script: "exit 130"},
		{name: "docker 125 daemon error", script: "echo 'docker: Error response from daemon: boom.' >&2; exit 125", wantErr: "start workstation: docker exited with status 125: docker: Error response from daemon: boom.: exit status 125"},
		{name: "shell exit 126 without a docker message is a normal end of session", script: "exit 126"},
		{name: "shell exit 127 without a docker message is a normal end of session", script: "exit 127"},
		{name: "shell exit 127 after an unrelated warning is a normal end of session", script: "echo 'WARNING: terminal size unknown' >&2; exit 127"},
		{name: "docker 126 not runnable", script: "echo 'docker: Error response from daemon: permission denied: unknown.' >&2; exit 126", wantErr: "start workstation: docker exited with status 126: docker: Error response from daemon: permission denied: unknown.: exit status 126"},
		{name: "docker 127 not found", script: "echo 'exec: \"x\": not found' >&2; exit 127", wantErr: "start workstation: docker exited with status 127: exec: \"x\": not found: exit status 127"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _, _, _, kubeconfig := shellHarness(t, map[string]string{"docker run": tt.script})
			err := m.Shell(context.Background(), ShellOptions{Kubeconfig: kubeconfig})
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Shell: got %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Errorf("Shell: got %v, want %q", err, tt.wantErr)
			}
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				t.Errorf("Shell docker failure: got %v, want wrapped *exec.ExitError", err)
			}
		})
	}
}

func TestNodeShell(t *testing.T) {
	root := writeRepo(t)
	running := map[string]string{"docker inspect -f {{.State.Running}}": "echo true"}
	tests := []struct {
		node    string
		wantErr bool
	}{
		{node: "control-plane"},
		{node: "worker"},
		{node: "worker2"},
		{node: "worker3", wantErr: true},
		{node: "", wantErr: true},
		{node: "../etc", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.node, func(t *testing.T) {
			fr := &fakeRunner{scripts: running}
			m := New(root, t.TempDir(), WithRunner(fr.run))
			m.stdin, m.stdout, m.stderr = strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}
			err := m.NodeShell(context.Background(), "kubedrill", tt.node)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NodeShell(%q): got error %v, wantErr %v", tt.node, err, tt.wantErr)
			}
			if tt.wantErr {
				if len(fr.calls) != 0 {
					t.Errorf("NodeShell(%q): got calls %v, want none", tt.node, fr.joined())
				}
				return
			}
			want := []string{
				"docker inspect -f {{.State.Running}} kubedrill-" + tt.node,
				"docker exec -it kubedrill-" + tt.node + " bash",
			}
			if got := fr.joined(); !reflect.DeepEqual(got, want) {
				t.Errorf("NodeShell(%q): got %v, want %v", tt.node, got, want)
			}
			exec := fr.calls[1].cmd
			if exec.WaitDelay != 5*time.Second || exec.Cancel == nil {
				t.Errorf("docker exec: WaitDelay %v Cancel nil=%v, want 5s and a Cancel", exec.WaitDelay, exec.Cancel == nil)
			}
			for _, c := range fr.calls {
				if !slices.Contains(c.cmd.Env, "DOCKER_CLI_HINTS=false") {
					t.Errorf("%s %v: env lacks DOCKER_CLI_HINTS=false", c.name, c.args)
				}
			}
		})
	}
	if err := New(root, t.TempDir(), WithRunner((&fakeRunner{}).run)).NodeShell(context.Background(), "", "worker"); err == nil {
		t.Errorf("NodeShell with empty cluster: got nil, want error")
	}

	t.Run("node not running", func(t *testing.T) {
		for name, scripts := range map[string]map[string]string{
			"inspect fails":    failing("docker inspect"),
			"state is false":   {"docker inspect -f {{.State.Running}}": "echo false"},
			"state is garbage": {"docker inspect -f {{.State.Running}}": "echo"},
		} {
			fr := &fakeRunner{scripts: scripts}
			m := New(root, t.TempDir(), WithRunner(fr.run))
			err := m.NodeShell(context.Background(), "kubedrill", "worker")
			want := "node kubedrill-worker is not running; create the cluster with: kubedrill up"
			if err == nil || err.Error() != want {
				t.Errorf("%s: got %v, want %q", name, err, want)
			}
			if len(fr.calls) != 1 {
				t.Errorf("%s: got calls %v, want only the inspect", name, fr.joined())
			}
		}
	})

	t.Run("exit codes", func(t *testing.T) {
		for script, wantErr := range map[string]bool{
			"exit 0":   false,
			"exit 1":   false,
			"exit 130": false,
			"exit 125": true,
			"exit 126": false,
			"exit 127": false,
			"echo 'OCI runtime exec failed: exec failed: unable to start container process: exec: \"bash\": executable file not found in $PATH: unknown' >&2; exit 126": true,
			"echo 'Error response from daemon: container abc is not running' >&2; exit 127":                                                                             true,
		} {
			fr := &fakeRunner{scripts: map[string]string{"docker inspect -f {{.State.Running}}": "echo true", "docker exec": script}}
			m := New(root, t.TempDir(), WithRunner(fr.run))
			m.stdin, m.stdout, m.stderr = strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}
			err := m.NodeShell(context.Background(), "kubedrill", "worker")
			if (err != nil) != wantErr {
				t.Errorf("NodeShell with docker exec %q: got %v, wantErr %v", script, err, wantErr)
			}
			if wantErr && !strings.HasPrefix(err.Error(), "node shell on kubedrill-worker: docker exited with status") {
				t.Errorf("NodeShell error text: got %q", err.Error())
			}
		}
	})
}

func TestValidLayout(t *testing.T) {
	for s, want := range map[string]bool{"tmux": true, "shell": true, "": false, "screen": false, "Tmux": false} {
		if ValidLayout(s) != want {
			t.Errorf("ValidLayout(%q): got %v, want %v", s, !want, want)
		}
	}
}

func TestDockerAvailable(t *testing.T) {
	root := writeRepo(t)
	fr := &fakeRunner{}
	if err := New(root, t.TempDir(), WithRunner(fr.run)).DockerAvailable(context.Background()); err != nil {
		t.Errorf("DockerAvailable: got %v, want nil", err)
	}
	if got := fr.joined(); len(got) != 1 || got[0] != "docker info" {
		t.Errorf("DockerAvailable calls: got %v, want [docker info]", got)
	}
	if !slices.Contains(fr.calls[0].cmd.Env, "DOCKER_CLI_HINTS=false") {
		t.Errorf("docker info env lacks DOCKER_CLI_HINTS=false")
	}
	fr = &fakeRunner{scripts: failing("docker info")}
	if err := New(root, t.TempDir(), WithRunner(fr.run)).DockerAvailable(context.Background()); err == nil {
		t.Errorf("DockerAvailable with failing docker: got nil, want error")
	}
}

func TestTailWriter(t *testing.T) {
	var sink bytes.Buffer
	tw := &tailWriter{w: &sink, limit: 4}
	for _, s := range []string{"ab", "cd", "ef"} {
		if _, err := tw.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	if sink.String() != "abcdef" {
		t.Errorf("forwarded: got %q, want abcdef", sink.String())
	}
	if tw.String() != "cdef" {
		t.Errorf("tail: got %q, want cdef", tw.String())
	}
}

func TestImageFilesMatchDockerfile(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", imageDir, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	copied := map[string]bool{"Dockerfile": true}
	for _, raw := range strings.Split(string(data), "\n") {
		fields := strings.Fields(raw)
		if len(fields) < 3 || fields[0] != "COPY" {
			continue
		}
		for _, f := range fields[1 : len(fields)-1] {
			if strings.HasPrefix(f, "--") {
				continue
			}
			copied[f] = true
		}
	}
	got := slices.Sorted(maps.Keys(copied))
	want := slices.Sorted(slices.Values(imageFiles))
	if !slices.Equal(got, want) {
		t.Errorf("Dockerfile COPY sources plus Dockerfile: got %v, want imageFiles %v", got, want)
	}
}

func TestEnsureBinaryPrunesOldBinaries(t *testing.T) {
	root := writeRepo(t)
	data := t.TempDir()
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	m := New(root, t.TempDir(), WithStateDir(data), WithRunner((&fakeRunner{}).run), WithOutput(&bytes.Buffer{}))
	m.now = func() time.Time { return now }
	current := wantBinary(data, root, m.goarch)
	dir := filepath.Dir(current)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string]time.Time{
		current: now.Add(-30 * 24 * time.Hour),
		filepath.Join(dir, "kubedrill-linux-arm64-old00000"): now.Add(-8 * 24 * time.Hour),
		filepath.Join(dir, "kubedrill-linux-amd64-recent00"): now.Add(-6 * 24 * time.Hour),
		filepath.Join(dir, "kubectl"):                        now.Add(-30 * 24 * time.Hour),
	}
	for path, at := range files {
		if err := os.WriteFile(path, []byte("bin"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.EnsureBinary(context.Background()); err != nil {
		t.Fatalf("EnsureBinary: %v", err)
	}
	for path := range files {
		_, err := os.Stat(path)
		gone := errors.Is(err, fs.ErrNotExist)
		if want := strings.HasSuffix(path, "old00000"); gone != want {
			t.Errorf("%s: removed %v, want %v", filepath.Base(path), gone, want)
		}
	}
}

func TestEnsureBinaryBuildFailure(t *testing.T) {
	root := writeRepo(t)
	t.Run("failing go build", func(t *testing.T) {
		data := t.TempDir()
		fr := &fakeRunner{scripts: failing("go build")}
		m := New(root, t.TempDir(), WithStateDir(data), WithRunner(fr.run), WithOutput(&bytes.Buffer{}))
		out := wantBinary(data, root, m.goarch)
		got, err := m.EnsureBinary(context.Background())
		if err == nil {
			t.Fatalf("EnsureBinary: got path %q and nil error, want error", got)
		}
		if want := "build linux kubedrill into " + out + ": "; !strings.HasPrefix(err.Error(), want) {
			t.Errorf("error: got %q, want prefix %q", err, want)
		}
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Errorf("error %v does not wrap *exec.ExitError", err)
		}
		if _, statErr := os.Stat(out); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("binary %s after a failed build: stat err %v, want not exist", out, statErr)
		}
	})
	t.Run("successful go build command", func(t *testing.T) {
		data := t.TempDir()
		fr := &fakeRunner{}
		m := New(root, t.TempDir(), WithStateDir(data), WithRunner(fr.run), WithOutput(&bytes.Buffer{}))
		out := wantBinary(data, root, m.goarch)
		if _, err := m.EnsureBinary(context.Background()); err != nil {
			t.Fatalf("EnsureBinary: %v", err)
		}
		if len(fr.calls) != 1 {
			t.Fatalf("calls: got %v, want one go build", fr.joined())
		}
		c := fr.calls[0]
		if c.cmd.Dir != root {
			t.Errorf("go build Dir: got %q, want %q", c.cmd.Dir, root)
		}
		for _, e := range []string{"GOOS=linux", "GOARCH=" + m.goarch, "CGO_ENABLED=0"} {
			if !slices.Contains(c.cmd.Env, e) {
				t.Errorf("go build env lacks %s: %v", e, c.cmd.Env)
			}
		}
		argv := " " + strings.Join(c.args, " ") + " "
		for _, want := range []string{" -buildvcs=false ", " -trimpath ", " -o " + out + " ", " ./cmd/kubedrill "} {
			if !strings.Contains(argv, want) {
				t.Errorf("go build argv lacks %q: %s", strings.TrimSpace(want), argv)
			}
		}
	})
}

func TestShellWrapsPrepareErrors(t *testing.T) {
	t.Run("missing bashrc", func(t *testing.T) {
		m, fr, root, _, kubeconfig := shellHarness(t, nil)
		if err := os.Remove(filepath.Join(root, "workstation", "bashrc")); err != nil {
			t.Fatal(err)
		}
		err := m.Shell(context.Background(), ShellOptions{Kubeconfig: kubeconfig})
		if err == nil {
			t.Fatal("Shell: got nil, want an image tag error")
		}
		if want := "start workstation: compute workstation image tag: "; !strings.HasPrefix(err.Error(), want) {
			t.Errorf("error: got %q, want prefix %q", err, want)
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("error %v does not wrap os.ErrNotExist", err)
		}
		if len(fr.calls) != 0 {
			t.Errorf("commands ran before the image tag failed: %v", fr.joined())
		}
	})
	t.Run("failing go build", func(t *testing.T) {
		m, fr, _, _, kubeconfig := shellHarness(t, failing("go build"))
		err := m.Shell(context.Background(), ShellOptions{Kubeconfig: kubeconfig})
		if err == nil {
			t.Fatal("Shell: got nil, want a build error")
		}
		if want := "start workstation: build linux kubedrill into "; !strings.HasPrefix(err.Error(), want) {
			t.Errorf("error: got %q, want prefix %q", err, want)
		}
		if _, ok := dockerRun(fr); ok {
			t.Errorf("docker run was called after the build failed: %v", fr.joined())
		}
		if slices.Contains(fr.joined(), "docker network inspect kind") {
			t.Errorf("network preflight ran after the build failed: %v", fr.joined())
		}
	})
}

func TestDockerProbesAreBounded(t *testing.T) {
	m, fr, _, _, kubeconfig := shellHarness(t, nil)
	if err := m.Shell(context.Background(), ShellOptions{Kubeconfig: kubeconfig}); err != nil {
		t.Fatal(err)
	}
	if err := m.DockerAvailable(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = m.NodeShell(context.Background(), "kubedrill", "worker")
	probes := 0
	for _, c := range fr.calls {
		if c.name != "docker" {
			continue
		}
		interactive := c.args[0] == "run" || c.args[0] == "exec"
		if interactive {
			if c.cmd.Cancel == nil {
				t.Errorf("%v: interactive call without a cancel hook", c.args)
			}
			continue
		}
		if c.args[0] == "build" {
			continue
		}
		probes++
		if c.cmd.WaitDelay != time.Second {
			t.Errorf("docker %v: WaitDelay %v, want the probe setting", c.args, c.cmd.WaitDelay)
		}
	}
	if probes < 4 {
		t.Errorf("probes: got %d in %v, want image inspect, network inspect, info and inspect", probes, fr.joined())
	}

	hang := &fakeRunner{scripts: map[string]string{"docker info": "sleep 30"}}
	slow := New(t.TempDir(), t.TempDir(), WithRunner(hang.run))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	deadline := make(chan time.Time, 1)
	slow.run = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		d, _ := ctx.Deadline()
		deadline <- d
		return hang.run(ctx, "true")
	}
	start := time.Now()
	_ = slow.DockerAvailable(ctx)
	if d := <-deadline; d.IsZero() || d.Sub(start) > 11*time.Second || d.Sub(start) < 9*time.Second {
		t.Errorf("docker info deadline: got %v after start, want about 10s", d.Sub(start))
	}
}

func TestEnsureImagePrunesOlderTags(t *testing.T) {
	root := writeRepo(t)
	old := time.Now().Add(-30 * 24 * time.Hour).Format(createdAtLayout)
	fr := &fakeRunner{scripts: map[string]string{
		"docker image inspect": "false",
		"docker image ls":      "printf 'aaaaaaaaaaaa\\t%s\\n<none>\\t%s\\ndev\\t%s\\nbbbbbbbbbbbb\\t%s\\n' '" + old + "' '" + old + "' '" + old + "' '" + old + "'",
		"docker image rm kubedrill-workstation:bbbbbbbbbbbb": "false",
	}}
	var out bytes.Buffer
	m := New(root, t.TempDir(), WithRunner(fr.run), WithOutput(&out))
	tag, err := m.EnsureImage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var removed []string
	for _, c := range fr.joined() {
		if strings.HasPrefix(c, "docker image rm ") {
			removed = append(removed, strings.TrimPrefix(c, "docker image rm "))
		}
	}
	if want := []string{"kubedrill-workstation:aaaaaaaaaaaa", "kubedrill-workstation:bbbbbbbbbbbb"}; !reflect.DeepEqual(removed, want) {
		t.Errorf("image rm calls: got %v, want %v (never %s, <none> or dev)", removed, want, tag)
	}
	if !strings.Contains(out.String(), "removed 1 older workstation image(s)\n") {
		t.Errorf("output: got %q, want the count of removed images", out.String())
	}
	fr = &fakeRunner{scripts: map[string]string{"docker image inspect": "false", "docker build": "false", "docker image ls": "printf 'aaaaaaaaaaaa\\t%s\\n' '" + old + "'"}}
	m = New(root, t.TempDir(), WithRunner(fr.run), WithOutput(&bytes.Buffer{}))
	if _, err := m.EnsureImage(context.Background()); err == nil {
		t.Fatal("EnsureImage with a failed build: got nil error")
	}
	for _, c := range fr.joined() {
		if strings.HasPrefix(c, "docker image rm") {
			t.Errorf("failed build pruned images: %q", c)
		}
	}
}

func TestShellRefusesFilesInsideTheJournal(t *testing.T) {
	m, fr, _, data, _ := shellHarness(t, nil)
	inside := filepath.Join(data, "kubeconfig-internal")
	if err := os.WriteFile(inside, []byte("apiVersion: v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := m.Shell(context.Background(), ShellOptions{Kubeconfig: inside})
	if err == nil || !strings.Contains(err.Error(), "inside the journal directory") {
		t.Errorf("Shell with the kubeconfig in the journal: got %v, want a refusal", err)
	}
	if _, ok := dockerRun(fr); ok {
		t.Errorf("docker run called with a kubeconfig in the journal")
	}
	m2, _, _, data2, kubeconfig := shellHarness(t, nil)
	m2.stateDir = data2
	if err := m2.Shell(context.Background(), ShellOptions{Kubeconfig: kubeconfig}); err == nil || !strings.Contains(err.Error(), "inside the journal directory") {
		t.Errorf("Shell with the binary in the journal: got %v, want a refusal", err)
	}
}

func TestShellRefusesJournalOverlap(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, m *Manager, root string)
		want  string
	}{
		{
			name: "journal inside the repository",
			setup: func(t *testing.T, m *Manager, root string) {
				m.dataDir = filepath.Join(root, ".data")
			},
			want: "and the repository",
		},
		{
			name: "repository inside the journal",
			setup: func(t *testing.T, m *Manager, root string) {
				moved := filepath.Join(m.dataDir, "checkout")
				if err := os.Rename(root, moved); err != nil {
					t.Fatal(err)
				}
				m.root = moved
			},
			want: "and the repository",
		},
		{
			name: "journal inside the cluster state directory",
			setup: func(t *testing.T, m *Manager, root string) {
				m.dataDir = filepath.Join(m.stateDir, "journal")
			},
			want: "and the cluster state directory",
		},
		{
			name: "cluster state directory inside the journal",
			setup: func(t *testing.T, m *Manager, root string) {
				m.stateDir = filepath.Join(m.dataDir, "state")
				if err := os.MkdirAll(m.stateDir, 0o700); err != nil {
					t.Fatal(err)
				}
			},
			want: "inside the journal directory",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, fr, root, _, kubeconfig := shellHarness(t, nil)
			tt.setup(t, m, root)
			if err := os.MkdirAll(m.dataDir, 0o700); err != nil {
				t.Fatal(err)
			}
			err := m.Shell(context.Background(), ShellOptions{Kubeconfig: kubeconfig})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Shell: got %v, want a refusal containing %q", err, tt.want)
			}
			if _, ok := dockerRun(fr); ok {
				t.Errorf("docker run called although the journal overlaps: %v", fr.joined())
			}
		})
	}
}

func TestShellSkipsAuditMountWhenPathIsAFile(t *testing.T) {
	m, fr, _, _, kubeconfig := shellHarness(t, nil)
	file := filepath.Join(t.TempDir(), "audit")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	m.auditDir = file
	if err := m.Shell(context.Background(), ShellOptions{Kubeconfig: kubeconfig}); err != nil {
		t.Fatal(err)
	}
	run, _ := dockerRun(fr)
	if argv := strings.Join(run.args, " "); strings.Contains(argv, "/audit") || strings.Contains(argv, "KUBEDRILL_AUDIT_LOG") {
		t.Errorf("docker run with an audit path that is a file: %s", argv)
	}
}

func TestReplayUserAndRefusals(t *testing.T) {
	root := writeRepo(t)
	data := t.TempDir()
	if err := os.MkdirAll(filepath.Join(data, "recordings"), 0o700); err != nil {
		t.Fatal(err)
	}
	cast := filepath.Join(data, "recordings", "task-x-1.cast")
	if err := os.WriteFile(cast, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRunner{}
	m := New(root, data, WithRunner(fr.run), WithOutput(&bytes.Buffer{}))
	m.stdin, m.stdout, m.stderr = strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}
	m.goos, m.uid, m.gid = "linux", 1000, 1001
	if err := m.Replay(context.Background(), "task-x-1.cast"); err != nil {
		t.Fatal(err)
	}
	run, ok := dockerRun(fr)
	if !ok {
		t.Fatalf("no docker run in %v", fr.joined())
	}
	argv := " " + strings.Join(run.args, " ") + " "
	for _, want := range []string{" --network none ", " --user 1000:1001 ", " -e HOME=/home/learner ", ",target=/data,readonly ", " asciinema play -i 2 /data/recordings/task-x-1.cast "} {
		if !strings.Contains(argv, want) {
			t.Errorf("replay argv lacks %q:\n%s", strings.TrimSpace(want), argv)
		}
	}
	m.goos = "darwin"
	fr.calls = nil
	if err := m.Replay(context.Background(), cast); err != nil {
		t.Fatal(err)
	}
	run, _ = dockerRun(fr)
	if strings.Contains(strings.Join(run.args, " "), "--user") {
		t.Errorf("replay on darwin sets --user: %v", run.args)
	}
	for _, bad := range []string{filepath.Dir(data), data, filepath.Join(data, "..", "elsewhere.cast"), ""} {
		if bad != "" && bad != data && bad != filepath.Dir(data) {
			if err := os.WriteFile(bad, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := m.Replay(context.Background(), bad); err == nil {
			t.Errorf("Replay(%q): got nil error, want a refusal", bad)
		}
	}
}

func TestNodeDebug(t *testing.T) {
	const image = "busybox:1@sha256:abc"
	finished := "kubectl delete pods --namespace kubedrill-debug --field-selector=status.phase!=Pending,status.phase!=Running --ignore-not-found --wait=false"
	list := "kubectl get pods --namespace kubedrill-debug -o jsonpath={.items[*].metadata.name}"
	created := "kubectl delete pods --namespace kubedrill-debug --ignore-not-found --wait=false node-debugger-kubedrill-worker-new"
	debug := func(node string) string {
		return "kubectl debug node/kubedrill-" + node + " --namespace kubedrill-debug -it --quiet --profile=sysadmin --image=" + image + " -- chroot /host /bin/bash -l"
	}
	pods := func(dir string) string {
		return "if [ -f " + dir + "/listed ]; then echo node-debugger-kubedrill-worker-old node-debugger-kubedrill-worker-new node-debugger-kubedrill-worker2-x other; " +
			"else touch " + dir + "/listed; echo node-debugger-kubedrill-worker-old; fi"
	}
	tests := []struct {
		name      string
		node      string
		scripts   func(dir string) map[string]string
		wantErr   string
		wantCalls []string
	}{
		{name: "session deletes only the pod it created", node: "worker", wantCalls: []string{"kubectl apply -f -", finished, list, debug("worker"), list, created}},
		{name: "shell ends non-zero", node: "worker", scripts: func(string) map[string]string { return map[string]string{"kubectl debug": "exit 3"} }, wantCalls: []string{"kubectl apply -f -", finished, list, debug("worker"), list, created}},
		{name: "no new pod for this node", node: "control-plane", wantCalls: []string{"kubectl apply -f -", finished, list, debug("control-plane"), list}},
		{name: "failing list keeps every pod", node: "worker", scripts: func(string) map[string]string { return failing("kubectl get") }, wantCalls: []string{"kubectl apply -f -", finished, list, debug("worker")}},
		{name: "namespace apply fails", node: "worker", scripts: func(string) map[string]string { return map[string]string{"kubectl apply": "cat; exit 1"} }, wantErr: "node debug kubedrill-worker: apply namespace kubedrill-debug: exit status 1: apiVersion: v1\nkind: Namespace\nmetadata:\n  name: kubedrill-debug", wantCalls: []string{"kubectl apply -f -"}},
		{name: "unknown node", node: "worker3", wantErr: `node debug: node "worker3" is not one of control-plane, worker, worker2`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			scripts := map[string]string{"kubectl get": pods(dir)}
			if tt.scripts != nil {
				for k, v := range tt.scripts(dir) {
					scripts[k] = v
				}
			}
			fr := &fakeRunner{scripts: scripts}
			m := New(writeRepo(t), t.TempDir(), WithRunner(fr.run))
			m.stdin, m.stdout, m.stderr = strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}
			err := m.NodeDebug(context.Background(), "kubedrill", tt.node, image)
			gotErr := ""
			if err != nil {
				gotErr = err.Error()
			}
			if gotErr != tt.wantErr {
				t.Fatalf("NodeDebug: got error %q, want %q", gotErr, tt.wantErr)
			}
			if got := fr.joined(); !reflect.DeepEqual(got, tt.wantCalls) {
				t.Errorf("NodeDebug calls:\n got %q\nwant %q", got, tt.wantCalls)
			}
			for _, c := range fr.calls {
				if c.name == "kubectl" && len(c.args) > 0 && c.args[0] == "debug" && (c.cmd.WaitDelay != 5*time.Second || c.cmd.Cancel == nil) {
					t.Errorf("kubectl debug: WaitDelay %v Cancel nil=%v, want 5s and a Cancel", c.cmd.WaitDelay, c.cmd.Cancel == nil)
				}
			}
		})
	}
}
