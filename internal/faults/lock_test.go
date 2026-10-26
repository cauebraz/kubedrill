package faults

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// holdLock takes the flock on <dir>/faults.lock from the test and returns
// the release function.
func holdLock(t *testing.T, dir string) func() {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, lockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}
}

func TestLockGivesUpWhenContextEnds(t *testing.T) {
	m, dir := newManager(t, &fakeDocker{})
	release := holdLock(t, dir)
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := m.Active(ctx)
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Active while another process holds the lock: got %v, want context.DeadlineExceeded", err)
	}
	if !strings.Contains(err.Error(), "lock fault state "+filepath.Join(dir, lockFile)) {
		t.Errorf("error %q does not name the lock file", err)
	}
	if elapsed < 300*time.Millisecond || elapsed > 3*time.Second {
		t.Errorf("Active returned after %s, want about the 350ms context deadline", elapsed)
	}
	if err := m.Apply(ctx, Pause, "worker", NetemOptions{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Apply with the lock held and the context over: got %v, want context.DeadlineExceeded", err)
	}
	if err := m.Heal(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Heal with the lock held and the context over: got %v, want context.DeadlineExceeded", err)
	}
}

func TestLockPollsUntilTheHolderReleases(t *testing.T) {
	fd := &fakeDocker{}
	m, dir := newManager(t, fd)
	release := holdLock(t, dir)
	go func() {
		time.Sleep(250 * time.Millisecond)
		release()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	if err := m.Apply(ctx, Pause, "worker", NetemOptions{}); err != nil {
		t.Fatalf("Apply after the holder released: got %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		t.Errorf("Apply returned after %s, want it to wait for the release (about 250ms)", elapsed)
	}
	active, err := m.Active(ctx)
	if err != nil || len(active) != 1 || active[0].Action != Pause {
		t.Errorf("Active: got (%v, %v), want the pause", active, err)
	}
}

func TestHealKeepsUnknownActionRecords(t *testing.T) {
	fd := &fakeDocker{}
	m, dir := newManager(t, fd)
	state := `[{"action":"reboot","node":"worker2","at":"2026-10-25T11:00:00Z"},{"action":"pause","node":"worker","at":"2026-10-25T11:01:00Z"}]` + "\n"
	if err := os.WriteFile(filepath.Join(dir, stateFile), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	err := m.Heal(context.Background())
	if err == nil || !strings.Contains(err.Error(), "heal reboot on worker2: unknown action") || !strings.Contains(err.Error(), filepath.Join(dir, stateFile)) {
		t.Fatalf("Heal with an unknown action: got %v, want an error naming the record and the state file", err)
	}
	want := []call{{"docker", "unpause", "kubedrill-worker"}}
	if len(fd.calls) != 1 || strings.Join(fd.calls[0], " ") != strings.Join(want[0], " ") {
		t.Errorf("docker calls: got %v, want %v", fd.calls, want)
	}
	active, aerr := m.Active(context.Background())
	if aerr != nil || len(active) != 1 || active[0].Action != "reboot" || active[0].Node != "worker2" {
		t.Errorf("Active after Heal: got (%v, %v), want only the unknown reboot record kept", active, aerr)
	}
}
