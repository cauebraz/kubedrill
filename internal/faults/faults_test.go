package faults

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type call []string

type fakeDocker struct {
	calls []call
	fail  map[string]fakeResult
}

type fakeResult struct {
	out string
	err error
}

func (f *fakeDocker) run(_ context.Context, name string, args ...string) ([]byte, error) {
	c := append(call{name}, args...)
	f.calls = append(f.calls, c)
	if r, ok := f.fail[strings.Join(args, " ")]; ok {
		return []byte(r.out), r.err
	}
	return nil, nil
}

var errExit error = &exec.ExitError{}

func newManager(t *testing.T, fd *fakeDocker) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	return New("kubedrill", dir, WithRunner(fd.run), WithNow(func() time.Time { return at })), dir
}

func TestApplyCommands(t *testing.T) {
	tests := []struct {
		name   string
		action Action
		node   string
		netem  NetemOptions
		want   call
	}{
		{name: "pause", action: Pause, node: "worker", want: call{"docker", "pause", "kubedrill-worker"}},
		{name: "unpause", action: Unpause, node: "worker", want: call{"docker", "unpause", "kubedrill-worker"}},
		{name: "stop kubelet", action: StopKubelet, node: "worker2", want: call{"docker", "exec", "kubedrill-worker2", "systemctl", "stop", "kubelet"}},
		{name: "start kubelet", action: StartKubelet, node: "worker2", want: call{"docker", "exec", "kubedrill-worker2", "systemctl", "start", "kubelet"}},
		{name: "netem delay and loss", action: Netem, node: "control-plane", netem: NetemOptions{Delay: 200 * time.Millisecond, Loss: 2.5}, want: call{"docker", "exec", "kubedrill-control-plane", "tc", "qdisc", "replace", "dev", "eth0", "root", "netem", "delay", "200000us", "loss", "2.5%"}},
		{name: "netem loss only", action: Netem, node: "worker", netem: NetemOptions{Loss: 10}, want: call{"docker", "exec", "kubedrill-worker", "tc", "qdisc", "replace", "dev", "eth0", "root", "netem", "loss", "10%"}},
		{name: "clear netem", action: ClearNetem, node: "worker", want: call{"docker", "exec", "kubedrill-worker", "tc", "qdisc", "del", "dev", "eth0", "root"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fd := &fakeDocker{}
			m, _ := newManager(t, fd)
			if err := m.Apply(context.Background(), tc.action, tc.node, tc.netem); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if !reflect.DeepEqual(fd.calls, []call{tc.want}) {
				t.Errorf("docker calls: got %v, want %v", fd.calls, []call{tc.want})
			}
		})
	}
}

func TestApplyValidation(t *testing.T) {
	tests := []struct {
		name   string
		action Action
		node   string
		netem  NetemOptions
		want   []string
	}{
		{name: "unknown node", action: Pause, node: "worker3", want: []string{`node "worker3" is not one of control-plane, worker, worker2`}},
		{name: "unknown action", action: "reboot", node: "worker", want: []string{`action "reboot" is not one of pause, unpause, stop-kubelet, start-kubelet, netem, clear-netem`}},
		{name: "netem without parameters", action: Netem, node: "worker", want: []string{"netem needs a delay or a loss"}},
		{name: "netem out of range", action: Netem, node: "worker", netem: NetemOptions{Delay: -time.Second, Loss: 101}, want: []string{"delay -1s is negative", "loss 101 is not between 0 and 100"}},
		{name: "all problems at once", action: "x", node: "y", want: []string{`action "x"`, `node "y"`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fd := &fakeDocker{}
			m, _ := newManager(t, fd)
			err := m.Apply(context.Background(), tc.action, tc.node, tc.netem)
			if err == nil {
				t.Fatalf("Apply: got nil error, want %v", tc.want)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("Apply error: got %q, want it to contain %q", err, w)
				}
			}
			if len(fd.calls) != 0 {
				t.Errorf("docker calls: got %v, want none", fd.calls)
			}
		})
	}
}

func TestStateLifecycle(t *testing.T) {
	fd := &fakeDocker{}
	m, dir := newManager(t, fd)
	ctx := context.Background()
	steps := []struct {
		action Action
		node   string
		netem  NetemOptions
	}{
		{StopKubelet, "worker", NetemOptions{}},
		{Netem, "worker2", NetemOptions{Delay: time.Second}},
		{Pause, "worker", NetemOptions{}},
		{Netem, "worker2", NetemOptions{Loss: 5}},
	}
	for _, s := range steps {
		if err := m.Apply(ctx, s.action, s.node, s.netem); err != nil {
			t.Fatalf("Apply %s %s: %v", s.action, s.node, err)
		}
	}
	if err := m.Apply(ctx, Pause, "worker", NetemOptions{}); !errors.Is(err, ErrAlreadyActive) {
		t.Errorf("second pause of worker: got %v, want ErrAlreadyActive", err)
	}
	if got := len(fd.calls); got != 4 {
		t.Errorf("docker calls: got %d, want 4 (second pause runs nothing)", got)
	}
	active, err := m.Active(context.Background())
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	var got []string
	for _, f := range active {
		got = append(got, string(f.Action)+" "+f.Node)
	}
	if want := []string{"stop-kubelet worker", "pause worker", "netem worker2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Active: got %v, want %v", got, want)
	}
	if active[2].Netem != (NetemOptions{Loss: 5}) {
		t.Errorf("netem options: got %+v, want loss 5 only", active[2].Netem)
	}

	reloaded := New("kubedrill", dir, WithRunner(fd.run))
	again, err := reloaded.Active(context.Background())
	if err != nil || !reflect.DeepEqual(again, active) {
		t.Errorf("Active from a new Manager: got (%v, %v), want %v", again, err, active)
	}

	fd.calls = nil
	if err := m.Apply(ctx, Unpause, "worker", NetemOptions{}); err != nil {
		t.Fatalf("Apply unpause: %v", err)
	}
	active, _ = m.Active(context.Background())
	if len(active) != 2 {
		t.Errorf("after unpause: got %v, want two faults", active)
	}

	fd.calls = nil
	if err := m.Heal(ctx); err != nil {
		t.Fatalf("Heal: %v", err)
	}
	want := []call{
		{"docker", "exec", "kubedrill-worker2", "tc", "qdisc", "del", "dev", "eth0", "root"},
		{"docker", "exec", "kubedrill-worker", "systemctl", "start", "kubelet"},
	}
	if !reflect.DeepEqual(fd.calls, want) {
		t.Errorf("Heal calls: got %v, want %v", fd.calls, want)
	}
	if _, err := os.Stat(filepath.Join(dir, stateFile)); !os.IsNotExist(err) {
		t.Errorf("state file after Heal: got %v, want it removed", err)
	}
	if active, err := m.Active(context.Background()); err != nil || len(active) != 0 {
		t.Errorf("Active after Heal: got (%v, %v), want none", active, err)
	}
}

func TestHealOrderAndTolerance(t *testing.T) {
	fd := &fakeDocker{fail: map[string]fakeResult{
		"unpause kubedrill-worker":                             {out: "Error response from daemon: Container abc is not paused", err: errExit},
		"exec kubedrill-worker2 tc qdisc del dev eth0 root":    {out: "Error: Cannot delete qdisc with handle of zero.", err: errExit},
		"exec kubedrill-control-plane systemctl start kubelet": {out: "Error response from daemon: No such container: kubedrill-control-plane", err: errExit},
	}}
	m, _ := newManager(t, fd)
	ctx := context.Background()
	for _, s := range []struct {
		a Action
		n string
		o NetemOptions
	}{{StopKubelet, "control-plane", NetemOptions{}}, {Netem, "worker2", NetemOptions{Delay: time.Millisecond}}, {Pause, "worker", NetemOptions{}}} {
		if err := m.Apply(ctx, s.a, s.n, s.o); err != nil {
			t.Fatalf("Apply %s: %v", s.a, err)
		}
	}
	fd.calls = nil
	if err := m.Heal(ctx); err != nil {
		t.Fatalf("Heal: got %v, want already-healed faults tolerated", err)
	}
	want := []call{
		{"docker", "unpause", "kubedrill-worker"},
		{"docker", "exec", "kubedrill-worker2", "tc", "qdisc", "del", "dev", "eth0", "root"},
		{"docker", "exec", "kubedrill-control-plane", "systemctl", "start", "kubelet"},
	}
	if !reflect.DeepEqual(fd.calls, want) {
		t.Errorf("Heal order: got %v, want %v", fd.calls, want)
	}
}

func TestHealKeepsFailures(t *testing.T) {
	fd := &fakeDocker{fail: map[string]fakeResult{
		"exec kubedrill-worker systemctl start kubelet": {out: "Failed to start kubelet.service: timeout", err: errExit},
	}}
	m, _ := newManager(t, fd)
	ctx := context.Background()
	if err := m.Apply(ctx, StopKubelet, "worker", NetemOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(ctx, Pause, "worker2", NetemOptions{}); err != nil {
		t.Fatal(err)
	}
	err := m.Heal(ctx)
	if err == nil || !strings.Contains(err.Error(), "heal stop-kubelet on worker: docker exec kubedrill-worker systemctl start kubelet: Failed to start kubelet.service: timeout") {
		t.Fatalf("Heal: got %v, want the kubelet failure", err)
	}
	if !errors.Is(err, errExit) {
		t.Errorf("Heal error %v does not wrap the runner error", err)
	}
	active, _ := m.Active(context.Background())
	if len(active) != 1 || active[0].Action != StopKubelet {
		t.Errorf("Active after partial heal: got %v, want only stop-kubelet worker", active)
	}
}

func TestApplyErrors(t *testing.T) {
	tests := []struct {
		name   string
		action Action
		netem  NetemOptions
		result fakeResult
		key    string
		want   string
		is     error
	}{
		{name: "netem unavailable", action: Netem, netem: NetemOptions{Delay: time.Millisecond}, key: "exec kubedrill-worker tc qdisc replace dev eth0 root netem delay 1000us", result: fakeResult{out: "Error: Specified qdisc kind is unknown.", err: errExit}, want: "fault netem on worker: tc netem is not available in this Docker VM kernel (sch_netem missing): Error: Specified qdisc kind is unknown.", is: ErrNetemUnavailable},
		{name: "pause on a missing node", action: Pause, key: "pause kubedrill-worker", result: fakeResult{out: "Error response from daemon: No such container: kubedrill-worker", err: errExit}, want: "fault pause on worker: docker pause kubedrill-worker: Error response from daemon: No such container", is: errExit},
		{name: "clear netem already clear", action: ClearNetem, key: "exec kubedrill-worker tc qdisc del dev eth0 root", result: fakeResult{out: "Error: Cannot delete qdisc with handle of zero.", err: errExit}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fd := &fakeDocker{fail: map[string]fakeResult{tc.key: tc.result}}
			m, _ := newManager(t, fd)
			err := m.Apply(context.Background(), tc.action, "worker", tc.netem)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Apply: got %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.HasPrefix(err.Error(), tc.want) {
				t.Fatalf("Apply: got %v, want prefix %q", err, tc.want)
			}
			if !errors.Is(err, tc.is) {
				t.Errorf("Apply error %v does not wrap %v", err, tc.is)
			}
			if active, _ := m.Active(context.Background()); len(active) != 0 {
				t.Errorf("Active after failed Apply: got %v, want none", active)
			}
		})
	}
}

func TestCorruptState(t *testing.T) {
	fd := &fakeDocker{}
	m, dir := newManager(t, fd)
	if err := os.WriteFile(filepath.Join(dir, stateFile), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, stateFile)
	if _, err := m.Active(context.Background()); err == nil || !strings.Contains(err.Error(), "parse fault state") || !strings.Contains(err.Error(), "remove "+path+" after checking the nodes (docker ps)") {
		t.Errorf("Active: got %v, want a parse error naming the file to remove", err)
	}
	if err := m.Heal(context.Background()); err == nil {
		t.Errorf("Heal: got nil error on corrupt state")
	}
}

func TestApplyUnknownNodeSentinel(t *testing.T) {
	m, _ := newManager(t, &fakeDocker{})
	err := m.Apply(context.Background(), Pause, "worker9", NetemOptions{})
	if !errors.Is(err, ErrUnknownNode) || !strings.Contains(err.Error(), "control-plane, worker, worker2") {
		t.Errorf("Apply on worker9: got %v, want ErrUnknownNode listing the valid nodes", err)
	}
	if !strings.Contains(err.Error(), `"worker9"`) {
		t.Errorf("Apply on worker9: got %v, want the bad node named", err)
	}
	err = m.Apply(context.Background(), "explode", "worker", NetemOptions{})
	if errors.Is(err, ErrUnknownNode) || err == nil || !strings.Contains(err.Error(), `action "explode" is not one of`) {
		t.Errorf("Apply with a bad action on a valid node: got %v, want the bad action named and no ErrUnknownNode", err)
	}
}

func TestDockerCallsAreBounded(t *testing.T) {
	var left time.Duration
	m := New("kubedrill", t.TempDir(), WithRunner(func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		if d, ok := ctx.Deadline(); ok {
			left = time.Until(d)
		}
		return nil, nil
	}))
	if err := m.Apply(context.Background(), Pause, "worker", NetemOptions{}); err != nil {
		t.Fatal(err)
	}
	if left <= 25*time.Second || left > 30*time.Second {
		t.Errorf("docker deadline: got %v left, want about 30s", left)
	}
}

func TestStateFileModes(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "state")
	m := New("kubedrill", dir, WithRunner((&fakeDocker{}).run))
	if err := m.Apply(context.Background(), Pause, "worker", NetemOptions{}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "faults.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("faults.json: got %v, %v, want mode 0600", info, err)
	}
	dinfo, err := os.Stat(dir)
	if err != nil || dinfo.Mode().Perm() != 0o700 {
		t.Errorf("state dir: got %v, %v, want mode 0700", dinfo, err)
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != "faults.json,faults.lock" {
		t.Errorf("state dir entries: got %v, want faults.json and faults.lock (no temp file left)", names)
	}
}

func TestApplyRecordsIntentBeforeDocker(t *testing.T) {
	var m *Manager
	var during []Fault
	runner := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		state, err := os.ReadFile(filepath.Join(m.stateDir, stateFile))
		if err == nil {
			var fs []Fault
			if json.Unmarshal(state, &fs) == nil {
				during = fs
			}
		}
		if args[0] == "pause" {
			return []byte("daemon gone"), errExit
		}
		return nil, nil
	}
	m = New("kubedrill", t.TempDir(), WithRunner(runner))
	if err := m.Apply(context.Background(), Pause, "worker", NetemOptions{}); err == nil {
		t.Fatal("Apply with a failing docker: got nil error")
	}
	if len(during) != 1 || during[0].Action != Pause || during[0].Node != "worker" {
		t.Errorf("state during the docker call: got %v, want the pause recorded first", during)
	}
	if active, err := m.Active(context.Background()); err != nil || len(active) != 0 {
		t.Errorf("Active after the failed call: got %v, %v, want the intent dropped", active, err)
	}
}

func TestLockSerializesProcesses(t *testing.T) {
	dir := t.TempDir()
	const n = 8
	nodes := []string{"control-plane", "worker", "worker2"}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := New("kubedrill", dir, WithRunner((&fakeDocker{}).run))
			if err := m.Apply(context.Background(), Netem, nodes[i%len(nodes)], NetemOptions{Delay: time.Duration(i+1) * time.Millisecond}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	active, err := New("kubedrill", dir).Active(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != len(nodes) {
		t.Errorf("concurrent Apply from separate managers: got %d faults %v, want one netem per node", len(active), active)
	}
}

func TestActionsListsEveryAction(t *testing.T) {
	got := Actions()
	want := []Action{Pause, Unpause, StopKubelet, StartKubelet, Netem, ClearNetem}
	if !slices.Equal(got, want) {
		t.Errorf("Actions: got %v, want %v", got, want)
	}
}

func TestApplyKeepsIntentWhenOutcomeUnknown(t *testing.T) {
	tests := []struct {
		name     string
		run      func(ctx context.Context, cancel context.CancelFunc) ([]byte, error)
		wantKept bool
		wantIs   error
	}{
		{name: "docker exits non-zero", run: func(context.Context, context.CancelFunc) ([]byte, error) {
			return []byte("Error response from daemon: boom"), errExit
		}, wantIs: errExit},
		{name: "docker killed by cancel", run: func(ctx context.Context, cancel context.CancelFunc) ([]byte, error) {
			cancel()
			<-ctx.Done()
			return nil, errExit
		}, wantKept: true, wantIs: errExit},
		{name: "runner returns the context error", run: func(ctx context.Context, cancel context.CancelFunc) ([]byte, error) {
			cancel()
			<-ctx.Done()
			return nil, ctx.Err()
		}, wantKept: true, wantIs: context.Canceled},
		{name: "docker not started", run: func(context.Context, context.CancelFunc) ([]byte, error) {
			return nil, errors.New("fork/exec docker: resource temporarily unavailable")
		}, wantKept: true},
		{name: "docker binary missing", run: func(context.Context, context.CancelFunc) ([]byte, error) {
			return nil, &exec.Error{Name: "docker", Err: exec.ErrNotFound}
		}, wantIs: exec.ErrNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			healing := false
			var healCalls []call
			runner := func(rctx context.Context, name string, args ...string) ([]byte, error) {
				if healing {
					healCalls = append(healCalls, append(call{name}, args...))
					return []byte("Error response from daemon: Container abc is not paused"), errExit
				}
				return tc.run(rctx, cancel)
			}
			m := New("kubedrill", t.TempDir(), WithRunner(runner))
			err := m.Apply(ctx, Pause, "worker", NetemOptions{})
			if err == nil {
				t.Fatal("Apply: got nil error, want the docker failure")
			}
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Errorf("Apply error %v does not wrap %v", err, tc.wantIs)
			}
			active, aerr := m.Active(context.Background())
			if aerr != nil {
				t.Fatal(aerr)
			}
			if got := len(active) == 1; got != tc.wantKept {
				t.Fatalf("intent record kept: got %v (%v), want %v", got, active, tc.wantKept)
			}
			if tc.wantKept && !strings.Contains(err.Error(), "kubedrill heal") {
				t.Errorf("Apply error: got %q, want it to name the heal command", err)
			}
			if !tc.wantKept {
				return
			}
			healing = true
			if err := m.Heal(context.Background()); err != nil {
				t.Fatalf("Heal of a fault that was never applied: got %v, want nil", err)
			}
			if want := []call{{"docker", "unpause", "kubedrill-worker"}}; !reflect.DeepEqual(healCalls, want) {
				t.Errorf("heal calls: got %v, want %v", healCalls, want)
			}
			if active, _ := m.Active(context.Background()); len(active) != 0 {
				t.Errorf("Active after Heal: got %v, want none", active)
			}
		})
	}
}

func TestForgetDropsRecordsWithoutDocker(t *testing.T) {
	fd := &fakeDocker{}
	m, _ := newManager(t, fd)
	if err := m.Apply(context.Background(), Pause, "worker", NetemOptions{}); err != nil {
		t.Fatal(err)
	}
	fd.calls = nil
	if err := m.Forget(context.Background()); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if len(fd.calls) != 0 {
		t.Errorf("Forget ran docker: %v", fd.calls)
	}
	if active, err := m.Active(context.Background()); err != nil || len(active) != 0 {
		t.Errorf("Active after Forget: %v, %v, want none", active, err)
	}
	if err := m.Apply(context.Background(), Pause, "worker", NetemOptions{}); err != nil {
		t.Errorf("Apply after Forget: %v, want the pause recorded again", err)
	}
}
