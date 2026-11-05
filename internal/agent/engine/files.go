package engine

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// AtomicWrite makes both the contents and directory entry durable before return.
func AtomicWrite(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	if err = mkdirDurable(dir); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".engine-*")
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
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	return syncDir(dir)
}
func syncDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}
func removeFile(path string) error {
	if e := os.Remove(path); e != nil {
		if errors.Is(e, os.ErrNotExist) {
			return nil
		}
		return e
	}
	return syncDir(filepath.Dir(path))
}

// Lock excludes a second agent, including one using another panel connection.
// The caller must keep the returned descriptor open for its entire lifetime.
func Lock(dir string) (*os.File, error) {
	if e := mkdirDurable(dir); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(filepath.Join(dir, "agent.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		_ = f.Close()
		return nil, e
	}
	return f, nil
}

// A newly created directory must itself be linked durably before files inside
// it can serve as a recovery journal after power loss.
func mkdirDurable(dir string) error {
	info, e := os.Stat(dir)
	if e == nil {
		if !info.IsDir() {
			return errors.New("engine path is not a directory")
		}
		return nil
	}
	if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	parent := filepath.Dir(dir)
	if parent == dir {
		return e
	}
	if e = mkdirDurable(parent); e != nil {
		return e
	}
	if e = os.Mkdir(dir, 0700); e != nil && !errors.Is(e, os.ErrExist) {
		return e
	}
	return syncDir(parent)
}
