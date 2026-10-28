package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
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
	prepares      int
	prepareOut    string
	prepareErr    error
	removed       []string
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

func (w *fakeWorkstation) Prepare(_ context.Context, out io.Writer) error {
	w.prepares++
	if w.prepareOut != "" {
		fmt.Fprintln(out, w.prepareOut)
	}
	return w.prepareErr
}

func (w *fakeWorkstation) ShellCommand(_ context.Context, o workstation.ShellOptions) (*exec.Cmd, error) {
	w.record("ShellCommand " + o.Name)
	w.shells = append(w.shells, o)
	return exec.Command("sh", "-c", "echo workstation ready; cat"), nil
}

func (w *fakeWorkstation) RemoveContainers(_ context.Context, prefix string) (int, error) {
	w.record("RemoveContainers " + prefix)
	return 0, nil
}

func (w *fakeWorkstation) RemoveContainer(_ context.Context, name string) error {
	w.record("RemoveContainer " + name)
	w.removed = append(w.removed, name)
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
	next := strings.Index(out, "Next: kubedrill check")
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
	mustContain(t, out, "started 001-hello-down (seed 1) in namespace task-hello-abcde\n\n", "The hello app returns 503", "Fundamentals: kubedrill learn (pods, deployments)", "Next: kubedrill check")
	mustNotContain(t, out, "kubedrill web")
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

func TestOfferRecreateAStaleCluster(t *testing.T) {
	cases := []struct {
		name        string
		active      bool
		stale       bool
		stdin       string
		wantDeleted int
		wantOffer   bool
		want        []string
	}{
		{name: "enter recreates", stale: true, stdin: "\n", wantDeleted: 1, wantOffer: true, want: []string{"older audit policy", "cluster kubedrill deleted"}},
		{name: "n keeps it and is not asked again", stale: true, stdin: "n\n", wantOffer: true, want: []string{"kept the cluster; kubedrill down --yes && kubedrill up recreates it"}},
		{name: "an active episode keeps the cluster", stale: true, active: true},
		{name: "a current policy asks nothing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			withWorkstation(f)
			if tc.active {
				if code, _, errOut := f.run(t, "", "start", "001", "--seed", "1"); code != 0 {
					t.Fatalf("start: exit %d: %s", code, errOut)
				}
			}
			f.cluster.exists, f.cluster.stale = true, tc.stale
			a, out := f.terminalApp(t, tc.stdin)
			if err := a.offerRecreate(context.Background(), f.cluster); err != nil {
				t.Fatalf("offerRecreate: %v", err)
			}
			if offered := strings.Contains(out.String(), "Recreate the cluster now? [Y/n]"); offered != tc.wantOffer {
				t.Errorf("recreate offer: got %v, want %v\n%s", offered, tc.wantOffer, out)
			}
			mustContain(t, out.String(), tc.want...)
			if f.cluster.deleted != tc.wantDeleted {
				t.Errorf("deleted: got %d, want %d", f.cluster.deleted, tc.wantDeleted)
			}
			if tc.wantOffer && tc.wantDeleted == 0 {
				again, out2 := f.terminalApp(t, "n\n")
				if err := again.offerRecreate(context.Background(), f.cluster); err != nil {
					t.Fatal(err)
				}
				mustNotContain(t, out2.String(), "Recreate the cluster now?")
			}
		})
	}
}

// terminalApp is an app on terminals that reads stdin, for the prompts the
// commands ask outside a cobra run.
func (f *fixture) terminalApp(t *testing.T, stdin string) (*app, *strings.Builder) {
	t.Helper()
	var out, errb strings.Builder
	a := newApp(Options{
		Factories:       f.factories(),
		Stdout:          &out,
		Stderr:          &errb,
		Stdin:           strings.NewReader(stdin),
		Getenv:          func(k string) string { return f.env[k] },
		Getwd:           func() (string, error) { return f.root, nil },
		IsTerminal:      true,
		IsInputTerminal: true,
		Now:             f.nowFunc(),
		Executable:      func() (string, error) { return "/usr/local/bin/kubedrill", nil },
		Rand:            func(n int64) int64 { return 6 % n },
		NamespaceSuffix: func() string { return "abcde" },
	})
	a.rootFlag = f.root
	return a, &out
}

// startExam starts an exam episode of 001 the way the browser workspace's
// exam rehearsal does, and returns what start printed.
func (f *fixture) startExam(t *testing.T, seed int64, yes bool) string {
	t.Helper()
	if f.env == nil {
		f.env = map[string]string{}
	}
	f.env["NO_COLOR"] = "1"
	a, out := f.terminalApp(t, "")
	if err := a.runStart(context.Background(), "001", startOptions{seed: seed, yes: yes, exam: true}); err != nil {
		t.Fatalf("start an exam: %v", err)
	}
	return out.String()
}
