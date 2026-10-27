package postgres

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"content/cms"
	"content/publishing"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	federationDestination string
	tagOrigins            []string
	pool                  *pgxpool.Pool
	maxConnections        int32
	schema                string
	mediaOrigin           string
	mediaOriginAliases    []string
	mediaQuotaLock        int64
	enrollment            *publishing.Enrollment
	scheduling            bool
}

func Open(ctx context.Context, dsn, schema, installation string, options ...Option) (*Store, error) {
	if dsn == "" || installation == "" {
		return nil, errors.New("runtime DSN and installation are required")
	}
	quoted, err := Schema(schema)
	if err != nil {
		return nil, err
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid runtime connection configuration")
	}
	s := &Store{schema: quoted, mediaQuotaLock: mediaQuotaLockID(installation, schema), maxConnections: 5}
	for _, option := range options {
		if err = option(s); err != nil {
			return nil, err
		}
	}
	cfg.MaxConns = s.maxConnections
	cfg.MinConns = 0
	cfg.MaxConnLifetime = 30 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	s.pool = pool
	var stored, module string
	if err = pool.QueryRow(ctx, "SELECT installation_id,module_id FROM "+quoted+".installation WHERE singleton").Scan(&stored, &module); err != nil {
		pool.Close()
		return nil, errors.New("schema is unavailable or not migrated")
	}
	if err = validateCMSBinding(stored, module, installation); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close()                         { s.pool.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *Store) Create(ctx context.Context, p cms.Post) (cms.Post, error) {
	if !cms.ValidKind(p.Kind) {
		return cms.Post{}, &cms.InvalidInput{Message: "invalid content kind"}
	}
	p.Kind = cms.ContentKind(p.Kind)
	if p.Author != nil {
		if err := p.Author.Validate(); err != nil {
			return cms.Post{}, err
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return cms.Post{}, err
	}
	defer tx.Rollback(ctx)
	var now time.Time
	if err = tx.QueryRow(ctx, "SELECT date_trunc('milliseconds',clock_timestamp())").Scan(&now); err != nil {
		return cms.Post{}, err
	}
	p.CreatedAt = now
	p.UpdatedAt = now
	if p.Status == "published" {
		p.PublishedAt = &now
	}
	_, err = tx.Exec(ctx, "INSERT INTO "+s.schema+`.posts(publication_id,id,uuid,title,slug,status,visibility,revision,created_at,updated_at,published_at,kind) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, p.Publication, p.ID, p.UUID, p.Title, p.Slug, p.Status, p.Visibility, p.Revision, p.CreatedAt, p.UpdatedAt, p.PublishedAt, p.Kind)
	if err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			return cms.Post{}, cms.ErrConflict
		}
		return cms.Post{}, err
	}
	if p.Kind == "post" {
		if err = s.enroll(ctx, tx, p.Publication, p.ID, p.UUID); err != nil {
			return cms.Post{}, err
		}
	}
	if err = s.writeRevision(ctx, tx, p); err != nil {
		return cms.Post{}, err
	}
	if err = s.syncMedia(ctx, tx, p); err != nil {
		return cms.Post{}, err
	}
	if p.Status == "published" {
		if err = s.writeEvent(ctx, tx, p, "post.published"); err != nil {
			return cms.Post{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return cms.Post{}, err
	}
	return p, nil
}
func (s *Store) selectSQL() string {
	return `SELECT p.id,p.uuid::text,p.publication_id,p.title,p.slug,p.status,p.visibility,p.revision,p.created_at,p.updated_at,p.published_at,r.source_html,r.html,r.metadata->'author',r.metadata->'presentation',p.kind,` + s.federationSelect() + `,p.trashed_at,(SELECT due_at FROM ` + s.schema + `.publication_schedules ps WHERE ps.publication_id=p.publication_id AND ps.post_id=p.id) FROM ` + s.schema + `.posts p JOIN ` + s.schema + `.post_revisions r ON r.publication_id=p.publication_id AND r.post_id=p.id AND r.revision=p.revision` + s.federationJoin()
}
func scan(row pgx.Row) (cms.Post, error) {
	var p cms.Post
	var author, presentation []byte
	var objectURL *string
	err := row.Scan(&p.ID, &p.UUID, &p.Publication, &p.Title, &p.Slug, &p.Status, &p.Visibility, &p.Revision, &p.CreatedAt, &p.UpdatedAt, &p.PublishedAt, &p.SourceHTML, &p.HTML, &author, &presentation, &p.Kind, &objectURL, &p.TrashedAt, &p.ScheduledFor)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, cms.ErrNotFound
	}
	if err == nil && len(author) > 0 {
		err = json.Unmarshal(author, &p.Author)
	}
	if err == nil && len(presentation) > 0 {
		err = json.Unmarshal(presentation, &p.Presentation)
	}
	if err == nil && objectURL != nil && publishing.DiscoveryURL(*objectURL) {
		p.ActivityPub = &cms.FederationProjection{ObjectID: *objectURL, State: "public", Acknowledged: true}
	}
	return p, err
}
func scope(q cms.Query) string {
	kind := ` AND p.kind='post'`
	if q.Kind == "page" {
		kind = ` AND p.kind='page'`
	}
	if q.Trashed && !q.PublicOnly {
		kind += ` AND p.trashed_at IS NOT NULL`
	} else {
		kind += ` AND p.trashed_at IS NULL`
	}
	if q.PublicOnly {
		return kind + ` AND p.status='published' AND p.visibility='public'`
	}
	return kind
}
func (s *Store) Get(ctx context.Context, q cms.Query) (cms.Post, error) {
	if !cms.ValidKind(q.Kind) || q.Publication == "" || (q.ID == "") == (q.Slug == "") {
		return cms.Post{}, cms.ErrNotFound
	}
	field, value := "id", q.ID
	if q.Slug != "" {
		field, value = "slug", q.Slug
	}
	return scan(s.pool.QueryRow(ctx, s.selectSQL()+" WHERE p.publication_id=$1 AND p."+field+"=$2"+scope(q), q.Publication, value))
}
func (s *Store) Browse(ctx context.Context, q cms.Query) ([]cms.Post, int64, error) {
	if !cms.ValidKind(q.Kind) || q.Publication == "" || q.Page < 1 || q.Page > 1000000 || q.Limit < 1 || q.Limit > 100 || len(q.Search) > 200 || !utf8.ValidString(q.Search) || strings.ContainsRune(q.Search, 0) || (q.Status != "" && q.Status != "draft" && q.Status != "published" && q.Status != "scheduled") {
		return nil, 0, &cms.InvalidInput{Message: "invalid browse parameters"}
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback(ctx)
	where := ` WHERE p.publication_id=$1` + scope(q)
	where += ` AND ($2='' OR p.status=$2) AND ($3='' OR strpos(lower(p.title),lower($3))>0 OR strpos(lower(p.slug),lower($3))>0)`
	var total int64
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM "+s.schema+`.posts p`+where, q.Publication, q.Status, q.Search).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := "p.created_at DESC,p.id DESC"
	if q.PublicOnly {
		order = "p.published_at DESC,p.id DESC"
	}
	if q.Kind == "page" {
		order = "p.title ASC,p.id ASC"
	}
	rows, err := tx.Query(ctx, s.selectSQL()+where+" ORDER BY "+order+" LIMIT $4 OFFSET $5", q.Publication, q.Status, q.Search, q.Limit, int64(q.Page-1)*int64(q.Limit))
	if err != nil {
		return nil, 0, err
	}
	posts := []cms.Post{}
	for rows.Next() {
		p, err := scan(rows)
		if err != nil {
			rows.Close()
			return nil, 0, err
		}
		posts = append(posts, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, 0, fmt.Errorf("read transaction failed: %w", err)
	}
	return posts, total, nil
}

// Update locks the post independently from its revision join. A competing
// writer must observe the newly committed token before it can modify the row.
func (s *Store) Update(ctx context.Context, publication, id string, in cms.UpdatePost) (cms.Post, error) {
	if err := in.ValidateToken(); err != nil {
		return cms.Post{}, err
	}
	if publication == "" || id == "" {
		return cms.Post{}, cms.ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return cms.Post{}, err
	}
	defer tx.Rollback(ctx)
	next, err := s.updateTx(ctx, tx, publication, id, in)
	if err != nil {
		return cms.Post{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return cms.Post{}, err
	}
	return next, nil
}

func (s *Store) updateTx(ctx context.Context, tx pgx.Tx, publication, id string, in cms.UpdatePost) (cms.Post, error) {
	var current cms.Post
	var archivePending bool
	err := tx.QueryRow(ctx, "SELECT id,uuid::text,publication_id,title,slug,status,visibility,revision,created_at,updated_at,published_at,kind,archive_pending,trashed_at FROM "+s.schema+".posts WHERE publication_id=$1 AND id=$2 FOR UPDATE", publication, id).Scan(&current.ID, &current.UUID, &current.Publication, &current.Title, &current.Slug, &current.Status, &current.Visibility, &current.Revision, &current.CreatedAt, &current.UpdatedAt, &current.PublishedAt, &current.Kind, &archivePending, &current.TrashedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return cms.Post{}, cms.ErrNotFound
	}
	if err != nil {
		return cms.Post{}, err
	}
	if archivePending {
		return cms.Post{}, cms.ErrArchivePending
	}
	if !current.UpdatedAt.Equal(in.ExpectedUpdatedAt) {
		return cms.Post{}, cms.ErrStaleUpdate
	}
	err = tx.QueryRow(ctx, "SELECT due_at FROM "+s.schema+".publication_schedules WHERE publication_id=$1 AND post_id=$2 FOR UPDATE", publication, id).Scan(&current.ScheduledFor)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return cms.Post{}, err
	}
	if !s.scheduling && (current.Status == "scheduled" && in.Status == nil && in.Trash == nil || in.Status != nil && *in.Status == "scheduled") {
		return cms.Post{}, &cms.InvalidInput{Message: "scheduling is disabled"}
	}
	var author, presentation []byte
	err = tx.QueryRow(ctx, "SELECT source_html,html,metadata->'author',metadata->'presentation' FROM "+s.schema+".post_revisions WHERE publication_id=$1 AND post_id=$2 AND revision=$3", publication, id, current.Revision).Scan(&current.SourceHTML, &current.HTML, &author, &presentation)
	if err != nil {
		return cms.Post{}, err
	}
	if len(author) > 0 {
		if err = json.Unmarshal(author, &current.Author); err != nil {
			return cms.Post{}, err
		}
	}
	if len(presentation) > 0 {
		if err = json.Unmarshal(presentation, &current.Presentation); err != nil {
			return cms.Post{}, err
		}
	}
	var databaseNow time.Time
	if err = tx.QueryRow(ctx, "SELECT date_trunc('milliseconds',clock_timestamp())").Scan(&databaseNow); err != nil {
		return cms.Post{}, err
	}
	next, err := cms.ApplyUpdateAt(current, in, databaseNow)
	if err != nil {
		return cms.Post{}, err
	}
	err = tx.QueryRow(ctx, "SELECT GREATEST(date_trunc('milliseconds',clock_timestamp()),$1::timestamptz + interval '1 millisecond')", current.UpdatedAt).Scan(&next.UpdatedAt)
	if err != nil {
		return cms.Post{}, err
	}
	next.Revision = current.Revision + 1
	if next.Status == "published" && (next.PublishedAt == nil || current.Status == "scheduled" && in.PublishedAt == nil) {
		t := next.UpdatedAt
		next.PublishedAt = &t
	}
	// published_at survives unpublish/republish unless explicitly edited.
	_, err = tx.Exec(ctx, "UPDATE "+s.schema+`.posts SET title=$3,slug=$4,status=$5,visibility=$6,revision=$7,updated_at=$8,published_at=$9,trashed_at=$10 WHERE publication_id=$1 AND id=$2`, publication, id, next.Title, next.Slug, next.Status, next.Visibility, next.Revision, next.UpdatedAt, next.PublishedAt, next.TrashedAt)
	if err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			return cms.Post{}, cms.ErrConflict
		}
		return cms.Post{}, err
	}
	if err = s.writeRevision(ctx, tx, next); err != nil {
		return cms.Post{}, err
	}
	if next.Status == "scheduled" {
		scheduleID, e := publishing.UUID()
		if e != nil {
			return cms.Post{}, e
		}
		_, err = tx.Exec(ctx, "INSERT INTO "+s.schema+`.publication_schedules(publication_id,post_id,schedule_id,due_at,expected_revision,next_attempt_at) VALUES($1,$2,$3,$4,$5,$4)
  ON CONFLICT(publication_id,post_id) DO UPDATE SET schedule_id=EXCLUDED.schedule_id,due_at=EXCLUDED.due_at,expected_revision=EXCLUDED.expected_revision,attempts=0,next_attempt_at=EXCLUDED.next_attempt_at,last_error_code=NULL`, publication, id, scheduleID, next.ScheduledFor, next.Revision)
	} else {
		_, err = tx.Exec(ctx, "DELETE FROM "+s.schema+".publication_schedules WHERE publication_id=$1 AND post_id=$2", publication, id)
	}
	if err != nil {
		return cms.Post{}, err
	}
	if err = s.syncMedia(ctx, tx, next); err != nil {
		return cms.Post{}, err
	}
	event := ""
	switch {
	case current.Status != "published" && next.Status == "published":
		event = "post.published"
	case current.Status == "published" && next.Status == "published":
		event = "post.updated"
	case current.Status == "published" && next.Status == "draft":
		event = "post.unpublished"
	}
	if event != "" {
		if err = s.writeEvent(ctx, tx, next, event); err != nil {
			return cms.Post{}, err
		}
	}
	return next, nil
}

func snapshotJSON(p cms.Post, includeBody bool) ([]byte, error) {
	var body *string
	if includeBody {
		body = &p.HTML
	}
	return json.Marshal(struct {
		ID           string     `json:"id"`
		UUID         string     `json:"uuid"`
		Title        string     `json:"title"`
		Slug         string     `json:"slug"`
		Status       string     `json:"status"`
		Visibility   string     `json:"visibility"`
		PublishedAt  *time.Time `json:"published_at"`
		TrashedAt    *time.Time `json:"trashed_at,omitempty"`
		ScheduledFor *time.Time `json:"scheduled_for,omitempty"`
		HTML         *string    `json:"html,omitempty"`
	}{p.ID, p.UUID, p.Title, p.Slug, p.Status, p.Visibility, p.PublishedAt, p.TrashedAt, p.ScheduledFor, body})
}
func (s *Store) writeRevision(ctx context.Context, tx pgx.Tx, p cms.Post) error {
	metadata, err := snapshotJSON(p, false)
	if err != nil {
		return err
	}
	if p.Author != nil || len(p.Presentation) > 0 || p.Kind == "page" {
		var fields map[string]any
		if err = json.Unmarshal(metadata, &fields); err != nil {
			return err
		}
		fields["kind"] = cms.ContentKind(p.Kind)
		if p.Author != nil {
			fields["author"] = p.Author
		}
		if len(p.Presentation) > 0 {
			fields["presentation"] = p.Presentation
		}
		if metadata, err = json.Marshal(fields); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, "INSERT INTO "+s.schema+`.post_revisions(publication_id,post_id,revision,source_html,html,renderer_version,created_at,metadata) VALUES($1,$2,$3,$4,$5,'html-unchanged-ghost-inert-v2-bluemonday-1.0.27',$6,$7)`, p.Publication, p.ID, p.Revision, p.SourceHTML, p.HTML, p.UpdatedAt, metadata)
	return err
}
func (s *Store) writeEvent(ctx context.Context, tx pgx.Tx, p cms.Post, eventType string) error {
	if p.Kind == "page" {
		return nil
	}
	eventBytes := make([]byte, 16)
	if _, err := rand.Read(eventBytes); err != nil {
		return err
	}
	eventBytes[6] = (eventBytes[6] & 15) | 64
	eventBytes[8] = (eventBytes[8] & 63) | 128
	eventID := fmt.Sprintf("%x-%x-%x-%x-%x", eventBytes[:4], eventBytes[4:6], eventBytes[6:8], eventBytes[8:10], eventBytes[10:])
	// Internal CMS-only intent. Withdrawal records omit the private draft body.
	snapshot, err := s.publishingSnapshot(p, eventType != "post.unpublished")
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "INSERT INTO "+s.schema+`.outbox_events(event_id,publication_id,post_id,revision,event_type,schema_version,snapshot,created_at) VALUES($1,$2,$3,$4,$5,1,$6,$7)`, eventID, p.Publication, p.ID, p.Revision, eventType, snapshot, p.UpdatedAt)
	return err
}
