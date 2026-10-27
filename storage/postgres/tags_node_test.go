package postgres

import (
	"content/cms"
	"content/internal/testoracle"
	"content/publishing"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestPostgresGoNodeAuthoredTags(t *testing.T) {
	if os.Getenv("CONTENT_TEST_ACTIVITYPUB") != "1" {
		t.Skip("requires synthetic loopback Node oracle")
	}
	f := newRoleFixture(t)
	writer, queue := publishingStores(t, f)
	if e := WithPublicTagOrigins([]string{"https://example.invalid"})(writer); e != nil {
		t.Fatal(e)
	}
	oracle := testoracle.Start(t, "delivery-test")
	sender, e := publishing.NewHTTPClient(oracle.URL+"/internal/v2/events", testoracle.Token)
	if e != nil {
		t.Fatal(e)
	}
	defer sender.Close()
	worker := publishing.Worker{Enabled: true, Publication: "delivery-test", Destination: "ap", Queue: queue, Sender: sender, Timeout: 5 * time.Second, Lease: 15 * time.Second, Idle: time.Millisecond, MaxAttempts: 3}
	p, e := cms.NewPost("delivery-test", cms.CreatePost{Title: "Tags across runtimes", Status: "published", HTML: "<p>Public article</p>"})
	if e != nil {
		t.Fatal(e)
	}
	p.Presentation = map[string]json.RawMessage{"tags": json.RawMessage(`[{"visibility":"public","name":"Unicode中文","url":"https://example.invalid/topics/unicode/"},{"visibility":"public","name":"Long label","url":"https://example.invalid/topics/label/"},{"visibility":"internal","name":"#private"}]`)}
	p, e = writer.Create(f.ctx, p)
	if e != nil {
		t.Fatal(e)
	}
	step := func() {
		t.Helper()
		if yes, e := worker.Step(f.ctx); e != nil || !yes {
			t.Fatal("worker did not advance", e)
		}
	}
	step()
	state := readOracle(t, oracle.URL)
	if len(state.Activities) != 1 {
		t.Fatal("missing Create")
	}
	var activity struct {
		Object struct {
			Tags []struct{ Type, Name, Href string } `json:"tag"`
		} `json:"object"`
	}
	if json.Unmarshal(state.Activities[0].Payload, &activity) != nil || len(activity.Object.Tags) != 2 || activity.Object.Tags[0].Name != "#Unicode中文" || activity.Object.Tags[0].Type != "Hashtag" || activity.Object.Tags[1].Name != "Long label" || activity.Object.Tags[1].Type != "Link" {
		t.Fatal("tag projection changed or leaked private metadata", string(state.Activities[0].Payload))
	}
	mustExec(t, f.ctx, f.admin, "UPDATE "+ident(f.schema)+".post_revisions SET metadata=jsonb_set(metadata,'{presentation,tags}','[]'::jsonb) WHERE post_id=$1 AND revision=$2", p.ID, p.Revision)
	title := "Tags removed"
	p, e = writer.Update(f.ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Title: &title})
	if e != nil {
		t.Fatal(e)
	}
	step()
	state = readOracle(t, oracle.URL)
	activity.Object.Tags = nil
	if len(state.Activities) != 2 || json.Unmarshal(state.Activities[1].Payload, &activity) != nil || len(activity.Object.Tags) != 0 {
		t.Fatal("tag removal failed")
	}
	draft := "draft"
	if _, e = writer.Update(f.ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Status: &draft}); e != nil {
		t.Fatal(e)
	}
	step()
	state = readOracle(t, oracle.URL)
	if len(state.Activities) != 3 || state.Activities[2].Kind != "Delete" {
		t.Fatal("withdrawal failed")
	}
}
