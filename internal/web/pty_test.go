package web

import (
	"bytes"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestStartInPTY(t *testing.T) {
	cmd := exec.Command("sh", "-c", `stty size; test -t 0 && echo is-a-tty; read line; echo "got $line"`)
	master, err := startInPTY(cmd, 100, 30)
	if err != nil {
		t.Fatalf("startInPTY: %v", err)
	}
	defer master.Close()
	if _, err := master.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	done := make(chan []byte, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, master)
		done <- buf.Bytes()
	}()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	var out string
	select {
	case b := <-done:
		out = string(b)
	case <-time.After(2 * time.Second):
		master.Close()
		out = string(<-done)
	}
	for _, want := range []string{"30 100", "is-a-tty", "got hello"} {
		if !strings.Contains(out, want) {
			t.Errorf("pty output: got %q, want it to contain %q", out, want)
		}
	}
}
