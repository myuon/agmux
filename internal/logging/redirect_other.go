//go:build !darwin && !linux

package logging

import "os"

func redirectStdio(f *os.File) {}
