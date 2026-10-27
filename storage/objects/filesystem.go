// Package objects implements private blob stores, never public directory serving.
package objects

import (
	"bytes"
	"content/media"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"time"
)

type Filesystem struct{ root *os.Root }

func NewFilesystem(directory string) (*Filesystem, error) {
	if !filepath.IsAbs(directory) {
		return nil, errors.New("media directory must be absolute")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, errors.New("cannot create media directory")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, errors.New("cannot open media directory")
	}
	return &Filesystem{root: root}, nil
}
func (f *Filesystem) Close() error { return f.root.Close() }
func (f *Filesystem) Put(ctx context.Context, key string, data []byte, _ string) error {
	if !media.ValidObjectKey(key) || len(data) > media.MaxBytes {
		return media.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// os.Root prevents path traversal and symlink escape, including during races.
	dir := path.Dir(key)
	if err := f.root.MkdirAll(dir, 0700); err != nil {
		return err
	}
	temp := dir + "/.upload-" + rand.Text()
	file, err := f.root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.root.Remove(temp)
	_, err = io.Copy(file, bytes.NewReader(data))
	if err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	// Content-addressed keys and serialized metadata finalization mean retries
	// write identical bytes. A failed/partial temporary file is never served.
	if err = f.root.Rename(temp, key); err != nil {
		return err
	}
	// Persist the directory entry before metadata may mark the object ready.
	directory, err := f.root.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
func (f *Filesystem) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if !media.ValidObjectKey(key) {
		return nil, media.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return f.root.Open(key)
}
func (f *Filesystem) Delete(ctx context.Context, key string) error {
	if !media.ValidObjectKey(key) {
		return media.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err := f.root.Remove(key)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// CleanupTemporary only removes this backend's stale, named upload scratch
// files beneath its private v1 namespace. It never traverses symlinks or removes
// ready content-addressed objects. Call with an age of at least 24 hours.
func (f *Filesystem) CleanupTemporary(ctx context.Context, before time.Time) (int, error) {
	if before.After(time.Now().Add(-24 * time.Hour)) {
		return 0, media.ErrInvalid
	}
	count := 0
	e := fs.WalkDir(f.root.FS(), "v1", func(name string, entry fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && name == "v1" {
			return nil
		}
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if count >= 100 {
			return fs.SkipAll
		}
		if !scratchPath.MatchString(name) {
			return nil
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		if !info.ModTime().Before(before) {
			return nil
		}
		if e = f.root.Remove(name); e != nil && !errors.Is(e, fs.ErrNotExist) {
			return e
		}
		count++
		return nil
	})
	return count, e
}

var scratchPath = regexp.MustCompile(`^v1/[0-9a-f]{64}/\.upload-[A-Z2-7]{26}$`)
