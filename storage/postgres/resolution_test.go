package postgres

import (
	"bytes"
	"content/cms"
	"content/publishing"
	"encoding/json"
	"testing"
	"time"
)

func grantResolution(t *testing.T, f *roleFixture, worker *Store) {
	t.Helper()
	q := ident(f.schema)
	role := ident(f.runtimeRole + "_delivery")
	mustExec(t, f.ctx, f.admin, "GRANT SELECT ON "+q+".publishing_archives TO "+role)
	mustExec(t, f.ctx, f.admin, "GRANT SELECT,INSERT ON "+q+".federation_resolution_jobs TO "+role)
	mustExec(t, f.ctx, f.admin, "GRANT UPDATE(state,attempts,next_attempt_at,lease_token,lease_until,last_error_code) ON "+q+".federation_resolution_jobs TO "+role)
}
func resolved(t *testing.T, payload []byte, url string) publishing.ResolutionOutcome {
	t.Helper()
	e, err := publishing.ReceiptEvent(payload)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := publishing.CanonicalEventHash(payload)
	if err != nil {
		t.Fatal(err)
	}
	r := publishing.ReceiptResolution{SchemaVersion: 1, EventID: e.EventID, Publication: e.Publication, ContentID: e.ContentID, Source: e.Source, Revision: e.Revision, EventHash: hash, Result: "applied", Projection: &publishing.ResolvedObject{ObjectID: url, ObjectInstance: e.ObjectInstance, Revision: e.Revision, State: "public"}, Reason: "confirmed"}
	return publishing.ResolutionOutcome{State: "complete", Code: "confirmed", Receipt: &r}
}
func claimResolution(t *testing.T, f *roleFixture, worker *Store) *publishing.Job {
	t.Helper()
	for range 100 {
		made, err := worker.PrepareResolution(f.ctx, "delivery-test", "ap")
		if err != nil {
			t.Fatal(err)
		}
		if !made {
			break
		}
	}
	job, err := worker.ClaimResolution(f.ctx, "delivery-test", "ap", 30*time.Second)
	if err != nil || job == nil {
		t.Fatal("resolution claim", err)
	}
	return job
}
func TestPostgresResolutionRetryDoesNotRepublish(t *testing.T) {
	f := newRoleFixture(t)
	writer, worker := projectionStores(t, f)
	grantResolution(t, f, worker)
	p := newPublishingPost(t, f, writer, "Resolved after accepted", "published")
	j := claimProjection(t, f, worker)
	if made, err := worker.PrepareResolution(f.ctx, p.Publication, "ap"); err != nil || made {
		t.Fatal("unaccepted resolved", err)
	}
	if err := worker.Finish(f.ctx, *j, publishing.Outcome{State: "accepted", Result: "applied"}); err != nil {
		t.Fatal(err)
	}
	query := claimResolution(t, f, worker)
	if !bytes.Equal(query.Payload, j.Payload) {
		t.Fatal("saved query changed")
	}
	if err := worker.FinishResolution(f.ctx, *query, publishing.ResolutionOutcome{State: "retry", Code: "transport_error"}); err != nil {
		t.Fatal(err)
	}
	var state string
	var attempts int
	if err := f.admin.QueryRow(f.ctx, "SELECT state,attempts FROM "+ident(f.schema)+".publishing_deliveries WHERE event_id=$1", j.EventID).Scan(&state, &attempts); err != nil || state != "accepted" || attempts != 1 {
		t.Fatal("query outage rewound delivery", state, attempts, err)
	}
	retry := claimResolution(t, f, worker)
	if !bytes.Equal(retry.Payload, query.Payload) || retry.LeaseToken == query.LeaseToken {
		t.Fatal("read retry changed identity")
	}
	outcome := resolved(t, retry.Payload, "https://example.invalid/objects/resolved")
	// Force failure after the projection INSERT: acceptance and projection roll back.
	mustExec(t, f.ctx, f.admin, "ALTER TABLE "+ident(f.schema)+".federation_resolution_jobs ADD CONSTRAINT reject_complete CHECK(state<>'complete') NOT VALID")
	if err := worker.FinishResolution(f.ctx, *retry, outcome); err == nil {
		t.Fatal("partial completion")
	}
	projected(t, f, writer, p, "")
	mustExec(t, f.ctx, f.admin, "ALTER TABLE "+ident(f.schema)+".federation_resolution_jobs DROP CONSTRAINT reject_complete")
	if err := worker.FinishResolution(f.ctx, *retry, outcome); err != nil {
		t.Fatal(err)
	}
	projected(t, f, writer, p, outcome.Receipt.Projection.ObjectID)
	if made, err := worker.PrepareResolution(f.ctx, p.Publication, "ap"); err != nil || made {
		t.Fatal("completed query requeued", err)
	}
	if next, err := worker.Claim(f.ctx, p.Publication, "ap", 30*time.Second); err != nil || next != nil {
		t.Fatal("accepted event replayed", err)
	}
	// Website API cannot read query payloads or alter the confirmed projection.
	_, err := f.runtime.Exec(f.ctx, "SELECT payload FROM "+ident(f.schema)+".federation_resolution_jobs")
	denied(t, err)
}
func TestPostgresArchiveResolutionIsSilentAndBound(t *testing.T) {
	f := newRoleFixture(t)
	writer, worker := projectionStores(t, f)
	grantResolution(t, f, worker)
	owner, err := Open(f.ctx, f.migrateDSN, f.schema, "installation-a", WithFederationProjection("ap"))
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	p, err := cms.NewPost("delivery-test", cms.CreatePost{Title: "Archive resolution", Status: "published", HTML: "<p>Archive</p>"})
	if err != nil {
		t.Fatal(err)
	}
	p, err = owner.Create(f.ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := owner.EnqueueArchive(f.ctx, ArchiveRequest{Publication: p.Publication, PostID: p.ID, ExpectedRevision: p.Revision, Enrollment: publishing.Enrollment{Destination: "ap", SourceID: "content", SiteURL: "https://example.invalid"}})
	if err != nil {
		t.Fatal(err)
	}
	job, err := owner.ArchiveQueue().Claim(f.ctx, p.Publication, "ap", 30*time.Second)
	if err != nil || job == nil {
		t.Fatal(err)
	}
	if err = owner.ArchiveQueue().Finish(f.ctx, *job, publishing.Outcome{State: "accepted", Result: "applied"}); err != nil {
		t.Fatal(err)
	}
	query := claimResolution(t, f, worker)
	if query.EventID != receipt.EventID || !bytes.Equal(query.Payload, job.Payload) {
		t.Fatal("archive query binding")
	}
	out := resolved(t, query.Payload, "https://example.invalid/objects/archive")
	forged := *out.Receipt
	forged.Source.ID = "forged"
	if err = worker.FinishResolution(f.ctx, *query, publishing.ResolutionOutcome{State: "complete", Receipt: &forged}); err == nil {
		t.Fatal("forged authority")
	}
	// Caller payload must not substitute an unrelated accepted record.
	query.Payload = []byte(`{"forged":true}`)
	if err = worker.FinishResolution(f.ctx, *query, out); err != nil {
		t.Fatal(err)
	}
	projected(t, f, writer, p, out.Receipt.Projection.ObjectID)
	var n int
	if err = f.admin.QueryRow(f.ctx, "SELECT count(*) FROM "+ident(f.schema)+".publishing_deliveries").Scan(&n); err != nil || n != 0 {
		t.Fatal("archive broadcast", n, err)
	}
	raw, _ := json.Marshal(out.Receipt)
	if bytes.Contains(raw, []byte("forged")) {
		t.Fatal("receipt altered")
	}
}

func TestPostgresResolutionAfterRepublishCannotRestoreRetiredURI(t *testing.T) {
	f := newRoleFixture(t)
	writer, worker := projectionStores(t, f)
	grantResolution(t, f, worker)
	p := newPublishingPost(t, f, writer, "Delayed lookup", "published")
	first := claimProjection(t, f, worker)
	if err := worker.Finish(f.ctx, *first, publishing.Outcome{State: "accepted", Result: "applied"}); err != nil {
		t.Fatal(err)
	}
	oldQuery := claimResolution(t, f, worker)
	oldOutcome := resolved(t, oldQuery.Payload, "https://example.invalid/objects/retired")
	draft, pub := "draft", "published"
	var err error
	p, err = writer.Update(f.ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Status: &draft})
	if err != nil {
		t.Fatal(err)
	}
	p, err = writer.Update(f.ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Status: &pub})
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.FinishResolution(f.ctx, *oldQuery, oldOutcome); err != nil {
		t.Fatal(err)
	}
	projected(t, f, writer, p, "")
	withdrawn := claimProjection(t, f, worker)
	if err = worker.Finish(f.ctx, *withdrawn, publishing.Outcome{State: "accepted", Result: "applied"}); err != nil {
		t.Fatal(err)
	}
	live := claimProjection(t, f, worker)
	if err = worker.Finish(f.ctx, *live, publishing.Outcome{State: "accepted", Result: "applied"}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		q := claimResolution(t, f, worker)
		e, err := publishing.ReceiptEvent(q.Payload)
		if err != nil {
			t.Fatal(err)
		}
		out := resolved(t, q.Payload, "https://example.invalid/objects/new-instance")
		if e.Document == nil {
			out.Receipt.Projection = nil
			out.Receipt.Reason = "not_public"
			out.Code = "not_public"
		}
		if err = worker.FinishResolution(f.ctx, *q, out); err != nil {
			t.Fatal(err)
		}
	}
	projected(t, f, writer, p, "https://example.invalid/objects/new-instance")
	title := "Updated"
	p, err = writer.Update(f.ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Title: &title})
	if err != nil {
		t.Fatal(err)
	}
	edit := claimProjection(t, f, worker)
	if err = worker.Finish(f.ctx, *edit, publishing.Outcome{State: "accepted", Result: "applied"}); err != nil {
		t.Fatal(err)
	}
	q := claimResolution(t, f, worker)
	negative := resolved(t, q.Payload, "https://example.invalid/objects/new-instance")
	negative.Receipt.Projection = nil
	negative.Receipt.Reason = "not_public"
	negative.Code = "not_public"
	if err = worker.FinishResolution(f.ctx, *q, negative); err != nil {
		t.Fatal(err)
	}
	projected(t, f, writer, p, "")
	// Explicit operator recovery resets only this failed discovery query, never a delivery.
	mustExec(t, f.ctx, f.admin, "UPDATE "+ident(f.schema)+".federation_resolution_jobs SET state='pending' WHERE event_id=$1", q.EventID)
	q2 := claimResolution(t, f, worker)
	if !bytes.Equal(q.Payload, q2.Payload) {
		t.Fatal("reconciliation changed bytes")
	}
	if err = worker.FinishResolution(f.ctx, *q2, resolved(t, q2.Payload, "https://example.invalid/objects/new-instance")); err != nil {
		t.Fatal(err)
	}
	projected(t, f, writer, p, "https://example.invalid/objects/new-instance")
}
