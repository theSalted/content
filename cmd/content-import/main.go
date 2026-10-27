// content-import is an offline operator command, never an HTTP creation API.
package main

import (
	"content/migration/ghost"
	"content/storage/postgres"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

func main() {
	file := flag.String("file", "", "Ghost export path")
	source := flag.String("source", "", "stable source namespace")
	publication := flag.String("publication", "", "destination publication")
	publicSnapshot := flag.String("public-snapshot", "", "optional matching public API snapshot for exact computed metadata")
	sourceURL := flag.String("source-url", "", "explicit original HTTPS origin for Ghost export URL placeholders")
	apply := flag.Bool("apply", false, "commit archive and compatible records; default is plan only")
	flag.Parse()
	if *file == "" || *source == "" || *publication == "" {
		fail("file, source and publication are required")
	}
	input, e := os.Open(*file)
	if e != nil {
		fail("cannot read export")
	}
	defer input.Close()
	raw, e := io.ReadAll(io.LimitReader(input, (64<<20)+1))
	if e != nil {
		fail("cannot read export")
	}
	var snapshot []byte
	if *publicSnapshot != "" {
		f, err := os.Open(*publicSnapshot)
		if err != nil {
			fail("cannot read public snapshot")
		}
		snapshot, err = io.ReadAll(io.LimitReader(f, (64<<20)+1))
		f.Close()
		if err != nil {
			fail("cannot read public snapshot")
		}
	}
	plan, e := ghost.BuildWithOptions(raw, *publication, ghost.Options{SourceURL: *sourceURL, PublicSnapshot: snapshot})
	if e != nil {
		fail(e.Error())
	}
	if !*apply {
		result := postgres.ImportResult{Reasons: map[string]int{}}
		for _, r := range plan.Records {
			if r.Post != nil {
				result.Imported++
			} else {
				result.Quarantined++
				result.Reasons[r.Reason]++
			}
		}
		json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": "plan", "export_sha256": plan.SHA256, "public_only": plan.PublicOnly, "source_url": plan.Options.SourceURL, "public_snapshot_sha256": plan.PublicSnapshotSHA256, "result": result})
		return
	}
	if os.Getenv("CONTENT_PUBLISHING_ENROLL_NEW") == "true" || os.Getenv("CONTENT_PUBLISHING_DELIVERY_ENABLED") == "true" {
		fail("disable enrollment and delivery for import")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	store, e := postgres.Open(ctx, os.Getenv("CONTENT_MIGRATION_DATABASE_URL"), os.Getenv("CONTENT_SCHEMA"), os.Getenv("CONTENT_INSTALLATION_ID"))
	if e != nil {
		fail("operator database unavailable or schema not migrated")
	}
	defer store.Close()
	result, e := store.ImportGhost(ctx, *publication, *source, plan)
	if e != nil {
		fail("import rejected; transaction rolled back (details withheld); reconcile source/identity or inspect grants")
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": "applied", "export_sha256": plan.SHA256, "public_only": plan.PublicOnly, "source_url": plan.Options.SourceURL, "public_snapshot_sha256": plan.PublicSnapshotSHA256, "result": result})
}
func fail(message string) { fmt.Fprintln(os.Stderr, message); os.Exit(1) }
