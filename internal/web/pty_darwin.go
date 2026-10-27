package web

import (
	"bytes"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// openPTY opens /dev/ptmx, grants and unlocks the slave and returns the
// master with the slave's path.
func openPTY() (*os.File, string, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, "", fmt.Errorf("open /dev/ptmx: %w", err)
	}
	fd := master.Fd()
	if err := ioctl(fd, syscall.TIOCPTYGRANT, 0); err != nil {
		master.Close()
		return nil, "", fmt.Errorf("grant pty: %w", err)
	}
	if err := ioctl(fd, syscall.TIOCPTYUNLK, 0); err != nil {
		master.Close()
		return nil, "", fmt.Errorf("unlock pty: %w", err)
	}
	name := make([]byte, 128)
	if err := ioctl(fd, syscall.TIOCPTYGNAME, uintptr(unsafe.Pointer(&name[0]))); err != nil {
		master.Close()
		return nil, "", fmt.Errorf("pty name: %w", err)
	}
	if i := bytes.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}
	return master, string(name), nil
}
