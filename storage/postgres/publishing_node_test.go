package postgres

import (
	"bytes"
	"content/cms"
	"content/internal/testoracle"
	"content/publishing"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type loseFirstACK struct {
	sender publishing.Sender
	lost   bool
	first  []byte
	t      *testing.T
}

func (s *loseFirstACK) Send(ctx context.Context, payload []byte) publishing.Outcome {
	if s.lost && s.first != nil {
		if !bytes.Equal(s.first, payload) {
			s.t.Error("retry changed exact event bytes")
		}
		s.first = nil
	}
	out := s.sender.Send(ctx, payload)
	if !s.lost && out.State == "accepted" {
		s.lost = true
		s.first = bytes.Clone(payload)
		return publishing.Outcome{State: "retry", Code: "simulated_lost_ack"}
	}
	return out
}
func TestPostgresGoNodePublishing(t *testing.T) {
	if os.Getenv("CONTENT_TEST_ACTIVITYPUB") != "1" {
		t.Skip("requires synthetic Node oracle opt-in")
	}
	f := newRoleFixture(t)
	writer, queue := publishingStores(t, f)
	oracle := testoracle.Start(t, "delivery-test")
	sender, err := publishing.NewHTTPClient(oracle.URL+"/internal/v2/events", testoracle.Token)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	lost := &loseFirstACK{sender: sender, t: t}
	worker := publishing.Worker{Enabled: true, Publication: "delivery-test", Destination: "ap", Queue: queue, Sender: lost, Timeout: 5 * time.Second, Lease: 15 * time.Second, Idle: time.Millisecond, MaxAttempts: 5}
	p := newPublishingPost(t, f, writer, strings.Repeat("😀", 255), "published")
	step := func() {
		t.Helper()
		if yes, err := worker.Step(f.ctx); err != nil || !yes {
			t.Fatal("worker step", yes, err)
		}
	}
	step() // Node commits, but Go simulates losing that 202.
	var eventID string
	var attempts int
	if err = f.admin.QueryRow(f.ctx, "SELECT event_id::text,attempts FROM "+ident(f.schema)+".publishing_deliveries WHERE post_id=$1", p.ID).Scan(&eventID, &attempts); err != nil || attempts != 1 {
		t.Fatal(err)
	}
	mustExec(t, f.ctx, f.admin, "UPDATE "+ident(f.schema)+".publishing_deliveries SET next_attempt_at=clock_timestamp() WHERE event_id=$1", eventID)
	step() // Same bytes => duplicate acknowledgment; no duplicate AP activity.
	var code, result string
	if err = f.admin.QueryRow(f.ctx, "SELECT last_error_code,accepted_result FROM "+ident(f.schema)+".publishing_deliveries WHERE event_id=$1", eventID).Scan(&code, &result); err != nil || code != "duplicate" || result != "applied" {
		t.Fatal("duplicate not accepted", code, result, err)
	}
	html := "<p>Edited public body</p>"
	p, err = writer.Update(f.ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, HTML: &html})
	if err != nil {
		t.Fatal(err)
	}
	step()
	draft := "draft"
	private := "<p>private staged body</p>"
	p, err = writer.Update(f.ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Status: &draft, HTML: &private})
	if err != nil {
		t.Fatal(err)
	}
	step()
	state := readOracle(t, oracle.URL)
	var first struct {
		Object struct {
			Name string `json:"name"`
		} `json:"object"`
	}
	if err = json.Unmarshal(state.Activities[0].Payload, &first); err != nil || first.Object.Name != p.Title {
		t.Fatal("Node changed/rejected Unicode title", err)
	}

	if len(state.Activities) != 3 || len(state.Receipts) != 3 || state.Objects[0].State != "withdrawn" {
		t.Fatal("withdrawal not durable", state)
	}
	for _, activity := range state.Activities {
		if activity.Public && activity.Kind != "Delete" {
			t.Fatal("old body publicly exposed")
		}
	}
	if public, err := writer.Get(f.ctx, cms.Query{Publication: p.Publication, ID: p.ID, PublicOnly: true}); err != cms.ErrNotFound {
		t.Fatal("draft public", public, err)
	}
	pub := "published"
	html = "<p>Republished public body</p>"
	p, err = writer.Update(f.ctx, p.Publication, p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Status: &pub, HTML: &html})
	if err != nil {
		t.Fatal(err)
	}
	step()
	state = readOracle(t, oracle.URL)
	if len(state.Activities) != 4 || len(state.Receipts) != 4 || len(state.Objects) != 1 || state.Objects[0].ContentID != p.UUID || state.Objects[0].InstanceID == p.UUID || state.Objects[0].State != "public" {
		t.Fatal("republish identity/activity mismatch", state)
	}
	for n, kind := range []string{"Create", "Update", "Delete", "Create"} {
		if state.Activities[n].Kind != kind {
			t.Fatal("wrong activity order", state)
		}
	}
	// Replay the frozen event after a local reconnect, proving durable bytes/IDs.
	workerDSN := queue.pool.Config().ConnString()
	queue.Close()
	reconnected, err := Open(f.ctx, workerDSN, f.schema, "installation-a")
	if err != nil {
		t.Fatal(err)
	}
	defer reconnected.Close()
	// Original writer and the DB remain untouched; sender sees an ordinary duplicate.
	var payload []byte
	if err = reconnected.pool.QueryRow(f.ctx, "SELECT payload FROM "+ident(f.schema)+".publishing_deliveries WHERE event_id=$1", eventID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if out := sender.Send(f.ctx, payload); out.State != "accepted" || out.Code != "duplicate" {
		t.Fatal("replay not idempotent", out)
	}
	// Runtime rejects wrong publication and a reused event ID with changed content.
	event, _ := publishing.Decode(payload)
	event.Publication = "other"
	raw, _ := publishing.Encode(event)
	if status := postOracle(t, oracle.URL, raw); status != 403 {
		t.Fatal("publication scope", status)
	}
	event.Publication = p.Publication
	event.Document.Title = "Forged duplicate"
	raw, _ = publishing.Encode(event)
	if status := postOracle(t, oracle.URL, raw); status != 409 {
		t.Fatal("event ID conflict", status)
	}
	// Exercise the module-owned positive/negative validation corpus over real HTTP.
	data, err := os.ReadFile(filepath.Join(os.Getenv("CONTENT_TEST_ACTIVITYPUB_MODULE"), "contracts", "fixtures", "v2-cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name  string         `json:"name"`
		Event map[string]any `json:"event"`
		Valid bool           `json:"valid"`
	}
	if json.Unmarshal(data, &cases) != nil {
		t.Fatal("bad corpus")
	}
	for _, c := range cases {
		eid, _ := publishing.UUID()
		cid, _ := publishing.UUID()
		c.Event["event_id"] = eid
		c.Event["content_id"] = cid
		c.Event["object_instance"] = cid
		c.Event["publication_id"] = "delivery-test"
		if doc, ok := c.Event["document"].(map[string]any); ok {
			doc["url"] = "https://example.invalid/fixture/"
		}
		if c.Valid && c.Event["type"] == "content.updated" {
			seedBytes, _ := json.Marshal(c.Event)
			seed, _ := publishing.Decode(seedBytes)
			seed.Type = "content.published"
			seed.Revision = "1"
			seed.EventID, _ = publishing.UUID()
			seedBytes, _ = publishing.Encode(seed)
			if got := postOracle(t, oracle.URL, seedBytes); got != 202 {
				t.Fatal("fixture publication seed failed", got)
			}
			c.Event["revision"] = "2"
		}
		raw, _ := json.Marshal(c.Event)
		want := 422
		if c.Valid {
			want = 202
		}
		if got := postOracle(t, oracle.URL, raw); got != want {
			t.Errorf("fixture %s: HTTP %d want %d", c.Name, got, want)
		}
	}
}

type oracleState struct {
	Objects []struct {
		ContentID  string `json:"content_id"`
		InstanceID string `json:"instance_id"`
		State      string `json:"state"`
	} `json:"objects"`
	Activities []struct {
		Kind    string          `json:"kind"`
		Public  bool            `json:"public"`
		Payload json.RawMessage `json:"payload"`
	} `json:"activities"`
	Receipts []json.RawMessage `json:"receipts"`
}

func readOracle(t *testing.T, base string) oracleState {
	t.Helper()
	client := http.Client{Timeout: 5 * time.Second}
	r, err := client.Get(base + "/_test/state")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var state oracleState
	if json.NewDecoder(r.Body).Decode(&state) != nil {
		t.Fatal("oracle state decode")
	}
	return state
}
func postOracle(t *testing.T, base string, raw []byte) int {
	t.Helper()
	r, _ := http.NewRequest("POST", base+"/internal/v2/events", bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+testoracle.Token)
	r.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}
