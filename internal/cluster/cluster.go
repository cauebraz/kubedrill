// Package cluster creates, inspects and deletes the local kind cluster the
// scenarios run on. Profiles under cluster/profiles/<name>.yaml are kind
// configurations written as Go templates over ProfileData; the base profile
// is the default. Addons under cluster/addons/<name> add manifests and
// containers to a running cluster. Every docker call runs with its own
// bound on top of the caller's context and reports failures through one
// helper (dockerError): 5 seconds for audit checks inside the control plane,
// 15 seconds for listing nodes, 60 seconds (dockerTimeout) for inspecting,
// starting and removing containers, which can wait on a busy daemon, and 5
// minutes for docker run, which may pull an image. kind's Create and Delete
// take no context: the context is checked before and right after each, and
// the readiness wait after Create polls with the context, so a cancelled
// context ends Ensure once kind returns.
package cluster

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"text/template"
	"time"

	kindcluster "sigs.k8s.io/kind/pkg/cluster"

	"github.com/cauebraz/kubedrill/internal/atomicfile"
	"github.com/cauebraz/kubedrill/internal/kube"
)

// Name is the kind cluster name every profile uses.
const Name = "kubedrill"

// DebugImage is the image of the node debugging pods (kubectl debug node)
// that kubedrill node uses where docker is not available. kubedrill up
// loads it on every node with PullImages so the first node shell needs no
// registry.
const DebugImage = "busybox:1.37.0@sha256:bdf57e528e45e4433820e045b29b4597825a1c9e38353532d90a01445013f82e"

const (
	defaultProfile = "base"
	contextName    = "kind-" + Name
	readyTimeout   = 2 * time.Minute
	readyPoll      = 2 * time.Second
	profileExt     = ".yaml"

	auditPolicyInNode = "/etc/kubernetes/audit-policy.yaml"
	auditDirInNode    = "/var/log/kubernetes/audit"
	auditTimeout      = 5 * time.Second
	auditSubdir       = "audit"
	auditLogName      = "audit.log"
	controlPlane      = Name + "-control-plane"
	managedByLabel    = "app.kubernetes.io/managed-by=kubedrill"

	internalKubeconfigName = "kubeconfig-internal"
	scriptKubeconfigName   = "kubeconfig-scripts"
	auditPolicyHashName    = "audit-policy.sha256"
	auditPolicyName        = "audit-policy.yaml"
	kindClusterLabel       = "io.x-k8s.kind.cluster"
	existsTimeout          = 15 * time.Second
	pullTimeout            = 60 * time.Second
)

var nodeNames = []string{"control-plane", "worker", "worker2"}

// NodeNames returns the kind node names of every profile: control-plane,
// worker and worker2. The slice is a copy.
func NodeNames() []string {
	return slices.Clone(nodeNames)
}

// StateDir returns the cluster state directory,
// $XDG_STATE_HOME/kubedrill/kubedrill or ~/.local/state/kubedrill/kubedrill,
// created with mode 0700 or restricted to it (atomicfile.MkdirPrivate). It
// holds what belongs to the cluster rather than to one journal: the audit
// log, faults.json, the kubectl wrapper of script checks and the linux
// kubedrill binary (bin/), the workstation kubeconfig (kubeconfig-internal),
// the kubeconfig of scenario scripts (kubeconfig-scripts) and the sha256 of
// the audit policy the cluster was created with (audit-policy.sha256), all
// outside the journal directory the workstation mounts read-write.
func StateDir() (string, error) {
	return stateDir(os.Getenv, os.UserHomeDir)
}

func stateDir(getenv func(string) string, homeDir func() (string, error)) (string, error) {
	base := getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := homeDir()
		if err != nil {
			return "", fmt.Errorf("cluster state directory: home directory: %w", err)
		}
		base = filepath.Join(home, ".local", "state")
	}
	dir := filepath.Join(base, "kubedrill", Name)
	if err := atomicfile.MkdirPrivate(dir); err != nil {
		return "", fmt.Errorf("cluster state directory: %w", err)
	}
	return dir, nil
}

// InternalKubeconfigPath returns <StateDir>/kubeconfig-internal, where the
// workstation kubeconfig (WriteInternalKubeconfig) belongs: outside the
// journal directory, which the workstation mounts read-write.
func InternalKubeconfigPath() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return InternalKubeconfigPathIn(dir), nil
}

// InternalKubeconfigPathIn is InternalKubeconfigPath for the cluster state
// directory stateDir, already resolved by StateDir.
func InternalKubeconfigPathIn(stateDir string) string {
	return filepath.Join(stateDir, internalKubeconfigName)
}

// ScriptKubeconfigPath returns <StateDir>/kubeconfig-scripts, where the
// pinned kubeconfig exported to scenario scripts (setup hooks, solve scripts
// and script checks; kube.WriteContextKubeconfig writes it with mode 0600)
// belongs: it holds only the cluster context, and it stays outside the
// journal directory, which the workstation mounts read-write.
func ScriptKubeconfigPath() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return ScriptKubeconfigPathIn(dir), nil
}

// ScriptKubeconfigPathIn is ScriptKubeconfigPath for the cluster state
// directory stateDir, already resolved by StateDir.
func ScriptKubeconfigPathIn(stateDir string) string {
	return filepath.Join(stateDir, scriptKubeconfigName)
}

// AuditDir returns <StateDir>/audit, the host directory the API server
// writes its audit log to; profiles receive it as ProfileData.AuditDir.
func AuditDir() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return AuditDirIn(dir), nil
}

// AuditDirIn is AuditDir for the cluster state directory stateDir, already
// resolved by StateDir.
func AuditDirIn(stateDir string) string {
	return filepath.Join(stateDir, auditSubdir)
}

// AuditLogPath returns <StateDir>/audit/audit.log, the API server audit log
// of the cluster. Every journal reads the same file; episodes have unique
// namespaces, so their events do not mix.
func AuditLogPath() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return AuditLogPathIn(dir), nil
}

// AuditLogPathIn is AuditLogPath for the cluster state directory stateDir,
// already resolved by StateDir.
func AuditLogPathIn(stateDir string) string {
	return filepath.Join(AuditDirIn(stateDir), auditLogName)
}

// ProfileData is the input of a profile template. Both paths are absolute
// host paths: AuditDir receives the API server audit log (audit.log), and
// Ensure fills it with AuditDirIn(state) when empty; AuditPolicy is the audit
// policy file mounted into the control plane. Templates quote both with
// printf "%q".
type ProfileData struct {
	AuditDir    string
	AuditPolicy string
}

// Tool is one external dependency the doctor reports on.
type Tool struct {
	Name    string
	Found   bool
	Version string
	Detail  string
	Install string
	// Optional is true for a tool kubedrill works without: kubectl, which
	// the workstation ships and only kubedrill smoke and scenario hooks
	// need on the host.
	Optional bool
}

type provider interface {
	List() ([]string, error)
	Create(name string, options ...kindcluster.CreateOption) error
	Delete(name, explicitKubeconfigPath string) error
	ExportKubeConfig(name, explicitPath string, internal bool) error
	KubeConfig(name string, internal bool) (string, error)
}

type runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Manager drives kind for one named cluster. The zero value is not usable;
// use New.
type Manager struct {
	profilesDir  string
	addonsDir    string
	auditPolicy  string
	out          io.Writer
	getenv       func(string) string
	stateDir     string
	provider     provider
	run          runner
	lookPath     func(file string) (string, error)
	readFile     func(name string) ([]byte, error)
	goos         string
	readyTimeout time.Duration
	readyPoll    time.Duration
}

// Option configures a Manager.
type Option func(*Manager)

// WithOutput sets the writer that receives kind's progress lines; the
// default is os.Stderr.
func WithOutput(w io.Writer) Option {
	return func(m *Manager) {
		if w != nil {
			m.out = w
		}
	}
}

// WithGetenv sets the function that reads environment variables such as
// KUBECONFIG; the default is os.Getenv.
func WithGetenv(fn func(string) string) Option {
	return func(m *Manager) {
		if fn != nil {
			m.getenv = fn
		}
	}
}

// WithStateDir sets the cluster state directory (StateDir, resolved once by
// the caller) that holds the audit policy record and, when ProfileData
// leaves AuditDir empty, the audit log directory. It is required: there is
// no default, and Ensure, AuditPolicyStale and the record removal of Delete
// need it.
func WithStateDir(dir string) Option {
	return func(m *Manager) {
		m.stateDir = dir
	}
}

// New returns a Manager whose profiles live under profilesDir, whose addons
// live in the addons directory next to it (cluster/addons for
// cluster/profiles) and whose repository audit policy is the
// audit-policy.yaml next to it (cluster/audit-policy.yaml).
func New(profilesDir string, opts ...Option) *Manager {
	m := &Manager{
		profilesDir:  profilesDir,
		addonsDir:    filepath.Join(filepath.Dir(profilesDir), "addons"),
		auditPolicy:  filepath.Join(filepath.Dir(profilesDir), auditPolicyName),
		out:          os.Stderr,
		getenv:       os.Getenv,
		run:          execRunner,
		lookPath:     exec.LookPath,
		readFile:     os.ReadFile,
		goos:         runtime.GOOS,
		readyTimeout: readyTimeout,
		readyPoll:    readyPoll,
	}
	for _, o := range opts {
		o(m)
	}
	m.provider = kindcluster.NewProvider(kindcluster.ProviderWithDocker(), kindcluster.ProviderWithLogger(newWriterLogger(m.out)))
	return m
}

func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// Exists reports whether the cluster is present: whether docker knows a
// container (running or not) labeled io.x-k8s.kind.cluster=kubedrill, the
// label kind puts on every node. The docker ps call gives up after 15
// seconds, so a hung daemon fails the check instead of freezing it.
func (m *Manager) Exists(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("list kind clusters: %w", err)
	}
	out, err := m.runFor(ctx, existsTimeout, "docker", "ps", "-aq", "--filter", "label="+kindClusterLabel+"="+Name)
	if err != nil {
		return false, fmt.Errorf("list kind clusters: %w", dockerError("docker ps", err))
	}
	return len(bytes.TrimSpace(out)) > 0, nil
}

// Ensure creates the cluster from profile when it does not exist and waits
// up to two minutes, polling every two seconds with ctx, until every node
// is Ready; an existing cluster gets the same wait, after a check that
// fails fast when docker reports a node container stopped. The profile is rendered as a Go template with data (an empty
// AuditDir becomes AuditDir()), and AuditDir is created with mode 0700 when
// missing. It is idempotent: an existing cluster is kept as it is, whatever
// its profile, and a create that loses the race with another process (kind:
// "node(s) already exist") counts as existing once the same readiness wait
// sees every node Ready, so the second process does not run ahead of the
// first one's creation. kind's Create takes no context: ctx is checked
// before it and right after it returns, so a cancellation during creation
// ends Ensure only when kind returns. A creation records the sha256 of the audit policy file it mounted in
// <StateDir>/audit-policy.sha256, which AuditPolicyStale compares with the
// repository file later. Afterwards the audit log is made readable on the
// host (see openAuditLog).
func (m *Manager) Ensure(ctx context.Context, profile string, data ProfileData) error {
	if err := m.ensure(ctx, profile, data); err != nil {
		return err
	}
	m.openAuditLog(ctx)
	return nil
}

func (m *Manager) ensure(ctx context.Context, profile string, data ProfileData) error {
	if profile == "" {
		profile = defaultProfile
	}
	path, err := m.profilePath(profile)
	if err != nil {
		return fmt.Errorf("ensure cluster %s: %w", Name, err)
	}
	kubeconfig, _, err := m.Kubeconfig()
	if err != nil {
		return fmt.Errorf("ensure cluster %s: %w", Name, err)
	}
	exists, err := m.Exists(ctx)
	if err != nil {
		return fmt.Errorf("ensure cluster %s: %w", Name, err)
	}
	if exists {
		if err := m.checkNodesRunning(ctx); err != nil {
			return fmt.Errorf("ensure cluster %s: %w", Name, err)
		}
		if err := m.waitNodesReady(ctx); err != nil {
			return fmt.Errorf("ensure cluster %s: wait for nodes: %w", Name, err)
		}
		return m.exportIfMissing(kubeconfig)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("ensure cluster %s: %w", Name, err)
	}
	if data.AuditDir == "" {
		state, err := m.state()
		if err != nil {
			return fmt.Errorf("ensure cluster %s: %w", Name, err)
		}
		data.AuditDir = AuditDirIn(state)
	}
	raw, err := renderProfile(path, data)
	if err != nil {
		return fmt.Errorf("ensure cluster %s: %w", Name, err)
	}
	if err := os.MkdirAll(data.AuditDir, 0o700); err != nil {
		return fmt.Errorf("ensure cluster %s: create audit directory: %w", Name, err)
	}
	err = m.provider.Create(Name,
		kindcluster.CreateWithRawConfig(raw),
		kindcluster.CreateWithKubeconfigPath(kubeconfig),
		kindcluster.CreateWithDisplayUsage(false),
		kindcluster.CreateWithDisplaySalutation(false),
	)
	lostRace := err != nil && strings.Contains(err.Error(), "node(s) already exist")
	if err != nil && !lostRace {
		return fmt.Errorf("ensure cluster %s: create from profile %s: %w", Name, path, err)
	}
	if !lostRace {
		if err := m.recordAuditPolicy(data.AuditPolicy); err != nil {
			fmt.Fprintf(m.out, "warning: %v\n", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("ensure cluster %s: %w", Name, err)
	}
	if err := m.waitNodesReady(ctx); err != nil {
		return fmt.Errorf("ensure cluster %s: wait for nodes: %w", Name, err)
	}
	if lostRace {
		return m.exportIfMissing(kubeconfig)
	}
	return nil
}

// checkNodesRunning fails when docker reports a kind node container as
// stopped or missing, so an existing cluster left stopped (for example after
// a Docker restart) or with a node container removed (docker rm) gets a
// remedy instead of the full readiness wait. An inspect that fails for
// another reason is ignored; the readiness wait reports what is wrong.
func (m *Manager) checkNodesRunning(ctx context.Context) error {
	names := make([]string, len(nodeNames))
	for i, n := range nodeNames {
		names[i] = Name + "-" + n
	}
	out, err := m.runFor(ctx, existsTimeout, "docker", append([]string{"inspect", "-f", "{{.Name}}={{.State.Running}}"}, names...)...)
	if err != nil && !noSuchObject(err) {
		return nil
	}
	seen := map[string]bool{}
	var stopped []string
	for _, line := range strings.Split(string(out), "\n") {
		name, running, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		name = strings.TrimPrefix(name, "/")
		seen[name] = true
		if running == "false" {
			stopped = append(stopped, name)
		}
	}
	var missing []string
	if err != nil {
		for _, n := range names {
			if !seen[n] {
				missing = append(missing, n)
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("node containers missing: %s; the cluster is incomplete (a docker rm removed them), recreate it with kubedrill down --yes && kubedrill up",
			strings.Join(missing, ", "))
	}
	if len(stopped) == 0 {
		return nil
	}
	return fmt.Errorf("node containers stopped: %s; start them with docker start %s, or recreate the cluster with kubedrill down --yes && kubedrill up",
		strings.Join(stopped, ", "), strings.Join(stopped, " "))
}

// noSuchObject reports whether a failed docker inspect says one of the
// named objects does not exist.
func noSuchObject(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && strings.Contains(strings.ToLower(string(exitErr.Stderr)), "no such object")
}

// waitNodesReady polls the nodes from inside the control plane node every
// m.readyPoll until every kind node reports Ready, m.readyTimeout passes or
// ctx ends, printing one progress line when the first query finds a node
// not Ready. A failing query (the control plane container or its API server
// not up yet) is retried; the last state or error is in the timeout error.
func (m *Manager) waitNodesReady(ctx context.Context) error {
	wctx, cancel := context.WithTimeout(ctx, m.readyTimeout)
	defer cancel()
	ticker := time.NewTicker(m.readyPoll)
	defer ticker.Stop()
	var last error
	announced := false
	for {
		out, err := m.run(wctx, "docker", "exec", controlPlane, "kubectl", "--kubeconfig", adminKubeconfig,
			"get", "nodes", "-o", `jsonpath={range .items[*]}{.metadata.name}={.status.conditions[?(@.type=="Ready")].status}{"\n"}{end}`)
		if err == nil {
			pending := notReadyNodes(string(out))
			if len(pending) == 0 {
				return nil
			}
			last = errors.New(strings.Join(pending, ", "))
		} else {
			last = dockerError("docker exec "+controlPlane, err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !announced {
			fmt.Fprintf(m.out, "waiting up to %s for the nodes to be Ready...\n", m.readyTimeout)
			announced = true
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wctx.Done():
			return fmt.Errorf("not ready after %s: %w", m.readyTimeout, last)
		case <-ticker.C:
		}
	}
}

// notReadyNodes parses name=status lines and returns, in nodeNames order,
// every kind node that is missing or whose Ready status is not True.
func notReadyNodes(out string) []string {
	status := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if name, s, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			status[name] = s
		}
	}
	var pending []string
	for _, n := range nodeNames {
		full := Name + "-" + n
		switch s, ok := status[full]; {
		case !ok:
			pending = append(pending, full+" missing")
		case s != "True":
			pending = append(pending, full+"="+s)
		}
	}
	return pending
}

// recordAuditPolicy writes the sha256 of the policy file at path to
// <StateDir>/audit-policy.sha256.
func (m *Manager) recordAuditPolicy(path string) error {
	sum, err := fileSHA256(path)
	if err != nil {
		return fmt.Errorf("record audit policy hash: %w", err)
	}
	record, err := m.auditPolicyHashPath()
	if err != nil {
		return fmt.Errorf("record audit policy hash: %w", err)
	}
	if err := atomicfile.Write(record, []byte(sum+"\n"), 0o600); err != nil {
		return fmt.Errorf("record audit policy hash: %w", err)
	}
	return nil
}

// errNoStateDir is the error of an operation that needs the cluster state
// directory on a Manager built without WithStateDir.
var errNoStateDir = errors.New("cluster state directory not set; build the manager with WithStateDir")

// state is the cluster state directory set by WithStateDir.
func (m *Manager) state() (string, error) {
	if m.stateDir == "" {
		return "", errNoStateDir
	}
	return m.stateDir, nil
}

func (m *Manager) auditPolicyHashPath() (string, error) {
	dir, err := m.state()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, auditPolicyHashName), nil
}

func fileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// AuditPolicyStale reports whether the audit policy the cluster runs with
// differs from the repository file (cluster/audit-policy.yaml next to the
// profiles directory): the API server reads the policy once at start, so a
// changed file takes effect only after kubedrill down and up. The recorded
// hash (<StateDir>/audit-policy.sha256, written by Ensure on creation) is
// compared with the repository file; without a record, a cluster that exists
// is asked for its /etc/kubernetes/audit-policy.yaml with docker exec and
// that content is compared instead, and a missing cluster is never stale.
func (m *Manager) AuditPolicyStale(ctx context.Context) (bool, error) {
	want, err := fileSHA256(m.auditPolicy)
	if err != nil {
		return false, fmt.Errorf("check audit policy: %w", err)
	}
	record, err := m.auditPolicyHashPath()
	if err != nil {
		return false, fmt.Errorf("check audit policy: %w", err)
	}
	recorded, err := os.ReadFile(record)
	switch {
	case err == nil:
		return strings.TrimSpace(string(recorded)) != want, nil
	case !errors.Is(err, fs.ErrNotExist):
		return false, fmt.Errorf("check audit policy: %w", err)
	}
	exists, err := m.Exists(ctx)
	if err != nil {
		return false, fmt.Errorf("check audit policy: %w", err)
	}
	if !exists {
		return false, nil
	}
	out, err := m.runFor(ctx, auditTimeout, "docker", "exec", controlPlane, "cat", auditPolicyInNode)
	if err != nil {
		return false, dockerError("check audit policy on "+controlPlane, err)
	}
	sum := sha256.Sum256(out)
	return hex.EncodeToString(sum[:]) != want, nil
}

// exportIfMissing writes the cluster context into kubeconfig unless it is
// already there.
func (m *Manager) exportIfMissing(kubeconfig string) error {
	ok, err := kube.HasContext(kubeconfig, contextName)
	if err != nil {
		return fmt.Errorf("ensure cluster %s: %w", Name, err)
	}
	if ok {
		return nil
	}
	if err := m.provider.ExportKubeConfig(Name, kubeconfig, false); err != nil {
		return fmt.Errorf("ensure cluster %s: export kubeconfig to %s: %w", Name, kubeconfig, err)
	}
	return nil
}

// openAuditLog makes the audit directory 0755 and the audit log 0644 inside
// the control plane, best effort, so a host user on Linux (where the bind
// mount keeps the API server's root ownership) can read the log without
// root. Limitation: on rotation the API server creates the new audit.log
// and the rotated audit-*.log files with mode 0600, unreadable on a Linux
// host until the next Ensure (kubedrill up); Docker Desktop on macOS maps
// ownership to the host user, so it is not affected.
func (m *Manager) openAuditLog(ctx context.Context) {
	_, _ = m.runFor(ctx, auditTimeout, "docker", "exec", controlPlane, "chmod", "0755", auditDirInNode)
	_, _ = m.runFor(ctx, auditTimeout, "docker", "exec", controlPlane, "sh", "-c", "[ ! -f "+auditDirInNode+"/"+auditLogName+" ] || chmod 0644 "+auditDirInNode+"/"+auditLogName)
}

// Created returns when the control plane container was created, from
// docker inspect; the CLI uses it to show the telemetry notice once per
// cluster. The call gives up after five seconds.
func (m *Manager) Created(ctx context.Context) (time.Time, error) {
	out, err := m.runFor(ctx, auditTimeout, "docker", "inspect", "-f", "{{.Created}}", controlPlane)
	if err != nil {
		return time.Time{}, fmt.Errorf("inspect %s: %w", controlPlane, err)
	}
	raw := strings.TrimSpace(string(out))
	at, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("inspect %s: created %q: %w", controlPlane, raw, err)
	}
	return at.UTC(), nil
}

// RenderProfile renders the profile named profile (a file name under the
// profiles directory without .yaml) over data exactly as Ensure does: data
// is validated (absolute paths, an existing audit policy file) and the
// template runs with missingkey=error. An unknown profile lists the
// available ones.
func (m *Manager) RenderProfile(profile string, data ProfileData) ([]byte, error) {
	path, err := m.profilePath(profile)
	if err != nil {
		return nil, err
	}
	return renderProfile(path, data)
}

// renderProfile validates data and executes the profile template at path.
func renderProfile(path string, data ProfileData) ([]byte, error) {
	var problems []string
	for _, f := range []struct{ name, value string }{{"AuditDir", data.AuditDir}, {"AuditPolicy", data.AuditPolicy}} {
		if !filepath.IsAbs(f.value) {
			problems = append(problems, fmt.Sprintf("%s %q is not an absolute path", f.name, f.value))
		}
	}
	if filepath.IsAbs(data.AuditPolicy) {
		if info, err := os.Stat(data.AuditPolicy); err != nil {
			problems = append(problems, fmt.Sprintf("audit policy: %v", err))
		} else if info.IsDir() {
			problems = append(problems, fmt.Sprintf("audit policy %s is a directory", data.AuditPolicy))
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("render profile %s: %s", path, strings.Join(problems, "; "))
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("render profile: %w", err)
	}
	tmpl, err := template.New(filepath.Base(path)).Option("missingkey=error").Parse(string(src))
	if err != nil {
		return nil, fmt.Errorf("render profile %s: %w", path, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render profile %s: %w", path, err)
	}
	return buf.Bytes(), nil
}

// AuditEnabled reports whether the control plane node carries the audit
// policy file, which is the case for clusters created from a profile with
// the audit configuration. The check gives up after five seconds.
func (m *Manager) AuditEnabled(ctx context.Context) (bool, error) {
	node := controlPlane
	_, err := m.runFor(ctx, auditTimeout, "docker", "exec", node, "test", "-f", auditPolicyInNode)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && len(bytes.TrimSpace(exitErr.Stderr)) == 0 {
		return false, nil
	}
	return false, dockerError("check audit policy on "+node, err)
}

// Delete removes the cluster, the recorded audit policy hash and then every
// addon container labeled app.kubernetes.io/managed-by=kubedrill (docker rm
// -f). A missing cluster is not an error. kind's Delete takes no context:
// ctx is checked before it and right after it returns, before the addon
// containers are touched.
func (m *Manager) Delete(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("delete cluster %s: %w", Name, err)
	}
	kubeconfig, _, err := m.Kubeconfig()
	if err != nil {
		return fmt.Errorf("delete cluster %s: %w", Name, err)
	}
	if err := m.provider.Delete(Name, kubeconfig); err != nil {
		return fmt.Errorf("delete cluster %s: %w", Name, err)
	}
	m.removeClusterRecords()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("delete cluster %s: %w", Name, err)
	}
	out, err := m.runFor(ctx, dockerTimeout, "docker", "ps", "-aq", "--filter", "label="+managedByLabel)
	if err != nil {
		return fmt.Errorf("delete cluster %s: list addon containers: %w", Name, dockerError("docker ps", err))
	}
	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		return nil
	}
	if _, err := m.runFor(ctx, dockerTimeout, "docker", append([]string{"rm", "-f"}, ids...)...); err != nil {
		return fmt.Errorf("delete cluster %s: remove addon containers %s: %w", Name, strings.Join(ids, " "), dockerError("docker rm", err))
	}
	return nil
}

// removeClusterRecords removes the files of the cluster state directory
// that describe the deleted cluster: the audit policy record and the two
// kubeconfigs that hold its credentials. A failure is a warning.
func (m *Manager) removeClusterRecords() {
	dir, err := m.state()
	if err != nil {
		return
	}
	files := []string{filepath.Join(dir, auditPolicyHashName), InternalKubeconfigPathIn(dir), ScriptKubeconfigPathIn(dir)}
	for _, f := range files {
		if err := os.Remove(f); err != nil && !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(m.out, "warning: remove %s: %v\n", f, err)
		}
	}
}

// Kubeconfig returns the kubeconfig path and the context name for the
// cluster. The path is the first entry of KUBECONFIG, or ~/.kube/config when
// the variable is empty: kind exports the cluster context into one file, and
// the first entry is where kubectl writes new contexts too, so the learner's
// kubectl and kubedrill agree on where kind-kubedrill lives. Further
// KUBECONFIG entries are read by kubectl but never receive the context.
func (m *Manager) Kubeconfig() (path, contextName string, err error) {
	return kubeconfigPath(m.getenv("KUBECONFIG"), os.UserHomeDir)
}

func kubeconfigPath(env string, homeDir func() (string, error)) (string, string, error) {
	for _, p := range filepath.SplitList(env) {
		if p != "" {
			return p, contextName, nil
		}
	}
	home, err := homeDir()
	if err != nil {
		return "", "", fmt.Errorf("resolve kubeconfig path: home directory: %w", err)
	}
	return filepath.Join(home, ".kube", "config"), contextName, nil
}

// Profiles lists the profile names available under profilesDir.
func (m *Manager) Profiles() ([]string, error) {
	entries, err := os.ReadDir(m.profilesDir)
	if err != nil {
		return nil, fmt.Errorf("list profiles in %s: %w", m.profilesDir, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != profileExt {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), profileExt))
	}
	sort.Strings(names)
	return names, nil
}

func (m *Manager) profilePath(profile string) (string, error) {
	if profile != filepath.Base(profile) || strings.ContainsAny(profile, "/\\") {
		return "", fmt.Errorf("profile %q: name must not contain path separators", profile)
	}
	path := filepath.Join(m.profilesDir, profile+profileExt)
	if _, err := os.Stat(path); err != nil {
		available, listErr := m.Profiles()
		if listErr != nil {
			return "", fmt.Errorf("profile %q not found at %s: %w", profile, path, listErr)
		}
		return "", fmt.Errorf("profile %q not found at %s; available: %s", profile, path, strings.Join(available, ", "))
	}
	return path, nil
}

// WriteInternalKubeconfig writes to dstPath a kubeconfig whose server address
// is the one reachable from containers on the kind docker network, which is
// what the workstation container needs. The file is written with mode 0600
// inside a 0700 directory through a temporary file and a rename, and left
// untouched when it already holds the same content.
func (m *Manager) WriteInternalKubeconfig(ctx context.Context, dstPath string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("write internal kubeconfig for %s: %w", Name, err)
	}
	cfg, err := m.provider.KubeConfig(Name, true)
	if err != nil {
		return fmt.Errorf("write internal kubeconfig for %s: %w", Name, err)
	}
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o700); err != nil {
		return fmt.Errorf("write internal kubeconfig for %s: %w", Name, err)
	}
	if _, err := atomicfile.WriteIfChanged(dstPath, []byte(cfg), 0o600); err != nil {
		return fmt.Errorf("write internal kubeconfig for %s: %w", Name, err)
	}
	return nil
}

// dockerError wraps err from a docker command run as op, adding the
// command's trimmed stderr when it exited with any.
func dockerError(op string, err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if stderr := bytes.TrimSpace(exitErr.Stderr); len(stderr) > 0 {
			return fmt.Errorf("%s: %s: %w", op, stderr, err)
		}
	}
	return fmt.Errorf("%s: %w", op, err)
}
