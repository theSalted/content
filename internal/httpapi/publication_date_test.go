package httpapi

import (
	"fmt"
	"testing"
	"time"
)

func TestPublicationDateEditRequiresTimezoneAndPreservesIdentity(t *testing.T) {
	s, m, auth := setup(t)
	if w := request(s, "POST", "/ghost/api/admin/posts/?source=html", `{"posts":[{"title":"Date fixture","html":"<p>Original</p>","status":"published"}]}`, auth); w.Code != 201 {
		t.Fatal(w.Code)
	}
	original := m.posts[0]
	path := "/ghost/api/admin/posts/" + original.ID + "/"
	for _, value := range []string{`"2020-01-02T03:04:05"`, `"2999-01-01T00:00:00Z"`, `"2020-01-01T00:00:00.000000001Z"`, `false`} {
		w := request(s, "PUT", path, fmt.Sprintf(`{"posts":[{"updated_at":%q,"published_at":%s}]}`, timestamp(m.posts[0].UpdatedAt), value), auth)
		if w.Code < 400 || m.posts[0].Revision != original.Revision {
			t.Fatal("invalid date saved", w.Code)
		}
	}
	w := request(s, "PUT", path, fmt.Sprintf(`{"posts":[{"updated_at":%q,"published_at":"2020-01-02T11:04:05.123+08:00"}]}`, timestamp(original.UpdatedAt)), auth)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	p := m.posts[0]
	if p.ID != original.ID || p.UUID != original.UUID || p.Slug != original.Slug || p.PublishedAt.Format(time.RFC3339Nano) != "2020-01-02T03:04:05.123Z" {
		t.Fatal("date normalization or identity changed")
	}
	w = request(s, "PUT", path, fmt.Sprintf(`{"posts":[{"updated_at":%q,"published_at":null}]}`, timestamp(p.UpdatedAt)), auth)
	if w.Code != 422 || m.posts[0].Revision != p.Revision {
		t.Fatal("null erased history", w.Code)
	}
}
