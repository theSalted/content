package postgres

import (
	"content/migration/ghost"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"github.com/jackc/pgx/v5"
	"strings"
	"unicode/utf8"
)

type ImportResult struct {
	Imported      int            `json:"imported"`
	Quarantined   int            `json:"quarantined"`
	Unchanged     int            `json:"unchanged"`
	OutboxWritten int            `json:"outbox_written"`
	Reasons       map[string]int `json:"reasons"`
}

// ImportGhost requires an operator role and an already migrated schema. It does
// not call Create/writeEvent/enrollment. No delivery record or public activity
// is generated, even for published records. Changed exports are never an
// implicit overwrite of an imported post or subsequent author edits.
func (s *Store) ImportGhost(ctx context.Context, publication, source string, plan ghost.Plan) (result ImportResult, resultErr error) {
	result = ImportResult{Reasons: map[string]int{}}
	defer func() {
		if resultErr != nil {
			result = ImportResult{}
		}
	}()
	for _, value := range []string{publication, source} {
		if value == "" || len(value) > 128 || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
			return result, errors.New("invalid import namespace")
		}
	}
	if s.enrollment != nil {
		return result, errors.New("imports require delivery enrollment disabled")
	}
	validated, e := ghost.BuildWithOptions(plan.Raw, publication, plan.Options)
	if e != nil {
		return result, e
	}
	plan = validated
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return result, e
	}
	defer tx.Rollback(ctx)
	hash := sha256.Sum256([]byte("content:cms:source-import:v1\x00" + s.schema + "\x00" + publication))
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(binary.BigEndian.Uint64(hash[:8]))); e != nil {
		return result, e
	}
	_, e = tx.Exec(ctx, "INSERT INTO "+s.schema+`.source_import_batches(publication_id,source_namespace,export_sha256,raw_export,public_only,source_url,public_snapshot_sha256,raw_public_snapshot) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT DO NOTHING`, publication, source, plan.SHA256, string(plan.Raw), plan.PublicOnly, plan.Options.SourceURL, plan.PublicSnapshotSHA256, string(plan.Options.PublicSnapshot))
	if e != nil {
		return result, e
	}
	var priorSourceURL, priorSnapshotHash string
	if e = tx.QueryRow(ctx, "SELECT source_url, public_snapshot_sha256 FROM "+s.schema+`.source_import_batches WHERE publication_id=$1 AND source_namespace=$2 AND export_sha256=$3`, publication, source, plan.SHA256).Scan(&priorSourceURL, &priorSnapshotHash); e != nil {
		return result, e
	}
	if priorSourceURL != plan.Options.SourceURL || priorSnapshotHash != plan.PublicSnapshotSHA256 {
		return result, errors.New("source URL mapping changed; explicit reconciliation required")
	}
	for _, record := range plan.Records {
		var prior string
		e = tx.QueryRow(ctx, "SELECT source_sha256 FROM "+s.schema+`.source_import_records WHERE publication_id=$1 AND source_namespace=$2 AND object_kind=$3 AND source_id=$4`, publication, source, record.Kind, record.SourceID).Scan(&prior)
		if e == nil {
			if prior != record.SHA256 {
				return result, errors.New("source record changed; explicit reconciliation required")
			}
			result.Unchanged++
			continue
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return result, e
		}
		state := "quarantined"
		var id, uuid any
		var revision any
		if record.Post != nil {
			p := *record.Post
			if p.Publication != publication {
				return result, errors.New("import publication mismatch")
			}
			_, e = tx.Exec(ctx, "INSERT INTO "+s.schema+`.posts(publication_id,id,uuid,title,slug,status,visibility,revision,created_at,updated_at,published_at,kind) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, p.Publication, p.ID, p.UUID, p.Title, p.Slug, p.Status, p.Visibility, p.Revision, p.CreatedAt, p.UpdatedAt, p.PublishedAt, p.Kind)
			if e != nil {
				return result, errors.New("post identity or slug conflict; no import changes committed")
			}
			if e = s.writeRevision(ctx, tx, p); e != nil {
				return result, e
			}
			if e = s.syncMedia(ctx, tx, p); e != nil {
				return result, e
			}
			id = p.ID
			uuid = p.UUID
			revision = p.Revision
			state = "imported"
			result.Imported++
		} else {
			result.Quarantined++
			result.Reasons[record.Reason]++
		}
		_, e = tx.Exec(ctx, "INSERT INTO "+s.schema+`.source_import_records(publication_id,source_namespace,object_kind,source_id,source_sha256,source_record,state,reason,cms_id,cms_uuid,imported_revision) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, publication, source, record.Kind, record.SourceID, record.SHA256, record.Raw, state, record.Reason, id, uuid, revision)
		if e != nil {
			return result, e
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return ImportResult{}, e
	}
	return result, nil
}
