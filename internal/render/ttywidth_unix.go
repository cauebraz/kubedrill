//go:build linux || darwin

package render

import (
	"io"
	"os"
	"syscall"
	"unsafe"
)

// TTYWidth asks the terminal behind w for its column count with the
// TIOCGWINSZ ioctl; it returns 0 when w is not a terminal.
func TTYWidth(w io.Writer) int {
	f, ok := w.(*os.File)
	if !ok {
		return 0
	}
	var ws struct{ row, col, xpixel, ypixel uint16 }
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	if errno != 0 {
		return 0
	}
	return int(ws.col)
}
