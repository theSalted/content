package stagingprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestOneStartGate(t *testing.T) {
	now := time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)
	if (OneGate{1.25, now}).Validate(now) != nil {
		t.Fatal("valid gate rejected")
	}
	for _, g := range []OneGate{{4.5, now}, {-1, now}, {math.NaN(), now}, {math.Inf(1), now}, {1, now.Add(-6 * time.Minute)}, {1, now.Add(2 * time.Minute)}} {
		if g.Validate(now) == nil {
			t.Fatal("unsafe spend/freshness accepted")
		}
	}
	deadline, _ := time.Parse(time.RFC3339, OneDeadline)
	late := deadline.Add(-44 * time.Minute)
	if (OneGate{0, late}).Validate(late) == nil {
		t.Fatal("late start accepted")
	}
}
func TestOneFixedScope(t *testing.T) {
	now := time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)
	env := fixtureEnv()
	env["CONTENT_ONE_POST_APPROVED"] = "true"
	env["CONTENT_ONE_POST_PUBLICATION"] = "publication-fixture"
	env["CONTENT_ONE_POST_ORIGIN"] = "https://example.invalid"
	env["CONTENT_ONE_POST_ACTOR"] = "https://example.invalid/actors/fixture"
	env["CONTENT_PUBLICATION_ID"] = "publication-fixture"
	env["CONTENT_SITE_URL"] = "https://example.invalid"
	env["CONTENT_TEST_SPEND_USD"] = "1.25"
	env["CONTENT_TEST_SPEND_MEASURED_AT"] = now.Format(time.RFC3339)
	c, _, clean, e := loadOneAt(func(k string) string { return env[k] }, now)
	defer clean()
	if e != nil || !c.RequirePeer || c.Publication != "publication-fixture" {
		t.Fatal("fixed one scope rejected", e)
	}
	for _, tc := range []struct{ k, v string }{{"CONTENT_ONE_POST_ACTOR", "https://wrong.example.invalid/actors/fixture"}, {"CONTENT_ONE_POST_APPROVED", "false"}, {"CONTENT_PUBLICATION_ID", "content-staging"}, {"CONTENT_SITE_URL", "https://wrong.example.invalid"}, {"CONTENT_PUBLISHING_ENDPOINT", "https://example.invalid/internal/v2/events"}, {"CONTENT_TEST_SPEND_USD", "NaN"}, {"CONTENT_TEST_SPEND_MEASURED_AT", "2026-10-25T00:00:00Z"}} {
		old := env[tc.k]
		env[tc.k] = tc.v
		_, _, clean, e := loadOneAt(func(k string) string { return env[k] }, now)
		clean()
		env[tc.k] = old
		if e == nil {
			t.Fatal("expanded or stale config allowed", tc.k)
		}
	}
	// The original staging command still rejects the new publication.
	_, clean, e = Load(func(k string) string { return env[k] })
	clean()
	if e == nil {
		t.Fatal("original staging scope was relaxed")
	}
}

func TestOnePeerReceipt(t *testing.T) {
	c := Config{ActorURL: "https://example.invalid/actors/fixture", SiteURL: "https://example.invalid", Publication: "content-production-test", RunID: "11111111-1111-4111-8111-111111111111"}
	id := "22222222-2222-4222-8222-222222222222"
	object := c.SiteURL + "/ap/objects/" + c.Publication + "/" + id
	rows := []delivery{{eventID: "publish", attempts: 1, payload: []byte("immutable-publish")}, {eventID: "withdraw", attempts: 1, payload: []byte("immutable-withdraw")}}
	fixture := map[string]any{"activities": []any{map[string]any{"id": "create", "type": "Create", "actor": c.ActorURL, "object": map[string]string{"id": object, "attributedTo": c.ActorURL, "name": OneTitle, "content": oneBody, "url": c.SiteURL + "/production-test-" + c.RunID + "/"}}, map[string]any{"id": "delete", "type": "Delete", "actor": c.ActorURL, "object": object}}, "attempts": []any{map[string]string{"id": "create", "type": "Create", "format": "legacy", "bodySha256": strings.Repeat("a", 64)}, map[string]string{"id": "delete", "type": "Delete", "format": "rfc9421", "bodySha256": strings.Repeat("b", 64)}}, "cmsAttempts": []any{map[string]any{"eventId": "publish", "bodySha256": digest(rows[0].payload), "originalStatus": 202, "deliveredStatus": 202}, map[string]any{"eventId": "withdraw", "bodySha256": digest(rows[1].payload), "originalStatus": 202, "deliveredStatus": 202}}}
	raw, _ := json.Marshal(fixture)
	var peer PeerStatus
	json.Unmarshal(raw, &peer)
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "" || r.URL.String() != object {
			t.Error("unexpected tombstone request")
		}
		return &http.Response{StatusCode: 410, Body: io.NopCloser(strings.NewReader(`{"type":"Tombstone"}`))}, nil
	})}
	if e := verifyOnePeer(context.Background(), c, client, peer, id, rows); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"actor", "attribution", "snapshot", "wrong object", "missing delete", "signature", "changed bytes"} {
		t.Run(name, func(t *testing.T) {
			var p PeerStatus
			json.Unmarshal(raw, &p)
			switch name {
			case "actor":
				p.Activities[1] = bytes.Replace(p.Activities[1], []byte(c.ActorURL), []byte("https://example.invalid/actors/wrong"), 1)
			case "attribution":
				p.Activities[0] = bytes.Replace(p.Activities[0], []byte(`"attributedTo":"`+c.ActorURL+`"`), []byte(`"attributedTo":"https://example.invalid/actors/wrong"`), 1)
			case "snapshot":
				p.Activities[0] = bytes.Replace(p.Activities[0], []byte(OneTitle), []byte("wrong"), 1)
			case "wrong object":
				p.Activities[1] = bytes.Replace(p.Activities[1], []byte(id), []byte("other"), 1)
			case "missing delete":
				p.Activities = p.Activities[:1]
			case "signature":
				p.Attempts[1].Format = "unchecked"
			case "changed bytes":
				p.CMSAttempts[1].BodySHA256 = "changed"
			}
			if verifyOnePeer(context.Background(), c, client, p, id, rows) == nil {
				t.Fatal("invalid production test evidence accepted")
			}
		})
	}
}
