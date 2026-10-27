package cli

import (
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
