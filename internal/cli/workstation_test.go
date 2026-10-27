package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cauebraz/kubedrill/internal/cluster"
	"github.com/cauebraz/kubedrill/internal/workstation"
)

// fakeWorkstation records calls in the shared log so the order against the
// cluster fake can be asserted.
type fakeWorkstation struct {
	root, dataDir string
	dockerErr     error
	log           *[]string
	shells        []workstation.ShellOptions
	nodes         []string
	debugs        []string
	debugErr      error
	replays       []string
}

func (w *fakeWorkstation) NodeDebug(_ context.Context, clusterName, node, image string) error {
	w.record("NodeDebug " + clusterName + " " + node + " " + image)
	w.debugs = append(w.debugs, node)
	return w.debugErr
}

func (w *fakeWorkstation) Replay(_ context.Context, cast string) error {
	w.record("Replay " + cast)
	w.replays = append(w.replays, cast)
	return nil
}

func (w *fakeWorkstation) record(s string) {
	if w.log != nil {
		*w.log = append(*w.log, s)
	}
}

func (w *fakeWorkstation) DockerAvailable(context.Context) error {
	w.record("DockerAvailable")
	return w.dockerErr
}

func (w *fakeWorkstation) Shell(_ context.Context, o workstation.ShellOptions) error {
	w.record("Shell")
	w.shells = append(w.shells, o)
	return nil
}

func (w *fakeWorkstation) NodeShell(_ context.Context, clusterName, node string) error {
	w.record("NodeShell " + clusterName + " " + node)
	w.nodes = append(w.nodes, node)
	return nil
}

// withWorkstation wires a fake workstation and a shared call log into f.
func withWorkstation(f *fixture) *[]string {
	log := &[]string{}
	f.ws = &fakeWorkstation{log: log}
	f.cluster.calls = log
	return log
}

func TestStartPrintsFundamentals(t *testing.T) {
	f := newFixture(t)
	code, out, errOut := f.run(t, "", "start", "001", "--seed", "1")
	if code != 0 {
		t.Fatalf("exit: got %d, want 0 (%s)", code, errOut)
	}
	mustContain(t, out, "Fundamentals: kubedrill learn (pods, deployments)")
	ticket := strings.Index(out, "The hello app returns 503")
	fund := strings.Index(out, "Fundamentals:")
	next := strings.Index(out, "Next: kubedrill watch")
	if !(ticket < fund && fund < next) {
		t.Errorf("order: ticket %d, fundamentals %d, next %d; want ticket < fundamentals < next", ticket, fund, next)
	}
	_, out, _ = f.run(t, "", "ticket")
	mustContain(t, out, "Fundamentals: kubedrill learn (pods, deployments)")
	f.journal.current = nil
	_, out, _ = f.run(t, "", "start", "002", "--seed", "1")
	if strings.Contains(out, "Fundamentals") {
		t.Errorf("scenario without concepts printed Fundamentals:\n%s", out)
	}
}

func TestStartOpensWorkstationInOrder(t *testing.T) {
	f := newFixture(t)
	f.stdoutTTY, f.stdinTTY = true, true
	f.env = map[string]string{"NO_COLOR": "1"}
	log := withWorkstation(f)
	code, out, errOut := f.run(t, "", "start", "001", "--seed", "1")
	if code != 0 {
		t.Fatalf("exit: got %d, want 0 (%s)", code, errOut)
	}
	want := "DockerAvailable,WriteInternalKubeconfig " + internalKubeconfig(t) + ",Shell"
	if got := strings.Join(*log, ","); got != want {
		t.Errorf("calls:\n got %s\nwant %s", got, want)
	}
	if len(f.ws.shells) != 1 {
		t.Fatalf("shells: got %d, want 1", len(f.ws.shells))
	}
	o := f.ws.shells[0]
	if o.Kubeconfig != internalKubeconfig(t) || o.Layout != "tmux" || o.ClusterName != "kubedrill" || len(o.Command) != 0 {
		t.Errorf("shell options: got %+v", o)
	}
	if f.ws.root != f.root || f.ws.dataDir != f.journal.dir {
		t.Errorf("workstation wired with root %q data %q; want %q and /data/kubedrill", f.ws.root, f.ws.dataDir, f.root)
	}
	mustContain(t, out, "started 001-hello-down (seed 1) in namespace task-hello-abcde\n\nFirst time with pods, deployments. See a worked example from another variant before your ticket? [y/N] worked example skipped\nticket shown in the workstation (kubedrill ticket reprints it)\nNew to Kubernetes? kubedrill story tells this ticket from zero\nFundamentals: kubedrill learn (pods, deployments)\nChecked by 1 check: kubedrill check --explain\nExpected: 15 min\nback on the host; kubedrill check grades from here too\n")
	mustNotContain(t, out, "The hello app returns 503", "Next: kubedrill watch")
	mustContain(t, errOut, "preparing the workstation...")
}

func TestStartLayoutFlag(t *testing.T) {
	f := newFixture(t)
	f.stdoutTTY, f.stdinTTY = true, true
	f.env = map[string]string{"NO_COLOR": "1"}
	withWorkstation(f)
	if code, _, errOut := f.run(t, "", "start", "001", "--seed", "1", "--layout", "shell"); code != 0 {
		t.Fatalf("exit: got %d, want 0 (%s)", code, errOut)
	}
	if len(f.ws.shells) != 1 || f.ws.shells[0].Layout != "shell" {
		t.Errorf("shells: got %+v, want one with layout shell", f.ws.shells)
	}
	f.journal.current = nil
	code, _, errOut := f.run(t, "", "start", "001", "--seed", "1", "--layout", "screen")
	if code != 1 {
		t.Errorf("bad layout exit: got %d, want 1", code)
	}
	mustContain(t, errOut, `--layout must be tmux or shell, got "screen"`)
	if len(f.kube.calls) != 3 {
		t.Errorf("bad layout touched the cluster: %v", f.kube.calls)
	}
}

func TestStartSkipsWorkstation(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		env    map[string]string
		stdout bool
		stdin  bool
		docker error
		notice string
	}{
		{name: "no-shell flag", args: []string{"--no-shell"}, stdout: true, stdin: true},
		{name: "KUBEDRILL_NO_SHELL", env: map[string]string{"KUBEDRILL_NO_SHELL": "1"}, stdout: true, stdin: true},
		{name: "inside the workstation", env: map[string]string{"KUBEDRILL_WORKSTATION": "1"}, stdout: true, stdin: true},
		{name: "stdout not a terminal", stdout: false, stdin: true},
		{name: "stdin not a terminal", stdout: true, stdin: false},
		{name: "docker unavailable", stdout: true, stdin: true, docker: errors.New("docker daemon unreachable"), notice: "workstation skipped: docker is unavailable (docker daemon unreachable); kubectl runs on the host"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.stdoutTTY, f.stdinTTY = tc.stdout, tc.stdin
			f.env = map[string]string{"NO_COLOR": "1"}
			for k, v := range tc.env {
				f.env[k] = v
			}
			log := withWorkstation(f)
			f.ws.dockerErr = tc.docker
			args := append([]string{"start", "001", "--seed", "1"}, tc.args...)
			code, out, errOut := f.run(t, "", args...)
			if code != 0 {
				t.Fatalf("exit: got %d, want 0 (%s)", code, errOut)
			}
			mustContain(t, out, "started 001-hello-down (seed 1)", "The hello app returns 503", "Fundamentals: kubedrill learn (pods, deployments)", "Next: kubedrill watch")
			mustNotContain(t, out, ticketInWorkstation)
			if len(f.ws.shells) != 0 || len(f.cluster.internal) != 0 {
				t.Errorf("workstation opened: calls %v", *log)
			}
			if strings.Contains(out, "back on the host") {
				t.Errorf("printed the return line without a shell:\n%s", out)
			}
			if tc.notice != "" {
				mustContain(t, errOut, tc.notice)
			} else if strings.Contains(errOut, "workstation skipped") {
				t.Errorf("unexpected notice: %s", errOut)
			}
		})
	}
}

func TestStartWithoutWorkstationWired(t *testing.T) {
	f := newFixture(t)
	f.stdoutTTY, f.stdinTTY = true, true
	f.env = map[string]string{"NO_COLOR": "1"}
	code, out, errOut := f.run(t, "", "start", "001", "--seed", "1")
	if code != 0 {
		t.Fatalf("exit: got %d, want 0 (%s)", code, errOut)
	}
	if strings.Contains(out, "back on the host") || len(f.cluster.internal) != 0 {
		t.Errorf("start touched the workstation path without a factory:\n%s", out)
	}
}

func TestShellCommand(t *testing.T) {
	t.Run("re-enters with the active episode", func(t *testing.T) {
		f := newShellFixture(t)
		log := withWorkstation(f)
		f.run(t, "", "start", "001", "--seed", "1", "--no-shell")
		*log = nil
		code, out, errOut := f.run(t, "", "shell", "--layout", "shell")
		if code != 0 {
			t.Fatalf("exit: got %d, want 0 (%s)", code, errOut)
		}
		want := "DockerAvailable,WriteInternalKubeconfig " + internalKubeconfig(t) + ",Shell"
		if got := strings.Join(*log, ","); got != want {
			t.Errorf("calls:\n got %s\nwant %s", got, want)
		}
		if f.ws.shells[0].Layout != "shell" || f.ws.shells[0].Kubeconfig != internalKubeconfig(t) {
			t.Errorf("shell options: %+v", f.ws.shells[0])
		}
		mustContain(t, out, "back on the host; kubedrill check grades from here too")
	})
	t.Run("without episode offers the tools", func(t *testing.T) {
		f := newShellFixture(t)
		withWorkstation(f)
		code, out, errOut := f.run(t, "", "shell")
		if code != 0 {
			t.Fatalf("exit: got %d, want 0 (%s)", code, errOut)
		}
		if len(f.ws.shells) != 1 || f.ws.shells[0].Kubeconfig != internalKubeconfig(t) || f.ws.shells[0].Layout != "tmux" {
			t.Errorf("shells: %+v", f.ws.shells)
		}
		mustNotContain(t, out, backOnHostLine)
	})
	t.Run("needs a terminal", func(t *testing.T) {
		for _, tc := range []struct{ stdout, stdin bool }{{false, true}, {true, false}} {
			f := newFixture(t)
			f.stdoutTTY, f.stdinTTY = tc.stdout, tc.stdin
			withWorkstation(f)
			code, _, errOut := f.run(t, "", "shell")
			if code != 1 {
				t.Errorf("stdout %v stdin %v: exit got %d, want 1", tc.stdout, tc.stdin, code)
			}
			mustContain(t, errOut, "kubedrill: shell needs a terminal on stdin and stdout; run it from an interactive terminal")
			if len(f.ws.shells) != 0 {
				t.Errorf("shell opened without a terminal")
			}
		}
	})
	t.Run("missing cluster is an error", func(t *testing.T) {
		f := newShellFixture(t)
		withWorkstation(f)
		f.cluster.exists = false
		code, _, errOut := f.run(t, "", "shell")
		if code != 1 {
			t.Errorf("exit: got %d, want 1", code)
		}
		if len(f.ws.shells) != 0 || len(f.cluster.internal) != 0 {
			t.Errorf("shell opened without a cluster: %+v %v", f.ws.shells, f.cluster.internal)
		}
		mustContain(t, errOut, "kubedrill: cluster kubedrill: missing; the workstation needs its kubeconfig, create it with: kubedrill up")
	})
	t.Run("docker unavailable is an error", func(t *testing.T) {
		f := newShellFixture(t)
		withWorkstation(f)
		f.ws.dockerErr = errors.New("daemon down")
		code, _, errOut := f.run(t, "", "shell")
		if code != 1 {
			t.Errorf("exit: got %d, want 1", code)
		}
		mustContain(t, errOut, "kubedrill: the workstation needs docker: daemon down")
	})
	t.Run("never nests", func(t *testing.T) {
		f := newShellFixture(t)
		withWorkstation(f)
		f.env["KUBEDRILL_WORKSTATION"] = "1"
		code, _, errOut := f.run(t, "", "shell")
		if code != 1 {
			t.Errorf("exit: got %d, want 1", code)
		}
		mustContain(t, errOut, "already inside the workstation")
		if len(f.ws.shells) != 0 {
			t.Errorf("nested shell opened")
		}
	})
	t.Run("no workstation wired", func(t *testing.T) {
		f := newShellFixture(t)
		code, _, errOut := f.run(t, "", "shell")
		if code != 1 {
			t.Errorf("exit: got %d, want 1", code)
		}
		mustContain(t, errOut, "no workstation is wired into this build")
	})
}

// newShellFixture is a fixture whose stdin and stdout are terminals without
// color, as shell requires.
func newShellFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.stdoutTTY, f.stdinTTY = true, true
	f.env = map[string]string{"NO_COLOR": "1"}
	return f
}

func TestNodeCommand(t *testing.T) {
	f := newFixture(t)
	withWorkstation(f)
	const usage = "usage: kubedrill node <control-plane|worker|worker2> (kubectl get nodes lists them as kubedrill-<node>)"
	code, _, errOut := f.run(t, "", "node", "worker9")
	if code != 1 {
		t.Errorf("worker9 exit: got %d, want 1", code)
	}
	mustContain(t, errOut, usage)
	code, _, errOut = f.run(t, "", "node")
	if code != 1 {
		t.Errorf("no argument exit: got %d, want 1", code)
	}
	mustContain(t, errOut, usage)
	if len(f.ws.nodes) != 0 {
		t.Fatalf("node shell opened for a rejected argument: %v", f.ws.nodes)
	}
	code, out, errOut := f.run(t, "", "node", "worker2")
	if code != 0 {
		t.Fatalf("worker2 exit: got %d, want 0 (%s)", code, errOut)
	}
	mustContain(t, out, "kubedrill node: root on kubedrill-worker2 (kind node; changes persist until kubedrill down)\n")
	if strings.Join(f.ws.nodes, ",") != "worker2" {
		t.Errorf("nodes: got %v, want [worker2]", f.ws.nodes)
	}
	f.ws.dockerErr = errors.New("daemon down")
	code, _, errOut = f.run(t, "", "node", "worker")
	if code != 1 {
		t.Errorf("docker down exit: got %d, want 1", code)
	}
	mustContain(t, errOut, "a node shell needs docker: daemon down")
	if len(f.ws.debugs) != 0 {
		t.Errorf("kubectl debug on the host: got %v, want none", f.ws.debugs)
	}
}

func TestNodeCommandInsideWorkstationUsesKubectlDebug(t *testing.T) {
	f := newFixture(t)
	log := withWorkstation(f)
	f.env = map[string]string{"KUBEDRILL_WORKSTATION": "1"}
	code, out, errOut := f.run(t, "", "node", "worker")
	if code != 0 {
		t.Fatalf("exit: got %d, want 0 (%s)", code, errOut)
	}
	mustContain(t, out, "kubedrill node: root on kubedrill-worker through kubectl debug node/kubedrill-worker --profile=sysadmin -- chroot /host (changes persist until kubedrill down)\n")
	if got, want := strings.Join(*log, "|"), "NodeDebug kubedrill worker "+cluster.DebugImage; got != want {
		t.Errorf("calls: got %q, want %q (no docker probe, no docker exec)", got, want)
	}
	f.ws.debugErr = errors.New("kubectl: executable file not found in $PATH")
	code, _, errOut = f.run(t, "", "node", "control-plane")
	if code != 1 {
		t.Errorf("failing kubectl exit: got %d, want 1", code)
	}
	mustContain(t, errOut, "node shell control-plane: kubectl: executable file not found in $PATH")
}

func TestStartInsideWorkstationPrintsTicketOnly(t *testing.T) {
	f := newShellFixture(t)
	f.env["KUBEDRILL_WORKSTATION"] = "1"
	log := withWorkstation(f)
	code, out, errOut := f.run(t, "", "start", "001", "--seed", "1")
	if code != 0 {
		t.Fatalf("exit: got %d, want 0 (%s)", code, errOut)
	}
	mustContain(t, out, "started 001-hello-down (seed 1) in namespace task-hello-abcde\n\n", "The hello app returns 503", "Fundamentals: kubedrill learn (pods, deployments)", "Next: kubedrill watch")
	mustNotContain(t, out, ticketInWorkstation, backOnHostLine)
	if len(*log) != 0 {
		t.Errorf("workstation or cluster calls inside the workstation: got %v, want none", *log)
	}
	if len(f.ws.shells) != 0 || len(f.cluster.internal) != 0 {
		t.Errorf("shell opened: shells %+v internal kubeconfigs %v", f.ws.shells, f.cluster.internal)
	}
	for _, unwanted := range []string{"preparing the workstation", "workstation skipped", "workstation"} {
		if strings.Contains(errOut, unwanted) {
			t.Errorf("stderr contains %q:\n%s", unwanted, errOut)
		}
	}
}

func internalKubeconfig(t *testing.T) string {
	t.Helper()
	path, err := cluster.InternalKubeconfigPath()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDefaultContinuesIntoPractice(t *testing.T) {
	cases := []struct {
		name      string
		active    bool
		stdin     string
		wantStart bool
		wantShell int
		want      []string
	}{
		{name: "enter starts the recommendation", stdin: "\n", wantStart: true, wantShell: 1, want: []string{"Start 001-hello-down now? [Y/n]", "started 001-hello-down"}},
		{name: "n leaves it", stdin: "n\n", want: []string{"Start 001-hello-down now? [Y/n]", "not started; run ./start.sh again"}},
		{name: "active episode resumes", active: true, wantShell: 1, want: []string{"resuming 001-hello-down; opening the workstation"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			withWorkstation(f)
			if tc.active {
				if code, _, errOut := f.run(t, "", "start", "001", "--seed", "1", "--no-shell"); code != 0 {
					t.Fatalf("start: exit %d: %s", code, errOut)
				}
			}
			f.stdoutTTY, f.stdinTTY = true, true
			code, out, errOut := f.run(t, tc.stdin)
			if code != 0 {
				t.Fatalf("default: exit %d: %s", code, errOut)
			}
			mustContain(t, out, tc.want...)
			if got := len(f.ws.shells); got != tc.wantShell {
				t.Errorf("workstation shells: got %d, want %d", got, tc.wantShell)
			}
			cur, _ := f.journal.Current()
			if started := cur != nil; started != (tc.wantStart || tc.active) {
				t.Errorf("active episode after the run: got %v, want %v", started, tc.wantStart || tc.active)
			}
		})
	}
}

func TestDefaultWithoutTerminalOnlyLists(t *testing.T) {
	f := newFixture(t)
	withWorkstation(f)
	code, out, _ := f.run(t, "\n")
	if code != 0 {
		t.Fatalf("default: exit %d", code)
	}
	mustNotContain(t, out, "[Y/n]", "resuming")
	if len(f.ws.shells) != 0 {
		t.Errorf("workstation shells: got %d, want 0", len(f.ws.shells))
	}
}
