package postgres

import (
	"content/cms"
	"content/media"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const mediaColumns = `id,publication_id,object_key,sha256,mime,extension,byte_size,width,height,state,created_at`

func scanMedia(row pgx.Row) (media.Asset, error) {
	var a media.Asset
	e := row.Scan(&a.ID, &a.Publication, &a.Key, &a.SHA256, &a.MIME, &a.Extension, &a.Size, &a.Width, &a.Height, &a.State, &a.CreatedAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return a, media.ErrNotFound
	}
	return a, e
}
func (s *Store) MediaReserve(ctx context.Context, a media.Asset) (media.Asset, error) {
	if !media.IDPattern.MatchString(a.ID) || !media.ValidObjectKey(a.Key) || a.Publication == "" {
		return media.Asset{}, media.ErrInvalid
	}
	if a.Size < 1 || a.Size > media.MaxBytes {
		return media.Asset{}, media.ErrInvalid
	}
	// Explicit READ COMMITTED matters: a waiter must see the previous reservation
	// after acquiring the schema-wide lock, even if the role default differs.
	tx, e := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if e != nil {
		return media.Asset{}, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", s.mediaQuotaLock); e != nil {
		return media.Asset{}, e
	}
	existing, e := scanMedia(tx.QueryRow(ctx, "SELECT "+mediaColumns+" FROM "+s.schema+".media_objects WHERE publication_id=$1 AND sha256=$2", a.Publication, a.SHA256))
	if e == nil {
		return existing, tx.Commit(ctx)
	}
	if !errors.Is(e, media.ErrNotFound) {
		return media.Asset{}, e
	}
	var limit, used int64
	if e = tx.QueryRow(ctx, "SELECT limit_bytes FROM "+s.schema+".media_quota WHERE singleton").Scan(&limit); e != nil {
		return media.Asset{}, e
	}
	if e = tx.QueryRow(ctx, "SELECT COALESCE(SUM(byte_size),0)::bigint FROM "+s.schema+".media_objects").Scan(&used); e != nil {
		return media.Asset{}, e
	}
	// Subtraction avoids overflow; over-limit legacy bytes are preserved, while
	// new reservations fail. Retries above remain available even at the limit.
	if limit < 1 || used > limit || a.Size > limit-used {
		return media.Asset{}, media.ErrQuota
	}
	_, e = tx.Exec(ctx, "INSERT INTO "+s.schema+`.media_objects(publication_id,id,object_key,sha256,mime,extension,byte_size,width,height) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, a.Publication, a.ID, a.Key, a.SHA256, a.MIME, a.Extension, a.Size, a.Width, a.Height)
	if e != nil {
		return media.Asset{}, e
	}
	stored, e := scanMedia(tx.QueryRow(ctx, "SELECT "+mediaColumns+" FROM "+s.schema+".media_objects WHERE publication_id=$1 AND id=$2", a.Publication, a.ID))
	if e != nil {
		return media.Asset{}, e
	}
	return stored, tx.Commit(ctx)
}
func (s *Store) MediaFinalize(ctx context.Context, publication, id string, put func(context.Context, media.Asset) error) (media.Asset, error) {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return media.Asset{}, e
	}
	defer tx.Rollback(ctx)
	a, e := scanMedia(tx.QueryRow(ctx, "SELECT "+mediaColumns+" FROM "+s.schema+".media_objects WHERE publication_id=$1 AND id=$2 FOR UPDATE", publication, id))
	if e != nil {
		return a, e
	}
	if a.State == "ready" {
		return a, tx.Commit(ctx)
	}
	if e = put(ctx, a); e != nil {
		return a, e
	}
	_, e = tx.Exec(ctx, "UPDATE "+s.schema+".media_objects SET state='ready' WHERE publication_id=$1 AND id=$2", publication, id)
	if e != nil {
		return a, e
	}
	// On ambiguous commit, leave object + durable pending/ready row intact. A
	// retry reuses this ID and key. Never delete an object after uncertain commit.
	e = tx.Commit(ctx)
	a.State = "ready"
	return a, e
}
func (s *Store) MediaGet(ctx context.Context, publication, id string, publicOnly bool) (media.Asset, error) {
	q := "SELECT " + mediaColumns + " FROM " + s.schema + ".media_objects m WHERE publication_id=$1 AND id=$2 AND state='ready'"
	if publicOnly {
		q += " AND EXISTS(SELECT 1 FROM " + s.schema + ".post_media pm JOIN " + s.schema + ".posts p ON p.publication_id=pm.publication_id AND p.id=pm.post_id WHERE pm.publication_id=m.publication_id AND pm.media_id=m.id AND p.status='published' AND p.visibility='public')"
	}
	return scanMedia(s.pool.QueryRow(ctx, q, publication, id))
}
func (s *Store) MediaCleanup(ctx context.Context, publication string, before time.Time, limit int, remove func(context.Context, media.Asset) error) (int, error) {
	if publication == "" || limit < 1 || limit > 50 || before.After(time.Now().Add(-24*time.Hour)) {
		return 0, media.ErrInvalid
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback(ctx)
	rows, e := tx.Query(ctx, "SELECT "+mediaColumns+" FROM "+s.schema+".media_objects WHERE publication_id=$1 AND state='pending' AND created_at<$2 ORDER BY created_at LIMIT $3 FOR UPDATE SKIP LOCKED", publication, before, limit)
	if e != nil {
		return 0, e
	}
	var pending []media.Asset
	for rows.Next() {
		a, e := scanMedia(rows)
		if e != nil {
			rows.Close()
			return 0, e
		}
		pending = append(pending, a)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return 0, e
	}
	for _, a := range pending {
		if e = remove(ctx, a); e != nil {
			return 0, e
		}
		if _, e = tx.Exec(ctx, "DELETE FROM "+s.schema+".media_objects WHERE publication_id=$1 AND id=$2 AND state='pending'", publication, a.ID); e != nil {
			return 0, e
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return 0, e
	}
	return len(pending), nil
}

// WithMediaOrigin enables atomic owned-image publication references. Without
// media enabled no extra media-table privileges are required by old instances.
func WithMediaOrigin(origin string, aliases ...string) Option {
	return func(s *Store) error {
		normalized, err := media.NormalizeOrigin(origin)
		if err != nil {
			return err
		}
		if len(aliases) > 8 {
			return errors.New("at most eight media origin aliases")
		}
		trusted := make([]string, 0, len(aliases))
		for _, value := range aliases {
			alias, err := media.NormalizeOrigin(value)
			if err != nil {
				return err
			}
			trusted = append(trusted, alias)
		}
		s.mediaOrigin, s.mediaOriginAliases = normalized, trusted
		return nil
	}
}
func (s *Store) syncMedia(ctx context.Context, tx pgx.Tx, p cms.Post) error {
	if s.mediaOrigin == "" {
		return nil
	}
	ids, e := media.References(s.mediaOrigin, p.HTML, s.mediaOriginAliases...)
	if e != nil {
		return &cms.InvalidInput{Message: "Invalid owned image reference"}
	}
	// Validate before replacing relations. The transaction also owns post/revision
	// and publication intent writes, so any failure leaves visibility unchanged.
	for _, ref := range ids {
		id := ref.ID
		var ready bool
		var extension string
		e = tx.QueryRow(ctx, "SELECT state='ready',extension FROM "+s.schema+".media_objects WHERE publication_id=$1 AND id=$2 FOR KEY SHARE", p.Publication, id).Scan(&ready, &extension)
		if errors.Is(e, pgx.ErrNoRows) || e == nil && (!ready || extension != ref.Extension) {
			return &cms.InvalidInput{Message: "Image is missing, unfinished or belongs to another publication"}
		}
		if e != nil {
			return e
		}
	}
	if _, e = tx.Exec(ctx, "DELETE FROM "+s.schema+".post_media WHERE publication_id=$1 AND post_id=$2", p.Publication, p.ID); e != nil {
		return e
	}
	for _, ref := range ids {
		id := ref.ID
		if _, e = tx.Exec(ctx, "INSERT INTO "+s.schema+".post_media(publication_id,post_id,media_id) VALUES($1,$2,$3)", p.Publication, p.ID, id); e != nil {
			return e
		}
	}
	return nil
}

// Distinct domain from migration/ownership/delivery locks; shared by all
// publications and processes in the same installation's owned CMS schema.
func mediaQuotaLockID(installation, schema string) int64 {
	sum := sha256.Sum256([]byte("content\x00" + installation + "\x00cms\x00media-quota\x00" + schema))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}
