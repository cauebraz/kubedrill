package web

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
	"unsafe"
)

// openPTY opens /dev/ptmx, unlocks the slave and returns the master with
// the slave's path.
func openPTY() (*os.File, string, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, "", fmt.Errorf("open /dev/ptmx: %w", err)
	}
	fd := master.Fd()
	var unlock int32
	if err := ioctl(fd, syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); err != nil {
		master.Close()
		return nil, "", fmt.Errorf("unlock pty: %w", err)
	}
	var n uint32
	if err := ioctl(fd, syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); err != nil {
		master.Close()
		return nil, "", fmt.Errorf("pty number: %w", err)
	}
	return master, "/dev/pts/" + strconv.Itoa(int(n)), nil
}
