//go:build !darwin && !linux && !windows

package output

import "os"

// isatty, where saw has no system call for it: a character device other than /dev/null.
func isatty(f *os.File) bool {
	st, err := f.Stat()
	if err != nil || st.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	null, err := os.Stat(os.DevNull)
	return err != nil || !os.SameFile(st, null)
}
