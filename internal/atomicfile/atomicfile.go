// Package atomicfile writes files through a temporary file in the same
// directory and a rename, so a reader never sees a partial file.
package atomicfile

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// Write replaces path with data and mode perm: a temporary file next to
// path is written, synced, chmodded and renamed over path. The parent
// directory must exist.
func Write(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	name := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		_ = os.Remove(name)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Chmod(perm); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// WriteIfChanged writes data to path with Write unless path already holds
// exactly data, in which case only its mode is set to perm. It reports
// whether the content was written.
func WriteIfChanged(path string, data []byte, perm fs.FileMode) (bool, error) {
	old, err := os.ReadFile(path)
	switch {
	case err == nil && bytes.Equal(old, data):
		info, err := os.Stat(path)
		if err != nil {
			return false, fmt.Errorf("write %s: %w", path, err)
		}
		if info.Mode().Perm() != perm {
			if err := os.Chmod(path, perm); err != nil {
				return false, fmt.Errorf("write %s: %w", path, err)
			}
		}
		return false, nil
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	if err := Write(path, data, perm); err != nil {
		return false, err
	}
	return true, nil
}

// MkdirPrivate creates dir and its missing parents with mode 0700 and
// restricts an existing dir to 0700 when the current user owns it; a
// directory owned by someone else is left as it is.
func MkdirPrivate(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create private directory %s: %w", dir, err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("create private directory %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("create private directory %s: not a directory", dir)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Getuid() || info.Mode().Perm() == 0o700 {
		return nil
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("create private directory %s: %w", dir, err)
	}
	return nil
}
