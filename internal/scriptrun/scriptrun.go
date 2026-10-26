// Package scriptrun runs scenario scripts (setup hooks, solutions, script
// checks) with bash in their own process group, an allowlisted environment
// and a bounded output capture. When the run ends, every process left in the
// group is killed, so a background child never outlives the run.
package scriptrun

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// DefaultLimit is the number of output bytes Run keeps when Spec.Limit is not
// positive.
const DefaultLimit = 64 << 10

// DefaultWaitDelay is how long Run waits for the output pipe to close after
// the process group was killed when Spec.WaitDelay is not positive.
const DefaultWaitDelay = 2 * time.Second

var hostVars = []string{"PATH", "HOME", "TMPDIR", "LANG", "LC_ALL", "TERM", "USER"}

// HostEnv returns the KEY=VALUE pairs of the process environment that a
// scenario script inherits: PATH, HOME, TMPDIR, LANG, LC_ALL, TERM and USER,
// each when set. Every other variable (cloud credentials, tokens, SSH agent)
// stays out of the script.
func HostEnv() []string {
	out := make([]string, 0, len(hostVars))
	for _, k := range hostVars {
		if v, ok := os.LookupEnv(k); ok {
			out = append(out, k+"="+v)
		}
	}
	return out
}

// Spec describes one script run. Exactly one of Script and File is set:
// Script runs as "bash -c Script", File runs as "bash File".
type Spec struct {
	Script string
	File   string
	// Dir is the working directory; empty means the current directory.
	Dir string
	// Env holds KEY=VALUE pairs appended to HostEnv; nothing else of the
	// process environment reaches the script.
	Env []string
	// Stream, when not nil, receives the combined output as it is produced,
	// without the Limit.
	Stream io.Writer
	// Limit caps the bytes kept in Result.Output; DefaultLimit when not
	// positive.
	Limit int
	// WaitDelay bounds the wait for the output pipe after the process group
	// was killed; DefaultWaitDelay when not positive.
	WaitDelay time.Duration
}

// Result is the outcome of a run. ExitCode is the exit status of bash, or -1
// when it was killed by a signal or did not start. Output is the combined
// stdout and stderr, at most Spec.Limit bytes; Truncated reports that bytes
// past the limit were dropped.
type Result struct {
	ExitCode  int
	Output    string
	Truncated bool
}

// Run executes the script described by s and waits for it. A non-zero exit
// status is reported in Result.ExitCode with a nil error. The error is not nil
// when the script could not start, when ctx ended before the script exited
// (wrapping ctx.Err()), or when writing to s.Stream failed. When ctx ends the
// whole process group is killed; when bash exits the group is killed too, so
// background children of the script never outlive Run.
func Run(ctx context.Context, s Spec) (Result, error) {
	res := Result{ExitCode: -1}
	args, err := s.args()
	if err != nil {
		return res, err
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return res, fmt.Errorf("run %s: output pipe: %w", s.name(), err)
	}
	defer pr.Close()

	cmd := exec.CommandContext(ctx, "bash", args...)
	cmd.Dir = s.Dir
	cmd.Env = append(HostEnv(), s.Env...)
	cmd.Stdout = pw
	cmd.Stderr = pw
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killGroup(cmd.Process.Pid) }
	cmd.WaitDelay = s.waitDelay()

	if err := cmd.Start(); err != nil {
		pw.Close()
		return res, fmt.Errorf("run %s: %w", s.name(), err)
	}
	pw.Close()

	out := &capped{limit: s.limit()}
	done := make(chan error, 1)
	go func() { done <- drain(pr, out, s.Stream) }()

	waitErr := cmd.Wait()
	_ = killGroup(cmd.Process.Pid)

	var streamErr error
	timer := time.NewTimer(s.waitDelay())
	select {
	case streamErr = <-done:
		timer.Stop()
	case <-timer.C:
		pr.Close()
		streamErr = <-done
	}

	res.Output = string(out.buf)
	res.Truncated = out.truncated
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	switch {
	case ctx.Err() != nil:
		return res, fmt.Errorf("run %s: %w", s.name(), ctx.Err())
	case cmd.ProcessState == nil:
		return res, fmt.Errorf("run %s: %w", s.name(), waitErr)
	case streamErr != nil:
		return res, fmt.Errorf("run %s: write output: %w", s.name(), streamErr)
	}
	return res, nil
}

func (s Spec) args() ([]string, error) {
	switch {
	case s.Script != "" && s.File != "":
		return nil, errors.New("run script: Script and File are mutually exclusive")
	case s.File != "":
		return []string{s.File}, nil
	case s.Script != "":
		return []string{"-c", s.Script}, nil
	}
	return nil, errors.New("run script: Script or File is required")
}

func (s Spec) name() string {
	if s.File != "" {
		return s.File
	}
	return "script"
}

func (s Spec) limit() int {
	if s.Limit > 0 {
		return s.Limit
	}
	return DefaultLimit
}

func (s Spec) waitDelay() time.Duration {
	if s.WaitDelay > 0 {
		return s.WaitDelay
	}
	return DefaultWaitDelay
}

func killGroup(pid int) error {
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func drain(r io.Reader, out *capped, stream io.Writer) error {
	var streamErr error
	buf := make([]byte, 32<<10)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			out.write(buf[:n])
			if stream != nil && streamErr == nil {
				if _, werr := stream.Write(buf[:n]); werr != nil {
					streamErr = werr
				}
			}
		}
		if err != nil {
			return streamErr
		}
	}
}

type capped struct {
	buf       []byte
	limit     int
	truncated bool
}

func (c *capped) write(p []byte) {
	room := c.limit - len(c.buf)
	if len(p) > room {
		p = p[:max(room, 0)]
		c.truncated = true
	}
	c.buf = append(c.buf, p...)
}
