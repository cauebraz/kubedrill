package grader

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cauebraz/kubedrill/internal/atomicfile"
)

// CheckUserAgent is argv[0] of the kubectl that script checks run. kubectl
// builds its user agent from argv[0], so the audit log records these
// requests as "kubedrill-check/<version> ...", and telemetry drops them as
// kubedrill's own.
const CheckUserAgent = "kubedrill-check"

// WithPathPrefix puts dir first on PATH for script checks, after every
// other environment pair, so a kubectl wrapper written by
// WriteKubectlWrapper shadows the real one. An empty dir changes nothing.
func WithPathPrefix(dir string) Option {
	return func(r *Runner) {
		r.pathPrefix = dir
	}
}

// WriteKubectlWrapper writes dir/kubectl, a bash script that runs the
// kubectl found on PATH with argv[0] set to CheckUserAgent, and returns its
// path. The directory is created with mode 0700 (an existing one is
// restricted to 0700, atomicfile.MkdirPrivate) and the script with 0755
// through a temporary file and a rename; an identical script is left as it
// is. A kubectl found inside dir is refused, so the wrapper never calls
// itself.
func WriteKubectlWrapper(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("write kubectl wrapper in %s: %w", dir, err)
	}
	real, err := exec.LookPath("kubectl")
	if err != nil {
		return "", fmt.Errorf("write kubectl wrapper in %s: %w", dir, err)
	}
	if real, err = filepath.Abs(real); err != nil {
		return "", fmt.Errorf("write kubectl wrapper in %s: %w", dir, err)
	}
	if filepath.Dir(real) == abs {
		return "", errors.New("write kubectl wrapper in " + dir + ": the kubectl on PATH is the wrapper itself")
	}
	if err := atomicfile.MkdirPrivate(abs); err != nil {
		return "", fmt.Errorf("write kubectl wrapper in %s: %w", dir, err)
	}
	path := filepath.Join(abs, "kubectl")
	script := "#!/bin/bash\nexec -a " + CheckUserAgent + " " + shellQuote(real) + " \"$@\"\n"
	if _, err := atomicfile.WriteIfChanged(path, []byte(script), 0o755); err != nil {
		return "", fmt.Errorf("write kubectl wrapper: %w", err)
	}
	return path, nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// prefixPath returns env with PATH=<dir>:<current PATH> appended, the
// current PATH being the last PATH pair in env or the process PATH.
func prefixPath(env []string, dir string) []string {
	current := os.Getenv("PATH")
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			current = v
		}
	}
	value := dir
	if current != "" {
		value += string(os.PathListSeparator) + current
	}
	return append(env, "PATH="+value)
}
