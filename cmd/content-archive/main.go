// content-archive is an operator-only command, excluded from runtime packaging.
package main

import (
	"content/internal/archivecmd"
	"content/internal/config"
	"content/storage/postgres"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	path := flag.String("manifest", "", "explicit archive manifest path")
	pin := flag.String("sha256", "", "expected manifest SHA256, mandatory for apply")
	apply := flag.Bool("apply", false, "persist reviewed receipts; default is read-only plan")
	flag.Parse()
	if *path == "" || flag.NArg() != 0 || (*apply && len(*pin) != 64) {
		return errors.New("manifest required; apply also requires exact sha256")
	}
	f, e := os.Open(*path)
	if e != nil {
		return errors.New("cannot open manifest")
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if e != nil {
		return errors.New("cannot read manifest")
	}
	m, e := archivecmd.Decode(raw, *pin)
	if e != nil {
		return e
	}
	dsn, schema, installation := os.Getenv("CONTENT_MIGRATION_DATABASE_URL"), os.Getenv("CONTENT_SCHEMA"), os.Getenv("CONTENT_INSTALLATION_ID")
	if dsn == "" || schema == "" || installation == "" {
		return errors.New("operator database URL, schema and installation required; no runtime credential fallback")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	connect, done := context.WithTimeout(ctx, 10*time.Second)
	options := []postgres.Option{postgres.WithConnectionLimit(1)}
	origins, e := config.LoadPublicTagOrigins(os.Getenv)
	if e != nil {
		done()
		return e
	}
	if len(origins) > 0 {
		options = append(options, postgres.WithPublicTagOrigins(origins))
	}
	store, e := postgres.Open(connect, dsn, schema, installation, options...)
	done()
	if e != nil {
		return errors.New("operator database unavailable (details redacted)")
	}
	defer store.Close()
	plan, e := archivecmd.Plan(ctx, store, m)
	if e != nil {
		return e
	}
	sum := sha256.Sum256(raw)
	enc := json.NewEncoder(os.Stdout)
	existing := 0
	for _, p := range plan {
		if p.Existing {
			existing++
		}
	}
	if e = enc.Encode(map[string]any{"mode": "plan", "manifest_sha256": hex.EncodeToString(sum[:]), "posts": len(plan), "existing_receipts": existing, "publication": m.Publication}); e != nil {
		return errors.New("plan output unavailable")
	}
	if !*apply {
		return nil
	}
	return archivecmd.Apply(ctx, store, plan, func(id string, r postgres.ArchiveReceipt) error {
		return enc.Encode(map[string]any{"mode": "enqueued", "post_id": id, "event_id": r.EventID, "content_id": r.ContentID, "revision": r.Revision, "state": r.State})
	})
}
