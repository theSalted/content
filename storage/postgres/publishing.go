package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"content/publishing"
	"github.com/jackc/pgx/v5"
)

// WithNewPostPublishing is opt-in and applies only to newly created posts.
// Enrollment freezes the source, destination and URL base; historical posts and
// outbox rows are never enrolled by migrations or by a delivery worker.
func WithNewPostPublishing(c publishing.Enrollment) Option {
	return func(s *Store) error {
		if err := c.Validate(); err != nil {
			return err
		}
		s.enrollment = &c
		return nil
	}
}

type Option func(*Store) error

func (s *Store) enroll(ctx context.Context, tx pgx.Tx, publication, id, uuid string) error {
	if s.enrollment == nil {
		return nil
	}
	c := s.enrollment
	_, err := tx.Exec(ctx, "INSERT INTO "+s.schema+`.publishing_streams(publication_id,post_id,content_id,destination,source_id,site_url) VALUES($1,$2,$3,$4,$5,$6)`, publication, id, uuid, c.Destination, c.SourceID, c.SiteURL)
	return err
}

// PrepareNext durably translates one immutable intent for an explicitly
// enrolled stream. It never fetches the mutable current post or sends HTTP.
func (s *Store) PrepareNext(ctx context.Context, publication, destination string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var id, contentID, state, instance string
	var cfg publishing.Enrollment
	var i publishing.Intent
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT s.post_id,s.content_id::text,s.lifecycle,COALESCE(s.object_instance::text,''),s.source_id,s.site_url,e.event_id::text,e.revision,e.event_type,e.created_at,e.snapshot
 FROM `+s.schema+`.publishing_streams s JOIN LATERAL (
 SELECT event_id,revision,event_type,created_at,snapshot FROM `+s.schema+`.outbox_events e
 WHERE e.publication_id=s.publication_id AND e.post_id=s.post_id AND e.revision>s.last_revision ORDER BY e.revision LIMIT 1
 ) e ON true WHERE s.publication_id=$1 AND s.destination=$2 AND s.blocked_reason IS NULL
 ORDER BY e.created_at,s.post_id FOR UPDATE OF s SKIP LOCKED LIMIT 1`, publication, destination).Scan(&id, &contentID, &state, &instance, &cfg.SourceID, &cfg.SiteURL, &i.EventID, &i.Revision, &i.Kind, &i.OccurredAt, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	cfg.Destination = destination
	i.Publication = publication
	block := func(code string) (bool, error) {
		_, err := tx.Exec(ctx, "UPDATE "+s.schema+".publishing_streams SET blocked_reason=$3 WHERE publication_id=$1 AND post_id=$2", publication, id, code)
		if err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	if json.Unmarshal(raw, &i.Snapshot) != nil || i.Snapshot.UUID != contentID || i.Snapshot.ID != id {
		return block("invalid_snapshot")
	}
	event, next, err := publishing.Map(i, cfg, state, instance)
	if err != nil {
		return block("mapping_rejected")
	}
	payload, err := publishing.Encode(event)
	if err != nil {
		return block("payload_rejected")
	}
	_, err = tx.Exec(ctx, "INSERT INTO "+s.schema+`.publishing_deliveries(event_id,publication_id,post_id,revision,destination,payload) VALUES($1,$2,$3,$4,$5,$6)`, i.EventID, publication, id, i.Revision, destination, payload)
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, "UPDATE "+s.schema+`.publishing_streams SET last_revision=$3,object_instance=$4,lifecycle=$5 WHERE publication_id=$1 AND post_id=$2`, publication, id, i.Revision, event.ObjectInstance, next)
	if err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
func (s *Store) Claim(ctx context.Context, publication, destination string, lease time.Duration) (*publishing.Job, error) {
	if publication == "" || destination == "" || lease < time.Millisecond || lease > 5*time.Minute {
		return nil, errors.New("invalid delivery claim")
	}
	token, err := publishing.UUID()
	if err != nil {
		return nil, err
	}
	var job publishing.Job
	err = s.pool.QueryRow(ctx, `WITH candidate AS (
 SELECT d.event_id FROM `+s.schema+`.publishing_deliveries d
 WHERE d.publication_id=$1 AND d.destination=$2 AND
 ((d.state='pending' AND d.next_attempt_at<=clock_timestamp()) OR (d.state='leased' AND d.lease_until<=clock_timestamp()))
 AND NOT EXISTS(SELECT 1 FROM `+s.schema+`.publishing_deliveries earlier WHERE earlier.publication_id=d.publication_id AND earlier.post_id=d.post_id AND earlier.revision<d.revision AND earlier.state<>'accepted')
 ORDER BY d.created_at,d.event_id FOR UPDATE OF d SKIP LOCKED LIMIT 1)
 UPDATE `+s.schema+`.publishing_deliveries d SET state='leased',attempts=attempts+1,lease_token=$3,lease_until=clock_timestamp()+($4::bigint*interval '1 millisecond')
 FROM candidate WHERE d.event_id=candidate.event_id RETURNING d.event_id::text,d.lease_token::text,d.payload,d.attempts`, publication, destination, token, lease.Milliseconds()).Scan(&job.EventID, &job.LeaseToken, &job.Payload, &job.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &job, err
}

var ErrLeaseLost = errors.New("delivery lease no longer owned")

func (s *Store) Finish(ctx context.Context, job publishing.Job, outcome publishing.Outcome) error {
	state := outcome.State
	if state == "retry" {
		state = "pending"
	}
	if (state != "pending" && state != "accepted" && state != "quarantined") || outcome.Delay < 0 || outcome.Delay > time.Hour || len(outcome.Code) > 64 {
		return errors.New("invalid delivery outcome")
	}
	var accepted *string
	if state == "accepted" {
		if outcome.Result != "applied" && outcome.Result != "stale" {
			return errors.New("invalid acknowledgment result")
		}
		accepted = &outcome.Result
	}
	if s.federationDestination != "" && outcome.Confirmation != nil {
		return s.finishConfirmedDelivery(ctx, job, outcome, state, accepted)
	}
	tag, err := s.pool.Exec(ctx, "UPDATE "+s.schema+`.publishing_deliveries SET state=$3,lease_token=NULL,lease_until=NULL,next_attempt_at=clock_timestamp()+($4::bigint*interval '1 millisecond'),last_error_code=$5,accepted_result=$6 WHERE event_id=$1 AND lease_token=$2 AND state='leased' AND lease_until>clock_timestamp()`, job.EventID, job.LeaseToken, state, outcome.Delay.Milliseconds(), outcome.Code, accepted)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}
