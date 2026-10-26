package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	kindversion "sigs.k8s.io/kind/pkg/cmd/kind/version"
)

const (
	installDocker  = "https://docs.docker.com/get-docker/"
	installKubectl = "https://kubernetes.io/docs/tasks/tools/"
	installKind    = "https://kind.sigs.k8s.io/docs/user/quick-start/#installation"
	toolTimeout    = 10 * time.Second
)

// Doctor reports on docker (daemon reachable), kubectl and kind. Each probe
// is bounded to ten seconds. It never installs anything.
func (m *Manager) Doctor(ctx context.Context) ([]Tool, error) {
	tools := []Tool{
		m.dockerTool(ctx),
		m.kubectlTool(ctx),
		kindTool(),
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
	}
	return t
}

func (m *Manager) kubectlTool(ctx context.Context) Tool {
	t := Tool{Name: "kubectl", Install: installKubectl}
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
