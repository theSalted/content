package postgres

import (
	"content/cms"
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
)

func WithScheduling(enabled bool) Option {
	return func(s *Store) error { s.scheduling = enabled; return nil }
}
func (s *Store) CheckScheduling(ctx context.Context) error {
	var allowed bool
	err := s.pool.QueryRow(ctx, "SELECT has_table_privilege(current_user,$1,'SELECT') AND has_table_privilege(current_user,$1,'INSERT') AND has_table_privilege(current_user,$1,'DELETE')", s.schema+".publication_schedules").Scan(&allowed)
	if err != nil || !allowed {
		return errors.New("schedule table or runtime privileges unavailable")
	}
	for _, column := range []string{"schedule_id", "due_at", "expected_revision", "attempts", "next_attempt_at", "last_error_code"} {
		if err = s.pool.QueryRow(ctx, "SELECT has_column_privilege(current_user,$1,$2,'UPDATE')", s.schema+".publication_schedules", column).Scan(&allowed); err != nil || !allowed {
			return errors.New("schedule runtime privileges unavailable")
		}
	}
	return nil
}

// CheckPublishingManagement runs only for the HTTP writer process, never for
// the narrower delivery worker. Both tables are needed even with scheduling off.
func (s *Store) CheckPublishingManagement(ctx context.Context) error {
	if err := s.CheckScheduling(ctx); err != nil {
		return err
	}
	var allowed bool
	err := s.pool.QueryRow(ctx, "SELECT has_table_privilege(current_user,$1,'SELECT') AND has_table_privilege(current_user,$1,'INSERT') AND has_column_privilege(current_user,$1,'ever_public','UPDATE')", s.schema+".post_slug_claims").Scan(&allowed)
	if err != nil || !allowed {
		return errors.New("slug claim table or runtime privileges unavailable")
	}
	return nil
}

// PublishDue locks post then schedule, exactly like an editor. No HTTP or
// delivery occurs here: publication, revision, outbox and job removal commit once.
func (s *Store) PublishDue(ctx context.Context, publication string) (bool, error) {
	if !s.scheduling || publication == "" {
		return false, errors.New("scheduler disabled or publication missing")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var id string
	var updated time.Time
	err = tx.QueryRow(ctx, `SELECT p.id,p.updated_at FROM `+s.schema+`.posts p JOIN `+s.schema+`.publication_schedules j ON p.publication_id=j.publication_id AND p.id=j.post_id
 WHERE p.publication_id=$1 AND p.status='scheduled' AND p.trashed_at IS NULL AND j.due_at<=clock_timestamp() AND j.next_attempt_at<=clock_timestamp()
 ORDER BY j.next_attempt_at,p.id FOR UPDATE OF p SKIP LOCKED LIMIT 1`, publication).Scan(&id, &updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var scheduleID, kind string
	var due, retryAt, databaseNow time.Time
	var revision, currentRevision int64
	var attempts int
	err = tx.QueryRow(ctx, "SELECT j.schedule_id::text,j.due_at,j.expected_revision,j.attempts,p.revision,p.kind,j.next_attempt_at,clock_timestamp() FROM "+s.schema+".publication_schedules j JOIN "+s.schema+".posts p ON p.publication_id=j.publication_id AND p.id=j.post_id WHERE j.publication_id=$1 AND j.post_id=$2 FOR UPDATE OF j", publication, id).Scan(&scheduleID, &due, &revision, &attempts, &currentRevision, &kind, &retryAt, &databaseNow)
	if err != nil {
		return false, err
	}
	if due.After(databaseNow) || retryAt.After(databaseNow) {
		return false, nil
	}
	code := "publication_failed"
	if revision != currentRevision {
		err = errors.New("schedule revision mismatch")
		code = "revision_mismatch"
	} else {
		published := "published"
		_, err = s.updateTx(ctx, tx, publication, id, cms.UpdatePost{Kind: kind, ExpectedUpdatedAt: updated, Status: &published, PublishedAt: &due})
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err == nil {
		return true, nil
	}
	_ = tx.Rollback(ctx)
	// A lost commit acknowledgment is harmless: the guarded retry finds no job.
	retryCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.deferSchedule(retryCtx, publication, id, scheduleID, revision, attempts, code)
	return true, errors.New("scheduled publication failed; retained for retry")
}
func (s *Store) deferSchedule(ctx context.Context, pub, id, scheduleID string, revision int64, attempts int, code string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var found string
	if err = tx.QueryRow(ctx, "SELECT id FROM "+s.schema+".posts WHERE publication_id=$1 AND id=$2 AND status='scheduled' FOR UPDATE", pub, id).Scan(&found); err != nil {
		return err
	}
	power := attempts + 1
	if power > 12 {
		power = 12
	}
	seconds := 1 << power
	if seconds > 3600 {
		seconds = 3600
	}
	_, err = tx.Exec(ctx, "UPDATE "+s.schema+".publication_schedules SET attempts=LEAST(attempts+1,1000000),next_attempt_at=clock_timestamp()+($6::bigint*interval '1 second'),last_error_code=$5 WHERE publication_id=$1 AND post_id=$2 AND schedule_id=$3 AND expected_revision=$4", pub, id, scheduleID, revision, code, seconds)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
