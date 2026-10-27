package postgres

import (
	"bytes"
	"content/cms"
	"encoding/hex"
	"encoding/json"
	"github.com/golang-jwt/jwt/v5"
	"testing"
	"time"
)

func TestPostgresUlyssesDraftMetadata(t *testing.T) {
	f := newRoleFixture(t)
	c := newEditClient(t, f, "ulysses-draft", 7)
	other := newEditClient(t, f, "other-draft", 8)
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{Audience: jwt.ClaimStrings{"/v4/admin/"}, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute))})
	token.Header["kid"] = hex.EncodeToString(bytes.Repeat([]byte{7}, 12))
	signed, e := token.SignedString(bytes.Repeat([]byte{7}, 32))
	if e != nil {
		t.Fatal(e)
	}
	c.token = "Ghost " + signed
	base := "/ghost/api/v4/admin/posts/"
	body := map[string]any{"title": "Ulysses synthetic", "slug": "ulysses-synthetic", "html": "<p>Private <strong>text</strong>.</p>", "status": "draft", "featured": false, "published_at": nil, "tags": []any{}}
	p := c.decode(c.call("POST", base+"?source=html", map[string]any{"posts": []any{body}}), 201)
	stored, e := c.store.Get(f.ctx, cms.Query{Publication: c.publication, ID: p.ID})
	if e != nil || string(stored.Presentation["featured"]) != "false" || stored.PublishedAt != nil || stored.HTML != body["html"] {
		t.Fatal("create metadata", e)
	}
	edit := map[string]any{"updated_at": p.UpdatedAt, "html": "<p>Edited</p>", "featured": true, "published_at": nil, "tags": []any{}}
	next := c.decode(c.call("PUT", base+p.ID+"/?source=html", map[string]any{"posts": []any{edit}}), 200)
	if c.call("PUT", base+p.ID+"/?source=html", map[string]any{"posts": []any{edit}}).Code != 409 {
		t.Fatal("stale accepted")
	}
	if other.edit(next, map[string]any{"featured": false}).Code != 404 {
		t.Fatal("cross publication edit")
	}
	stored, e = c.store.Get(f.ctx, cms.Query{Publication: c.publication, ID: p.ID})
	if e != nil || stored.Revision != 2 || string(stored.Presentation["featured"]) != "true" || stored.PublishedAt != nil {
		t.Fatal("edit metadata", e)
	}
	var oldFeatured bool
	if e = f.admin.QueryRow(f.ctx, "SELECT (metadata->'presentation'->>'featured')::boolean FROM "+ident(f.schema)+".post_revisions WHERE publication_id=$1 AND post_id=$2 AND revision=1", c.publication, p.ID).Scan(&oldFeatured); e != nil || oldFeatured {
		t.Fatal("historical metadata changed", e)
	}
	w := c.call("GET", base+p.ID+"/", nil)
	var response struct{ Posts []struct{ Featured bool } }
	json.Unmarshal(w.Body.Bytes(), &response)
	if w.Code != 200 || len(response.Posts) != 1 || !response.Posts[0].Featured {
		t.Fatal("readback")
	}
	if c.public(p.ID).Code != 404 {
		t.Fatal("private exposed")
	}
	if revisions, events := editCounts(t, f, c.publication, p.ID); revisions != 2 || events != 0 {
		t.Fatal("partial writes", revisions, events)
	}
	published := c.decode(c.call("PUT", base+p.ID+"/", map[string]any{"posts": []any{map[string]any{"updated_at": next.UpdatedAt, "status": "published"}}}), 200)
	if published.PublishedAt == nil || c.public(p.ID).Code != 200 {
		t.Fatal("publishing failed")
	}
	for _, fields := range []map[string]any{{"featured": nil}, {"published_at": time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)}, {"published_at": nil, "status": "draft"}, {"tags": []any{"unsupported"}}} {
		fields["updated_at"] = published.UpdatedAt
		w = c.call("PUT", base+p.ID+"/", map[string]any{"posts": []any{fields}})
		if w.Code != 400 && w.Code != 422 {
			t.Fatal("invalid metadata accepted", w.Code)
		}
	}
	if revisions, events := editCounts(t, f, c.publication, p.ID); revisions != 3 || events != 1 {
		t.Fatal("invalid update wrote partial state", revisions, events)
	}
	stored, e = c.store.Get(f.ctx, cms.Query{Publication: c.publication, ID: p.ID})
	if e != nil || stored.Status != "published" || stored.PublishedAt == nil || stored.PublishedAt.UTC().Format("2006-01-02T15:04:05.000Z") != *published.PublishedAt {
		t.Fatal("publication date changed", e)
	}
}
