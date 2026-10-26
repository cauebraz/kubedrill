package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/cauebraz/kubedrill/internal/cluster"
	"github.com/cauebraz/kubedrill/internal/journal"
	"github.com/cauebraz/kubedrill/internal/scenario"
)

const unreachableKubeconfig = `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://127.0.0.1:1
  name: kind-kubedrill
contexts:
- context:
    cluster: kind-kubedrill
    user: kind-kubedrill
  name: kind-kubedrill
current-context: kind-kubedrill
users:
- name: kind-kubedrill
  user:
    token: x
`

func TestWorkstationClusterRefusesLifecycle(t *testing.T) {
	w := &workstationCluster{}
	ctx := context.Background()
	tests := []struct {
		name string
		call func() error
		want string
	}{
		{name: "Ensure", call: func() error { return w.Ensure(ctx, "base", cluster.ProfileData{}) }, want: "kubedrill up runs on the host, not inside the workstation"},
		{name: "EnsureAddons", call: func() error { return w.EnsureAddons(ctx, nil, []string{"metrics-server"}) }, want: "kubedrill up runs on the host, not inside the workstation"},
		{name: "AuditEnabled", call: func() error { _, err := w.AuditEnabled(ctx); return err }, want: "the audit check runs on the host, not inside the workstation"},
		{name: "Delete", call: func() error { return w.Delete(ctx) }, want: "kubedrill down runs on the host, not inside the workstation"},
		{name: "Doctor", call: func() error { _, err := w.Doctor(ctx); return err }, want: "kubedrill doctor runs on the host, not inside the workstation"},
		{name: "WriteInternalKubeconfig", call: func() error { return w.WriteInternalKubeconfig(ctx, "/tmp/x") }, want: "kubedrill shell runs on the host, not inside the workstation"},
		{name: "Created", call: func() error { _, err := w.Created(ctx); return err }, want: "the cluster creation time runs on the host, not inside the workstation"},
		{name: "RenderProfile", call: func() error { _, err := w.RenderProfile("base", cluster.ProfileData{}); return err }, want: "rendering a cluster profile runs on the host, not inside the workstation"},
		{name: "AuditPolicyStale", call: func() error { _, err := w.AuditPolicyStale(ctx); return err }, want: "the audit policy check runs on the host, not inside the workstation"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil || err.Error() != tc.want {
				t.Errorf("%s: got %v, want %q", tc.name, err, tc.want)
			}
		})
	}
}

type fakeAddons struct{}

func (fakeAddons) Addons() []string     { return []string{"gateway", "metrics-server"} }
func (fakeAddons) BaseAddons() []string { return []string{"metrics-server"} }

func TestWorkstationClusterKubeconfigAndAddons(t *testing.T) {
	w := &workstationCluster{addons: fakeAddons{}, kubeconfig: "/home/learner/.kube/config"}
	path, name, err := w.Kubeconfig()
	if err != nil || path != "/home/learner/.kube/config" || name != "" {
		t.Errorf("Kubeconfig: got (%q, %q, %v), want the mounted kubeconfig and an empty context", path, name, err)
	}
	if _, _, err := (&workstationCluster{}).Kubeconfig(); err == nil {
		t.Errorf("Kubeconfig without KUBECONFIG: got nil error, want one")
	}
	if got := w.Addons(); strings.Join(got, ",") != "gateway,metrics-server" {
		t.Errorf("Addons: got %v, want the repository addons", got)
	}
	if got := w.BaseAddons(); strings.Join(got, ",") != "metrics-server" {
		t.Errorf("BaseAddons: got %v, want the base addons", got)
	}
	if got := (&workstationCluster{}).Addons(); got != nil {
		t.Errorf("Addons without a lister: got %v, want nil", got)
	}
}

const signalHelperEnv = "KUBEDRILL_TEST_SIGNAL_HELPER"

func TestInterruptContextSecondSignalKills(t *testing.T) {
	if os.Getenv(signalHelperEnv) == "1" {
		ctx, stop := interruptContext(context.Background())
		defer stop()
		if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
			os.Exit(3)
		}
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
			os.Exit(4)
		}
		for i := 0; i < 200; i++ {
			_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
			time.Sleep(10 * time.Millisecond)
		}
		os.Exit(0)
	}
	cmd := exec.Command(os.Args[0], "-test.run", "^TestInterruptContextSecondSignalKills$")
	cmd.Env = append(os.Environ(), signalHelperEnv+"=1")
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("helper: got %v, want it killed by the second SIGINT", err)
	}
	ws, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() || ws.Signal() != syscall.SIGINT {
		t.Errorf("helper exit: got %v, want killed by SIGINT after the context was canceled", exitErr)
	}
}

func TestInterruptContextStopReleases(t *testing.T) {
	ctx, stop := interruptContext(context.Background())
	stop()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Errorf("context after stop: got not done, want done")
	}
}

func TestWorkstationClusterExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(path, []byte(unreachableKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	w := &workstationCluster{kubeconfig: path}
	ok, err := w.Exists(context.Background())
	if ok || err == nil {
		t.Fatalf("Exists against an unreachable server: got (%v, %v), want (false, error)", ok, err)
	}
	if !strings.Contains(err.Error(), "ping cluster") {
		t.Errorf("Exists error: got %q, want the ping error", err.Error())
	}

	w = &workstationCluster{kubeconfig: filepath.Join(dir, "missing")}
	if ok, err := w.Exists(context.Background()); ok || err == nil {
		t.Errorf("Exists with a missing kubeconfig: got (%v, %v), want (false, error)", ok, err)
	}
}

func TestFactoriesReadWorkstationThroughGetenv(t *testing.T) {
	env := map[string]string{workstationEnv: "1", "KUBECONFIG": "/tmp/kubeconfig"}
	f := factories(io.Discard, func(k string) string { return env[k] }, time.Now, resolveStateDir())
	if _, ok := f.Cluster(t.TempDir()).(*workstationCluster); !ok {
		t.Errorf("Cluster inside the workstation: got %T, want *workstationCluster", f.Cluster(t.TempDir()))
	}
	got, err := f.ScriptKubeconfig(t.TempDir(), "/other", "ctx")
	if err != nil || got != "/tmp/kubeconfig" {
		t.Errorf("ScriptKubeconfig inside the workstation: got (%q, %v), want /tmp/kubeconfig from getenv", got, err)
	}

	if fm := f.Faults(t.TempDir()); fm != nil {
		t.Errorf("Faults inside the workstation: got %T, want nil (node containers are not reachable)", fm)
	}

	f = factories(io.Discard, func(string) string { return "" }, time.Now, resolveStateDir())
	if _, ok := f.Cluster(t.TempDir()).(*workstationCluster); ok {
		t.Errorf("Cluster on the host: got *workstationCluster, want the kind manager")
	}
	if fm := f.Faults(t.TempDir()); fm == nil {
		t.Errorf("Faults on the host: got nil, want the fault manager")
	}
}

const auditLine = `{"kind":"Event","apiVersion":"audit.k8s.io/v1","level":"Metadata","stage":"ResponseComplete","verb":"%s","user":{"username":"kubernetes-admin"},"userAgent":"kubectl/v1.37.0","objectRef":{"resource":"%s","apiGroup":"%s","apiVersion":"v1","namespace":"task-hello-ab12c","name":"%s"},"requestReceivedTimestamp":"%s","stageTimestamp":"%s","responseStatus":{"code":200}}`

func writeJournalFiles(t *testing.T, dir string, start time.Time) {
	t.Helper()
	for _, d := range []string{"audit", "shell", "recordings"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	at := func(sec int) string {
		return start.Add(time.Duration(sec) * time.Second).UTC().Format(time.RFC3339Nano)
	}
	audit := strings.Join([]string{
		fmt.Sprintf(auditLine, "get", "pods", "", "hello-1", at(5), at(5)),
		fmt.Sprintf(auditLine, "patch", "deployments", "apps", "hello", at(30), at(30)),
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "audit", "audit.log"), []byte(audit), 0o600); err != nil {
		t.Fatal(err)
	}
	shell := fmt.Sprintf("{\"ts\":%q,\"cmd\":\"kubectl get pods\",\"exit\":0,\"ms\":10}\n{\"ts\":%q,\"cmd\":\"kubectl set image deploy/hello app=nginx:1.27\",\"exit\":0,\"ms\":10}\n", at(4), at(29))
	if err := os.WriteFile(filepath.Join(dir, "shell", "commands.jsonl"), []byte(shell), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		fmt.Sprintf("task-hello-ab12c-%d.cast", start.Unix()+60),
		fmt.Sprintf("task-hello-ab12c-%d.cast", start.Unix()+1),
		fmt.Sprintf("task-hello-ab12c-%d.cast", start.Unix()-3600),
		fmt.Sprintf("task-other-zz999-%d.cast", start.Unix()+2),
	} {
		if err := os.WriteFile(filepath.Join(dir, "recordings", name), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEpisodeTelemetry(t *testing.T) {
	start := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Minute)
	sc := &scenario.Scenario{Meta: scenario.Meta{ID: "000-hello-down", FixTargets: []string{"apps/deployments/hello"}}}
	rd := &scenario.Rendered{Scenario: sc, Namespace: "task-hello-ab12c"}
	ep := journal.Episode{ScenarioID: sc.Meta.ID, Namespace: "task-hello-ab12c", StartedAt: start, EndedAt: &end}
	tests := []struct {
		name          string
		audit         bool
		inWorkstation bool
		wantSource    string
		wantVerdict   journal.Verdict
		wantTarget    string
	}{
		{name: "audit on", audit: true, wantSource: "audit+shell", wantVerdict: "root-cause", wantTarget: "apps/deployments/hello"},
		{name: "audit off on the host", audit: false, wantSource: "shell", wantVerdict: "unknown"},
		{name: "workstation with an audit file", audit: false, inWorkstation: true, wantSource: "audit+shell", wantVerdict: "root-cause", wantTarget: "apps/deployments/hello"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeJournalFiles(t, dir, start)
			got := episodeTelemetry(io.Discard, dir, filepath.Join(dir, "audit", "audit.log"), ep, rd, func() bool { return tc.audit }, tc.inWorkstation, end.Add(time.Hour))
			if got.Source != tc.wantSource || got.Verdict != tc.wantVerdict || got.FirstWriteTarget != tc.wantTarget {
				t.Errorf("telemetry: got source %q verdict %q target %q, want %q %q %q", got.Source, got.Verdict, got.FirstWriteTarget, tc.wantSource, tc.wantVerdict, tc.wantTarget)
			}
			if len(got.Commands) != 2 || got.Commands[0] != "kubectl get pods" {
				t.Errorf("commands: got %q, want the two kubectl lines in order", got.Commands)
			}
			want := []string{
				filepath.Join(dir, "recordings", fmt.Sprintf("task-hello-ab12c-%d.cast", start.Unix()+1)),
				filepath.Join(dir, "recordings", fmt.Sprintf("task-hello-ab12c-%d.cast", start.Unix()+60)),
			}
			if strings.Join(got.Recordings, ",") != strings.Join(want, ",") {
				t.Errorf("recordings: got %q, want %q", got.Recordings, want)
			}
		})
	}
}

func TestEpisodeTelemetryWithoutFiles(t *testing.T) {
	start := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	ep := journal.Episode{Namespace: "task-hello-ab12c", StartedAt: start}
	dir := t.TempDir()
	got := episodeTelemetry(io.Discard, dir, filepath.Join(dir, "audit.log"), ep, nil, func() bool { return false }, false, start.Add(time.Minute))
	if got.Source != "none" || got.Verdict != "unknown" || len(got.Recordings) != 0 {
		t.Errorf("no files: got %+v, want source none, verdict unknown, no recordings", got)
	}
}

func TestEpisodeTelemetryAuditOnButMissing(t *testing.T) {
	start := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Minute)
	ep := journal.Episode{Namespace: "task-hello-ab12c", StartedAt: start, EndedAt: &end}
	dir := t.TempDir()
	got := episodeTelemetry(io.Discard, dir, filepath.Join(dir, "missing", "audit.log"), ep, nil, func() bool { return true }, false, end)
	if got.Source != "none" || got.Verdict != "unknown" {
		t.Errorf("audit on, log missing: got source %q verdict %q, want none and unknown (not no-writes)", got.Source, got.Verdict)
	}
}

func TestTelemetryReadsClusterAuditLog(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	start := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	journalDir := t.TempDir()
	writeJournalFiles(t, journalDir, start)
	auditDir := filepath.Join(state, "kubedrill", "kubedrill", "audit")
	if err := os.MkdirAll(auditDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(journalDir, "audit", "audit.log"), filepath.Join(auditDir, "audit.log")); err != nil {
		t.Fatal(err)
	}
	sc := &scenario.Scenario{Meta: scenario.Meta{ID: "000-hello-down", FixTargets: []string{"apps/deployments/hello"}}}
	rd := &scenario.Rendered{Scenario: sc, Namespace: "task-hello-ab12c"}
	ep := journal.Episode{Namespace: "task-hello-ab12c", StartedAt: start}
	clock := start.Add(2 * time.Minute)
	f := factories(io.Discard, func(string) string { return "" }, func() time.Time { return clock }, resolveStateDir())
	got := f.Telemetry(journalDir, func() bool { return true })(ep, rd)
	if got.Verdict != "root-cause" || got.Source != "audit+shell" {
		t.Errorf("telemetry from the cluster audit log: got source %q verdict %q, want audit+shell root-cause", got.Source, got.Verdict)
	}
	clock = start.Add(10 * time.Second)
	if got := f.Telemetry(journalDir, func() bool { return true })(ep, rd); got.Writes != 0 {
		t.Errorf("telemetry window ending at the injected clock (10s): got %d writes, want 0", got.Writes)
	}

	envLog := filepath.Join(t.TempDir(), "audit.log")
	f = factories(io.Discard, func(k string) string {
		if k == "KUBEDRILL_AUDIT_LOG" {
			return envLog
		}
		return ""
	}, func() time.Time { return start.Add(time.Hour) }, resolveStateDir())
	if got := f.Telemetry(journalDir, func() bool { return true })(ep, rd); got.Verdict != "unknown" {
		t.Errorf("KUBEDRILL_AUDIT_LOG pointing at a missing file: got verdict %q, want unknown", got.Verdict)
	}
}

func TestConnectScriptChecksUsePinnedKubeconfigAndWrapper(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	bin := t.TempDir()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found")
	}
	if err := os.Symlink(bash, filepath.Join(bin, "kubectl")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	dir := t.TempDir()
	kc := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(kc, []byte(unreachableKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	f := factories(io.Discard, func(string) string { return "" }, time.Now, resolveStateDir())
	_, g, err := f.Connect(kc, "kind-kubedrill", "/pinned/kubeconfig")
	if err != nil {
		t.Fatal(err)
	}
	script := `test "$KUBECONFIG" = /pinned/kubeconfig && kubectl -c 'ps -o args= -p $$; true'`
	rd := &scenario.Rendered{Scenario: &scenario.Scenario{Dir: dir}, Namespace: "task-x", Checks: []scenario.Check{{ID: "s", Script: script, Matches: "^kubedrill-check "}}}
	grade, err := g.Grade(context.Background(), rd)
	if err != nil {
		t.Fatal(err)
	}
	if !grade.Passed {
		t.Errorf("script check: got %+v, want a pass (pinned KUBECONFIG and kubectl argv[0] kubedrill-check)", grade.Results)
	}
}

func TestScriptKubeconfigNeedsTheKindContext(t *testing.T) {
	f := factories(io.Discard, func(string) string { return "" }, time.Now, resolveStateDir())
	dir := t.TempDir()
	learner := filepath.Join(dir, "config")
	other := "apiVersion: v1\nkind: Config\ncontexts:\n- name: prod\n  context:\n    cluster: prod\n    user: prod\nclusters:\n- name: prod\n  cluster:\n    server: https://prod.example:6443\nusers:\n- name: prod\n  user:\n    token: secret\ncurrent-context: prod\n"
	if err := os.WriteFile(learner, []byte(other), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := f.ScriptKubeconfig(dir, learner, "kind-kubedrill")
	want := "context kind-kubedrill not found in " + learner + "; run kubedrill up"
	if err == nil || err.Error() != want || got != "" {
		t.Errorf("ScriptKubeconfig without the kind context: got (%q, %v), want error %q and no path", got, err, want)
	}
}
