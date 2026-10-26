package kube

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/cauebraz/kubedrill/internal/scenario"
)

const (
	probePrefix         = "kubedrill-probe-"
	probeContainer      = "probe"
	probeDefaultTimeout = 60 * time.Second
	probeWaitGrace      = 30 * time.Second
	probeLogLimit       = 64 * 1024
	probeDeleteTimeout  = 15 * time.Second
	probePollInterval   = time.Second
	probeRunAsUser      = 65532
)

var podGVR = schema.GroupVersionResource{Version: "v1", Resource: "pods"}

// RunProbe runs image with command as a one-shot pod in ns, waits for it to
// finish within the context deadline, and returns its exit code and logs.
// The pod is deleted on every path.
func (c *Client) RunProbe(ctx context.Context, ns string, probe scenario.Probe) (ProbeResult, error) {
	if probe.Image == "" {
		return ProbeResult{}, errors.New("run probe: image is empty")
	}
	timeout, err := ProbeTimeout(probe)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("run probe: %w", err)
	}
	name := probePrefix + RandomSuffix(5)
	pods := c.dyn.Resource(podGVR).Namespace(ns)

	if _, err := pods.Create(ctx, probePod(name, probe, timeout), metav1.CreateOptions{FieldManager: fieldManager}); err != nil {
		return ProbeResult{}, fmt.Errorf("run probe %s in %q: create pod: %w", name, ns, err)
	}
	defer func() {
		dctx, cancel := context.WithTimeout(context.Background(), probeDeleteTimeout)
		defer cancel()
		_ = pods.Delete(dctx, name, metav1.DeleteOptions{})
	}()

	waitCtx, cancel := context.WithTimeout(ctx, timeout+probeWaitGrace)
	defer cancel()
	var final *unstructured.Unstructured
	var lastWaiting string
	err = wait.PollUntilContextCancel(waitCtx, probePollInterval, true, func(ctx context.Context) (bool, error) {
		pod, err := pods.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return false, fmt.Errorf("pod disappeared")
			}
			return false, nil
		}
		phase, _, _ := unstructured.NestedString(pod.Object, "status", "phase")
		if phase == "Succeeded" || phase == "Failed" {
			final = pod
			return true, nil
		}
		if reason := waitingReason(pod); reason != "" {
			lastWaiting = reason
		}
		return false, nil
	})
	if err != nil {
		if lastWaiting != "" {
			return ProbeResult{}, fmt.Errorf("run probe %s in %q: wait for completion: %w (container waiting: %s)", name, ns, err, lastWaiting)
		}
		return ProbeResult{}, fmt.Errorf("run probe %s in %q: wait for completion: %w", name, ns, err)
	}

	code, ok := terminatedExitCode(final)
	if !ok {
		reason, _, _ := unstructured.NestedString(final.Object, "status", "reason")
		message, _, _ := unstructured.NestedString(final.Object, "status", "message")
		return ProbeResult{}, fmt.Errorf("run probe %s in %q: pod failed before the container ran: %s %s", name, ns, reason, message)
	}
	output, err := c.podLogs(ctx, ns, name)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("run probe %s in %q: %w", name, ns, err)
	}
	return ProbeResult{ExitCode: code, Output: output}, nil
}

func (c *Client) podLogs(ctx context.Context, ns, name string) (string, error) {
	stream, err := c.core.Get().
		Namespace(ns).
		Resource("pods").
		Name(name).
		SubResource("log").
		Param("container", probeContainer).
		Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("read logs: %w", err)
	}
	defer stream.Close()
	data, err := io.ReadAll(io.LimitReader(stream, probeLogLimit))
	if err != nil {
		return "", fmt.Errorf("read logs: %w", err)
	}
	return string(data), nil
}

func probePod(name string, probe scenario.Probe, timeout time.Duration) *unstructured.Unstructured {
	command := make([]any, 0, len(probe.Command))
	for _, c := range probe.Command {
		command = append(command, c)
	}
	container := map[string]any{
		"name":  probeContainer,
		"image": probe.Image,
		"securityContext": map[string]any{
			"allowPrivilegeEscalation": false,
			"capabilities":             map[string]any{"drop": []any{"ALL"}},
			"runAsNonRoot":             true,
			"runAsUser":                int64(probeRunAsUser),
			"seccompProfile":           map[string]any{"type": "RuntimeDefault"},
		},
		"resources": map[string]any{
			"requests": map[string]any{"cpu": "10m", "memory": "16Mi"},
			"limits":   map[string]any{"memory": "64Mi"},
		},
	}
	if len(command) > 0 {
		container["command"] = command
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]any{
			"name":   name,
			"labels": map[string]any{managedByLabel: fieldManager},
		},
		"spec": map[string]any{
			"restartPolicy":                "Never",
			"activeDeadlineSeconds":        int64(timeout / time.Second),
			"automountServiceAccountToken": false,
			"containers":                   []any{container},
		},
	}}
}

func waitingReason(pod *unstructured.Unstructured) string {
	statuses, _, _ := unstructured.NestedSlice(pod.Object, "status", "containerStatuses")
	for _, s := range statuses {
		m, ok := s.(map[string]any)
		if !ok {
			continue
		}
		if n, _, _ := unstructured.NestedString(m, "name"); n != probeContainer {
			continue
		}
		reason, _, _ := unstructured.NestedString(m, "state", "waiting", "reason")
		return reason
	}
	return ""
}

// ProbeTimeout is the time a probe pod may run: p.Timeout parsed as a Go
// duration, 60s when it is empty. A timeout that does not parse or is below
// one second is an error.
func ProbeTimeout(p scenario.Probe) (time.Duration, error) {
	raw := strings.TrimSpace(p.Timeout)
	if raw == "" {
		return probeDefaultTimeout, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("parse timeout %q: %w", raw, err)
	}
	if d < time.Second {
		return 0, fmt.Errorf("timeout %q is below 1s", raw)
	}
	return d, nil
}

func terminatedExitCode(pod *unstructured.Unstructured) (int, bool) {
	statuses, _, _ := unstructured.NestedSlice(pod.Object, "status", "containerStatuses")
	for _, s := range statuses {
		m, ok := s.(map[string]any)
		if !ok {
			continue
		}
		if n, _, _ := unstructured.NestedString(m, "name"); n != probeContainer {
			continue
		}
		code, found, err := unstructured.NestedInt64(m, "state", "terminated", "exitCode")
		if err != nil || !found {
			return 0, false
		}
		return int(code), true
	}
	return 0, false
}

// RandomSuffix returns n characters drawn uniformly from lowercase letters
// and digits with crypto/rand, for names such as episode namespaces and
// probe pods.
func RandomSuffix(n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	const limit = 256 - 256%len(alphabet)
	out := make([]byte, 0, n)
	buf := make([]byte, 2*n+8)
	for len(out) < n {
		_, _ = rand.Read(buf)
		for _, b := range buf {
			if int(b) < limit && len(out) < n {
				out = append(out, alphabet[int(b)%len(alphabet)])
			}
		}
	}
	return string(out)
}
