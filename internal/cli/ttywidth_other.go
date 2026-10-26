//go:build !linux && !darwin

package cli

import "io"

// ttyWidth returns 0: the terminal size is asked only on Linux and macOS.
func ttyWidth(io.Writer) int { return 0 }
