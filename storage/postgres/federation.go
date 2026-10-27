package postgres

import (
	"content/publishing"
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"regexp"
	"strconv"
)

var federationDestinationPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// WithFederationProjection requires the explicitly granted CMS-owned projection.
// It never reads the AP schema or treats an enqueued stream as confirmation.
func WithFederationProjection(destination string) Option {
	return func(s *Store) error {
		if !federationDestinationPattern.MatchString(destination) {
			return errors.New("invalid federation projection destination")
		}
		s.federationDestination = destination
		return nil
	}
}
func (s *Store) federationSelect() string {
	if s.federationDestination == "" {
		return "NULL::text"
	}
	return "f.object_url"
}
func (s *Store) federationJoin() string {
	if s.federationDestination == "" {
		return ""
	}
	// Destination is an allowlisted identifier-shaped value, never caller SQL.
	return ` LEFT JOIN ` + s.schema + `.federation_projections f ON f.publication_id=p.publication_id AND f.post_id=p.id AND f.destination='` + s.federationDestination + `' AND f.public_generation=p.public_generation AND f.confirmed AND p.public_generation>0 AND p.kind='post' AND p.status='published' AND p.visibility='public'`
}

// finishConfirmedDelivery locks the post before its receipt, matching editor and
// archive lock order. Confirmation and queue acceptance commit or roll back as one.
// job.Payload is deliberately ignored: only immutable bytes under the lease bind
// the receipt to a CMS article, revision and public object instance.
func (s *Store) finishConfirmedDelivery(ctx context.Context, job publishing.Job, out publishing.Outcome, state string, accepted *string) error {
	var pub, id string
	err := s.pool.QueryRow(ctx, "SELECT publication_id,post_id FROM "+s.schema+".publishing_deliveries WHERE event_id=$1", job.EventID).Scan(&pub, &id)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var lockedID string
	if err = tx.QueryRow(ctx, "SELECT id FROM "+s.schema+".posts WHERE publication_id=$1 AND id=$2 FOR UPDATE", pub, id).Scan(&lockedID); err != nil {
		return err
	}
	var raw []byte
	var destination string
	var revision int64
	err = tx.QueryRow(ctx, "SELECT payload,destination,revision FROM "+s.schema+".publishing_deliveries WHERE event_id=$1 AND lease_token=$2 AND state='leased' AND lease_until>clock_timestamp() FOR UPDATE", job.EventID, job.LeaseToken).Scan(&raw, &destination, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return err
	}
	e, err := publishing.Decode(raw)
	if err != nil || e.EventID != job.EventID || e.Publication != pub || e.Revision != strconv.FormatInt(revision, 10) {
		return errors.New("delivery confirmation binding mismatch")
	}
	if err = s.confirmPublic(ctx, tx, pub, id, destination, e.EventID, e.ContentID, e.ObjectInstance, e.Revision, e.Type, out); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "UPDATE "+s.schema+`.publishing_deliveries SET state=$3,lease_token=NULL,lease_until=NULL,next_attempt_at=clock_timestamp()+($4::bigint*interval '1 millisecond'),last_error_code=$5,accepted_result=$6 WHERE event_id=$1 AND lease_token=$2`, job.EventID, job.LeaseToken, state, out.Delay.Milliseconds(), out.Code, accepted)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// A post-row lock serializes this with withdrawal/republish. Generation is set by
// a database trigger even if an older CMS writer is still serving during rollout.
func (s *Store) confirmPublic(ctx context.Context, tx pgx.Tx, pub, id, destination, eventID, contentID, instance, revision, eventType string, out publishing.Outcome) error {
	c := out.Confirmation
	if c == nil || s.federationDestination == "" {
		return nil
	}
	expected := "public"
	switch eventType {
	case "content.published", "content.updated":
	case "content.withdrawn":
		expected = "withdrawn"
	case "content.deleted":
		expected = "deleted"
	default:
		return errors.New("unsupported confirmed lifecycle")
	}
	if out.State != "accepted" || out.Result != "applied" || c.State != expected || !publishing.DiscoveryURL(c.ObjectID) {
		return errors.New("invalid applied confirmation")
	}
	if expected != "public" || destination != s.federationDestination {
		return nil
	}
	rev, err := strconv.ParseInt(revision, 10, 64)
	if err != nil || rev < 1 {
		return errors.New("invalid confirmed revision")
	}
	var gen, current int64
	var uuid, kind, status, visibility string
	err = tx.QueryRow(ctx, "SELECT public_generation,revision,uuid::text,kind,status,visibility FROM "+s.schema+".posts WHERE publication_id=$1 AND id=$2 FOR UPDATE", pub, id).Scan(&gen, &current, &uuid, &kind, &status, &visibility)
	if err != nil {
		return err
	}
	if c.Revision != "" {
		currentReceipt, err := strconv.ParseInt(c.Revision, 10, 64)
		if err != nil || !publishing.ValidCounter(c.Revision) || currentReceipt < rev {
			return errors.New("invalid receiver revision")
		}
		if currentReceipt > current {
			return nil
		}
	}
	if uuid != contentID {
		return errors.New("confirmation content identity mismatch")
	}
	// Late confirmation of a withdrawn generation cannot establish discovery.
	if kind != "post" || status != "published" || visibility != "public" || gen == 0 || rev < gen || rev > current {
		return nil
	}
	var oldGen, oldRev int64
	var oldInstance, oldURL string
	var confirmed bool
	err = tx.QueryRow(ctx, "SELECT public_generation,acknowledged_revision,object_instance::text,object_url,confirmed FROM "+s.schema+".federation_projections WHERE publication_id=$1 AND post_id=$2 AND destination=$3", pub, id, destination).Scan(&oldGen, &oldRev, &oldInstance, &oldURL, &confirmed)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil && oldGen == gen {
		if oldInstance != instance || oldURL != c.ObjectID {
			return errors.New("confirmed public identity changed inside one lifecycle")
		}
		if oldRev >= rev && confirmed {
			return nil
		}
	}
	_, err = tx.Exec(ctx, "INSERT INTO "+s.schema+`.federation_projections(publication_id,post_id,destination,public_generation,object_instance,object_url,acknowledged_revision,event_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(publication_id,post_id,destination) DO UPDATE SET public_generation=EXCLUDED.public_generation,object_instance=EXCLUDED.object_instance,object_url=EXCLUDED.object_url,acknowledged_revision=EXCLUDED.acknowledged_revision,event_id=EXCLUDED.event_id,acknowledged_at=clock_timestamp(),confirmed=true`, pub, id, destination, gen, instance, c.ObjectID, rev, eventID)
	return err
}
