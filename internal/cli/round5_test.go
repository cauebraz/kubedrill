package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cauebraz/kubedrill/internal/grader"
	"github.com/cauebraz/kubedrill/internal/scenario"
)

func TestLazyConnNeedsThePinnedKubeconfig(t *testing.T) {
	tests := []struct {
		name       string
		script     func(journalDir, kubeconfig, contextName string) (string, error)
		nilFactory bool
		wantErr    string
		wantScript string
	}{
		{name: "factory error", script: func(string, string, string) (string, error) { return "", errors.New("no kind context") }, wantErr: "pinned to that context"},
		{name: "empty path", script: func(string, string, string) (string, error) { return "", nil }, wantErr: "no path returned"},
		{name: "pinned", script: func(dir, _, _ string) (string, error) { return dir + "/pinned", nil }, wantScript: "/pinned"},
		{name: "no factory", nilFactory: true, wantScript: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			fs := f.factories()
			fs.ScriptKubeconfig = tt.script
			if tt.nilFactory {
				fs.ScriptKubeconfig = nil
			}
			var connected []string
			fs.Connect = func(_, _, script string) (Kube, Grader, error) {
				connected = append(connected, script)
				return f.kube, f.grader, nil
			}
			a := newApp(Options{Factories: fs, Getenv: func(string) string { return "" }, Getwd: func() (string, error) { return f.root, nil }})
			conn, err := a.getConn()
			if err != nil {
				t.Fatal(err)
			}
			_, err = conn.NamespaceLabels(context.Background(), "ns")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) || len(connected) != 0 {
					t.Errorf("got err %v connects %q, want error %q and no connect", err, connected, tt.wantErr)
				}
				return
			}
			if err != nil || len(connected) != 1 || !strings.HasSuffix(connected[0], tt.wantScript) || (tt.wantScript == "") != (connected[0] == "") {
				t.Errorf("got err %v connects %q, want one connect with script ending %q", err, connected, tt.wantScript)
			}
		})
	}
}

type staticKube struct {
	*fakeKube
}

func (staticKube) NamespaceLabels(context.Context, string) (map[string]string, error) {
	return map[string]string{"app.kubernetes.io/managed-by": "kubedrill"}, nil
}

type staticGrader struct{}

func (staticGrader) Grade(context.Context, *scenario.Rendered) (grader.Grade, error) {
	return passGrade(), nil
}

func TestLazyConnOpensOnceUnderConcurrency(t *testing.T) {
	f := newFixture(t)
	fs := f.factories()
	var connects, failures atomic.Int32
	failures.Store(1)
	fs.Connect = func(string, string, string) (Kube, Grader, error) {
		connects.Add(1)
		if failures.Add(-1) >= 0 {
			return nil, nil, errors.New("apiserver not ready")
		}
		return staticKube{&fakeKube{}}, staticGrader{}, nil
	}
	a := newApp(Options{Factories: fs, Getenv: func(string) string { return "" }, Getwd: func() (string, error) { return f.root, nil }})
	conn, err := a.getConn()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Grade(context.Background(), &scenario.Rendered{}); err == nil {
		t.Fatalf("first open: got no error, want the connect failure")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var err error
			if i%2 == 0 {
				_, err = conn.Grade(context.Background(), &scenario.Rendered{})
			} else {
				_, err = conn.NamespaceLabels(context.Background(), "ns")
			}
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent call: %v", err)
		}
	}
	if got := connects.Load(); got != 2 {
		t.Errorf("connects: got %d, want 2 (one failure not cached, one success shared)", got)
	}
}

type ctxCluster struct {
	*fakeCluster
}

func (c ctxCluster) AuditEnabled(ctx context.Context) (bool, error) {
	c.auditAsks++
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return c.audit, c.auditErr
}

func TestAuditStateCachesOnlyAnswers(t *testing.T) {
	f := newFixture(t)
	fs := f.factories()
	fs.Cluster = func(string) Cluster { return ctxCluster{f.cluster} }
	a := newApp(Options{Factories: fs, Getenv: func(string) string { return "" }, Getwd: func() (string, error) { return f.root, nil }})
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	f.cluster.audit = true
	steps := []struct {
		ctx       context.Context
		auditErr  error
		wantOn    bool
		wantKnown bool
		wantAsks  int
	}{
		{cancelled, nil, false, false, 1},
		{context.Background(), errors.New("docker exec failed"), false, false, 2},
		{context.Background(), nil, true, true, 3},
		{cancelled, errors.New("not asked again"), true, true, 3},
	}
	for i, s := range steps {
		f.cluster.auditErr = s.auditErr
		on, known := a.auditState(s.ctx)
		if on != s.wantOn || known != s.wantKnown || f.cluster.auditAsks != s.wantAsks {
			t.Errorf("step %d: got on %v known %v asks %d, want %v %v %d", i, on, known, f.cluster.auditAsks, s.wantOn, s.wantKnown, s.wantAsks)
		}
	}
}

type cleanupKube struct {
	*fakeKube
	applyErr error
	mu       sync.Mutex
	live     []string
}

func (k *cleanupKube) record(ctx context.Context, call string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if ctx.Err() == nil {
		k.live = append(k.live, call)
	}
}

func (k *cleanupKube) DeleteNamespace(ctx context.Context, ns string) error {
	k.record(ctx, "delete "+ns)
	return k.fakeKube.DeleteNamespace(ctx, ns)
}

func (k *cleanupKube) DeleteNamespaces(ctx context.Context, selector, keep string) (int, error) {
	k.record(ctx, "gc "+selector+" keep "+keep)
	return k.fakeKube.DeleteNamespaces(ctx, selector, keep)
}

func (k *cleanupKube) ApplyManifests(ctx context.Context, ns string, m []string) error {
	if k.applyErr != nil {
		return k.applyErr
	}
	return k.fakeKube.ApplyManifests(ctx, ns, m)
}

func TestSmokeCleansUpAfterCancelAndFailedReset(t *testing.T) {
	sweep := "gc app.kubernetes.io/managed-by=kubedrill,kubedrill.io/journal=0123abcd keep "
	tests := []struct {
		name      string
		applyErr  error
		cancelRun bool
		wantErr   string
		wantLive  []string
	}{
		{name: "cancelled during solve.sh", cancelRun: true, wantErr: "context canceled", wantLive: []string{"delete task-hello-abcde", sweep}},
		{name: "failed reset", applyErr: errors.New("admission webhook denied"), wantErr: "2 of 2 runs failed", wantLive: []string{sweep}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.grader.grades = []grader.Grade{failGrade(), passGrade(), failGrade(), passGrade()}
			k := &cleanupKube{fakeKube: f.kube, applyErr: tt.applyErr}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fs := f.factories()
			fs.Connect = func(string, string, string) (Kube, Grader, error) { return k, f.grader, nil }
			fs.Run = func(context.Context, string, []string, io.Writer) error {
				if tt.cancelRun {
					cancel()
				}
				return nil
			}
			var out strings.Builder
			a := newApp(Options{Factories: fs, Stdout: &out, Stderr: &out, Getenv: func(string) string { return "" }, Getwd: func() (string, error) { return f.root, nil }, NamespaceSuffix: func() string { return "abcde" }})
			err := a.runSmoke(ctx, []string{"001"}, true, false)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("runSmoke: got %v, want error containing %q", err, tt.wantErr)
			}
			var cleanup []string
			for _, c := range k.live {
				if strings.HasPrefix(c, "delete ") || strings.HasSuffix(c, " keep ") {
					cleanup = append(cleanup, c)
				}
			}
			if strings.Join(cleanup, "|") != strings.Join(tt.wantLive, "|") {
				t.Errorf("cleanup calls under a live context: got %q, want %q\n%s", cleanup, tt.wantLive, out.String())
			}
		})
	}
}
