package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"content/cms"
	"content/publishing"
	"github.com/jackc/pgx/v5"
)

type ArchiveRequest struct {
	Publication, PostID string
	ExpectedRevision    int64
	Enrollment          publishing.Enrollment
}
type ArchiveReceipt struct {
	EventID, ContentID, State string
	Revision                  int64
}

var ErrArchiveConflict = errors.New("archive enrollment conflicts with existing stream or receipt")

// EnqueueArchive is an explicit bounded operator action, never called by import
// or normal startup. The post row lock serializes enrollment with every CMS edit.
// No network occurs here; archive bytes and the paused live stream commit together.
func (s *Store) EnqueueArchive(ctx context.Context, r ArchiveRequest) (ArchiveReceipt, error) {
	var receipt ArchiveReceipt
	if r.Publication == "" || r.PostID == "" || r.ExpectedRevision < 1 {
		return receipt, errors.New("explicit post and expected revision required")
	}
	if err := r.Enrollment.Validate(); err != nil {
		return receipt, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return receipt, err
	}
	defer tx.Rollback(ctx)
	// Lock independently from the revision join. At READ COMMITTED a writer
	// may commit while this waits; the next statement must read its fresh revision.
	var lockedID string
	err = tx.QueryRow(ctx, "SELECT id FROM "+s.schema+".posts WHERE publication_id=$1 AND id=$2 FOR UPDATE", r.Publication, r.PostID).Scan(&lockedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return receipt, cms.ErrNotFound
	}
	if err != nil {
		return receipt, err
	}
	p, err := scan(tx.QueryRow(ctx, s.selectSQL()+" WHERE p.publication_id=$1 AND p.id=$2", r.Publication, r.PostID))
	if err != nil {
		return receipt, err
	}
	var destination, source, site string
	err = tx.QueryRow(ctx, "SELECT event_id::text,content_id::text,state,revision,destination,source_id,site_url FROM "+s.schema+".publishing_archives WHERE publication_id=$1 AND post_id=$2", r.Publication, r.PostID).Scan(&receipt.EventID, &receipt.ContentID, &receipt.State, &receipt.Revision, &destination, &source, &site)
	if err == nil {
		if receipt.ContentID != p.UUID || receipt.Revision != r.ExpectedRevision || destination != r.Enrollment.Destination || source != r.Enrollment.SourceID || site != r.Enrollment.SiteURL {
			return ArchiveReceipt{}, ErrArchiveConflict
		}
		return receipt, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return receipt, err
	}
	if p.Revision != r.ExpectedRevision {
		return receipt, cms.ErrStaleUpdate
	}
	if p.Kind != "post" || p.Status != "published" || p.Visibility != "public" || p.PublishedAt == nil {
		return receipt, errors.New("archive enrollment requires a public published post")
	}
	var exists bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM "+s.schema+".publishing_streams WHERE publication_id=$1 AND post_id=$2)", r.Publication, r.PostID).Scan(&exists); err != nil {
		return receipt, err
	}
	if exists {
		return receipt, ErrArchiveConflict
	}
	eventID, err := publishing.UUID()
	if err != nil {
		return receipt, err
	}
	tags, err := s.publicTags(p)
	if err != nil {
		return receipt, err
	}
	a, err := publishing.MapArchive(publishing.Intent{Publication: p.Publication, EventID: eventID, Kind: "post.published", Revision: p.Revision, OccurredAt: p.UpdatedAt, Snapshot: publishing.Snapshot{ID: p.ID, UUID: p.UUID, Title: p.Title, Slug: p.Slug, Status: p.Status, Visibility: p.Visibility, PublishedAt: p.PublishedAt, HTML: p.HTML, Tags: tags}}, r.Enrollment)
	if err != nil {
		return receipt, err
	}
	payload, err := publishing.EncodeArchive(a)
	if err != nil {
		return receipt, err
	}
	_, err = tx.Exec(ctx, "INSERT INTO "+s.schema+`.publishing_streams(publication_id,post_id,content_id,destination,source_id,site_url,blocked_reason) VALUES($1,$2,$3,$4,$5,$6,'archive_pending')`, p.Publication, p.ID, p.UUID, r.Enrollment.Destination, r.Enrollment.SourceID, r.Enrollment.SiteURL)
	if err != nil {
		return receipt, err
	}
	_, err = tx.Exec(ctx, "INSERT INTO "+s.schema+`.publishing_archives(event_id,publication_id,post_id,content_id,revision,destination,source_id,site_url,payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, eventID, p.Publication, p.ID, p.UUID, p.Revision, r.Enrollment.Destination, r.Enrollment.SourceID, r.Enrollment.SiteURL, payload)
	if err != nil {
		return receipt, err
	}
	_, err = tx.Exec(ctx, "UPDATE "+s.schema+".posts SET archive_pending=true WHERE publication_id=$1 AND id=$2", p.Publication, p.ID)
	if err != nil {
		return receipt, err
	}
	receipt = ArchiveReceipt{eventID, p.UUID, "pending", p.Revision}
	return receipt, tx.Commit(ctx)
}

// ArchiveQueue adapts the durable archive lane to the existing retry/lease worker.
// Its preparation step is intentionally empty: no automatic historical enrollment.
type ArchiveQueue struct{ store *Store }

func (s *Store) ArchiveQueue() ArchiveQueue                                      { return ArchiveQueue{s} }
func (q ArchiveQueue) PrepareNext(context.Context, string, string) (bool, error) { return false, nil }
func (q ArchiveQueue) Claim(ctx context.Context, publication, destination string, lease time.Duration) (*publishing.Job, error) {
	if publication == "" || destination == "" || lease < time.Millisecond || lease > 5*time.Minute {
		return nil, errors.New("invalid archive claim")
	}
	token, err := publishing.UUID()
	if err != nil {
		return nil, err
	}
	var job publishing.Job
	err = q.store.pool.QueryRow(ctx, `WITH candidate AS (
 SELECT event_id FROM `+q.store.schema+`.publishing_archives
 WHERE publication_id=$1 AND destination=$2 AND ((state='pending' AND next_attempt_at<=clock_timestamp()) OR (state='leased' AND lease_until<=clock_timestamp()))
 ORDER BY created_at,event_id FOR UPDATE SKIP LOCKED LIMIT 1)
 UPDATE `+q.store.schema+`.publishing_archives a SET state='leased',attempts=attempts+1,lease_token=$3,lease_until=clock_timestamp()+($4::bigint*interval '1 millisecond')
 FROM candidate WHERE a.event_id=candidate.event_id RETURNING a.event_id::text,a.lease_token::text,a.payload,a.attempts`, publication, destination, token, lease.Milliseconds()).Scan(&job.EventID, &job.LeaseToken, &job.Payload, &job.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &job, err
}
func (q ArchiveQueue) Finish(ctx context.Context, job publishing.Job, out publishing.Outcome) error {
	state := out.State
	if state == "retry" {
		state = "pending"
	}
	if (state != "pending" && state != "accepted" && state != "quarantined") || out.Delay < 0 || out.Delay > time.Hour || len(out.Code) > 64 {
		return errors.New("invalid archive outcome")
	}
	if state == "accepted" && out.Result != "applied" {
		state = "quarantined"
		out.Code = "archive_ack_mismatch"
	}
	s := q.store
	// Resolve identifiers without locking, then always lock post before receipt:
	// same order as enrollment and editor writes, avoiding lock-order inversion.
	var pub, id string
	if err := s.pool.QueryRow(ctx, "SELECT publication_id,post_id FROM "+s.schema+".publishing_archives WHERE event_id=$1", job.EventID).Scan(&pub, &id); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var frozen bool
	if err = tx.QueryRow(ctx, "SELECT archive_pending FROM "+s.schema+".posts WHERE publication_id=$1 AND id=$2 FOR UPDATE", pub, id).Scan(&frozen); err != nil {
		return err
	}
	var contentID string
	var revision int64
	var payload []byte
	var destination string
	err = tx.QueryRow(ctx, "SELECT content_id::text,revision,payload,destination FROM "+s.schema+".publishing_archives WHERE event_id=$1 AND state='leased' AND lease_token=$2 AND lease_until>clock_timestamp() FOR UPDATE", job.EventID, job.LeaseToken).Scan(&contentID, &revision, &payload, &destination)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return err
	}
	if !frozen {
		return errors.New("archive edit barrier missing")
	}
	var accepted *string
	if s.federationDestination != "" && out.Confirmation != nil {
		a, err := publishing.DecodeArchive(payload)
		if err != nil || a.EventID != job.EventID || a.Publication != pub || a.ContentID != contentID || a.Revision != fmt.Sprint(revision) {
			return errors.New("archive confirmation binding mismatch")
		}
		if err = s.confirmPublic(ctx, tx, pub, id, destination, a.EventID, a.ContentID, a.ObjectInstance, a.Revision, "content.published", out); err != nil {
			return err
		}
	}
	if state == "accepted" {
		result := "applied"
		accepted = &result
		tag, err := tx.Exec(ctx, "UPDATE "+s.schema+`.publishing_streams SET last_revision=$3,object_instance=$4,lifecycle='public',blocked_reason=NULL WHERE publication_id=$1 AND post_id=$2 AND blocked_reason='archive_pending' AND last_revision=0 AND lifecycle='unpublished' AND content_id=$4`, pub, id, revision, contentID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrArchiveConflict
		}
		if _, err = tx.Exec(ctx, "UPDATE "+s.schema+".posts SET archive_pending=false WHERE publication_id=$1 AND id=$2", pub, id); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, "UPDATE "+s.schema+`.publishing_archives SET state=$3,lease_token=NULL,lease_until=NULL,next_attempt_at=clock_timestamp()+($4::bigint*interval '1 millisecond'),last_error_code=$5,accepted_result=$6 WHERE event_id=$1 AND lease_token=$2`, job.EventID, job.LeaseToken, state, out.Delay.Milliseconds(), out.Code, accepted)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
