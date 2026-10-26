//go:build !linux && !darwin

package render

import "io"

// TTYWidth returns 0: the terminal size is asked only on Linux and macOS.
func TTYWidth(io.Writer) int { return 0 }
