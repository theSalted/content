package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"content/cms"
	"content/publishing"
)

func publishingStores(t *testing.T, f *roleFixture) (*Store, *Store) {
	t.Helper()
	role := f.runtimeRole + "_delivery"
	createTestRole(t, f.ctx, f.admin, role, f.rolePassword)
	t.Cleanup(func() {
		mustExec(t, context.Background(), f.admin, "DROP OWNED BY "+ident(role))
		mustExec(t, context.Background(), f.admin, "DROP ROLE "+ident(role))
	})
	q := ident(f.schema)
	mustExec(t, f.ctx, f.admin, "GRANT CONNECT ON DATABASE "+ident(f.database)+" TO "+ident(role))
	mustExec(t, f.ctx, f.admin, "GRANT USAGE ON SCHEMA "+q+" TO "+ident(role))
	mustExec(t, f.ctx, f.admin, "GRANT SELECT ON "+q+".installation,"+q+".outbox_events,"+q+".publishing_streams,"+q+".publishing_deliveries TO "+ident(role))
	mustExec(t, f.ctx, f.admin, "GRANT INSERT ON "+q+".publishing_deliveries TO "+ident(role))
	mustExec(t, f.ctx, f.admin, "GRANT UPDATE(last_revision,object_instance,lifecycle,blocked_reason) ON "+q+".publishing_streams TO "+ident(role))
	mustExec(t, f.ctx, f.admin, "GRANT UPDATE(state,attempts,next_attempt_at,lease_token,lease_until,last_error_code,accepted_result) ON "+q+".publishing_deliveries TO "+ident(role))
	mustExec(t, f.ctx, f.admin, "GRANT INSERT ON "+q+".publishing_streams TO "+ident(f.runtimeRole))
	dsn, _ := url.Parse(f.runtimeDSN)
	dsn.User = url.UserPassword(role, f.rolePassword)
	worker, err := Open(f.ctx, dsn.String(), f.schema, "installation-a")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(worker.Close)
	writer, err := Open(f.ctx, f.runtimeDSN, f.schema, "installation-a", WithNewPostPublishing(publishing.Enrollment{Destination: "ap", SourceID: "content", SiteURL: "https://example.invalid"}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(writer.Close)
	return writer, worker
}
func newPublishingPost(t *testing.T, f *roleFixture, writer *Store, title, status string) cms.Post {
	t.Helper()
	p, err := cms.NewPost("delivery-test", cms.CreatePost{Title: title, HTML: "<p>Original</p>", Status: status})
	if err != nil {
		t.Fatal(err)
	}
	p, err = writer.Create(f.ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func prepareAll(t *testing.T, f *roleFixture, worker *Store) {
	t.Helper()
	for n := 0; n < 100; n++ {
		yes, err := worker.PrepareNext(f.ctx, "delivery-test", "ap")
		if err != nil {
			t.Fatal(err)
		}
		if !yes {
			return
		}
	}
	t.Fatal("preparation did not stop")
}
func accepted(t *testing.T, f *roleFixture, worker *Store) *publishing.Job {
	t.Helper()
	job, err := worker.Claim(f.ctx, "delivery-test", "ap", 30*time.Second)
	if err != nil || job == nil {
		t.Fatal("claim", job, err)
	}
	if err = worker.Finish(f.ctx, *job, publishing.Outcome{State: "accepted", Result: "applied", Code: "applied"}); err != nil {
		t.Fatal(err)
	}
	return job
}
func TestPostgresPublishingDelivery(t *testing.T) {
	f := newRoleFixture(t)
	writer, worker := publishingStores(t, f)
	t.Run("history remains un-enrolled and default writes create no delivery", func(t *testing.T) {
		original, err := Open(f.ctx, f.runtimeDSN, f.schema, "installation-a")
		if err != nil {
			t.Fatal(err)
		}
		defer original.Close()
		p := newPublishingPost(t, f, original, "Historical", "published")
		title := "Edited historical post"
		if _, err := writer.Update(f.ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Title: &title}); err != nil {
			t.Fatal(err)
		}
		if yes, err := worker.PrepareNext(f.ctx, "delivery-test", "ap"); err != nil || yes {
			t.Fatal("history auto-enrolled", yes, err)
		}
		var count int
		if err = f.admin.QueryRow(f.ctx, "SELECT count(*) FROM "+ident(f.schema)+".outbox_events WHERE post_id=$1", p.ID).Scan(&count); err != nil || count != 2 {
			t.Fatal("history lost", count, err)
		}
	})
	t.Run("new draft enrollment stays silent until first publication", func(t *testing.T) {
		p := newPublishingPost(t, f, writer, "Enrolled draft", "draft")
		if yes, err := worker.PrepareNext(f.ctx, "delivery-test", "ap"); err != nil || yes {
			t.Fatal("draft prepared a public event", yes, err)
		}
		published := "published"
		if _, err := writer.Update(f.ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Status: &published}); err != nil {
			t.Fatal(err)
		}
		prepareAll(t, f, worker)
		job := accepted(t, f, worker)
		event, err := publishing.Decode(job.Payload)
		if err != nil || event.Revision != "2" || event.Type != "content.published" || event.ObjectInstance != p.UUID {
			t.Fatal(event, err)
		}
	})
	t.Run("queued lifecycle preserves exact snapshots and new republish instance", func(t *testing.T) {
		p := newPublishingPost(t, f, writer, "Enrolled", "published")
		newHTML := "<p>Changed</p>"
		p, err := writer.Update(f.ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, HTML: &newHTML})
		if err != nil {
			t.Fatal(err)
		}
		draft := "draft"
		private := "<p>Private draft must never federate</p>"
		p, err = writer.Update(f.ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Status: &draft, HTML: &private})
		if err != nil {
			t.Fatal(err)
		}
		pub := "published"
		publicHTML := "<p>Republished</p>"
		_, err = writer.Update(f.ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Status: &pub, HTML: &publicHTML})
		if err != nil {
			t.Fatal(err)
		}
		prepareAll(t, f, worker)
		var events []publishing.Event
		for n := 0; n < 4; n++ {
			job := accepted(t, f, worker)
			e, err := publishing.Decode(job.Payload)
			if err != nil {
				t.Fatal(err)
			}
			events = append(events, e)
		}
		if events[0].Document.HTML != "<p>Original</p>" || events[1].Document.HTML != newHTML || events[2].Document != nil || events[3].Document.HTML != publicHTML {
			t.Fatal("mutable or private snapshot")
		}
		if events[0].ObjectInstance != p.UUID || events[1].ObjectInstance != p.UUID || events[2].ObjectInstance != p.UUID || events[3].ObjectInstance == p.UUID || events[3].ContentID != p.UUID {
			t.Fatal("lifecycle identity lost")
		}
		if yes, err := worker.PrepareNext(f.ctx, "delivery-test", "ap"); err != nil || yes {
			t.Fatal("prepared twice")
		}
	})
	t.Run("lease expiry retry bytes ordering and scope", func(t *testing.T) {
		p := newPublishingPost(t, f, writer, "Lease", "published")
		title := "Updated"
		_, err := writer.Update(f.ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Title: &title})
		if err != nil {
			t.Fatal(err)
		}
		prepareAll(t, f, worker)
		if j, err := worker.Claim(f.ctx, "other-publication", "ap", 30*time.Second); err != nil || j != nil {
			t.Fatal("cross publication claim")
		}
		if j, err := worker.Claim(f.ctx, "delivery-test", "other-destination", 30*time.Second); err != nil || j != nil {
			t.Fatal("cross destination claim")
		}
		var jobs [2]*publishing.Job
		var errs [2]error
		var wg sync.WaitGroup
		for n := range 2 {
			wg.Add(1)
			go func() { defer wg.Done(); jobs[n], errs[n] = worker.Claim(f.ctx, "delivery-test", "ap", 30*time.Second) }()
		}
		wg.Wait()
		var first *publishing.Job
		for n, j := range jobs {
			if errs[n] != nil {
				t.Fatal(errs[n])
			}
			if j != nil {
				if first != nil {
					t.Fatal("two claims for same stream")
				}
				first = j
			}
		}
		if first == nil {
			t.Fatal("no claim")
		}
		mustExec(t, f.ctx, f.admin, "UPDATE "+ident(f.schema)+".publishing_deliveries SET lease_until=clock_timestamp()-interval '1 second' WHERE event_id=$1", first.EventID)
		retry, err := worker.Claim(f.ctx, "delivery-test", "ap", 30*time.Second)
		if err != nil || retry == nil || retry.EventID != first.EventID || retry.LeaseToken == first.LeaseToken || retry.Attempts != 2 || !bytes.Equal(retry.Payload, first.Payload) {
			t.Fatal("retry changed identity/bytes", retry, err)
		}
		if err = worker.Finish(f.ctx, *first, publishing.Outcome{State: "accepted", Result: "applied"}); !errors.Is(err, ErrLeaseLost) {
			t.Fatal("old lease acknowledged", err)
		}
		if err = worker.Finish(f.ctx, *retry, publishing.Outcome{State: "retry", Code: "transport_error", Delay: time.Hour}); err != nil {
			t.Fatal(err)
		}
		if j, err := worker.Claim(f.ctx, "delivery-test", "ap", 30*time.Second); err != nil || j != nil {
			t.Fatal("retry delay/order bypass")
		}
		mustExec(t, f.ctx, f.admin, "UPDATE "+ident(f.schema)+".publishing_deliveries SET next_attempt_at=clock_timestamp() WHERE event_id=$1", retry.EventID)
		accepted(t, f, worker)
		accepted(t, f, worker)
	})
	t.Run("quarantine blocks later events for that article only", func(t *testing.T) {
		p := newPublishingPost(t, f, writer, "Quarantine", "published")
		title := "Later"
		_, err := writer.Update(f.ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Title: &title})
		if err != nil {
			t.Fatal(err)
		}
		prepareAll(t, f, worker)
		job, err := worker.Claim(f.ctx, "delivery-test", "ap", 30*time.Second)
		if err != nil || job == nil {
			t.Fatal(err)
		}
		if err = worker.Finish(f.ctx, *job, publishing.Outcome{State: "quarantined", Code: "http_rejected"}); err != nil {
			t.Fatal(err)
		}
		if j, err := worker.Claim(f.ctx, "delivery-test", "ap", 30*time.Second); err != nil || j != nil {
			t.Fatal("quarantined predecessor bypass")
		}
		other := newPublishingPost(t, f, writer, "Independent", "published")
		prepareAll(t, f, worker)
		j := accepted(t, f, worker)
		e, _ := publishing.Decode(j.Payload)
		if e.ContentID != other.UUID {
			t.Fatal("other stream blocked")
		}
	})
	t.Run("preparation failure rolls back mapping and payload", func(t *testing.T) {
		p := newPublishingPost(t, f, writer, "Prepare rollback", "published")
		mustExec(t, f.ctx, f.admin, "ALTER TABLE "+ident(f.schema)+".publishing_deliveries ADD CONSTRAINT reject_preparation CHECK(false) NOT VALID")
		if _, err := worker.PrepareNext(f.ctx, "delivery-test", "ap"); err == nil {
			t.Fatal("constraint did not fail")
		}
		mustExec(t, f.ctx, f.admin, "ALTER TABLE "+ident(f.schema)+".publishing_deliveries DROP CONSTRAINT reject_preparation")
		var revision int64
		if err := f.admin.QueryRow(f.ctx, "SELECT last_revision FROM "+ident(f.schema)+".publishing_streams WHERE post_id=$1", p.ID).Scan(&revision); err != nil || revision != 0 {
			t.Fatal("partial mapping committed", revision, err)
		}
		prepareAll(t, f, worker)
		accepted(t, f, worker)
	})
	t.Run("unsupported projection blocks durably without losing CMS source", func(t *testing.T) {
		p, err := cms.NewPost("delivery-test", cms.CreatePost{Title: "Payload expansion", Status: "published", HTML: strings.Repeat("\\", (1<<20)-100)})
		if err != nil {
			t.Fatal(err)
		}
		p, err = writer.Create(f.ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		prepareAll(t, f, worker)
		var code string
		if err := f.admin.QueryRow(f.ctx, "SELECT blocked_reason FROM "+ident(f.schema)+".publishing_streams WHERE post_id=$1", p.ID).Scan(&code); err != nil || code != "payload_rejected" {
			t.Fatal(code, err)
		}
		saved, err := writer.Get(f.ctx, cms.Query{Publication: p.Publication, ID: p.ID, PublicOnly: true})
		if err != nil || saved.HTML != p.HTML {
			t.Fatal("projection changed CMS")
		}
		if yes, err := worker.PrepareNext(f.ctx, "delivery-test", "ap"); err != nil || yes {
			t.Fatal("blocked stream busy loop")
		}
	})
	t.Run("worker and API grants preserve immutable payload and module ownership", func(t *testing.T) {
		for _, query := range []string{"SELECT * FROM " + ident(f.otherSchema) + ".posts", "SELECT * FROM " + ident(f.consumerSchema) + ".private_records", "SELECT * FROM " + ident(f.schema) + ".posts", "UPDATE " + ident(f.schema) + ".publishing_deliveries SET payload='{}'::bytea", "UPDATE " + ident(f.schema) + ".publishing_streams SET source_id='forged'", "UPDATE " + ident(f.schema) + ".outbox_events SET snapshot='{}'", "CREATE TABLE " + ident(f.schema) + ".forbidden_delivery(id int)"} {
			_, err := worker.pool.Exec(f.ctx, query)
			denied(t, err)
		}
		for _, query := range []string{"SELECT * FROM " + ident(f.schema) + ".publishing_deliveries", "UPDATE " + ident(f.schema) + ".publishing_streams SET lifecycle='public'"} {
			_, err := writer.pool.Exec(f.ctx, query)
			denied(t, err)
		}
	})
	t.Run("enrollment failure rolls back post revision and intent", func(t *testing.T) {
		mustExec(t, f.ctx, f.admin, "ALTER TABLE "+ident(f.schema)+".publishing_streams ADD CONSTRAINT reject_enrollment CHECK(false) NOT VALID")
		defer mustExec(t, f.ctx, f.admin, "ALTER TABLE "+ident(f.schema)+".publishing_streams DROP CONSTRAINT reject_enrollment")
		p, _ := cms.NewPost("delivery-test", cms.CreatePost{Title: "Must roll back", Status: "published"})
		if _, err := writer.Create(f.ctx, p); err == nil {
			t.Fatal("enrollment failure ignored")
		}
		var count int
		if err := f.admin.QueryRow(f.ctx, "SELECT count(*) FROM "+ident(f.schema)+".posts WHERE id=$1", p.ID).Scan(&count); err != nil || count != 0 {
			t.Fatal("partial post", count, err)
		}
	})
	// Payloads are bytea, not reserialized JSONB. All prepared deliveries decode.
	rows, err := f.admin.Query(f.ctx, "SELECT payload FROM "+ident(f.schema)+".publishing_deliveries")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var payload []byte
		if rows.Scan(&payload) != nil || !json.Valid(payload) {
			t.Fatal("invalid stored payload")
		}
	}
}
