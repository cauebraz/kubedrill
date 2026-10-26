package cluster

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/cauebraz/kubedrill/internal/yamldoc"
)

const (
	addonFile         = "addon.yaml"
	dockerTimeout     = 60 * time.Second
	dockerPullTimeout = 5 * time.Minute
	addonReadyTimeout = 3 * time.Minute
	specHashLabel     = "kubedrill.io/spec-hash"
	adminKubeconfig   = "/etc/kubernetes/admin.conf"
)

var addonNameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// AddonApplier applies manifests of any scope to the cluster;
// kube.Client.ApplyCluster satisfies it.
type AddonApplier interface {
	ApplyCluster(ctx context.Context, manifests []string) error
}

type addon struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Base        bool            `json:"base"`
	Manifests   []string        `json:"manifests"`
	Container   *addonContainer `json:"container,omitempty"`
}

// addonContainer is a container an addon runs next to the kind nodes.
// SecurityOpt, Memory and PidsLimit map to docker run --security-opt,
// --memory and --pids-limit; zero values add no flag.
type addonContainer struct {
	Name        string   `json:"name"`
	Image       string   `json:"image"`
	Network     string   `json:"network"`
	Restart     string   `json:"restart"`
	Volumes     []string `json:"volumes"`
	Args        []string `json:"args"`
	SecurityOpt []string `json:"security_opt,omitempty"`
	Memory      string   `json:"memory,omitempty"`
	PidsLimit   int      `json:"pids_limit,omitempty"`
}

// specHash is the first 12 hex digits of a sha256 over the JSON of c; a
// container whose label differs runs an older spec and is recreated.
func (c *addonContainer) specHash() string {
	data, _ := json.Marshal(c)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:12]
}

// WithAddonsDir sets the directory that holds one subdirectory per addon;
// the default is the addons directory next to the profiles directory.
func WithAddonsDir(dir string) Option {
	return func(m *Manager) {
		if dir != "" {
			m.addonsDir = dir
		}
	}
}

// Addons returns the names of the addons under the addons directory, sorted.
// A directory without addon.yaml is not an addon; an unreadable addons
// directory yields no names.
func (m *Manager) Addons() []string {
	entries, err := os.ReadDir(m.addonsDir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() || !addonNameRE.MatchString(e.Name()) {
			continue
		}
		if _, err := os.Stat(filepath.Join(m.addonsDir, e.Name(), addonFile)); err != nil {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// BaseAddons returns the names of the addons whose addon.yaml sets
// base: true; every cluster gets them.
func (m *Manager) BaseAddons() []string {
	var names []string
	for _, n := range m.Addons() {
		a, err := m.loadAddon(n)
		if err == nil && a.Base {
			names = append(names, n)
		}
	}
	return names
}

// EnsureAddons applies the manifests of each named addon with k, starts its
// container when it declares one, and waits up to 3 minutes for every
// Deployment in the applied manifests to be Available, printing "waiting
// for <addon>: <namespace>/<deployment>..." for each and "<addon> ready
// (<seconds>s)" at the end. It is idempotent: manifests are server-side
// applied, and a running container whose spec-hash label matches the
// declared spec is left alone; any other is recreated. Every addon is
// validated before the first one is applied.
func (m *Manager) EnsureAddons(ctx context.Context, k AddonApplier, names []string) error {
	addons := make([]*addon, 0, len(names))
	var problems []string
	for _, n := range names {
		a, err := m.loadAddon(n)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		addons = append(addons, a)
	}
	if len(problems) > 0 {
		return fmt.Errorf("ensure addons: %s", strings.Join(problems, "; "))
	}
	for _, a := range addons {
		manifests := make([]string, 0, len(a.Manifests))
		for _, f := range a.Manifests {
			data, err := os.ReadFile(filepath.Join(m.addonsDir, a.Name, f))
			if err != nil {
				return fmt.Errorf("ensure addon %s: %w", a.Name, err)
			}
			manifests = append(manifests, string(data))
		}
		if len(manifests) > 0 {
			if k == nil {
				return fmt.Errorf("ensure addon %s: no manifest applier", a.Name)
			}
			if err := k.ApplyCluster(ctx, manifests); err != nil {
				return fmt.Errorf("ensure addon %s: %w", a.Name, err)
			}
		}
		if a.Container != nil {
			if err := m.ensureContainer(ctx, a.Container); err != nil {
				return fmt.Errorf("ensure addon %s: %w", a.Name, err)
			}
		}
		if err := m.waitDeployments(ctx, a.Name, manifests); err != nil {
			return fmt.Errorf("ensure addon %s: %w", a.Name, err)
		}
	}
	return nil
}

// waitDeployments waits, with kubectl wait inside the control plane node,
// for every Deployment declared in manifests to report Available, within
// one 3 minute budget for the addon.
func (m *Manager) waitDeployments(ctx context.Context, addon string, manifests []string) error {
	deps := deploymentsIn(manifests)
	if len(deps) == 0 {
		return nil
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, addonReadyTimeout)
	defer cancel()
	for _, d := range deps {
		fmt.Fprintf(m.out, "waiting for %s: %s...\n", addon, d)
		ns, name, _ := strings.Cut(d, "/")
		left := time.Until(start.Add(addonReadyTimeout)).Round(time.Second)
		if left < time.Second {
			left = time.Second
		}
		_, err := m.run(ctx, "docker", "exec", controlPlane, "kubectl", "--kubeconfig", adminKubeconfig,
			"wait", "--for=condition=Available", "-n", ns, "deployment/"+name, "--timeout="+strconv.Itoa(int(left/time.Second))+"s")
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && len(bytes.TrimSpace(exitErr.Stderr)) > 0 {
				return fmt.Errorf("deployment %s not available after %s: %s: %w", d, time.Since(start).Round(time.Second), bytes.TrimSpace(exitErr.Stderr), err)
			}
			return fmt.Errorf("deployment %s not available after %s: %w", d, time.Since(start).Round(time.Second), err)
		}
	}
	fmt.Fprintf(m.out, "%s ready (%ds)\n", addon, int(time.Since(start).Round(time.Second)/time.Second))
	return nil
}

// deploymentsIn lists the apps/v1 Deployments of manifests as
// namespace/name, in order; a Deployment without a namespace is in default.
// Manifests are cut with yamldoc.Split; documents that are not YAML objects
// are skipped: the apply has already accepted or refused them.
func deploymentsIn(manifests []string) []string {
	var out []string
	for _, mf := range manifests {
		for _, doc := range yamldoc.Split([]byte(mf)) {
			var o struct {
				APIVersion string `json:"apiVersion"`
				Kind       string `json:"kind"`
				Metadata   struct {
					Name      string `json:"name"`
					Namespace string `json:"namespace"`
				} `json:"metadata"`
			}
			if yaml.Unmarshal(doc, &o) != nil || o.Kind != "Deployment" || !strings.HasPrefix(o.APIVersion, "apps/") || o.Metadata.Name == "" {
				continue
			}
			ns := o.Metadata.Namespace
			if ns == "" {
				ns = "default"
			}
			out = append(out, ns+"/"+o.Metadata.Name)
		}
	}
	return out
}

func (m *Manager) loadAddon(name string) (*addon, error) {
	if !addonNameRE.MatchString(name) {
		return nil, fmt.Errorf("addon %q: name must be lowercase letters, digits and hyphens", name)
	}
	path := filepath.Join(m.addonsDir, name, addonFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("addon %q not found at %s; available: %s", name, path, strings.Join(m.Addons(), ", "))
		}
		return nil, fmt.Errorf("addon %q: %w", name, err)
	}
	var a addon
	if err := yaml.UnmarshalStrict(data, &a); err != nil {
		return nil, fmt.Errorf("addon %q: parse %s: %w", name, path, err)
	}
	var problems []string
	if a.Name != name {
		problems = append(problems, fmt.Sprintf("name %q does not match the directory", a.Name))
	}
	if len(a.Manifests) == 0 && a.Container == nil {
		problems = append(problems, "declares neither manifests nor a container")
	}
	for _, f := range a.Manifests {
		if f == "" || f != filepath.Base(f) || strings.ContainsAny(f, `/\`) || f == addonFile {
			problems = append(problems, fmt.Sprintf("manifest %q must be a file name in the addon directory", f))
		}
	}
	if c := a.Container; c != nil {
		if c.Name == "" {
			problems = append(problems, "container.name is empty")
		}
		if !strings.Contains(c.Image, "@sha256:") {
			problems = append(problems, fmt.Sprintf("container.image %q is not pinned by digest", c.Image))
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("addon %q: %s", name, strings.Join(problems, "; "))
	}
	return &a, nil
}

// runFor runs one command with its own timeout on top of ctx.
func (m *Manager) runFor(ctx context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return m.run(ctx, name, args...)
}

func (m *Manager) ensureContainer(ctx context.Context, c *addonContainer) error {
	out, err := m.runFor(ctx, dockerTimeout, "docker", "inspect", "-f", `{{index .Config.Labels "`+specHashLabel+`"}}|{{.State.Running}}`, c.Name)
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || !bytes.Contains(bytes.ToLower(exitErr.Stderr), []byte("no such")) {
			return fmt.Errorf("inspect container %s: %w", c.Name, err)
		}
		return m.runContainer(ctx, c)
	}
	hash, running, _ := strings.Cut(strings.TrimSpace(string(out)), "|")
	want := c.specHash()
	switch {
	case hash == want && running == "true":
		return nil
	case hash == want:
		if _, err := m.runFor(ctx, dockerTimeout, "docker", "start", c.Name); err != nil {
			return fmt.Errorf("start container %s: %w", c.Name, err)
		}
		return nil
	}
	if _, err := m.runFor(ctx, dockerTimeout, "docker", "rm", "-f", c.Name); err != nil {
		return fmt.Errorf("replace container %s (spec %q, want %s): %w", c.Name, hash, want, err)
	}
	return m.runContainer(ctx, c)
}

func (m *Manager) runContainer(ctx context.Context, c *addonContainer) error {
	args := []string{"run", "-d", "--name", c.Name, "--label", managedByLabel, "--label", specHashLabel + "=" + c.specHash()}
	for _, o := range c.SecurityOpt {
		args = append(args, "--security-opt", o)
	}
	if c.Memory != "" {
		args = append(args, "--memory", c.Memory)
	}
	if c.PidsLimit > 0 {
		args = append(args, "--pids-limit", strconv.Itoa(c.PidsLimit))
	}
	if c.Network != "" {
		args = append(args, "--network", c.Network)
	}
	if c.Restart != "" {
		args = append(args, "--restart", c.Restart)
	}
	for _, v := range c.Volumes {
		args = append(args, "-v", v)
	}
	args = append(args, c.Image)
	args = append(args, c.Args...)
	if _, err := m.runFor(ctx, dockerPullTimeout, "docker", args...); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(bytes.TrimSpace(exitErr.Stderr)) > 0 {
			return fmt.Errorf("run container %s: %s: %w", c.Name, bytes.TrimSpace(exitErr.Stderr), err)
		}
		return fmt.Errorf("run container %s: %w", c.Name, err)
	}
	return nil
}
