package jseek

import (
	"io"
	"os"
	"path/filepath"
)

// Crash-safe file writing primitives. The substrate's durability story rests on
// two rules:
//
//  1. A write is only acknowledged after its bytes are on stable storage
//     (fsync), so a power loss cannot lose a confirmed write.
//  2. A file is only ever replaced atomically (write-temp + fsync + rename +
//     dir-fsync), so a crash leaves either the complete old file or the complete
//     new one — never a torn mix.
//
// These are the same rules SQLite and every serious embedded store follows.

// syncDir fsyncs a directory so a rename/create within it is durable. On
// platforms where opening a directory for sync is unsupported it is a no-op
// (the rename itself is still atomic; only the directory-entry durability is
// best-effort there).
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		// Some filesystems (e.g. certain network mounts) reject directory
		// fsync; the atomic rename still holds, so tolerate it.
		if isUnsupportedSync(err) {
			return nil
		}
		return err
	}
	return nil
}

// WriteFileAtomic writes data to path crash-safely: it writes a temporary file
// in the same directory, fsyncs it, renames it over path, then fsyncs the
// directory. A crash at any point leaves path either untouched or fully
// replaced — never partially written.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".jseek-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if we fail before the rename.
	defer func() {
		if tmp != nil {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	tmp = nil // ownership transferred to the rename
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return syncDir(dir)
}

// SealFileAtomic seals data into a self-contained store written crash-safely to
// path. Equivalent to Seal into a buffer + WriteFileAtomic, but streams to the
// temp file to avoid holding the whole image in memory twice.
func SealFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".jseek-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if tmp != nil {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()
	if err := Seal(tmp, data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	tmp = nil
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return syncDir(dir)
}

// appendFileSync appends p to f and fsyncs, so the appended bytes are durable
// before the call returns. Used by the WAL so a confirmed write survives power
// loss.
func appendFileSync(f *os.File, p []byte) error {
	if _, err := f.Write(p); err != nil {
		return err
	}
	return f.Sync()
}

var _ io.Writer = (*os.File)(nil)
