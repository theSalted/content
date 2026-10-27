package postgres

import (
	"bytes"
	"content/cms"
	"content/publishing"
	"context"
	"crypto/rand"
	"errors"
	"github.com/jackc/pgx/v5"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Existing disposable loopback DB only; unique synthetic schema, no new DB/roles.
func TestArchiveEnrollmentPostgres(t *testing.T) {
	dsn := os.Getenv("CONTENT_TEST_DATABASE_URL")
	if dsn == "" || os.Getenv("CONTENT_TEST_ALLOW_SCHEMA_CREATE") != "1" {
		t.Skip("requires opted-in existing disposable loopback test database")
	}
	cfg, e := pgx.ParseConfig(dsn)
	if e != nil || cfg.Host != "127.0.0.1" || !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatal("loopback test database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, e := pgx.ConnectConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close(context.Background())
	schema := "archive_test_" + strings.ToLower(rand.Text()[:10])
	q, _ := Schema(schema)
	if _, e = conn.Exec(ctx, "CREATE SCHEMA "+q); e != nil {
		t.Fatal(e)
	}
	defer func() {
		if _, e := conn.Exec(context.Background(), "DROP SCHEMA "+q+" CASCADE"); e != nil {
			t.Error(e)
		}
	}()
	for range 2 {
		if e = Migrate(ctx, dsn, schema, "archive-test"); e != nil {
			t.Fatal(e)
		}
	}
	store, e := Open(ctx, dsn, schema, "archive-test")
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	queue := store.ArchiveQueue()
	enrollment := publishing.Enrollment{Destination: "ap", SourceID: "content", SiteURL: "https://example.invalid/blog/"}
	makePost := func(kind, status string) cms.Post {
		p, e := cms.NewPost("publication", cms.CreatePost{Title: "Synthetic archive", Status: status, HTML: "<p>Original</p>"})
		if e != nil {
			t.Fatal(e)
		}
		p.Kind = kind
		p, e = store.Create(ctx, p)
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	request := func(p cms.Post) ArchiveRequest { return ArchiveRequest{p.Publication, p.ID, p.Revision, enrollment} }
	t.Run("private pages wrong scope and stale revision rejected", func(t *testing.T) {
		for _, p := range []cms.Post{makePost("post", "draft"), makePost("page", "published")} {
			if _, e := store.EnqueueArchive(ctx, request(p)); e == nil {
				t.Fatal("nonpublic post enrolled")
			}
		}
		p := makePost("post", "published")
		r := request(p)
		r.Publication = "other"
		if _, e := store.EnqueueArchive(ctx, r); !errors.Is(e, cms.ErrNotFound) {
			t.Fatal(e)
		}
		r = request(p)
		r.ExpectedRevision++
		if _, e := store.EnqueueArchive(ctx, r); !errors.Is(e, cms.ErrStaleUpdate) {
			t.Fatal(e)
		}
	})
	t.Run("concurrent author edit or archive wins without snapshot mismatch", func(t *testing.T) {
		for n := 0; n < 8; n++ {
			p := makePost("post", "published")
			req := request(p)
			title := "Concurrent title"
			gate, e := conn.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = gate.Exec(ctx, "SELECT id FROM "+q+".posts WHERE id=$1 FOR UPDATE", p.ID); e != nil {
				t.Fatal(e)
			}
			archived := make(chan error, 1)
			edited := make(chan error, 1)
			go func() { _, e := store.EnqueueArchive(ctx, req); archived <- e }()
			go func() {
				_, e := store.Update(ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Title: &title})
				edited <- e
			}()
			if e = gate.Commit(ctx); e != nil {
				t.Fatal(e)
			}
			ae, ee := <-archived, <-edited
			if ae == nil {
				if !errors.Is(ee, cms.ErrArchivePending) {
					t.Fatal("edit crossed archive barrier", ee)
				}
				job, e := queue.Claim(ctx, p.Publication, "ap", time.Minute)
				if e != nil || job == nil {
					t.Fatal(e)
				}
				a, e := publishing.DecodeArchive(job.Payload)
				if e != nil || a.ContentID != p.UUID || a.Document.Title != p.Title {
					t.Fatal("mixed revision snapshot", e)
				}
				if e = queue.Finish(ctx, *job, publishing.Outcome{State: "accepted", Result: "applied"}); e != nil {
					t.Fatal(e)
				}
			} else if !errors.Is(ae, cms.ErrStaleUpdate) || ee != nil {
				t.Fatal("unexpected race outcome", ae, ee)
			}
		}
	})
	p := makePost("post", "published")
	r := request(p)
	var first ArchiveReceipt
	t.Run("concurrent enrollment is one immutable job and paused stream", func(t *testing.T) {
		out := make(chan ArchiveReceipt, 2)
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		for range 2 {
			wg.Add(1)
			go func() { defer wg.Done(); v, e := store.EnqueueArchive(ctx, r); out <- v; errs <- e }()
		}
		wg.Wait()
		close(out)
		close(errs)
		for e := range errs {
			if e != nil {
				t.Fatal(e)
			}
		}
		for v := range out {
			if first.EventID == "" {
				first = v
			} else if v.EventID != first.EventID {
				t.Fatal("duplicate event ID")
			}
		}
		var count int
		conn.QueryRow(ctx, "SELECT count(*) FROM "+q+".publishing_archives WHERE post_id=$1", p.ID).Scan(&count)
		if count != 1 {
			t.Fatal(count)
		}
		if yes, e := store.PrepareNext(ctx, p.Publication, "ap"); e != nil || yes {
			t.Fatal("archive replayed old outbox", yes, e)
		}
		conn.QueryRow(ctx, "SELECT count(*) FROM "+q+".publishing_deliveries").Scan(&count)
		if count != 0 {
			t.Fatal("archive created live delivery")
		}
	})
	t.Run("edit and withdrawal held until exact ack", func(t *testing.T) {
		title := "Edited too soon"
		draft := "draft"
		for _, u := range []cms.UpdatePost{{ExpectedUpdatedAt: p.UpdatedAt, Title: &title}, {ExpectedUpdatedAt: p.UpdatedAt, Status: &draft}} {
			if _, e := store.Update(ctx, p.Publication, p.ID, u); !errors.Is(e, cms.ErrArchivePending) {
				t.Fatal(e)
			}
		}
		var rev int64
		conn.QueryRow(ctx, "SELECT revision FROM "+q+".posts WHERE id=$1", p.ID).Scan(&rev)
		if rev != p.Revision {
			t.Fatal("barrier mutated post")
		}
	})
	t.Run("lost ack retry is byte identical and activates only after ack", func(t *testing.T) {
		if j, e := queue.Claim(ctx, "wrong", "ap", time.Second); e != nil || j != nil {
			t.Fatal("scope", e)
		}
		j, e := queue.Claim(ctx, p.Publication, "ap", 5*time.Second)
		if e != nil || j == nil {
			t.Fatal(e)
		}
		raw := append([]byte(nil), j.Payload...)
		a, e := publishing.DecodeArchive(raw)
		if e != nil || a.ContentID != p.UUID || a.ObjectInstance != p.UUID || a.Document.PublishedAt != p.PublishedAt.UTC().Format(time.RFC3339Nano) {
			t.Fatal(a, e)
		}
		// Receiver may have committed; retry must neither release edits nor regenerate.
		if e = queue.Finish(ctx, *j, publishing.Outcome{State: "retry", Code: "lost_ack"}); e != nil {
			t.Fatal(e)
		}
		j2, e := queue.Claim(ctx, p.Publication, "ap", 5*time.Second)
		if e != nil || j2 == nil || !bytes.Equal(raw, j2.Payload) || j2.EventID != j.EventID || j2.LeaseToken == j.LeaseToken {
			t.Fatal("retry changed", e)
		}
		if e = queue.Finish(ctx, *j, publishing.Outcome{State: "accepted", Result: "applied"}); !errors.Is(e, ErrLeaseLost) {
			t.Fatal("stale lease accepted", e)
		}
		// Fail after stream/barrier writes; the transaction must roll everything back.
		if _, e = conn.Exec(ctx, "ALTER TABLE "+q+".publishing_archives ADD CONSTRAINT reject_archive_ack CHECK(state<>'accepted') NOT VALID"); e != nil {
			t.Fatal(e)
		}
		if e = queue.Finish(ctx, *j2, publishing.Outcome{State: "accepted", Result: "applied"}); e == nil {
			t.Fatal("injected acknowledgment failure swallowed")
		}
		var frozen bool
		var blocked string
		if e = conn.QueryRow(ctx, "SELECT p.archive_pending,s.blocked_reason FROM "+q+".posts p JOIN "+q+".publishing_streams s ON s.publication_id=p.publication_id AND s.post_id=p.id WHERE p.id=$1", p.ID).Scan(&frozen, &blocked); e != nil || !frozen || blocked != "archive_pending" {
			t.Fatal("partial activation committed", frozen, blocked, e)
		}
		if _, e = conn.Exec(ctx, "ALTER TABLE "+q+".publishing_archives DROP CONSTRAINT reject_archive_ack"); e != nil {
			t.Fatal(e)
		}
		if e = queue.Finish(ctx, *j2, publishing.Outcome{State: "accepted", Result: "applied", Code: "duplicate"}); e != nil {
			t.Fatal(e)
		}
		var state, instance string
		var revision int64
		conn.QueryRow(ctx, "SELECT lifecycle,object_instance::text,last_revision FROM "+q+".publishing_streams WHERE post_id=$1", p.ID).Scan(&state, &instance, &revision)
		if state != "public" || instance != p.UUID || revision != p.Revision {
			t.Fatal(state, instance, revision)
		}
		if yes, e := store.PrepareNext(ctx, p.Publication, "ap"); e != nil || yes {
			t.Fatal("old outbox replayed after ack", yes, e)
		}
	})
	t.Run("later Update and withdrawal retain object and exclude private body", func(t *testing.T) {
		title := "Edited after archive"
		var e error
		p, e = store.Update(ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Title: &title})
		if e != nil {
			t.Fatal(e)
		}
		draft := "draft"
		private := "<p>Private after withdrawal</p>"
		p, e = store.Update(ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Status: &draft, HTML: &private})
		if e != nil {
			t.Fatal(e)
		}
		for range 2 {
			if yes, e := store.PrepareNext(ctx, p.Publication, "ap"); e != nil || !yes {
				t.Fatal(yes, e)
			}
		}
		for i, kind := range []string{"content.updated", "content.withdrawn"} {
			job, e := store.Claim(ctx, p.Publication, "ap", time.Minute)
			if e != nil || job == nil {
				t.Fatal(e)
			}
			ev, e := publishing.Decode(job.Payload)
			if e != nil || ev.Type != kind || ev.ContentID != p.UUID || ev.ObjectInstance != p.UUID {
				t.Fatal(ev, e)
			}
			if i == 1 && ev.Document != nil {
				t.Fatal("private body leaked")
			}
			if e = store.Finish(ctx, *job, publishing.Outcome{State: "accepted", Result: "applied"}); e != nil {
				t.Fatal(e)
			}
		}
		again, e := store.EnqueueArchive(ctx, r)
		if e != nil || again.EventID != first.EventID || again.State != "accepted" {
			t.Fatal("retry resurrected", again, e)
		}
		changed := r
		changed.Enrollment.SiteURL = "https://different.invalid/blog/"
		if _, e := store.EnqueueArchive(ctx, changed); !errors.Is(e, ErrArchiveConflict) {
			t.Fatal("receipt changed", e)
		}
	})
	t.Run("quarantined stale acknowledgment never unfreezes", func(t *testing.T) {
		p := makePost("post", "published")
		if _, e := store.EnqueueArchive(ctx, request(p)); e != nil {
			t.Fatal(e)
		}
		j, e := queue.Claim(ctx, p.Publication, "ap", time.Minute)
		if e != nil || j == nil {
			t.Fatal(e)
		}
		if e = queue.Finish(ctx, *j, publishing.Outcome{State: "accepted", Result: "stale"}); e != nil {
			t.Fatal(e)
		}
		title := "unsafe"
		if _, e = store.Update(ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Title: &title}); !errors.Is(e, cms.ErrArchivePending) {
			t.Fatal(e)
		}
		var state string
		conn.QueryRow(ctx, "SELECT state FROM "+q+".publishing_archives WHERE event_id=$1", j.EventID).Scan(&state)
		if state != "quarantined" {
			t.Fatal(state)
		}
	})
}
