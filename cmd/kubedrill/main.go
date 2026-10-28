// Command kubedrill is the learner's entry point: it wires the real cluster,
// kube, grader, scenario, journal, faults, telemetry and curriculum packages
// into the cli.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cauebraz/kubedrill/internal/cli"
	"github.com/cauebraz/kubedrill/internal/cluster"
	"github.com/cauebraz/kubedrill/internal/concepts"
	"github.com/cauebraz/kubedrill/internal/curriculum"
	"github.com/cauebraz/kubedrill/internal/env"
	"github.com/cauebraz/kubedrill/internal/faults"
	"github.com/cauebraz/kubedrill/internal/grader"
	"github.com/cauebraz/kubedrill/internal/journal"
	"github.com/cauebraz/kubedrill/internal/kube"
	"github.com/cauebraz/kubedrill/internal/render"
	"github.com/cauebraz/kubedrill/internal/scenario"
	"github.com/cauebraz/kubedrill/internal/telemetry"
	"github.com/cauebraz/kubedrill/internal/workstation"
)

const (
	checkTimeout        = 30 * time.Second
	staleScriptKubeconf = "kubeconfig"
	workstationEnv      = "KUBEDRILL_WORKSTATION"
)

func main() {
	kube.QuietClientLogs()
	ctx, stop := interruptContext(context.Background())
	code := cli.Main(ctx, os.Args[1:], cli.Options{
		Factories:       factories(os.Stderr, os.Getenv, time.Now, resolveStateDir()),
		Stdout:          os.Stdout,
		Stderr:          os.Stderr,
		Stdin:           os.Stdin,
		Getenv:          os.Getenv,
		Getwd:           os.Getwd,
		IsTerminal:      render.IsTerminal(os.Stdout),
		IsInputTerminal: render.IsTerminal(os.Stdin),
	})
	stop()
	os.Exit(code)
}

// interruptContext returns a context canceled by the first SIGINT or SIGTERM.
// Once it is canceled the signals get their default behavior back, so a
// second Ctrl-C ends a step that does not watch the context (kind create).
// The returned stop releases the signals and waits for the goroutine that
// does so.
func interruptContext(parent context.Context) (context.Context, func()) {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		stop()
	}()
	return ctx, func() {
		stop()
		<-done
	}
}

// stateDir is the cluster state directory (cluster.StateDir), resolved once
// in main; err is set when it cannot be resolved.
type stateDir struct {
	path string
	err  error
}

func resolveStateDir() stateDir {
	p, err := cluster.StateDir()
	return stateDir{path: p, err: err}
}

// factories wires the real packages. state is the cluster state directory
// resolved once by main; the cluster manager (its audit policy record),
// the workstation, the script check wrapper, the pinned script kubeconfig,
// the audit log path and the cli (StateDir) use it, and fail when it could
// not be resolved (the cli and the workstation with its error). The
// kubeconfig handed to scenario scripts is a pinned copy holding only the
// cluster context, written to cluster.ScriptKubeconfigPathIn
// (<cluster state>/kubeconfig-scripts, mode 0600) by ScriptKubeconfig,
// which also removes the <journal>/kubeconfig an older version wrote there,
// best effort; the cli passes that path to Connect, whose grader exports it
// to script checks. On the host Connect writes the pinned copy itself when
// it receives none and fails when that fails, so script checks never see
// the learner's kubeconfig. Script checks run with <cluster state>/bin
// first on PATH, where a kubectl wrapper sets argv[0] to kubedrill-check so
// their requests never count as the learner's. Inside the workstation
// container (KUBEDRILL_WORKSTATION=1) KUBECONFIG already is the pinned
// internal copy with a single context, so scripts receive it unchanged and
// Connect reads it with an empty context name; the cluster is
// workstationCluster, which refuses every host-only operation, and no fault
// manager is wired because the node containers are not reachable. On the
// host the fault manager records faults in the cluster state directory the
// cli hands it, whatever journal is in use, and the workstation keeps the
// linux kubedrill binary there too, outside the journal it mounts
// read-write. Telemetry reads the cluster audit log (KUBEDRILL_AUDIT_LOG
// when set, as in the workstation, else cluster.AuditLogPathIn), and the
// shell log (journal.ShellLogPath) and the recordings
// (journal.RecordingsDir) of the journal directory; an episode still active
// is summarized up to now().
func factories(stderr io.Writer, getenv func(string) string, now func() time.Time, state stateDir) cli.Factories {
	inWorkstation := getenv(workstationEnv) == "1"
	return cli.Factories{
		Cluster: func(profilesDir string) cli.Cluster {
			m := cluster.New(profilesDir, cluster.WithOutput(stderr), cluster.WithGetenv(getenv), cluster.WithStateDir(state.path))
			if inWorkstation {
				return &workstationCluster{addons: m, kubeconfig: getenv("KUBECONFIG")}
			}
			return m
		},
		Journal:  func(dir string) (cli.Journal, error) { return journal.Open(dir) },
		Catalog:  func(root string, packs []string) (cli.Catalog, error) { return scenario.NewCatalog(root, packs...) },
		Concepts: func(root string) (cli.Concepts, error) { return concepts.Load(root) },
		Workstation: func(root, dataDir string) cli.Workstation {
			if state.err != nil {
				return unavailableWorkstation{err: fmt.Errorf("workstation: %w", state.err)}
			}
			opts := []workstation.Option{workstation.WithOutput(stderr), workstation.WithStateDir(state.path)}
			opts = append(opts, workstation.WithAuditDir(cluster.AuditDirIn(state.path)))
			return workstation.New(root, dataDir, opts...)
		},
		ScriptKubeconfig: func(journalDir, kubeconfig, contextName string) (string, error) {
			if inWorkstation {
				return getenv("KUBECONFIG"), nil
			}
			return pinScriptKubeconfig(state, journalDir, kubeconfig, contextName)
		},
		Connect: func(kubeconfig, contextName, scriptKubeconfig string) (cli.Kube, cli.Grader, error) {
			if inWorkstation {
				kubeconfig, contextName = getenv("KUBECONFIG"), ""
				if scriptKubeconfig == "" {
					scriptKubeconfig = kubeconfig
				}
			} else if scriptKubeconfig == "" {
				pinned, err := pinScriptKubeconfig(state, "", kubeconfig, contextName)
				if err != nil {
					return nil, nil, fmt.Errorf("write script kubeconfig: %w", err)
				}
				scriptKubeconfig = pinned
			}
			k, err := kube.New(kubeconfig, contextName)
			if err != nil {
				return nil, nil, err
			}
			opts := []grader.Option{
				grader.WithCheckTimeout(checkTimeout),
				grader.WithEnvFunc(func(rd *scenario.Rendered) []string {
					return env.ScriptEnv(rd, scriptKubeconfig, "")
				}),
			}
			dir, err := checkBinDir(state)
			switch {
			case err == nil:
				opts = append(opts, grader.WithPathPrefix(dir))
			case !errors.Is(err, exec.ErrNotFound):
				fmt.Fprintf(stderr, "warning: script checks run the plain kubectl and count as learner requests: %v\n", err)
			}
			return k, grader.New(k, opts...), nil
		},
		Curriculum: curriculum.Load,
		Lint:       scenario.Lint,
		Scaffold:   scenario.Scaffold,
		Run: func(ctx context.Context, script string, envv []string, out io.Writer) error {
			return env.RunScript(out)(ctx, script, envv)
		},
		StateDir: func() (string, error) { return state.path, state.err },
		Faults: func(stateDir string) cli.Faults {
			if inWorkstation {
				return nil
			}
			if stateDir == "" {
				fmt.Fprintln(stderr, "warning: no fault manager: cluster state directory is empty")
				return nil
			}
			return faults.New(cluster.Name, stateDir)
		},
		Telemetry: func(journalDir string, auditAvailable func() bool) cli.TelemetryFunc {
			return func(ep journal.Episode, rd *scenario.Rendered) journal.Telemetry {
				return episodeTelemetry(stderr, journalDir, auditLogPath(getenv, state), ep, rd, auditAvailable, inWorkstation, now())
			}
		},
	}
}

// pinScriptKubeconfig writes the context contextName of kubeconfig to
// <cluster state>/kubeconfig-scripts (cluster.ScriptKubeconfigPathIn) and
// returns that path; it fails with the state error when the cluster state
// directory could not be resolved; it removes the
// <journalDir>/kubeconfig an older version wrote, best effort, when
// journalDir is set.
func pinScriptKubeconfig(state stateDir, journalDir, kubeconfig, contextName string) (string, error) {
	if state.err != nil {
		return "", state.err
	}
	ok, err := kube.HasContext(kubeconfig, contextName)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("context %s not found in %s; run kubedrill up", contextName, kubeconfig)
	}
	pinned := cluster.ScriptKubeconfigPathIn(state.path)
	if err := kube.WriteContextKubeconfig(kubeconfig, contextName, pinned); err != nil {
		return "", err
	}
	if journalDir != "" {
		_ = os.Remove(filepath.Join(journalDir, staleScriptKubeconf))
	}
	return pinned, nil
}

// unavailableWorkstation is the workstation when the cluster state
// directory, which holds its linux binary, cannot be resolved: every method
// returns err.
type unavailableWorkstation struct {
	err error
}

func (u unavailableWorkstation) DockerAvailable(context.Context) error { return u.err }

func (u unavailableWorkstation) Prepare(context.Context, io.Writer) error { return u.err }

func (u unavailableWorkstation) ShellCommand(context.Context, workstation.ShellOptions) (*exec.Cmd, error) {
	return nil, u.err
}

func (u unavailableWorkstation) RemoveContainer(context.Context, string) error { return u.err }

func (u unavailableWorkstation) NodeShell(context.Context, string, string) error { return u.err }

func (u unavailableWorkstation) NodeDebug(context.Context, string, string, string) error {
	return u.err
}

func (u unavailableWorkstation) Replay(context.Context, string) error { return u.err }

// checkBinDir writes the kubedrill-check kubectl wrapper into
// <cluster state>/bin and returns that directory.
func checkBinDir(state stateDir) (string, error) {
	if state.err != nil {
		return "", state.err
	}
	dir := filepath.Join(state.path, "bin")
	if _, err := grader.WriteKubectlWrapper(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// auditLogPath is KUBEDRILL_AUDIT_LOG when set, else the audit log under
// the cluster state directory; "" when neither resolves.
func auditLogPath(getenv func(string) string, state stateDir) string {
	if p := getenv(auditLogEnv); p != "" {
		return p
	}
	if state.err != nil {
		return ""
	}
	return cluster.AuditLogPathIn(state.path)
}

const auditLogEnv = "KUBEDRILL_AUDIT_LOG"

const shellLogRotateSize = 10 << 20

// episodeTelemetry summarizes the episode window (start to end, or now)
// from the audit log at auditPath, and the shell log and the recordings of
// the episode namespace under dir. Inside the workstation the cluster
// cannot be asked whether the audit log is on, so an audit log file counts
// as on. An audit log that is on but missing or unreadable leaves the
// verdict unknown instead of reporting no writes. A shell log that cannot be
// read is reported on stderr in one line and left out. Once read, a shell
// log over 10 MiB is renamed to commands.jsonl.1, so the next episode starts
// on a short file; a failed rename is reported on stderr.
func episodeTelemetry(stderr io.Writer, dir, auditPath string, ep journal.Episode, rd *scenario.Rendered, auditAvailable func() bool, inWorkstation bool, now time.Time) journal.Telemetry {
	since, until := ep.StartedAt, now
	if ep.EndedAt != nil {
		until = *ep.EndedAt
	}
	ns := ep.Namespace
	if ns == "" && rd != nil {
		ns = rd.Namespace
	}
	var fixTargets []string
	if rd != nil && rd.Scenario != nil {
		fixTargets = rd.Scenario.Meta.FixTargets
	}
	available := auditPath != "" && auditAvailable != nil && auditAvailable()
	if !available && inWorkstation && auditPath != "" {
		if st, err := os.Stat(auditPath); err == nil && !st.IsDir() {
			available = true
		}
	}
	var events []telemetry.AuditEvent
	if available {
		var err error
		if events, err = telemetry.ReadAudit(auditPath, since, until, ns, fixTargets); err != nil {
			events, available = nil, false
		}
	}
	shellPath := journal.ShellLogPath(dir)
	shell, err := telemetry.ReadShellLog(shellPath, since, until)
	if err != nil {
		fmt.Fprintf(stderr, "warning: telemetry without the shell log: %v\n", err)
		shell = nil
	}
	if _, err := telemetry.RotateShellLog(shellPath, shellLogRotateSize); err != nil {
		fmt.Fprintf(stderr, "warning: %v\n", err)
	}
	if rd != nil && rd.Scenario != nil && rd.Scenario.Meta.RestartIsFix {
		for i := range events {
			if events[i].Restart {
				events[i].MetadataOnly = false
			}
		}
	}
	t := telemetry.Summarize(events, available, shell, fixTargets)
	t.Recordings = recordings(dir, ns, since, until)
	return t
}

// recordings lists <dir>/recordings/<ns>-<unix>.cast files started within
// the window, oldest first.
func recordings(dir, ns string, since, until time.Time) []string {
	if ns == "" {
		return nil
	}
	matches, err := filepath.Glob(filepath.Join(journal.RecordingsDir(dir), ns+"-*.cast"))
	if err != nil {
		return nil
	}
	type rec struct {
		path string
		at   int64
	}
	var found []rec
	for _, m := range matches {
		stamp := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(m), ns+"-"), ".cast")
		at, err := strconv.ParseInt(stamp, 10, 64)
		if err != nil || at < since.Unix()-1 || at > until.Unix() {
			continue
		}
		found = append(found, rec{m, at})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].at < found[j].at })
	out := make([]string, 0, len(found))
	for _, r := range found {
		out = append(out, r.path)
	}
	return out
}

// workstationCluster is the cluster seen from inside the workstation
// container: the kind cluster, its state directory and docker live on the
// host. Exists asks the API server through the mounted kubeconfig,
// Kubeconfig returns that kubeconfig with an empty context name (it holds a
// single context), Addons and BaseAddons read the repository, and every
// other operation is refused with a message naming the host.
type workstationCluster struct {
	addons     addonLister
	kubeconfig string
}

// addonLister is the part of cluster.Manager that only reads the
// repository and is safe inside the container.
type addonLister interface {
	Addons() []string
	BaseAddons() []string
}

var _ cli.Cluster = (*workstationCluster)(nil)

func (w *workstationCluster) Exists(ctx context.Context) (bool, error) {
	k, err := kube.New(w.kubeconfig, "")
	if err != nil {
		return false, err
	}
	if err := k.Ping(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (w *workstationCluster) Kubeconfig() (string, string, error) {
	if w.kubeconfig == "" {
		return "", "", errors.New("KUBECONFIG is not set inside the workstation; open a terminal from kubedrill web")
	}
	return w.kubeconfig, "", nil
}

func (w *workstationCluster) Addons() []string {
	if w.addons == nil {
		return nil
	}
	return w.addons.Addons()
}

func (w *workstationCluster) BaseAddons() []string {
	if w.addons == nil {
		return nil
	}
	return w.addons.BaseAddons()
}

func (w *workstationCluster) Ensure(context.Context, string, cluster.ProfileData) error {
	return hostOnly("kubedrill up")
}

func (w *workstationCluster) EnsureAddons(context.Context, cluster.AddonApplier, []string) error {
	return hostOnly("kubedrill up")
}

func (w *workstationCluster) PullImages(context.Context, []string) error {
	return hostOnly("kubedrill up")
}

func (w *workstationCluster) RenderProfile(string, cluster.ProfileData) ([]byte, error) {
	return nil, hostOnly("rendering a cluster profile")
}

func (w *workstationCluster) AuditEnabled(context.Context) (bool, error) {
	return false, hostOnly("the audit check")
}

func (w *workstationCluster) AuditPolicyStale(context.Context) (bool, error) {
	return false, hostOnly("the audit policy check")
}

func (w *workstationCluster) Created(context.Context) (time.Time, error) {
	return time.Time{}, hostOnly("the cluster creation time")
}

func (w *workstationCluster) Delete(context.Context) error {
	return hostOnly("kubedrill down")
}

func (w *workstationCluster) Doctor(context.Context) ([]cluster.Tool, error) {
	return nil, hostOnly("kubedrill doctor")
}

func (w *workstationCluster) WriteInternalKubeconfig(context.Context, string) error {
	return hostOnly("kubedrill web")
}

func hostOnly(op string) error {
	return errors.New(op + " runs on the host, not inside the workstation")
}
