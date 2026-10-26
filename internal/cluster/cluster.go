// Package cluster creates, inspects and deletes the local kind cluster the
// scenarios run on. Profiles under cluster/profiles/<name>.yaml are kind
// configurations written as Go templates over ProfileData; the base profile
// is the default. Addons under cluster/addons/<name> add manifests and
// containers to a running cluster.
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

const (
	defaultProfile = "base"
	contextName    = "kind-" + Name
	readyTimeout   = 2 * time.Minute
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
	return filepath.Join(dir, internalKubeconfigName), nil
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
	return filepath.Join(dir, scriptKubeconfigName), nil
}

// AuditDir returns <StateDir>/audit, the host directory the API server
// writes its audit log to; profiles receive it as ProfileData.AuditDir.
func AuditDir() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, auditSubdir), nil
}

// AuditLogPath returns <StateDir>/audit/audit.log, the API server audit log
// of the cluster. Every journal reads the same file; episodes have unique
// namespaces, so their events do not mix.
func AuditLogPath() (string, error) {
	dir, err := AuditDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, auditLogName), nil
}

// ProfileData is the input of a profile template. Both paths are absolute
// host paths: AuditDir receives the API server audit log (audit.log), and
// Ensure fills it with AuditDir() when empty; AuditPolicy is the audit
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
	profilesDir string
	addonsDir   string
	auditPolicy string
	out         io.Writer
	getenv      func(string) string
	provider    provider
	run         runner
	lookPath    func(file string) (string, error)
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

// New returns a Manager whose profiles live under profilesDir, whose addons
// live in the addons directory next to it (cluster/addons for
// cluster/profiles) and whose repository audit policy is the
// audit-policy.yaml next to it (cluster/audit-policy.yaml).
func New(profilesDir string, opts ...Option) *Manager {
	m := &Manager{
		profilesDir: profilesDir,
		addonsDir:   filepath.Join(filepath.Dir(profilesDir), "addons"),
		auditPolicy: filepath.Join(filepath.Dir(profilesDir), auditPolicyName),
		out:         os.Stderr,
		getenv:      os.Getenv,
		run:         execRunner,
		lookPath:    exec.LookPath,
	}
	for _, o := range opts {
		o(m)
	}
	m.provider = kindcluster.NewProvider(kindcluster.ProviderWithLogger(newWriterLogger(m.out)))
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
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(bytes.TrimSpace(exitErr.Stderr)) > 0 {
			return false, fmt.Errorf("list kind clusters: docker ps: %s: %w", bytes.TrimSpace(exitErr.Stderr), err)
		}
		return false, fmt.Errorf("list kind clusters: docker ps: %w", err)
	}
	return len(bytes.TrimSpace(out)) > 0, nil
}

// Ensure creates the cluster from profile when it does not exist and waits
// until every node is Ready. The profile is rendered as a Go template with
// data (an empty AuditDir becomes AuditDir()), and AuditDir is created with
// mode 0700 when missing. It is idempotent: an existing cluster is kept as
// it is, whatever its profile, and a create that loses the race with
// another process (kind: "node(s) already exist") counts as existing. A
// creation records the sha256 of the audit policy file it mounted in
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
		return m.exportIfMissing(kubeconfig)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("ensure cluster %s: %w", Name, err)
	}
	if data.AuditDir == "" {
		if data.AuditDir, err = AuditDir(); err != nil {
			return fmt.Errorf("ensure cluster %s: %w", Name, err)
		}
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
		kindcluster.CreateWithWaitForReady(readyTimeout),
		kindcluster.CreateWithDisplayUsage(false),
		kindcluster.CreateWithDisplaySalutation(false),
	)
	if err != nil {
		if strings.Contains(err.Error(), "node(s) already exist") {
			return m.exportIfMissing(kubeconfig)
		}
		return fmt.Errorf("ensure cluster %s: create from profile %s: %w", Name, path, err)
	}
	if err := m.recordAuditPolicy(data.AuditPolicy); err != nil {
		fmt.Fprintf(m.out, "warning: %v\n", err)
	}
	return nil
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

func (m *Manager) auditPolicyHashPath() (string, error) {
	dir, err := stateDir(m.getenv, os.UserHomeDir)
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
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(bytes.TrimSpace(exitErr.Stderr)) > 0 {
			return false, fmt.Errorf("check audit policy on %s: %s: %w", controlPlane, bytes.TrimSpace(exitErr.Stderr), err)
		}
		return false, fmt.Errorf("check audit policy on %s: %w", controlPlane, err)
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
	if exitErr != nil && len(bytes.TrimSpace(exitErr.Stderr)) > 0 {
		return false, fmt.Errorf("check audit policy on %s: %s: %w", node, bytes.TrimSpace(exitErr.Stderr), err)
	}
	return false, fmt.Errorf("check audit policy on %s: %w", node, err)
}

// Delete removes the cluster, the recorded audit policy hash and then every
// addon container labeled app.kubernetes.io/managed-by=kubedrill (docker rm
// -f). A missing cluster is not an error.
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
	if record, err := m.auditPolicyHashPath(); err == nil {
		if err := os.Remove(record); err != nil && !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(m.out, "warning: remove %s: %v\n", record, err)
		}
	}
	out, err := m.runFor(ctx, dockerTimeout, "docker", "ps", "-aq", "--filter", "label="+managedByLabel)
	if err != nil {
		return fmt.Errorf("delete cluster %s: list addon containers: %w", Name, err)
	}
	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		return nil
	}
	if _, err := m.runFor(ctx, dockerTimeout, "docker", append([]string{"rm", "-f"}, ids...)...); err != nil {
		return fmt.Errorf("delete cluster %s: remove addon containers %s: %w", Name, strings.Join(ids, " "), err)
	}
	return nil
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
