//go:build !windows

// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"os"
	"syscall"
)

func acquireFileLock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}
func replaceFile(old, new string) error { return os.Rename(old, new) }
func syncDirectory(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
