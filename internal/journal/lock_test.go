package journal

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	helperEnv     = "JOURNAL_LOCK_HELPER"
	helperDirEnv  = "JOURNAL_LOCK_DIR"
	helperWaitEnv = "JOURNAL_LOCK_WAIT"
)

func TestLockHelperProcess(t *testing.T) {
	mode := os.Getenv(helperEnv)
	if mode == "" {
		t.Skip("helper process for TestLockAcrossProcesses")
	}
	s, err := Open(os.Getenv(helperDirEnv))
	if err != nil {
		fmt.Println("open error:", err)
		os.Exit(2)
	}
	if w, err := time.ParseDuration(os.Getenv(helperWaitEnv)); err == nil {
		s.lockCfg.wait = w
	}
	unlock, err := s.lock()
	switch mode {
	case "hold":
		if err != nil {
			fmt.Println("hold error:", err)
			os.Exit(2)
		}
		fmt.Println("locked")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		unlock()
		os.Exit(0)
	case "try":
		if err != nil {
			fmt.Println("busy:", err)
			os.Exit(0)
		}
		unlock()
		fmt.Println("acquired")
		os.Exit(0)
	}
	os.Exit(2)
}

func TestLockAcrossProcesses(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err != nil {
		t.Fatal(err)
	}
	holder := exec.Command(os.Args[0], "-test.run=^TestLockHelperProcess$")
	holder.Env = append(os.Environ(), helperEnv+"=hold", helperDirEnv+"="+dir)
	stdin, err := holder.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdin.Close(); _ = holder.Wait() })
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "locked" {
		t.Fatalf("holder: got %q, %v, want locked", line, err)
	}

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.lockCfg.wait = 200 * time.Millisecond
	if unlock, err := s.lock(); err == nil {
		unlock()
		t.Fatal("lock while another process holds it: got nil error, want held by")
	} else if !strings.Contains(err.Error(), fmt.Sprintf("held by pid %d", holder.Process.Pid)) {
		t.Errorf("lock while held: got %v, want it to name pid %d", err, holder.Process.Pid)
	}

	stdin.Close()
	if err := holder.Wait(); err != nil {
		t.Fatalf("holder exit: %v", err)
	}
	s.lockCfg.wait = 2 * time.Second
	unlock, err := s.lock()
	if err != nil {
		t.Fatalf("lock after the holder released it: %v", err)
	}
	unlock()
	if _, err := os.Stat(filepath.Join(dir, lockFile)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("lock file after unlock: got %v, want it removed", err)
	}
}

func TestLockContentionInProcess(t *testing.T) {
	dir := t.TempDir()
	const n = 8
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		inside  int
		maxSeen int
	)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := Open(dir)
			if err != nil {
				t.Error(err)
				return
			}
			for j := 0; j < 5; j++ {
				unlock, err := s.lock()
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				inside++
				maxSeen = max(maxSeen, inside)
				mu.Unlock()
				time.Sleep(time.Millisecond)
				mu.Lock()
				inside--
				mu.Unlock()
				unlock()
			}
		}()
	}
	wg.Wait()
	if maxSeen != 1 {
		t.Errorf("holders at once: got %d, want 1", maxSeen)
	}
}

func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

func TestLockStaleRule(t *testing.T) {
	old := time.Now().Add(-time.Minute)
	here := func(pid int) func(lockConfig) string {
		return func(c lockConfig) string {
			return fmt.Sprintf(`{"pid":%d,"hostname":%q,"pid_ns":%q}`, pid, c.hostname, c.pidNS)
		}
	}
	fixed := func(content string) func(lockConfig) string {
		return func(lockConfig) string { return content }
	}
	tests := []struct {
		name      string
		content   func(lockConfig) string
		mtime     time.Time
		wantTaken bool
	}{
		{name: "dead pid on this host, fresh", content: here(deadPID(t)), wantTaken: true},
		{name: "live pid on this host, old", content: here(os.Getpid()), mtime: old},
		{name: "other host, fresh", content: fixed(`{"pid":1,"hostname":"workstation"}`)},
		{name: "dead pid, same hostname, other pid namespace, fresh", content: func(c lockConfig) string {
			return fmt.Sprintf(`{"pid":%d,"hostname":%q,"pid_ns":"pid:[1]"}`, deadPID(t), c.hostname)
		}},
		{name: "other host, old", content: fixed(`{"pid":1,"hostname":"workstation"}`), mtime: old, wantTaken: true},
		{name: "empty, fresh", content: fixed("")},
		{name: "empty, old (legacy flock file)", content: fixed(""), mtime: old, wantTaken: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := openTest(t)
			s.lockCfg.wait = 150 * time.Millisecond
			path := filepath.Join(s.Dir(), lockFile)
			content := tc.content(s.lockCfg)
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if !tc.mtime.IsZero() {
				if err := os.Chtimes(path, tc.mtime, tc.mtime); err != nil {
					t.Fatal(err)
				}
			}
			unlock, err := s.lock()
			if got := err == nil; got != tc.wantTaken {
				t.Fatalf("lock over %q: got taken=%v (err %v), want %v", content, got, err, tc.wantTaken)
			}
			if err == nil {
				unlock()
			}
			if _, err := os.Stat(path + breakSuffix); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("breaker file: got %v, want it removed", err)
			}
		})
	}
}

func TestUnlockLeavesAnotherOwnersLock(t *testing.T) {
	s := openTest(t)
	unlock, err := s.lock()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Dir(), lockFile)
	other := []byte(`{"pid":1,"hostname":"workstation","token":"other"}`)
	if err := os.WriteFile(path, other, 0o600); err != nil {
		t.Fatal(err)
	}
	unlock()
	if got, err := os.ReadFile(path); err != nil || string(got) != string(other) {
		t.Errorf("lock file after unlocking a broken lock: got %q, %v, want the other owner's lock kept", got, err)
	}
}
