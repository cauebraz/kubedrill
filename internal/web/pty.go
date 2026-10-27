package web

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

// winsize mirrors struct winsize of TIOCSWINSZ.
type winsize struct {
	Rows, Cols, X, Y uint16
}

// startInPTY starts cmd with a new pseudo-terminal as its controlling
// terminal and standard streams, and returns the master side.
func startInPTY(cmd *exec.Cmd, cols, rows uint16) (*os.File, error) {
	master, slaveName, err := openPTY()
	if err != nil {
		return nil, err
	}
	slave, err := os.OpenFile(slaveName, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, fmt.Errorf("open pty %s: %w", slaveName, err)
	}
	defer slave.Close()
	if err := setSize(master, cols, rows); err != nil {
		master.Close()
		return nil, err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		master.Close()
		return nil, fmt.Errorf("start %s: %w", cmd.Path, err)
	}
	return master, nil
}

// setSize sets the window size of the terminal behind f.
func setSize(f *os.File, cols, rows uint16) error {
	if cols == 0 || rows == 0 {
		return nil
	}
	ws := winsize{Rows: rows, Cols: cols}
	if err := ioctl(f.Fd(), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&ws))); err != nil {
		return fmt.Errorf("set terminal size: %w", err)
	}
	return nil
}

func ioctl(fd, req, arg uintptr) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, req, arg); errno != 0 {
		return errno
	}
	return nil
}
