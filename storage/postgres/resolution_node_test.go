package postgres

import (
	"bytes"
	"content/cms"
	"content/internal/testoracle"
	"content/publishing"
	"os"
	"testing"
	"time"
)

func TestPostgresGoActualNodeReceiptResolution(t *testing.T) {
	if os.Getenv("CONTENT_TEST_ACTIVITYPUB") != "1" {
		t.Skip("requires synthetic actual AP receiver")
	}
	f := newRoleFixture(t)
	writer, queue := projectionStores(t, f)
	grantResolution(t, f, queue)
	oracle := testoracle.Start(t, "delivery-test")
	sender, err := publishing.NewHTTPClient(oracle.URL+"/internal/v2/events", testoracle.Token)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	resolver, err := publishing.NewReceiptHTTPClient(oracle.URL+"/internal/v2/receipts/resolve", testoracle.Token)
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.Close()
	live := publishing.Worker{Enabled: true, Publication: "delivery-test", Destination: "ap", Queue: queue, Sender: sender, Timeout: 5 * time.Second, Lease: 15 * time.Second, Idle: time.Millisecond, MaxAttempts: 5}
	resolve := publishing.ResolutionWorker{Enabled: true, Publication: "delivery-test", Destination: "ap", Queue: queue, Resolver: resolver, Timeout: 5 * time.Second, Lease: 15 * time.Second, Idle: time.Millisecond, MaxAttempts: 5}
	step := func() {
		t.Helper()
		if yes, err := live.Step(f.ctx); err != nil || !yes {
			t.Fatal("live step", yes, err)
		}
	}
	lookup := func() {
		t.Helper()
		if yes, err := resolve.Step(f.ctx); err != nil || !yes {
			t.Fatal("resolution step", yes, err)
		}
	}
	p := newPublishingPost(t, f, writer, "Real receiver query", "published")
	projected(t, f, writer, p, "")
	step()
	projected(t, f, writer, p, "")
	lookup()
	saved, err := writer.Get(f.ctx, cms.Query{Publication: p.Publication, ID: p.ID, PublicOnly: true})
	if err != nil || saved.ActivityPub == nil {
		t.Fatal("missing actual confirmation", err)
	}
	first := saved.ActivityPub.ObjectID
	projected(t, f, writer, p, first)
	var original []byte
	if err = f.admin.QueryRow(f.ctx, "SELECT payload FROM "+ident(f.schema)+".publishing_deliveries WHERE post_id=$1 ORDER BY revision LIMIT 1", p.ID).Scan(&original); err != nil {
		t.Fatal(err)
	}
	title := "Same instance edited"
	p, err = writer.Update(f.ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Title: &title})
	if err != nil {
		t.Fatal(err)
	}
	projected(t, f, writer, p, first)
	step()
	lookup()
	projected(t, f, writer, p, first)
	// Exact old applied event resolves today's later revision only for same instance.
	same := resolver.Resolve(f.ctx, original)
	if same.State != "complete" || same.Receipt.Projection == nil || same.Receipt.Projection.ObjectID != first || same.Receipt.Projection.Revision != "2" {
		t.Fatal("same-instance later edit", same)
	}
	draft, pub := "draft", "published"
	p, err = writer.Update(f.ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Status: &draft})
	if err != nil {
		t.Fatal(err)
	}
	step()
	lookup()
	old := resolver.Resolve(f.ctx, original)
	if old.State != "complete" || old.Receipt.Projection != nil || old.Receipt.Reason != "not_public" {
		t.Fatal("withdrawn query", old)
	}
	p, err = writer.Update(f.ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Status: &pub})
	if err != nil {
		t.Fatal(err)
	}
	projected(t, f, writer, p, "")
	step()
	projected(t, f, writer, p, "")
	lookup()
	saved, err = writer.Get(f.ctx, cms.Query{Publication: p.Publication, ID: p.ID, PublicOnly: true})
	if err != nil || saved.ActivityPub == nil || saved.ActivityPub.ObjectID == first {
		t.Fatal("republish confirmation", err)
	}
	superseded := resolver.Resolve(f.ctx, original)
	if superseded.State != "complete" || superseded.Receipt.Projection != nil || superseded.Receipt.Reason != "superseded" {
		t.Fatal("retired query rebound", superseded)
	}
	before := readOracle(t, oracle.URL)
	for range 3 {
		if out := resolver.Resolve(f.ctx, original); out.State != "complete" {
			t.Fatal(out)
		}
	}
	after := readOracle(t, oracle.URL)
	if len(after.Activities) != len(before.Activities) || len(after.Receipts) != len(before.Receipts) {
		t.Fatal("read resolution wrote activities/receipts")
	}
	// Exact payload conflict is quarantined, with no accepted-write replay.
	altered := bytes.Replace(original, []byte("Real receiver query"), []byte("Changed exact request"), 1)
	if out := resolver.Resolve(f.ctx, altered); out.State != "quarantined" || out.Code != "resolution_rejected" {
		t.Fatal("changed query accepted", out)
	}
}
