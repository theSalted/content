package stagingprobe

import (
	"content/cms"
	"content/publishing"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestRecoveryReadOnlyURL(t *testing.T) {
	s, e := readOnlyURL("postgres://runtime:private@db.invalid/content?sslmode=verify-full&default_transaction_read_only=off")
	if e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(s)
	if u.Query().Get("default_transaction_read_only") != "on" || u.Query().Get("sslmode") != "verify-full" {
		t.Fatal("readonly or TLS lost")
	}
}
func recoveryFixture() (Config, cms.Post, []delivery, []savedIntent, PeerStatus) {
	c := Config{Publication: "content-staging", Source: "content"}
	p := cms.Post{ID: strings.Repeat("a", 24), UUID: "11111111-1111-4111-8111-111111111111", Slug: "staging-test"}
	bodies := []string{"<p>Original immutable staging snapshot.</p>", "<p>Edited public staging snapshot.</p>", "", "<p>Republished staging snapshot.</p>", ""}
	kinds := []string{"post.published", "post.updated", "post.unpublished", "post.published", "post.unpublished"}
	types := []string{"content.published", "content.updated", "content.withdrawn", "content.published", "content.withdrawn"}
	ds := []delivery{}
	ins := []savedIntent{}
	peer := PeerStatus{}
	for i := 0; i < 5; i++ {
		id := strconv.Itoa(i+1) + "1111111-1111-4111-8111-111111111111"
		title := "Synthetic Go staging edited"
		if i == 0 {
			title = "Synthetic Go staging draft"
		}
		status := "published"
		if bodies[i] == "" {
			status = "draft"
		}
		ev := publishing.Event{SchemaVersion: 2, Publication: c.Publication, EventID: id, ContentID: p.UUID, ObjectInstance: p.UUID, DeliveryPolicy: "live", Source: publishing.Source{ID: c.Source, Epoch: "1"}, Revision: strconv.Itoa(i + 2), OccurredAt: "2026-10-26T00:00:00Z", Type: types[i]}
		if i >= 3 {
			ev.ObjectInstance = "22222222-2222-4222-8222-222222222222"
		}
		if bodies[i] != "" {
			ev.Document = &publishing.Document{Title: title, HTML: bodies[i], Visibility: "public", URL: "https://example.invalid/test/", PublishedAt: "2026-10-26T00:00:00Z"}
		}
		b, _ := publishing.Encode(ev)
		d := delivery{eventID: id, state: "accepted", result: "applied", revision: int64(i + 2), attempts: 1, payload: b}
		if i == 0 {
			d.attempts = 2
			d.code = "duplicate"
		}
		ds = append(ds, d)
		s := publishing.Snapshot{ID: p.ID, UUID: p.UUID, Slug: p.Slug, Title: title, Status: status, Visibility: "public", HTML: bodies[i]}
		raw, _ := json.Marshal(s)
		if bodies[i] == "" {
			var m map[string]any
			json.Unmarshal(raw, &m)
			delete(m, "html")
			raw, _ = json.Marshal(m)
		}
		ins = append(ins, savedIntent{eventID: id, kind: kinds[i], revision: int64(i + 2), snapshot: s, raw: raw})
		for n := 0; n < d.attempts; n++ {
			var attempt struct {
				EventID, BodySHA256             string
				OriginalStatus, DeliveredStatus int
				Ack                             string
			}
			attempt.EventID = id
			attempt.BodySHA256 = digest(b)
			attempt.OriginalStatus = 202
			attempt.DeliveredStatus = 202
			peer.CMSAttempts = append(peer.CMSAttempts, attempt)
		}
	}
	return c, p, ds, ins, peer
}
func TestRecoveryPersistedEvidence(t *testing.T) {
	c, p, ds, ins, peer := recoveryFixture()
	if _, e := verifySaved(c, p, ds, ins, peer); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"bytes", "snapshot", "instance", "private", "state", "count", "attempts"} {
		t.Run(name, func(t *testing.T) {
			c, p, ds, ins, peer := recoveryFixture()
			switch name {
			case "bytes":
				peer.CMSAttempts[0].BodySHA256 = "changed"
			case "snapshot":
				ins[0].snapshot.HTML = "changed"
			case "instance":
				var ev publishing.Event
				json.Unmarshal(ds[3].payload, &ev)
				ev.ObjectInstance = p.UUID
				ds[3].payload, _ = publishing.Encode(ev)
			case "private":
				ins[2].raw = []byte(`{"html":"PRIVATE-STAGING-CANARY"}`)
			case "state":
				ds[1].state = "pending"
			case "count":
				ds = ds[:4]
			case "attempts":
				ds[0].attempts = 1
			}
			if _, e := verifySaved(c, p, ds, ins, peer); e == nil {
				t.Fatal("invalid recovery evidence accepted")
			}
		})
	}
}
