package postgres

import (
	"content/cms"
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
)

type ArchiveBinding struct {
	ArchiveReceipt
	Publication, PostID, Destination, SourceID, SiteURL string
}

func (s *Store) ArchiveBinding(ctx context.Context, publication, id string) (ArchiveBinding, error) {
	var b ArchiveBinding
	e := s.pool.QueryRow(ctx, "SELECT event_id::text,content_id::text,state,revision,publication_id,post_id,destination,source_id,site_url FROM "+s.schema+".publishing_archives WHERE publication_id=$1 AND post_id=$2", publication, id).Scan(&b.EventID, &b.ContentID, &b.State, &b.Revision, &b.Publication, &b.PostID, &b.Destination, &b.SourceID, &b.SiteURL)
	if errors.Is(e, pgx.ErrNoRows) {
		e = cms.ErrNotFound
	}
	return b, e
}

type QueueCounts struct {
	Pending     int64 `json:"pending"`
	Leased      int64 `json:"leased"`
	Quarantined int64 `json:"quarantined"`
	Accepted    int64 `json:"accepted"`
}
type PublishingStatus struct {
	Live           QueueCounts `json:"live"`
	Archive        QueueCounts `json:"archive"`
	BlockedStreams int64       `json:"blocked_streams"`
}

func (s *Store) PublishingStatus(ctx context.Context, publication, destination string) (PublishingStatus, error) {
	var out PublishingStatus
	if publication == "" || destination == "" {
		return out, errors.New("status scope required")
	}
	for _, v := range []struct {
		table  string
		target *QueueCounts
	}{{"publishing_deliveries", &out.Live}, {"publishing_archives", &out.Archive}} {
		e := s.pool.QueryRow(ctx, "SELECT count(*) FILTER(WHERE state='pending'),count(*) FILTER(WHERE state='leased'),count(*) FILTER(WHERE state='quarantined'),count(*) FILTER(WHERE state='accepted') FROM "+s.schema+"."+v.table+" WHERE publication_id=$1 AND destination=$2", publication, destination).Scan(&v.target.Pending, &v.target.Leased, &v.target.Quarantined, &v.target.Accepted)
		if e != nil {
			return out, e
		}
	}
	e := s.pool.QueryRow(ctx, "SELECT count(*) FROM "+s.schema+".publishing_streams WHERE publication_id=$1 AND destination=$2 AND blocked_reason IS NOT NULL", publication, destination).Scan(&out.BlockedStreams)
	return out, e
}
