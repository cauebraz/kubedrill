package journal

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

const (
	breakSuffix    = ".break"
	lockStaleAfter = 2 * time.Minute
	lockWait       = 150 * time.Second
	lockPollMin    = time.Millisecond
	lockPollMax    = 50 * time.Millisecond
)

type lockOwner struct {
	PID      int       `json:"pid"`
	Hostname string    `json:"hostname"`
	PIDNS    string    `json:"pid_ns,omitempty"`
	At       time.Time `json:"at"`
	Token    string    `json:"token"`
}

type lockConfig struct {
	staleAfter time.Duration
	wait       time.Duration
	hostname   string
	pidNS      string
	pid        int
}

func defaultLockConfig() lockConfig {
	host, _ := os.Hostname()
	ns, _ := os.Readlink("/proc/self/ns/pid")
	return lockConfig{staleAfter: lockStaleAfter, wait: lockWait, hostname: host, pidNS: ns, pid: os.Getpid()}
}

// lock takes the journal lock: <dir>/.lock created with O_CREATE|O_EXCL and
// holding the owner's pid, hostname, time and a random token. It is held
// across the read-modify-write of current.json and episodes.jsonl so another
// kubedrill process (the workstation, which sees the directory through a
// bind mount where flock does not cross, or a second terminal) never
// interleaves with it. A lock held by someone else is retried with a backoff
// for up to the configured wait; a stale lock (see stale) is broken. The
// returned function releases the lock when this process still owns it.
func (s *Store) lock() (func(), error) {
	path := s.path(lockFile)
	token := make([]byte, 8)
	if _, err := rand.Read(token); err != nil {
		return nil, fmt.Errorf("lock journal: %w", err)
	}
	deadline := time.Now().Add(s.lockCfg.wait)
	delay := lockPollMin
	for {
		owner := lockOwner{PID: s.lockCfg.pid, Hostname: s.lockCfg.hostname, PIDNS: s.lockCfg.pidNS, At: time.Now().UTC(), Token: hex.EncodeToString(token)}
		content, err := json.Marshal(owner)
		if err != nil {
			return nil, fmt.Errorf("lock journal: %w", err)
		}
		ok, err := createExclusive(path, content)
		if err != nil {
			return nil, fmt.Errorf("lock journal %s: %w", path, err)
		}
		if ok {
			return func() { s.unlock(path, content) }, nil
		}
		if err := s.breakIfStale(path); err != nil {
			return nil, fmt.Errorf("lock journal %s: %w", path, err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("lock journal %s: held by %s for more than %s", path, describeOwner(path), s.lockCfg.wait)
		}
		time.Sleep(delay)
		delay = min(2*delay, lockPollMax)
	}
}

func (s *Store) unlock(path string, content []byte) {
	if got, err := os.ReadFile(path); err == nil && bytes.Equal(got, content) {
		_ = os.Remove(path)
	}
}

// createExclusive creates path with content when it does not exist. It
// reports false when path already exists.
func createExclusive(path string, content []byte) (bool, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, werr := f.Write(content)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(path)
		return false, err
	}
	return true, nil
}

// breakIfStale removes the lock at path when it is stale. The removal runs
// under a second exclusive file, <path>.break, after the lock is read again,
// so two waiters never remove a lock that a third one just took.
func (s *Store) breakIfStale(path string) error {
	seen, info, err := readLock(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !s.stale(seen, info.ModTime()) {
		return nil
	}
	breaker := path + breakSuffix
	ok, err := createExclusive(breaker, nil)
	if err != nil {
		return err
	}
	if !ok {
		if bi, err := os.Stat(breaker); err == nil && time.Since(bi.ModTime()) > s.lockCfg.staleAfter {
			_ = os.Remove(breaker)
		}
		return nil
	}
	defer os.Remove(breaker)
	again, _, err := readLock(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(again, seen) {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("break stale lock: %w", err)
	}
	return nil
}

func readLock(path string) ([]byte, os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	return data, info, nil
}

// stale reports whether a lock with content data, last modified at mod, is
// abandoned: its owner runs on this host (same hostname and, on Linux, same
// pid namespace, since workstation containers share one hostname) and its
// pid no longer exists, or it is older than staleAfter and its owner is on
// another host (the workstation container or the host seen from it) or
// unreadable. A lock owned by a live pid on this host is never stale.
func (s *Store) stale(data []byte, mod time.Time) bool {
	var owner lockOwner
	parsed := json.Unmarshal(data, &owner) == nil && owner.PID > 0
	sameHost := parsed && owner.Hostname == s.lockCfg.hostname && owner.PIDNS == s.lockCfg.pidNS
	if sameHost && !pidAlive(owner.PID) {
		return true
	}
	if time.Since(mod) <= s.lockCfg.staleAfter {
		return false
	}
	return !sameHost
}

func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func describeOwner(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "an unknown process"
	}
	var owner lockOwner
	if json.Unmarshal(data, &owner) != nil {
		return "an unknown process"
	}
	return fmt.Sprintf("pid %d on %s since %s", owner.PID, owner.Hostname, owner.At.Format(time.RFC3339))
}
