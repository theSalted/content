package main

import (
	"content/internal/config"
	"content/media"
	"content/storage/objects"
	"content/storage/postgres"
	"context"
	"errors"
	"log/slog"
	"os"
	"time"
)

func openMedia(c config.Config, store *postgres.Store) (*media.Service, func(), error) {
	if c.Media == nil {
		return nil, func() {}, nil
	}
	var blob media.Objects
	close := func() {}
	switch c.Media.Backend {
	case "filesystem":
		f, e := objects.NewFilesystem(c.Media.Directory)
		if e != nil {
			return nil, close, e
		}
		blob = f
		close = func() { _ = f.Close() }
	case "s3":
		s, e := objects.NewS3(c.Media.S3)
		if e != nil {
			return nil, close, e
		}
		blob = s
		close = s.Close
	default:
		return nil, close, errors.New("unsupported media backend")
	}
	service, e := media.New(store, blob, c.Media.Origin, c.Installation+"\x00"+c.Schema, c.Media.OriginAliases...)
	if e != nil {
		close()
		return nil, func() {}, e
	}
	return service, close, nil
}
func cleanupMedia() error {
	c, e := config.LoadMediaCleanup(os.Getenv)
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	store, e := postgres.Open(ctx, c.DatabaseURL, c.Schema, c.Installation)
	if e != nil {
		return errors.New("media cleanup database unavailable (details redacted)")
	}
	defer store.Close()
	service, close, e := openMedia(c, store)
	if e != nil {
		return e
	}
	defer close()
	count, e := service.Cleanup(ctx, c.Publication, time.Now().Add(-24*time.Hour))
	if e != nil {
		return errors.New("pending media cleanup failed; retry safely (details redacted)")
	}
	temps := 0
	if fs, ok := service.Objects.(*objects.Filesystem); ok {
		temps, e = fs.CleanupTemporary(ctx, time.Now().Add(-24*time.Hour))
		if e != nil {
			return errors.New("media temporary-file cleanup failed (details redacted)")
		}
	}
	slog.Info("media cleanup complete", "pending_objects", count, "temporary_files", temps)
	return nil
}
