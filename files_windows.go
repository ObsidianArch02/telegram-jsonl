//go:build windows

// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"os"

	"golang.org/x/sys/windows"
)

func acquireFileLock(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
}

func replaceFile(old, new string) error {
	from, err := windows.UTF16PtrFromString(old)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(new)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

// MoveFileEx WRITE_THROUGH supplies the rename durability on Windows.
func syncDirectory(string) error { return nil }
