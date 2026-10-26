package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestPromptsEndOnInterrupt(t *testing.T) {
	tests := []struct {
		name      string
		stdin     string
		interrupt bool
		wantOK    bool
		wantAns   string
		wantErr   error
	}{
		{name: "answer before any interrupt", stdin: "y\n", wantOK: true, wantAns: "y"},
		{name: "end of input", stdin: "", wantAns: ""},
		{name: "interrupt while waiting", interrupt: true, wantErr: errInterrupted},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, prompt := range []string{"confirm", "ask"} {
				var stdin io.Reader = strings.NewReader(tc.stdin)
				if tc.interrupt {
					pr, pw := io.Pipe()
					t.Cleanup(func() { pw.Close() })
					stdin = pr
				}
				var out strings.Builder
				a := newApp(Options{Stdout: &out, Stderr: io.Discard, Stdin: stdin, IsTerminal: true, IsInputTerminal: true})
				stop := make(chan struct{})
				a.interrupted = stop
				if tc.interrupt {
					close(stop)
				}
				start := time.Now()
				var err error
				switch prompt {
				case "confirm":
					var ok bool
					ok, err = a.confirm("Delete it?", false)
					if ok != tc.wantOK {
						t.Errorf("confirm: got %v, want %v", ok, tc.wantOK)
					}
				case "ask":
					var ans string
					ans, err = a.ask("Name?")
					if ans != tc.wantAns {
						t.Errorf("ask: got %q, want %q", ans, tc.wantAns)
					}
				}
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("%s error: got %v, want %v", prompt, err, tc.wantErr)
				}
				if elapsed := time.Since(start); elapsed > 2*time.Second {
					t.Errorf("%s returned after %v, want at once", prompt, elapsed)
				}
			}
		})
	}
}

func TestDefaultPromptEndsOnFirstInterrupt(t *testing.T) {
	f := newFixture(t)
	withWorkstation(f)
	pr, pw := io.Pipe()
	t.Cleanup(func() { pw.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	var out, errb strings.Builder
	done := make(chan int, 1)
	go func() {
		done <- Main(ctx, []string{"--root", f.root}, Options{
			Factories:       f.factories(),
			Stdout:          &out,
			Stderr:          &errb,
			Stdin:           pr,
			Getenv:          func(k string) string { return f.env[k] },
			Getwd:           func() (string, error) { return f.root, nil },
			IsTerminal:      true,
			IsInputTerminal: true,
			Now:             f.nowFunc(),
			Executable:      func() (string, error) { return "/usr/local/bin/kubedrill", nil },
			Rand:            func(n int64) int64 { return 6 % n },
			NamespaceSuffix: func() string { return "abcde" },
		})
	}()
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case code := <-done:
		if code != ExitError {
			t.Errorf("exit: got %d, want %d", code, ExitError)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the Start now? prompt kept waiting after the interrupt")
	}
	mustContain(t, out.String(), "Start 001-hello-down now? [Y/n]")
	mustContain(t, errb.String(), "kubedrill: interrupted")
	if cur, _ := f.journal.Current(); cur != nil {
		t.Errorf("episode started after an interrupt at the prompt: %s", cur.ID)
	}
	if len(f.ws.shells) != 0 {
		t.Errorf("workstation opened after an interrupt: %d shells", len(f.ws.shells))
	}
}
