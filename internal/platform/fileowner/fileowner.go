// Package fileowner keeps panel state files owned by the panel user when an operator command runs as root.
package fileowner

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// Keep records the owner of the panel database and PKI directory and returns a function that restores it
// on the database files (including -wal and -shm) and the whole PKI tree. Without it, files created by a
// root command could not be written by the panel running as its own user. As a non-root caller, or when
// the owner is root or unknown, the returned function does nothing. An empty pkiDir skips the PKI tree.
func Keep(db, pkiDir string) (restore func()) {
	if os.Geteuid() != 0 {
		return func() {}
	}

	uid, gid, ok := owner(db, pkiDir, filepath.Dir(db))
	if !ok || uid == 0 {
		return func() {}
	}

	return func() {
		for _, p := range []string{db, db + "-wal", db + "-shm"} {
			_ = os.Lchown(p, uid, gid)
		}

		if pkiDir == "" {
			return
		}

		//nolint:gosec // chowning the operator-configured PKI tree; no file content is read.
		_ = filepath.WalkDir(pkiDir, func(p string, _ fs.DirEntry, err error) error {
			if err == nil {
				_ = os.Lchown(p, uid, gid)
			}

			return nil
		})
	}
}

// owner returns the owner of the first existing path.
func owner(paths ...string) (uid, gid int, ok bool) {
	for _, p := range paths {
		if p == "" {
			continue
		}

		fi, err := os.Stat(p)
		if err != nil {
			continue
		}

		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			return int(st.Uid), int(st.Gid), true
		}
	}

	return 0, 0, false
}
