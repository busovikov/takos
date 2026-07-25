// Package fsstore provides the low-level, crash-safe filesystem primitives
// shared by the tracker core and the daemons: atomic snapshot writes,
// advisory locks and markdown frontmatter parsing.
package fsstore

import (
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to path through a temp file in the same
// directory: write → fsync → rename → fsync(dir). Readers never observe a
// partially written file; a crash leaves either the old or the new content.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename has happened

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	syncDir(dir)
	return nil
}

// syncDir fsyncs a directory so the rename itself is durable. Best-effort:
// some filesystems (and macOS) don't support fsync on directories.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	defer d.Close()
	_ = d.Sync()
}
