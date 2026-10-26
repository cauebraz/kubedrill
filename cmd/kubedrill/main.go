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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Main(ctx, os.Args[1:], cli.Options{
		Factories:       factories(os.Stderr, os.Getenv, time.Now),
		Stdout:          os.Stdout,
		Stderr:          os.Stderr,
		Stdin:           os.Stdin,
		Getenv:          os.Getenv,
		Getwd:           os.Getwd,
		IsTerminal:      isTerminal(os.Stdout),
		IsInputTerminal: isTerminal(os.Stdin),
	})
	stop()
	os.Exit(code)
}

// factories wires the real packages. The kubeconfig handed to scenario
// scripts is a pinned copy holding only the cluster context, written to
// cluster.ScriptKubeconfigPath (<cluster state>/kubeconfig-scripts, mode
// 0600) by ScriptKubeconfig, which also removes the <journal>/kubeconfig an
// older version wrote there, best effort; the cli passes that path to
// Connect, whose grader exports it to script checks (the cluster kubeconfig
// when it is empty). Script checks run with <cluster state>/bin first on
// PATH, where a kubectl wrapper sets argv[0] to kubedrill-check so their
// requests never count as the learner's. Inside the workstation container
// (KUBEDRILL_WORKSTATION=1) KUBECONFIG already is the pinned internal copy
// with a single context, so scripts receive it unchanged and Connect reads
// it with an empty context name; the cluster manager is wrapped so
// lifecycle commands refuse to run and Exists answers from the API server,
// and no fault manager is wired because the node containers are not
// reachable. On the host the fault manager records faults in the cluster
// state directory the cli hands it, whatever journal is in use, and the
// workstation keeps the linux kubedrill binary there too, outside the
// journal it mounts read-write. Telemetry reads the cluster audit log
// (KUBEDRILL_AUDIT_LOG when set, as in the workstation, else
// cluster.AuditLogPath), and the shell log (journal.ShellLogPath) and the
// recordings (journal.RecordingsDir) of the journal directory; an episode
// still active is summarized up to now().
func factories(stderr io.Writer, getenv func(string) string, now func() time.Time) cli.Factories {
	inWorkstation := getenv(workstationEnv) == "1"
	return cli.Factories{
		Cluster: func(profilesDir string) cli.Cluster {
			m := cluster.New(profilesDir, cluster.WithOutput(stderr), cluster.WithGetenv(getenv))
			if inWorkstation {
				return &workstationCluster{Manager: m, kubeconfig: getenv("KUBECONFIG")}
			}
			return m
		},
		Journal:  func(dir string) (cli.Journal, error) { return journal.Open(dir) },
		Catalog:  func(root string) (cli.Catalog, error) { return scenario.NewCatalog(root) },
		Concepts: func(root string) (cli.Concepts, error) { return concepts.Load(root) },
		Workstation: func(root, dataDir string) cli.Workstation {
			opts := []workstation.Option{workstation.WithOutput(stderr)}
			if dir, err := cluster.StateDir(); err == nil {
				opts = append(opts, workstation.WithStateDir(dir))
			}
			if dir, err := cluster.AuditDir(); err == nil {
				opts = append(opts, workstation.WithAuditDir(dir))
			}
			return workstation.New(root, dataDir, opts...)
		},
		ScriptKubeconfig: func(journalDir, kubeconfig, contextName string) (string, error) {
			if inWorkstation {
				return getenv("KUBECONFIG"), nil
			}
			ok, err := kube.HasContext(kubeconfig, contextName)
			if err != nil {
				return "", err
			}
			if !ok {
				return "", fmt.Errorf("context %s not found in %s; run kubedrill up", contextName, kubeconfig)
			}
			pinned, err := cluster.ScriptKubeconfigPath()
			if err != nil {
				return "", err
			}
			if err := kube.WriteContextKubeconfig(kubeconfig, contextName, pinned); err != nil {
				return "", err
			}
			if journalDir != "" {
				_ = os.Remove(filepath.Join(journalDir, staleScriptKubeconf))
			}
			return pinned, nil
		},
		Connect: func(kubeconfig, contextName, scriptKubeconfig string) (cli.Kube, cli.Grader, error) {
			if inWorkstation {
				kubeconfig, contextName = getenv("KUBECONFIG"), ""
			}
			if scriptKubeconfig == "" {
				scriptKubeconfig = kubeconfig
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
			if dir, err := checkBinDir(); err != nil {
				fmt.Fprintf(stderr, "warning: script checks run the plain kubectl and count as learner requests: %v\n", err)
			} else {
				opts = append(opts, grader.WithPathPrefix(dir))
			}
			return k, grader.New(k, opts...), nil
		},
		Curriculum: curriculum.Load,
		Lint:       scenario.Lint,
		Scaffold:   scenario.Scaffold,
		Run: func(ctx context.Context, script string, envv []string, out io.Writer) error {
			return env.RunScript(out)(ctx, script, envv)
		},
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
				return episodeTelemetry(journalDir, auditLogPath(getenv), ep, rd, auditAvailable, inWorkstation, now())
			}
		},
	}
}

// checkBinDir writes the kubedrill-check kubectl wrapper into
// <cluster state>/bin and returns that directory.
func checkBinDir() (string, error) {
	state, err := cluster.StateDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(state, "bin")
	if _, err := grader.WriteKubectlWrapper(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// auditLogPath is KUBEDRILL_AUDIT_LOG when set, else the cluster audit log;
// "" when neither resolves.
func auditLogPath(getenv func(string) string) string {
	if p := getenv(auditLogEnv); p != "" {
		return p
	}
	p, err := cluster.AuditLogPath()
	if err != nil {
		return ""
	}
	return p
}

const auditLogEnv = "KUBEDRILL_AUDIT_LOG"

// episodeTelemetry summarizes the episode window (start to end, or now)
// from the audit log at auditPath, and the shell log and the recordings of
// the episode namespace under dir. Inside the workstation the cluster
// cannot be asked whether the audit log is on, so an audit log file counts
// as on. An audit log that is on but missing or unreadable leaves the
// verdict unknown instead of reporting no writes.
func episodeTelemetry(dir, auditPath string, ep journal.Episode, rd *scenario.Rendered, auditAvailable func() bool, inWorkstation bool, now time.Time) journal.Telemetry {
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
	shell, err := telemetry.ReadShellLog(journal.ShellLogPath(dir), since, until)
	if err != nil {
		shell = nil
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

// workstationCluster is the cluster manager seen from inside the workstation
// container: the kind cluster lives on the host, so Exists asks the API
// server through the mounted kubeconfig and every lifecycle operation is
// refused with a message naming the host.
type workstationCluster struct {
	*cluster.Manager
	kubeconfig string
}

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

func (w *workstationCluster) Ensure(context.Context, string, cluster.ProfileData) error {
	return hostOnly("kubedrill up")
}

func (w *workstationCluster) EnsureAddons(context.Context, cluster.AddonApplier, []string) error {
	return hostOnly("kubedrill up")
}

func (w *workstationCluster) AuditEnabled(context.Context) (bool, error) {
	return false, hostOnly("the audit check")
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
	return hostOnly("kubedrill shell")
}

func hostOnly(op string) error {
	return errors.New(op + " runs on the host, not inside the workstation")
}

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}
