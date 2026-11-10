package output

import (
	"os"
	"syscall"
	"unsafe"
)

// isatty asks the terminal driver for f's settings, which only a terminal has.
func isatty(f *os.File) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCGETA, uintptr(unsafe.Pointer(&t)))
	return errno == 0
}
