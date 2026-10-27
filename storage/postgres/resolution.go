package postgres

import (
	"content/publishing"
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
)

// Accepted delivery is immutable and independent of discovery query retries.
// This copies exact bytes once; it does not enqueue or replay public activities.
func (s *Store) PrepareResolution(ctx context.Context, pub, destination string) (bool, error) {
	if s.federationDestination == "" {
		return false, nil
	}
	if pub == "" || destination != s.federationDestination {
		return false, errors.New("invalid resolution scope")
	}
	tag, err := s.pool.Exec(ctx, `INSERT INTO `+s.schema+`.federation_resolution_jobs(publication_id,event_id,destination,post_id,payload,lane)
 SELECT x.publication_id,x.event_id,x.destination,x.post_id,x.payload,x.lane FROM (
 SELECT publication_id,event_id,destination,post_id,payload,created_at,'live'::text lane FROM `+s.schema+`.publishing_deliveries WHERE state='accepted' AND accepted_result='applied'
 UNION ALL SELECT publication_id,event_id,destination,post_id,payload,created_at,'archive'::text lane FROM `+s.schema+`.publishing_archives WHERE state='accepted' AND accepted_result='applied') x
 WHERE x.publication_id=$1 AND x.destination=$2 AND NOT EXISTS(SELECT 1 FROM `+s.schema+`.federation_resolution_jobs j WHERE j.publication_id=x.publication_id AND j.event_id=x.event_id AND j.destination=x.destination)
 ORDER BY x.created_at,x.event_id LIMIT 1 ON CONFLICT DO NOTHING`, pub, destination)
	return err == nil && tag.RowsAffected() > 0, err
}
func (s *Store) ClaimResolution(ctx context.Context, pub, destination string, lease time.Duration) (*publishing.Job, error) {
	if s.federationDestination == "" || pub == "" || destination != s.federationDestination || lease < time.Millisecond || lease > 5*time.Minute {
		return nil, errors.New("invalid resolution claim")
	}
	token, err := publishing.UUID()
	if err != nil {
		return nil, err
	}
	var job publishing.Job
	err = s.pool.QueryRow(ctx, `WITH candidate AS (
 SELECT event_id FROM `+s.schema+`.federation_resolution_jobs WHERE publication_id=$1 AND destination=$2 AND ((state='pending' AND next_attempt_at<=clock_timestamp()) OR(state='leased' AND lease_until<=clock_timestamp()))
 ORDER BY next_attempt_at,event_id FOR UPDATE SKIP LOCKED LIMIT 1)
 UPDATE `+s.schema+`.federation_resolution_jobs j SET state='leased',lease_token=$3,lease_until=clock_timestamp()+($4::bigint*interval '1 millisecond'),attempts=attempts+1
 FROM candidate c WHERE j.event_id=c.event_id AND j.publication_id=$1 AND j.destination=$2 RETURNING j.event_id::text,j.lease_token::text,j.payload,j.attempts`, pub, destination, token, lease.Milliseconds()).Scan(&job.EventID, &job.LeaseToken, &job.Payload, &job.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &job, err
}
func (s *Store) FinishResolution(ctx context.Context, job publishing.Job, out publishing.ResolutionOutcome) error {
	state := out.State
	if state == "retry" {
		state = "pending"
	}
	if s.federationDestination == "" || (state != "pending" && state != "complete" && state != "quarantined") || out.Delay < 0 || out.Delay > time.Hour || len(out.Code) > 64 || (state == "complete") != (out.Receipt != nil) {
		return errors.New("invalid resolution outcome")
	}
	var pub, id string
	err := s.pool.QueryRow(ctx, "SELECT publication_id,post_id FROM "+s.schema+".federation_resolution_jobs WHERE event_id=$1 AND destination=$2 AND lease_token=$3", job.EventID, s.federationDestination, job.LeaseToken).Scan(&pub, &id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var locked string
	if err = tx.QueryRow(ctx, "SELECT id FROM "+s.schema+".posts WHERE publication_id=$1 AND id=$2 FOR UPDATE", pub, id).Scan(&locked); err != nil {
		return err
	}
	var raw []byte
	err = tx.QueryRow(ctx, "SELECT payload FROM "+s.schema+".federation_resolution_jobs WHERE event_id=$1 AND publication_id=$2 AND destination=$3 AND lease_token=$4 AND state='leased' AND lease_until>clock_timestamp() FOR UPDATE", job.EventID, pub, s.federationDestination, job.LeaseToken).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return err
	}
	if state == "complete" {
		wire, err := json.Marshal(out.Receipt)
		if err != nil {
			return err
		}
		receipt, err := publishing.DecodeReceipt(raw, wire)
		if err != nil {
			return err
		}
		event, err := publishing.ReceiptEvent(raw)
		if err != nil || event.EventID != job.EventID || event.Publication != pub {
			return errors.New("resolution binding mismatch")
		}
		var source string
		if err = tx.QueryRow(ctx, "SELECT source_id FROM "+s.schema+".publishing_streams WHERE publication_id=$1 AND post_id=$2 AND destination=$3", pub, id, s.federationDestination).Scan(&source); err != nil {
			return err
		}
		if source != event.Source.ID || event.Source.Epoch != "1" {
			return errors.New("resolution authority mismatch")
		}
		if p := receipt.Projection; p != nil {
			confirmation := publishing.Outcome{State: "accepted", Result: "applied", Confirmation: &publishing.Confirmation{ObjectID: p.ObjectID, State: p.State, Revision: p.Revision}}
			if err = s.confirmPublic(ctx, tx, pub, id, s.federationDestination, event.EventID, event.ContentID, event.ObjectInstance, event.Revision, event.Type, confirmation); err != nil {
				return err
			}
		} else if receipt.Result == "applied" {
			// An authoritative non-public response can invalidate only this exact
			// instance/generation, never a newer republished instance.
			_, err = tx.Exec(ctx, "UPDATE "+s.schema+`.federation_projections f SET confirmed=false FROM `+s.schema+`.posts p WHERE f.publication_id=$1 AND f.post_id=$2 AND f.destination=$3 AND f.object_instance=$4 AND p.publication_id=f.publication_id AND p.id=f.post_id AND p.uuid=$5 AND p.public_generation=f.public_generation AND p.public_generation<=$6 AND p.revision>=$6`, pub, id, s.federationDestination, event.ObjectInstance, event.ContentID, event.Revision)
			if err != nil {
				return err
			}
		}
	}
	_, err = tx.Exec(ctx, "UPDATE "+s.schema+`.federation_resolution_jobs SET state=$5,lease_token=NULL,lease_until=NULL,next_attempt_at=clock_timestamp()+($6::bigint*interval '1 millisecond'),last_error_code=$7 WHERE event_id=$1 AND publication_id=$2 AND destination=$3 AND lease_token=$4`, job.EventID, pub, s.federationDestination, job.LeaseToken, state, out.Delay.Milliseconds(), out.Code)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Counts only, never event bodies, credentials or destination URLs.
func (s *Store) ResolutionStatus(ctx context.Context, pub, destination string) (map[string]int64, error) {
	if s.federationDestination == "" || destination != s.federationDestination || pub == "" {
		return nil, errors.New("invalid resolution status scope")
	}
	counts := map[string]int64{"pending": 0, "leased": 0, "complete": 0, "quarantined": 0}
	rows, err := s.pool.Query(ctx, "SELECT state,count(*) FROM "+s.schema+".federation_resolution_jobs WHERE publication_id=$1 AND destination=$2 GROUP BY state", pub, destination)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var n int64
		if err = rows.Scan(&state, &n); err != nil {
			return nil, err
		}
		counts[state] = n
	}
	return counts, rows.Err()
}
