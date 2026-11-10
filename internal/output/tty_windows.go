package output

import (
	"os"
	"syscall"
)

// isatty reports whether f is a console, which only has a console mode.
func isatty(f *os.File) bool {
	var mode uint32
	return syscall.GetConsoleMode(syscall.Handle(f.Fd()), &mode) == nil
}
