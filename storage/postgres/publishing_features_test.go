package postgres

import (
	"content/cms"
	"content/media"
	"content/publishing"
	"content/storage/objects"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

type featureFixture struct {
	ctx            context.Context
	admin          *pgx.Conn
	store          *Store
	dsn, schema, q string
}

func featureDB(t *testing.T) *featureFixture { return featureDBVersion(t, 14) }
func featureDBVersion(t *testing.T, version int) *featureFixture {
	t.Helper()
	dsn := os.Getenv("CONTENT_TEST_DATABASE_URL")
	if dsn == "" || os.Getenv("CONTENT_TEST_ALLOW_SCHEMA_CREATE") != "1" {
		t.Skip("requires explicitly disposable loopback test database")
	}
	cfg, e := pgx.ParseConfig(dsn)
	if e != nil {
		t.Fatal("invalid test DSN")
	}
	if (cfg.Host != "127.0.0.1" && cfg.Host != "localhost" && cfg.Host != "::1") || !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatal("local _test database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	admin, e := pgx.ConnectConfig(ctx, cfg)
	if e != nil {
		t.Fatal("local test connection failed")
	}
	schema := "cms_features_" + strings.ToLower(rand.Text()[:12])
	q := ident(schema)
	mustExec(t, ctx, admin, "CREATE SCHEMA "+q)
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, e := admin.Exec(clean, "DROP SCHEMA "+q+" CASCADE")
		if e != nil {
			t.Error("schema cleanup", e)
		}
		admin.Close(clean)
	})
	if version == 14 {
		if e = Migrate(ctx, dsn, schema, "feature-test"); e != nil {
			t.Fatal("migrate", e)
		}
		if e = Migrate(ctx, dsn, schema, "feature-test"); e != nil {
			t.Fatal("repeat migration", e)
		}
	} else {
		legacy, e := sql.Open("pgx", dsn)
		if e != nil {
			t.Fatal(e)
		}
		defer legacy.Close()
		files := fstest.MapFS{}
		entries, _ := migrations.ReadDir("migrations")
		for _, entry := range entries {
			n, _ := strconv.Atoi(strings.Split(entry.Name(), "_")[0])
			if n > version {
				continue
			}
			body, e := migrations.ReadFile("migrations/" + entry.Name())
			if e != nil {
				t.Fatal(e)
			}
			files[entry.Name()] = &fstest.MapFile{Data: []byte(strings.ReplaceAll(string(body), "{{schema}}", q))}
		}
		ledger, e := database.NewStore(database.DialectPostgres, q+".goose_db_version")
		if e != nil {
			t.Fatal(e)
		}
		provider, e := goose.NewProvider(goose.DialectCustom, legacy, files, goose.WithStore(schemaLedger{ledger}), goose.WithDisableGlobalRegistry(true))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = provider.Up(ctx); e != nil {
			t.Fatal(e)
		}
		mustExec(t, ctx, admin, "INSERT INTO "+q+".installation(singleton,installation_id) VALUES(true,'feature-test')")
	}
	s, e := Open(ctx, dsn, schema, "feature-test", WithScheduling(true), WithNewPostPublishing(publishing.Enrollment{Destination: "test", SourceID: "cms", SiteURL: "https://site.example/blog"}))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	return &featureFixture{ctx, admin, s, dsn, schema, q}
}
func (f *featureFixture) create(t *testing.T, slug, status string) cms.Post {
	t.Helper()
	p, e := cms.NewPost("one", cms.CreatePost{Title: slug, Slug: slug, Status: status, HTML: "<p>Synthetic body</p>"})
	if e != nil {
		t.Fatal(e)
	}
	p, e = f.store.Create(f.ctx, p)
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func (f *featureFixture) update(t *testing.T, p cms.Post, u cms.UpdatePost) cms.Post {
	t.Helper()
	u.ExpectedUpdatedAt = p.UpdatedAt
	u.Kind = p.Kind
	next, e := f.store.Update(f.ctx, p.Publication, p.ID, u)
	if e != nil {
		t.Fatal(e)
	}
	return next
}
func (f *featureFixture) scheduled(t *testing.T, slug string) cms.Post {
	p := f.create(t, slug, "draft")
	status := "scheduled"
	due := time.Now().UTC().Truncate(time.Millisecond).Add(time.Hour)
	return f.update(t, p, cms.UpdatePost{Status: &status, PublishedAt: &due})
}

// Simulate a persisted due time during downtime, without sleeping or altering
// the host/database clock. Only this fixture's synthetic schedule row changes.
func (f *featureFixture) due(t *testing.T, p cms.Post) time.Time {
	t.Helper()
	var due time.Time
	e := f.admin.QueryRow(f.ctx, "UPDATE "+f.q+".publication_schedules SET due_at=date_trunc('milliseconds',clock_timestamp()-interval '1 minute'),next_attempt_at=clock_timestamp()-interval '1 minute' WHERE publication_id=$1 AND post_id=$2 RETURNING due_at", p.Publication, p.ID).Scan(&due)
	if e != nil {
		t.Fatal(e)
	}
	return due
}
func (f *featureFixture) counts(t *testing.T, id string) (int, int, int) {
	t.Helper()
	var revisions, events, jobs int
	e := f.admin.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM "+f.q+".post_revisions WHERE post_id=$1),(SELECT count(*) FROM "+f.q+".outbox_events WHERE post_id=$1),(SELECT count(*) FROM "+f.q+".publication_schedules WHERE post_id=$1)", id).Scan(&revisions, &events, &jobs)
	if e != nil {
		t.Fatal(e)
	}
	return revisions, events, jobs
}
func TestSlugClaimsChainsPrivacyCollisionsAndStableAPIdentity(t *testing.T) {
	f := featureDB(t)
	p := f.create(t, "first", "published")
	id, uuid := p.ID, p.UUID
	for _, slug := range []string{"second", "third"} {
		p = f.update(t, p, cms.UpdatePost{Slug: &slug})
	}
	for _, slug := range []string{"first", "second", "third"} {
		r, e := f.store.ResolveURL(f.ctx, "one", "post", slug)
		if e != nil || r.PostID != id || r.Slug != "third" || r.Alias != (slug != "third") {
			t.Fatal("chain", r, e)
		}
	}
	other := f.create(t, "other", "draft")
	old := "first"
	if _, e := f.store.Update(f.ctx, "one", other.ID, cms.UpdatePost{ExpectedUpdatedAt: other.UpdatedAt, Slug: &old}); !errors.Is(e, cms.ErrConflict) {
		t.Fatal("reserved alias reassigned", e)
	}
	if _, e := f.store.ResolveURL(f.ctx, "two", "post", "first"); !errors.Is(e, cms.ErrNotFound) {
		t.Fatal("publication leak")
	}
	if _, e := f.store.ResolveURL(f.ctx, "one", "page", "first"); !errors.Is(e, cms.ErrNotFound) {
		t.Fatal("kind leak")
	}
	// Existing mapper must keep one AP instance across public slug edits.
	rows, e := f.admin.Query(f.ctx, "SELECT event_id::text,revision,event_type,created_at,snapshot FROM "+f.q+".outbox_events WHERE post_id=$1 ORDER BY revision", id)
	if e != nil {
		t.Fatal(e)
	}
	state, instance := "unpublished", ""
	count := 0
	for rows.Next() {
		var i publishing.Intent
		var raw []byte
		i.Publication = "one"
		if e = rows.Scan(&i.EventID, &i.Revision, &i.Kind, &i.OccurredAt, &raw); e != nil {
			t.Fatal(e)
		}
		if e = json.Unmarshal(raw, &i.Snapshot); e != nil {
			t.Fatal(e)
		}
		ev, next, e := publishing.Map(i, *f.store.enrollment, state, instance)
		if e != nil {
			t.Fatal(e)
		}
		if ev.ContentID != uuid || (count > 0 && ev.ObjectInstance != instance) {
			t.Fatal("AP identity changed")
		}
		state, instance = next, ev.ObjectInstance
		count++
	}
	rows.Close()
	if count != 3 {
		t.Fatal("missing update intents")
	}
	p = f.update(t, p, cms.UpdatePost{Slug: &old})
	r, e := f.store.ResolveURL(f.ctx, "one", "post", "first")
	if e != nil || r.Alias || r.Slug != "first" {
		t.Fatal("rename back loop")
	}
	draft := "draft"
	p = f.update(t, p, cms.UpdatePost{Status: &draft})
	for _, slug := range []string{"first", "second", "third"} {
		if _, e = f.store.ResolveURL(f.ctx, "one", "post", slug); !errors.Is(e, cms.ErrNotFound) {
			t.Fatal("withdrawn alias exposed")
		}
	}
	if p.ID != id || p.UUID != uuid {
		t.Fatal("CMS identity changed")
	}
	hidden := f.create(t, "secret-draft-slug", "draft")
	newSlug := "public-only-name"
	hidden = f.update(t, hidden, cms.UpdatePost{Slug: &newSlug})
	pub := "published"
	hidden = f.update(t, hidden, cms.UpdatePost{Status: &pub})
	if _, e = f.store.ResolveURL(f.ctx, "one", "post", "secret-draft-slug"); !errors.Is(e, cms.ErrNotFound) {
		t.Fatal("draft slug leaked after publish")
	}
	trash := true
	hidden = f.update(t, hidden, cms.UpdatePost{Trash: &trash})
	if _, e = f.store.ResolveURL(f.ctx, "one", "post", newSlug); !errors.Is(e, cms.ErrNotFound) {
		t.Fatal("trash alias visible")
	}
}
func TestSlugClaimConcurrentConflictAndRollback(t *testing.T) {
	f := featureDB(t)
	a, b := f.create(t, "alpha", "published"), f.create(t, "beta", "published")
	slug := "shared-name"
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, p := range []cms.Post{a, b} {
		wg.Add(1)
		go func(p cms.Post) {
			defer wg.Done()
			_, e := f.store.Update(f.ctx, "one", p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Slug: &slug})
			results <- e
		}(p)
	}
	wg.Wait()
	close(results)
	ok, conflict := 0, 0
	for e := range results {
		if e == nil {
			ok++
		} else if errors.Is(e, cms.ErrConflict) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatal(ok, conflict)
	}
	p := f.create(t, "rollback-slug", "published")
	bad := "should-not-reserve"
	mustExec(t, f.ctx, f.admin, "ALTER TABLE "+f.q+".outbox_events ADD CONSTRAINT reject_update CHECK(event_type<>'post.updated') NOT VALID")
	if _, e := f.store.Update(f.ctx, "one", p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Slug: &bad}); e == nil {
		t.Fatal("failed event accepted")
	}
	var n int
	f.admin.QueryRow(f.ctx, "SELECT count(*) FROM "+f.q+".post_slug_claims WHERE slug=$1", bad).Scan(&n)
	if n != 0 {
		t.Fatal("partial alias persisted")
	}
}
func TestScheduledPublishRestartAndConcurrentWorkers(t *testing.T) {
	f := featureDB(t)
	p := f.scheduled(t, "restart")
	if p.PublishedAt != nil || p.ScheduledFor == nil {
		t.Fatal("scheduled date overwrote history")
	}
	if _, e := f.store.Get(f.ctx, cms.Query{Publication: "one", ID: p.ID, PublicOnly: true}); !errors.Is(e, cms.ErrNotFound) {
		t.Fatal("scheduled public")
	}
	if worked, e := f.store.PublishDue(f.ctx, "one"); e != nil || worked {
		t.Fatal("published early", e)
	}
	due := f.due(t, p)
	f.store.Close()
	s, e := Open(f.ctx, f.dsn, f.schema, "feature-test", WithScheduling(true))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	f.store = s
	results := make(chan bool, 8)
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); w, e := s.PublishDue(f.ctx, "one"); results <- w; errs <- e }()
	}
	wg.Wait()
	close(results)
	close(errs)
	n := 0
	for w := range results {
		if w {
			n++
		}
	}
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if n != 1 {
		t.Fatal("duplicate worker", n)
	}
	got, e := s.Get(f.ctx, cms.Query{Publication: "one", ID: p.ID, PublicOnly: true})
	if e != nil || got.ID != p.ID || got.UUID != p.UUID || !got.PublishedAt.Equal(due) {
		t.Fatal("restart/date/identity", e)
	}
	if r, e, j := f.counts(t, p.ID); r != 3 || e != 1 || j != 0 {
		t.Fatal("not atomic", r, e, j)
	}
}
func TestScheduledCancelRescheduleTrashAndStaleEdit(t *testing.T) {
	f := featureDB(t)
	p := f.create(t, "history", "published")
	original := *p.PublishedAt
	draft := "draft"
	p = f.update(t, p, cms.UpdatePost{Status: &draft})
	scheduled := "scheduled"
	due := time.Now().UTC().Truncate(time.Millisecond).Add(time.Hour)
	p = f.update(t, p, cms.UpdatePost{Status: &scheduled, PublishedAt: &due})
	old := p
	due = due.Add(time.Hour)
	p = f.update(t, p, cms.UpdatePost{Status: &scheduled, PublishedAt: &due})
	if _, e := f.store.Update(f.ctx, "one", p.ID, cms.UpdatePost{ExpectedUpdatedAt: old.UpdatedAt, Status: &draft}); !errors.Is(e, cms.ErrStaleUpdate) {
		t.Fatal("stale cancel", e)
	}
	p = f.update(t, p, cms.UpdatePost{Status: &draft})
	if !p.PublishedAt.Equal(original) || p.ScheduledFor != nil {
		t.Fatal("cancel lost date")
	}
	if _, _, j := f.counts(t, p.ID); j != 0 {
		t.Fatal("cancel retained due job")
	}
	p = f.update(t, p, cms.UpdatePost{Status: &scheduled, PublishedAt: &due})
	trash := true
	p = f.update(t, p, cms.UpdatePost{Trash: &trash})
	if p.Status != "draft" || p.TrashedAt == nil {
		t.Fatal("trash")
	}
	if worked, e := f.store.PublishDue(f.ctx, "one"); worked || e != nil {
		t.Fatal("cancelled job published", e)
	}
}
func TestScheduleOutboxRollbackRetryAndConnectionKill(t *testing.T) {
	f := featureDB(t)
	p := f.scheduled(t, "failure")
	due := f.due(t, p)
	mustExec(t, f.ctx, f.admin, "ALTER TABLE "+f.q+".outbox_events ADD CONSTRAINT reject_publish CHECK(event_type<>'post.published') NOT VALID")
	if worked, e := f.store.PublishDue(f.ctx, "one"); !worked || e == nil {
		t.Fatal("failure swallowed")
	}
	if r, e, j := f.counts(t, p.ID); r != 2 || e != 0 || j != 1 {
		t.Fatal("partial state", r, e, j)
	}
	var attempts int
	var code string
	if e := f.admin.QueryRow(f.ctx, "SELECT attempts,last_error_code FROM "+f.q+".publication_schedules WHERE post_id=$1", p.ID).Scan(&attempts, &code); e != nil || attempts != 1 || code != "publication_failed" {
		t.Fatal("retry metadata", e)
	}
	if worked, e := f.store.PublishDue(f.ctx, "one"); worked || e != nil {
		t.Fatal("ignored backoff")
	}
	mustExec(t, f.ctx, f.admin, "ALTER TABLE "+f.q+".outbox_events DROP CONSTRAINT reject_publish")
	// Simulate process termination after writes but before COMMIT on this fixture's backend only.
	tx, e := f.store.pool.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	var pid int
	tx.QueryRow(f.ctx, "SELECT pg_backend_pid()").Scan(&pid)
	published := "published"
	if _, e = f.store.updateTx(f.ctx, tx, "one", p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Status: &published, PublishedAt: &due}); e != nil {
		t.Fatal(e)
	}
	mustExec(t, f.ctx, f.admin, "SELECT pg_terminate_backend($1)", pid)
	if e = tx.Commit(f.ctx); e == nil {
		t.Fatal("killed transaction committed")
	}
	if r, e, j := f.counts(t, p.ID); r != 2 || e != 0 || j != 1 {
		t.Fatal("crash partial state", r, e, j)
	}
	f.due(t, p)
	if w, e := f.store.PublishDue(f.ctx, "one"); !w || e != nil {
		t.Fatal("recovery", e)
	}
	if r, e, j := f.counts(t, p.ID); r != 3 || e != 1 || j != 0 {
		t.Fatal("recovery duplicated", r, e, j)
	}
	if w, e := f.store.PublishDue(f.ctx, "one"); w || e != nil {
		t.Fatal("lost-ack retry duplicated")
	}
}
func TestSchedulerCancelAndEditRaces(t *testing.T) {
	f := featureDB(t)
	draft := "draft"
	for i := 0; i < 12; i++ {
		p := f.scheduled(t, "race-"+strings.ToLower(rand.Text()[:8]))
		f.due(t, p)
		start := make(chan struct{})
		errs := make(chan error, 2)
		go func() { <-start; _, e := f.store.PublishDue(f.ctx, "one"); errs <- e }()
		go func() {
			<-start
			_, e := f.store.Update(f.ctx, "one", p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Status: &draft})
			errs <- e
		}()
		close(start)
		for range 2 {
			e := <-errs
			if e != nil && !errors.Is(e, cms.ErrStaleUpdate) {
				t.Fatal(e)
			}
		}
		got, e := f.store.Get(f.ctx, cms.Query{Publication: "one", ID: p.ID})
		if e != nil {
			t.Fatal(e)
		}
		r, events, jobs := f.counts(t, p.ID)
		if r != 3 || jobs != 0 || (got.Status == "published" && events != 1) || (got.Status == "draft" && events != 0) {
			t.Fatal("race corrupted state", got.Status, r, events, jobs)
		}
	}
}

func TestSlugScheduleUpgradeFromManagementPreservesHistory(t *testing.T) {
	f := featureDBVersion(t, 12)
	published := f.create(t, "old-public", "published")
	hidden := f.create(t, "never-public", "draft")
	// A date alone is deliberately not evidence of publication.
	mustExec(t, f.ctx, f.admin, "UPDATE "+f.q+".posts SET published_at=clock_timestamp()-interval '1 day' WHERE id=$1", hidden.ID)
	var before string
	if e := f.admin.QueryRow(f.ctx, "SELECT json_agg(p ORDER BY id)::text FROM "+f.q+".posts p").Scan(&before); e != nil {
		t.Fatal(e)
	}
	for range 2 {
		if e := Migrate(f.ctx, f.dsn, f.schema, "feature-test"); e != nil {
			t.Fatal(e)
		}
	}
	var after string
	if e := f.admin.QueryRow(f.ctx, "SELECT json_agg(p ORDER BY id)::text FROM "+f.q+".posts p").Scan(&after); e != nil || before != after {
		t.Fatal("upgrade rewrote existing posts", e)
	}
	var claims, jobs, events int
	var falselyPublic bool
	if e := f.admin.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM "+f.q+".post_slug_claims),(SELECT count(*) FROM "+f.q+".publication_schedules),(SELECT count(*) FROM "+f.q+".outbox_events),(SELECT ever_public FROM "+f.q+".post_slug_claims WHERE post_id=$1)", hidden.ID).Scan(&claims, &jobs, &events, &falselyPublic); e != nil || claims != 2 || jobs != 0 || events != 1 || falselyPublic {
		t.Fatal("upgrade seeded incorrect intent", e, claims, jobs, events, falselyPublic)
	}
	if e := f.store.CheckPublishingManagement(f.ctx); e != nil {
		t.Fatal(e)
	}
	if r, e := f.store.ResolveURL(f.ctx, "one", "post", published.Slug); e != nil || r.PostID != published.ID {
		t.Fatal("current public route lost", e)
	}
	if _, e := f.store.ResolveURL(f.ctx, "one", "post", hidden.Slug); !errors.Is(e, cms.ErrNotFound) {
		t.Fatal("dated draft leaked")
	}
}

func TestScheduledMediaPrivacyAndPagePublication(t *testing.T) {
	f := featureDB(t)
	origin := "https://images.example.invalid"
	f.store.mediaOrigin = origin
	fs, e := objects.NewFilesystem(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer fs.Close()
	service, e := media.New(f.store, fs, origin, "feature-test")
	if e != nil {
		t.Fatal(e)
	}
	asset, e := service.Upload(f.ctx, "one", mediaPNG(t, 15), "image/png", "")
	if e != nil {
		t.Fatal(e)
	}
	p, e := cms.NewPost("one", cms.CreatePost{Title: "Scheduled image", HTML: `<p><img src="` + service.URL(asset) + `"></p>`})
	if e != nil {
		t.Fatal(e)
	}
	p, e = f.store.Create(f.ctx, p)
	if e != nil {
		t.Fatal(e)
	}
	due := time.Now().UTC().Truncate(time.Millisecond).Add(time.Hour)
	status := "scheduled"
	p = f.update(t, p, cms.UpdatePost{Status: &status, PublishedAt: &due})
	if _, e = f.store.MediaGet(f.ctx, "one", asset.ID, true); !errors.Is(e, media.ErrNotFound) {
		t.Fatal("scheduled media public")
	}
	f.due(t, p)
	if _, e = f.store.PublishDue(f.ctx, "one"); e != nil {
		t.Fatal(e)
	}
	if _, _, e = service.Read(f.ctx, "one", asset.ID, true); e != nil {
		t.Fatal("due publication did not activate media", e)
	}
	page, e := cms.NewPost("one", cms.CreatePost{Title: "Scheduled page", HTML: "<p>Page</p>"})
	if e != nil {
		t.Fatal(e)
	}
	page.Kind = "page"
	page, e = f.store.Create(f.ctx, page)
	if e != nil {
		t.Fatal(e)
	}
	page = f.update(t, page, cms.UpdatePost{Status: &status, PublishedAt: &due})
	f.due(t, page)
	if _, e = f.store.PublishDue(f.ctx, "one"); e != nil {
		t.Fatal(e)
	}
	if _, e = f.store.Get(f.ctx, cms.Query{Publication: "one", ID: page.ID, Kind: "page", PublicOnly: true}); e != nil {
		t.Fatal("page did not publish", e)
	}
	if _, events, jobs := f.counts(t, page.ID); events != 0 || jobs != 0 {
		t.Fatal("page emitted federation", events, jobs)
	}
}
