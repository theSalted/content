package postgres

import (
	"bytes"
	"content/cms"
	"content/publishing"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func projectionStores(t *testing.T, f *roleFixture) (*Store, *Store) {
	t.Helper()
	writer, worker := publishingStores(t, f)
	q := ident(f.schema)
	api := ident(f.runtimeRole)
	delivery := ident(f.runtimeRole + "_delivery")
	mustExec(t, f.ctx, f.admin, "GRANT SELECT(publication_id,post_id,destination,public_generation,object_url,confirmed) ON "+q+".federation_projections TO "+api)
	mustExec(t, f.ctx, f.admin, "GRANT SELECT(publication_id,id,uuid,kind,status,visibility,revision,public_generation),UPDATE(archive_pending) ON "+q+".posts TO "+delivery)
	mustExec(t, f.ctx, f.admin, "GRANT SELECT,INSERT ON "+q+".federation_projections TO "+delivery)
	mustExec(t, f.ctx, f.admin, "GRANT UPDATE(public_generation,object_instance,object_url,acknowledged_revision,event_id,acknowledged_at,confirmed) ON "+q+".federation_projections TO "+delivery)
	for _, s := range []*Store{writer, worker} {
		if err := WithFederationProjection("ap")(s); err != nil {
			t.Fatal(err)
		}
	}
	return writer, worker
}
func projected(t *testing.T, f *roleFixture, s *Store, p cms.Post, want string) {
	t.Helper()
	got, err := s.Get(f.ctx, cms.Query{Publication: p.Publication, ID: p.ID, PublicOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if want == "" {
		if got.ActivityPub != nil {
			t.Fatal("unconfirmed discovery", got.ActivityPub)
		}
	} else if got.ActivityPub == nil || got.ActivityPub.ObjectID != want || got.ActivityPub.State != "public" || !got.ActivityPub.Acknowledged {
		t.Fatal("incorrect confirmed discovery", got.ActivityPub)
	}
	rows, n, err := s.Browse(f.ctx, cms.Query{Publication: p.Publication, PublicOnly: true, Page: 1, Limit: 100})
	if err != nil || n < 1 {
		t.Fatal("browse", err)
	}
	for _, r := range rows {
		if r.ID == p.ID {
			if (r.ActivityPub == nil) != (got.ActivityPub == nil) || r.ActivityPub != nil && *r.ActivityPub != *got.ActivityPub {
				t.Fatal("browse/read mismatch")
			}
			return
		}
	}
	t.Fatal("post absent from browse")
}
func claimProjection(t *testing.T, f *roleFixture, worker *Store) *publishing.Job {
	t.Helper()
	prepareAll(t, f, worker)
	j, err := worker.Claim(f.ctx, "delivery-test", "ap", 30*time.Second)
	if err != nil || j == nil {
		t.Fatal("claim", err)
	}
	return j
}
func publicConfirmation(url string) publishing.Outcome {
	return publishing.Outcome{State: "accepted", Result: "applied", Confirmation: &publishing.Confirmation{ObjectID: url, State: "public"}}
}
func TestPostgresConfirmedDiscoveryLifecycle(t *testing.T) {
	f := newRoleFixture(t)
	writer, worker := projectionStores(t, f)
	p := newPublishingPost(t, f, writer, "Confirmed discovery", "published")
	projected(t, f, writer, p, "")
	j := claimProjection(t, f, worker)
	projected(t, f, writer, p, "") // prepared and leased still unconfirmed
	// A lost lease cannot install a URL, and caller payload cannot rebind the job.
	lost := *j
	lost.LeaseToken = "00000000-0000-4000-8000-000000000001"
	if err := worker.Finish(f.ctx, lost, publicConfirmation("https://example.invalid/objects/forged")); !errors.Is(err, ErrLeaseLost) {
		t.Fatal("lost lease", err)
	}
	j.Payload = []byte(`{"forged":"another post"}`)
	first := "https://example.invalid/objects/confirmed-original"
	if err := worker.Finish(f.ctx, *j, publicConfirmation(first)); err != nil {
		t.Fatal(err)
	}
	projected(t, f, writer, p, first)
	title := "Pending ordinary edit"
	var err error
	p, err = writer.Update(f.ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Title: &title})
	if err != nil {
		t.Fatal(err)
	}
	projected(t, f, writer, p, first)
	update := claimProjection(t, f, worker)
	// The same lifecycle cannot change URI; queue acceptance must also roll back.
	if err = worker.Finish(f.ctx, *update, publicConfirmation("https://example.invalid/objects/wrong")); err == nil {
		t.Fatal("changed identity accepted")
	}
	var state string
	if err = f.admin.QueryRow(f.ctx, "SELECT state FROM "+ident(f.schema)+".publishing_deliveries WHERE event_id=$1", update.EventID).Scan(&state); err != nil || state != "leased" {
		t.Fatal("partial queue acceptance", state, err)
	}
	// Withdraw and republish before an old update acknowledgement arrives.
	draft := "draft"
	p, err = writer.Update(f.ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Status: &draft})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Get(f.ctx, cms.Query{Publication: p.Publication, ID: p.ID, PublicOnly: true}); !errors.Is(err, cms.ErrNotFound) {
		t.Fatal("draft exposed", err)
	}
	admin, err := writer.Get(f.ctx, cms.Query{Publication: p.Publication, ID: p.ID})
	if err != nil || admin.ActivityPub != nil {
		t.Fatal("draft mapping leaked", err)
	}
	pub := "published"
	p, err = writer.Update(f.ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Status: &pub})
	if err != nil {
		t.Fatal(err)
	}
	projected(t, f, writer, p, "")
	if err = worker.Finish(f.ctx, *update, publicConfirmation(first)); err != nil {
		t.Fatal("late applied ACK", err)
	}
	projected(t, f, writer, p, "")
	withdrawn := claimProjection(t, f, worker)
	if err = worker.Finish(f.ctx, *withdrawn, publishing.Outcome{State: "accepted", Result: "applied", Confirmation: &publishing.Confirmation{ObjectID: first, State: "withdrawn"}}); err != nil {
		t.Fatal(err)
	}
	republished := claimProjection(t, f, worker)
	event, err := publishing.Decode(republished.Payload)
	if err != nil || event.ObjectInstance == p.UUID {
		t.Fatal("new instance missing", err)
	}
	projected(t, f, writer, p, "")
	final := "https://example.invalid/objects/confirmed-republished"
	raw := append([]byte(nil), republished.Payload...)
	mustExec(t, f.ctx, f.admin, "UPDATE "+ident(f.schema)+".publishing_deliveries SET lease_until=clock_timestamp()-interval '1 second' WHERE event_id=$1", republished.EventID)
	retry, err := worker.Claim(f.ctx, p.Publication, "ap", 30*time.Second)
	if err != nil || retry == nil || !bytes.Equal(raw, retry.Payload) {
		t.Fatal("lost ACK retry bytes", err)
	}
	out := publicConfirmation(final)
	out.Code = "duplicate"
	if err = worker.Finish(f.ctx, *retry, out); err != nil {
		t.Fatal(err)
	}
	projected(t, f, writer, p, final)
	// API publication boundaries hold even with the projection join enabled.
	if _, err = writer.Get(f.ctx, cms.Query{Publication: "other", ID: p.ID, PublicOnly: true}); !errors.Is(err, cms.ErrNotFound) {
		t.Fatal("cross publication", err)
	}
}
func TestPostgresDiscoveryStaleLegacyAndRoleIsolation(t *testing.T) {
	f := newRoleFixture(t)
	writer, worker := projectionStores(t, f)
	for _, result := range []string{"stale", "applied"} {
		p := newPublishingPost(t, f, writer, "No versioned confirmation "+result, "published")
		j := claimProjection(t, f, worker)
		if err := worker.Finish(f.ctx, *j, publishing.Outcome{State: "accepted", Result: result}); err != nil {
			t.Fatal(err)
		}
		projected(t, f, writer, p, "")
	}
	p := newPublishingPost(t, f, writer, "Invalid confirmation", "published")
	j := claimProjection(t, f, worker)
	for _, out := range []publishing.Outcome{
		{State: "accepted", Result: "stale", Confirmation: &publishing.Confirmation{ObjectID: "https://example.invalid/object", State: "public"}},
		publicConfirmation("https://user:secret@example.invalid/object"),
		{State: "retry", Confirmation: &publishing.Confirmation{ObjectID: "https://example.invalid/object", State: "public"}},
	} {
		if err := worker.Finish(f.ctx, *j, out); err == nil {
			t.Fatal("invalid confirmation accepted")
		}
	}
	projected(t, f, writer, p, "")
	q := ident(f.schema)
	for _, query := range []string{"SELECT event_id FROM " + q + ".federation_projections", "SELECT * FROM " + q + ".publishing_deliveries", "DELETE FROM " + q + ".federation_projections", "UPDATE " + q + ".posts SET public_generation=1", "INSERT INTO " + q + ".federation_projections DEFAULT VALUES"} {
		_, err := f.runtime.Exec(f.ctx, query)
		denied(t, err)
	}
	for _, query := range []string{"SELECT source_html FROM " + q + ".post_revisions", "SELECT title FROM " + q + ".posts", "UPDATE " + q + ".posts SET title='forged'", "DELETE FROM " + q + ".federation_projections", "SELECT * FROM " + ident(f.consumerSchema) + ".private_records"} {
		_, err := worker.pool.Exec(f.ctx, query)
		denied(t, err)
	}
	// A direct runtime status change cannot recycle a prior generation/revision.
	draft := "draft"
	p, err := writer.Update(f.ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Status: &draft})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.runtime.Exec(f.ctx, "UPDATE "+q+".posts SET status='published' WHERE id=$1", p.ID); err == nil {
		t.Fatal("publication generation reused")
	}
}
func TestPostgresDiscoveryConfirmationWithdrawalRace(t *testing.T) {
	f := newRoleFixture(t)
	writer, worker := projectionStores(t, f)
	for n := 0; n < 5; n++ {
		p := newPublishingPost(t, f, writer, fmt.Sprint("race ", n), "published")
		j := claimProjection(t, f, worker)
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			errs <- worker.Finish(f.ctx, *j, publicConfirmation("https://example.invalid/objects/race"))
		}()
		go func() {
			defer wg.Done()
			draft := "draft"
			_, err := writer.Update(f.ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Status: &draft})
			errs <- err
		}()
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		got, err := writer.Get(context.Background(), cms.Query{Publication: p.Publication, ID: p.ID})
		if err != nil || got.Status != "draft" || got.ActivityPub != nil {
			t.Fatal("race exposed draft", err)
		}
		// Drain the withdrawal before the next test article.
		withdrawn := claimProjection(t, f, worker)
		if err = worker.Finish(f.ctx, *withdrawn, publishing.Outcome{State: "accepted", Result: "applied"}); err != nil {
			t.Fatal(err)
		}
	}
}
