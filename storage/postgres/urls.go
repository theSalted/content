package postgres

import (
	"content/cms"
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
)

func (s *Store) ResolveURL(ctx context.Context, publication, kind, slug string) (cms.URLResolution, error) {
	var result cms.URLResolution
	if publication == "" || (kind != "post" && kind != "page") || !cms.ValidSlug(slug) {
		return result, cms.ErrNotFound
	}
	err := s.pool.QueryRow(ctx, `SELECT p.id,p.slug,p.kind,c.slug<>p.slug FROM `+s.schema+`.post_slug_claims c
 JOIN `+s.schema+`.posts p ON p.publication_id=c.publication_id AND p.id=c.post_id
 WHERE c.publication_id=$1 AND c.slug=$2 AND p.kind=$3 AND p.status='published' AND p.visibility='public'
 AND p.trashed_at IS NULL AND (c.ever_public OR c.slug=p.slug)`, publication, slug, kind).Scan(&result.PostID, &result.Slug, &result.Kind, &result.Alias)
	if errors.Is(err, pgx.ErrNoRows) {
		err = cms.ErrNotFound
	}
	return result, err
}
