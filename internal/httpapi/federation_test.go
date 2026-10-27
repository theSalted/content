package httpapi

import (
	"content/cms"
	"encoding/json"
	"net/url"
	"testing"
)

func TestPublicFederationProjection(t *testing.T) {
	s := &Server{}
	p := cms.Post{Kind: "post", Title: "Example", Status: "published", Visibility: "public"}
	if got, ok := s.projectRead(p, true, url.Values{})["activitypub"]; !ok || got != nil {
		t.Fatal("missing unconfirmed null")
	}
	p.ActivityPub = &cms.FederationProjection{ObjectID: "https://example.invalid/ap/objects/confirmed", State: "public", Acknowledged: true}
	got := s.projectRead(p, true, url.Values{})
	raw, err := json.Marshal(got["activitypub"])
	if err != nil || string(raw) != `{"object_id":"https://example.invalid/ap/objects/confirmed","state":"public","acknowledged":true}` {
		t.Fatal(string(raw), err)
	}
	if len(s.projectRead(p, true, url.Values{"fields": {"activitypub"}})) != 1 {
		t.Fatal("field selection")
	}
	if _, ok := s.projectRead(p, true, url.Values{"fields": {"title"}})["activitypub"]; ok {
		t.Fatal("unrequested field")
	}
	for _, state := range []string{"withdrawn", "queued", ""} {
		p.ActivityPub.State = state
		if s.projectRead(p, true, url.Values{})["activitypub"] != nil {
			t.Fatal("unconfirmed state")
		}
	}
	p.ActivityPub.State = "public"
	p.ActivityPub.ObjectID = "https://example.invalid/object?token=secret"
	if s.projectRead(p, true, url.Values{})["activitypub"] != nil {
		t.Fatal("unsafe URI")
	}
	p.ActivityPub.ObjectID = "https://example.invalid/object"
	for _, kind := range []string{"page", "post"} {
		for _, status := range []string{"draft", "published"} {
			for _, public := range []bool{false, true} {
				p.Kind = kind
				p.Status = status
				_, ok := s.projectRead(p, public, url.Values{})["activitypub"]
				if ok != (public && kind == "post" && status == "published") {
					t.Fatal("projection privacy", kind, status, public)
				}
			}
		}
	}
}
