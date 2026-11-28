package logging

import (
	"os"
	"syscall"
)

// redirectStdio points fd 1 and 2 at f.
func redirectStdio(f *os.File) {
	_ = syscall.Dup3(int(f.Fd()), 1, 0)
	_ = syscall.Dup3(int(f.Fd()), 2, 0)
}
