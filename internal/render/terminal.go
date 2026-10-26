package render

import (
	"os"

	"golang.org/x/term"
)

// IsTerminal reports whether r is an *os.File open on a terminal. It asks
// the terminal driver for its attributes, so /dev/null and other character
// devices that are not terminals report false.
func IsTerminal(r any) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}
