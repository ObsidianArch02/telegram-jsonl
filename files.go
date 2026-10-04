package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Atomic replacement also removes superseded message bodies from the active file.
func atomicWrite(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".write-*")
	if err != nil {
		return err
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = replaceFile(f.Name(), path); err != nil {
		return err
	}
	return syncDirectory(dir)
}

func writeJSON(path string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return atomicWrite(path, append(b, '\n'))
}

func readJSON(path string, value any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, value); err != nil {
		return fmt.Errorf("decode %s: %w", filepath.Base(path), err)
	}
	return nil
}

func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("data directory must be a real directory")
	}
	return os.Chmod(dir, 0700)
}

// The OS releases the lock when its file handle closes or the process exits.
func lockDirectory(dir string) (*os.File, error) {
	return lockFile(dir, ".lock")
}

func lockFile(dir, name string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := acquireFileLock(f); err != nil {
		_ = f.Close()
		return nil, errors.New("another process is using this data directory")
	}
	return f, nil
}

type failure struct {
	mu     sync.Mutex
	err    error
	cancel func(error)
}

func (f *failure) report(err error) error {
	if err == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err == nil {
		f.err = err
		if f.cancel != nil {
			f.cancel(err)
		}
	}
	return err
}

func (f *failure) check() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}
