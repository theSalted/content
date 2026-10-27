package postgres

import (
	"bytes"
	"content/cms"
	"content/publishing"
	"encoding/json"
	"testing"
	"time"
)

func TestPostgresAuthoredTagSnapshots(t *testing.T) {
	f := newRoleFixture(t)
	writer, worker := publishingStores(t, f)
	if e := WithPublicTagOrigins([]string{"https://example.invalid"})(writer); e != nil {
		t.Fatal(e)
	}
	p, e := cms.NewPost("delivery-test", cms.CreatePost{Title: "Authored tags", HTML: "<p>Public article</p>", Status: "draft"})
	if e != nil {
		t.Fatal(e)
	}
	p.Presentation = map[string]json.RawMessage{"tags": json.RawMessage(`[{"name":"Original ✨","visibility":"public","url":"https://example.invalid/topics/original/"},{"name":"#secret","visibility":"internal"}]`)}
	p, e = writer.Create(f.ctx, p)
	if e != nil {
		t.Fatal(e)
	}
	if yes, e := worker.PrepareNext(f.ctx, p.Publication, "ap"); e != nil || yes {
		t.Fatal("draft leaked")
	}
	pub := "published"
	p, e = writer.Update(f.ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Status: &pub})
	if e != nil {
		t.Fatal(e)
	}
	// Mutate test-owned metadata as a migration would, before delivery preparation:
	// the already-written snapshot must retain its authored original.
	changed := `[{"name":"Changed","visibility":"public","url":"https://example.invalid/topics/changed/"}]`
	mustExec(t, f.ctx, f.admin, "UPDATE "+ident(f.schema)+".post_revisions SET metadata=jsonb_set(metadata,'{presentation,tags}',$1::jsonb) WHERE post_id=$2 AND revision=$3", changed, p.ID, p.Revision)
	prepareAll(t, f, worker)
	job, e := worker.Claim(f.ctx, p.Publication, "ap", 30*time.Second)
	if e != nil || job == nil {
		t.Fatal(e)
	}
	event, e := publishing.Decode(job.Payload)
	if e != nil || len(event.Document.Tags) != 1 || event.Document.Tags[0].Name != "Original ✨" {
		t.Fatal("mutable/private tags", e)
	}
	original := append([]byte(nil), job.Payload...)
	mustExec(t, f.ctx, f.admin, "UPDATE "+ident(f.schema)+".publishing_deliveries SET lease_until=clock_timestamp()-interval '1 second' WHERE event_id=$1", job.EventID)
	retry, e := worker.Claim(f.ctx, p.Publication, "ap", 30*time.Second)
	if e != nil || retry == nil || !bytes.Equal(original, retry.Payload) {
		t.Fatal("retry changed bytes", e)
	}
	if e = worker.Finish(f.ctx, *retry, publishing.Outcome{State: "accepted", Result: "applied"}); e != nil {
		t.Fatal(e)
	}
	title := "Updated public article"
	p, e = writer.Update(f.ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Title: &title})
	if e != nil {
		t.Fatal(e)
	}
	prepareAll(t, f, worker)
	next := accepted(t, f, worker)
	updated, e := publishing.Decode(next.Payload)
	if e != nil || len(updated.Document.Tags) != 1 || updated.Document.Tags[0].Name != "Changed" {
		t.Fatal("new revision failed to replace tags", e)
	}
	// A malformed explicitly public URL must roll back the post revision and event.
	invalid := `[{"name":"Missing canonical URL","visibility":"public"}]`
	mustExec(t, f.ctx, f.admin, "UPDATE "+ident(f.schema)+".post_revisions SET metadata=jsonb_set(metadata,'{presentation,tags}',$1::jsonb) WHERE post_id=$2 AND revision=$3", invalid, p.ID, p.Revision)
	before := p.Revision
	title = "Must roll back"
	if _, e = writer.Update(f.ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Title: &title}); e == nil {
		t.Fatal("invalid public projection silently omitted")
	}
	saved, e := writer.Get(f.ctx, cms.Query{Publication: p.Publication, ID: p.ID})
	if e != nil || saved.Revision != before || saved.Title != p.Title {
		t.Fatal("partial update", e)
	}
	var count int
	if e = f.admin.QueryRow(f.ctx, "SELECT count(*) FROM "+ident(f.schema)+".outbox_events WHERE post_id=$1", p.ID).Scan(&count); e != nil || count != 2 {
		t.Fatal("partial outbox", count, e)
	}
	draft := "draft"
	private := "<p>Private body</p>"
	p, e = writer.Update(f.ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Status: &draft, HTML: &private})
	if e != nil {
		t.Fatal("withdrawal blocked by tag metadata", e)
	}
	prepareAll(t, f, worker)
	withdrawn := accepted(t, f, worker)
	event, e = publishing.Decode(withdrawn.Payload)
	if e != nil || event.Document != nil || bytes.Contains(withdrawn.Payload, []byte("Private")) {
		t.Fatal("private withdrawal data", e)
	}
}
func TestTagProjectionDefaultKeepsExistingSnapshotBytes(t *testing.T) {
	p, e := cms.NewPost("example", cms.CreatePost{Title: "Example", Status: "published"})
	if e != nil {
		t.Fatal(e)
	}
	p.Presentation = map[string]json.RawMessage{"tags": json.RawMessage(`[{"name":"Missing URL","visibility":"public"}]`)}
	expected, e := snapshotJSON(p, true)
	if e != nil {
		t.Fatal(e)
	}
	s := Store{}
	actual, e := s.publishingSnapshot(p, true)
	if e != nil || !bytes.Equal(expected, actual) {
		t.Fatal("default altered historic shape", e)
	}
}
