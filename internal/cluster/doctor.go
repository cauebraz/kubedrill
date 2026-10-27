package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	kindversion "sigs.k8s.io/kind/pkg/cmd/kind/version"
)

const (
	installDocker  = "https://docs.docker.com/get-docker/"
	installKubectl = "https://kubernetes.io/docs/tasks/tools/"
	installKind    = "https://kind.sigs.k8s.io/docs/user/quick-start/#installation"
	toolTimeout    = 10 * time.Second

	// minDockerMemory is the docker memory below which the three nodes, the
	// scenario pods and the workstation do not fit comfortably; the idle
	// cluster uses about 1.3 GiB.
	minDockerMemory = 3 << 30
	// minInotifyInstances and minInotifyWatches are the inotify limits kind
	// documents for multi-node clusters on Linux hosts.
	minInotifyInstances = 512
	minInotifyWatches   = 524288
	inotifyDir          = "/proc/sys/fs/inotify"
)

// Doctor reports on docker (daemon reachable, memory), kubectl (optional:
// the workstation ships its own), kind and, on a Linux host, the inotify
// limits. Each probe is bounded to ten seconds. It never installs
// anything.
func (m *Manager) Doctor(ctx context.Context) ([]Tool, error) {
	tools := []Tool{
		m.dockerTool(ctx),
		m.kubectlTool(ctx),
		kindTool(),
	}
	if m.goos == "linux" {
		if t, ok := m.inotifyTool(); ok {
			tools = append(tools, t)
		}
	}
	if err := ctx.Err(); err != nil {
		return tools, fmt.Errorf("doctor: %w", err)
	}
	return tools, nil
}

func (m *Manager) dockerTool(ctx context.Context) Tool {
	t := Tool{Name: "docker", Install: installDocker}
	if _, err := m.lookPath("docker"); err != nil {
		t.Detail = "binary not found on PATH"
		return t
	}
	t.Found = true
	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()
	out, err := m.run(ctx, "docker", "version", "--format", "{{.Server.Version}}")
	if err != nil {
		t.Detail = "daemon not running"
		return t
	}
	t.Version = parseDockerVersion(out)
	if t.Version == "" {
		t.Detail = "daemon not running"
		return t
	}
	if mem, err := m.run(ctx, "docker", "info", "--format", "{{.MemTotal}}"); err == nil {
		if n, err := strconv.ParseInt(strings.TrimSpace(string(mem)), 10, 64); err == nil && n > 0 && n < minDockerMemory {
			t.Detail = fmt.Sprintf("%.1f GiB of memory, below the 3 GiB the cluster needs; raise it in Docker Desktop, Settings > Resources", float64(n)/(1<<30))
		}
	}
	return t
}

// inotifyTool reads the host inotify limits; ok is false when they cannot
// be read. Below the limits kind documents, node containers fail with
// "too many open files".
func (m *Manager) inotifyTool() (Tool, bool) {
	read := func(name string) (int, bool) {
		data, err := m.readFile(inotifyDir + "/" + name)
		if err != nil {
			return 0, false
		}
		n, err := strconv.Atoi(strings.TrimSpace(string(data)))
		return n, err == nil
	}
	instances, ok1 := read("max_user_instances")
	watches, ok2 := read("max_user_watches")
	if !ok1 || !ok2 {
		return Tool{}, false
	}
	t := Tool{Name: "inotify", Found: true, Version: fmt.Sprintf("%d instances, %d watches", instances, watches)}
	if instances < minInotifyInstances || watches < minInotifyWatches {
		t.Detail = fmt.Sprintf("below %d instances and %d watches, nodes can fail with too many open files; raise them: sudo sysctl fs.inotify.max_user_instances=%d fs.inotify.max_user_watches=%d", minInotifyInstances, minInotifyWatches, minInotifyInstances, minInotifyWatches)
	}
	return t, true
}

func (m *Manager) kubectlTool(ctx context.Context) Tool {
	t := Tool{Name: "kubectl", Install: installKubectl, Optional: true}
	if _, err := m.lookPath("kubectl"); err != nil {
		t.Detail = "binary not found on PATH"
		return t
	}
	t.Found = true
	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()
	out, err := m.run(ctx, "kubectl", "version", "--client", "-o", "json")
	if err != nil {
		t.Detail = fmt.Sprintf("kubectl version failed: %v", err)
		return t
	}
	v, err := parseKubectlVersion(out)
	if err != nil {
		t.Detail = err.Error()
		return t
	}
	t.Version = v
	return t
}

func kindTool() Tool {
	return Tool{
		Name:    "kind",
		Found:   true,
		Version: "v" + kindversion.Version(),
		Detail:  "embedded library",
		Install: installKind,
	}
}

func parseDockerVersion(out []byte) string {
	v := strings.TrimSpace(string(out))
	if v == "" || strings.Contains(v, "Cannot connect") || strings.HasPrefix(v, "<no value>") {
		return ""
	}
	return v
}

func parseKubectlVersion(out []byte) (string, error) {
	var parsed struct {
		ClientVersion struct {
			GitVersion string `json:"gitVersion"`
		} `json:"clientVersion"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return "", fmt.Errorf("parse kubectl version output: %w", err)
	}
	if parsed.ClientVersion.GitVersion == "" {
		return "", fmt.Errorf("parse kubectl version output: clientVersion.gitVersion is empty")
	}
	return parsed.ClientVersion.GitVersion, nil
}
