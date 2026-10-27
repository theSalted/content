package httpapi

import (
	"content/cms"
	"encoding/json"
	"fmt"
	"github.com/golang-jwt/jwt/v5"
	"testing"
)

// Body shapes are measured Ulysses42 metadata; values here are synthetic.
func TestUlyssesObservedDraftFields(t *testing.T) {
	s, m, _ := setup(t)
	claims := currentClaims()
	claims.Audience = jwt.ClaimStrings{"/v4/admin/"}
	auth := signed(t, s.admin, jwt.SigningMethodHS256, claims, s.admin.id)
	for _, featured := range []bool{false, true} {
		body := fmt.Sprintf(`{"posts":[{"title":"Ulysses synthetic","slug":"ulysses-%t","html":"<p>Private <strong>text</strong>.</p>","status":"draft","featured":%t,"published_at":null,"tags":[]}]}`, featured, featured)
		w := request(s, "POST", "/ghost/api/v4/admin/posts/?source=html", body, auth)
		if w.Code != 201 {
			t.Fatal(w.Code, w.Body.String())
		}
		var response struct {
			Posts []struct {
				ID, UUID, HTML, Status, UpdatedAt string
				Featured                          bool
				PublishedAt                       *string `json:"published_at"`
			}
		}
		if json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Posts) != 1 {
			t.Fatal("response")
		}
		p := m.posts[len(m.posts)-1]
		q := response.Posts[0]
		if q.Featured != featured || q.Status != "draft" || q.PublishedAt != nil || len(q.ID) != 24 || len(q.UUID) != 36 || p.HTML != "<p>Private <strong>text</strong>.</p>" {
			t.Fatal("draft contract mismatch")
		}
		path := "/ghost/api/v4/admin/posts/" + p.ID + "/?source=html"
		edit := fmt.Sprintf(`{"posts":[{"updated_at":%q,"title":"Edited","html":"<p>Edited</p>","featured":%t,"published_at":null,"tags":[]}]}`, timestamp(p.UpdatedAt), !featured)
		w = request(s, "PUT", path, edit, auth)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if request(s, "PUT", path, edit, auth).Code != 409 {
			t.Fatal("stale accepted")
		}
		current := m.posts[len(m.posts)-1]
		var value bool
		json.Unmarshal(current.Presentation["featured"], &value)
		if value == featured || current.PublishedAt != nil || current.HTML != "<p>Edited</p>" {
			t.Fatal("edit not persisted")
		}
		if request(s, "GET", "/ghost/api/content/posts/"+p.ID+"/?key="+fixtureContentKey, "", "").Code != 404 {
			t.Fatal("draft exposed")
		}
	}
}
func TestDraftMetadataValidationAndDatePreservation(t *testing.T) {
	s, m, auth := setup(t)
	for _, fields := range []string{`"featured":null`, `"featured":"false"`, `"featured":1`, `"published_at":"2020-01-01T00:00:00.000Z"`, `"published_at":false`} {
		w := request(s, "POST", "/ghost/api/admin/posts/?source=html", `{"posts":[{"title":"Invalid",`+fields+`}]}`, auth)
		if w.Code != 400 || len(m.posts) != 0 {
			t.Fatal("invalid metadata saved", fields, w.Code)
		}
	}
	w := request(s, "POST", "/ghost/api/admin/posts/?source=html", `{"posts":[{"title":"Published","status":"published","featured":true,"published_at":null}]}`, auth)
	if w.Code != 201 {
		t.Fatal(w.Code)
	}
	p := m.posts[0]
	body := fmt.Sprintf(`{"posts":[{"updated_at":%q,"published_at":null,"status":"draft","featured":false}]}`, timestamp(p.UpdatedAt))
	if request(s, "PUT", "/ghost/api/admin/posts/"+p.ID+"/", body, auth).Code != 422 || m.posts[0].Status != "published" || m.posts[0].PublishedAt == nil {
		t.Fatal("publication history cleared")
	}
	// Applying metadata edits clones the revision rather than changing history.
	p.Presentation = cms.Presentation{"featured": json.RawMessage(`true`), "tags": json.RawMessage(`[{"name":"Before"}]`), "meta_title": json.RawMessage(`"Preserved"`)}
	f := false
	q, e := cms.ApplyUpdate(p, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Featured: &f, ClearTags: true})
	if e != nil || string(p.Presentation["featured"]) != "true" || string(q.Presentation["featured"]) != "false" || string(q.Presentation["tags"]) != "[]" || string(q.Presentation["meta_title"]) != `"Preserved"` {
		t.Fatal("metadata revision mutation", e)
	}
}
