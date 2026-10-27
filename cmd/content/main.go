package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"content/internal/config"
	"content/internal/httpapi"
	"content/internal/review"
	"content/scheduling"
	"content/storage/postgres"
)

func main() {
	if err := run(); err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) == 2 && os.Args[1] == "hash-password" {
		info, err := os.Stdin.Stat()
		if err != nil {
			return errors.New("cannot read password input")
		}
		if info.Mode()&os.ModeCharDevice != 0 {
			return errors.New("hash-password requires piped input from a password manager or protected file; do not put a password in command arguments")
		}
		raw, err := io.ReadAll(io.LimitReader(os.Stdin, 1026))
		if err != nil {
			return errors.New("cannot read password input")
		}
		value, err := review.HashPassword(strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r"))
		if err != nil {
			return err
		}
		fmt.Println(value)
		return nil
	}

	if len(os.Args) != 2 || (os.Args[1] != "serve" && os.Args[1] != "migrate" && os.Args[1] != "deliver" && os.Args[1] != "media-cleanup") {
		fmt.Println("Usage: content serve | migrate | deliver | hash-password | media-cleanup\nConfiguration and supported API: docs/implementation.md")
		return nil
	}
	command := os.Args[1]
	if command == "media-cleanup" {
		return cleanupMedia()
	}
	if command == "deliver" {
		return deliver()
	}
	cfg, err := config.Load(command, os.Getenv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if command == "migrate" {
		migrationCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		if err = postgres.Migrate(migrationCtx, cfg.DatabaseURL, cfg.Schema, cfg.Installation); err != nil {
			return errors.New("migration failed; check schema ownership, direct connection and installation configuration (connection details redacted)")
		}
		slog.Info("migration complete")
		return nil
	}
	key, err := httpapi.ParseAdminKey(cfg.AdminKey)
	if err != nil {
		return err
	}
	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	options := []postgres.Option{postgres.WithScheduling(cfg.Scheduler)}
	if cfg.FederationDestination != "" {
		options = append(options, postgres.WithFederationProjection(cfg.FederationDestination))
	}
	if len(cfg.PublicTagOrigins) > 0 {
		options = append(options, postgres.WithPublicTagOrigins(cfg.PublicTagOrigins))
	}
	if cfg.Media != nil {
		options = append(options, postgres.WithMediaOrigin(cfg.Media.Origin, cfg.Media.OriginAliases...))
	}
	if cfg.Enrollment != nil {
		options = append(options, postgres.WithNewPostPublishing(*cfg.Enrollment))
	}
	store, err := postgres.Open(connectCtx, cfg.DatabaseURL, cfg.Schema, cfg.Installation, options...)
	cancel()
	if err != nil {
		return errors.New("database unavailable or schema not migrated for this installation (connection details redacted)")
	}
	defer store.Close()
	check, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = store.CheckPublishingManagement(check)
	cancel()
	if err != nil {
		return errors.New("publishing management schema or runtime grants unavailable")
	}
	if cfg.Scheduler {
		workerCtx, stopWorker := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			scheduling.Run(workerCtx, store, cfg.Publication, func(error) { slog.Warn("scheduled publication failed; job retained for retry") })
		}()
		defer func() { stopWorker(); <-done }()
	}
	mediaService, closeMedia, err := openMedia(cfg, store)
	if err != nil {
		return err
	}
	defer closeMedia()
	if cfg.Review != nil {
		cfg.Review.Media = mediaService
		cfg.Review.Author = cfg.DefaultAuthor
		cfg.Review.Sessions = store
	}
	httpOptions := []httpapi.Option{httpapi.WithSiteMetadata(httpapi.SiteMetadata{Title: cfg.SiteTitle, Description: cfg.SiteDescription, Logo: cfg.SiteLogo}), httpapi.WithScheduling(cfg.Scheduler)}
	if cfg.PageURL != "" {
		httpOptions = append(httpOptions, httpapi.WithPageURL(cfg.PageURL))
	}
	if mediaService != nil {
		httpOptions = append(httpOptions, httpapi.WithMedia(mediaService))
	}
	if cfg.DefaultAuthor != nil {
		httpOptions = append(httpOptions, httpapi.WithDefaultAuthor(*cfg.DefaultAuthor))
		if cfg.PublishingIdentity {
			httpOptions = append(httpOptions, httpapi.WithPublishingIdentity(*cfg.DefaultAuthor))
		}
	}
	handler, err := httpapi.New(store, cfg.Publication, cfg.SiteURL, key, cfg.ContentKey, httpOptions...)
	if err != nil {
		return err
	}
	var root http.Handler = handler
	if cfg.Review != nil {
		ui, err := review.New(store, *cfg.Review)
		if err != nil {
			return err
		}
		root = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if review.Handles(r.URL.Path) {
				ui.ServeHTTP(w, r)
				return
			}
			handler.ServeHTTP(w, r)
		})
	}
	server := &http.Server{Addr: cfg.Listen, Handler: root, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	result := make(chan error, 1)
	go func() {
		slog.Info("content listening", "address", cfg.Listen, "profile", "posts-html-v1")
		result <- server.ListenAndServe()
	}()
	select {
	case err = <-result:
		if !errors.Is(err, http.ErrServerClosed) {
			return errors.New("HTTP server failed")
		}
		return nil
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
