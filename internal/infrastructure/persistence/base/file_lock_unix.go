//go:build !windows

package base

import (
	"os"

	"golang.org/x/sys/unix"
)

func lockFileHandle(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}

func unlockFileHandle(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
