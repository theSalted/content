package httpapi

import (
	"content/cms"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPagesReadIsSeparateFromPostsAndPrivatePages(t *testing.T) {
	s, m, auth := setup(t)
	if e := WithPageURL("https://pages.example.invalid")(s); e != nil {
		t.Fatal(e)
	}
	makeRecord := func(kind, slug, status string) cms.Post {
		p, e := cms.NewPost("publication-a", cms.CreatePost{Title: slug, Slug: slug, Status: status, HTML: "<p>Source</p>"})
		if e != nil {
			t.Fatal(e)
		}
		p.Kind = kind
		p, e = m.Create(t.Context(), p)
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	post := makeRecord("post", "post", "published")
	page := makeRecord("page", "page", "published")
	private := makeRecord("page", "draft-page", "draft")
	for _, resource := range []string{"posts", "pages"} {
		w := request(s, "GET", "/ghost/api/content/"+resource+"/?key="+fixtureContentKey+"&fields=id,title", "", "")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var result map[string]json.RawMessage
		json.Unmarshal(w.Body.Bytes(), &result)
		var rows []map[string]any
		json.Unmarshal(result[resource], &rows)
		want := post.ID
		if resource == "pages" {
			want = page.ID
		}
		if len(rows) != 1 || rows[0]["id"] != want {
			t.Fatal("resource mixing", w.Body.String())
		}
	}
	for _, path := range []string{"/ghost/api/content/pages/" + post.ID + "/", "/ghost/api/content/posts/" + page.ID + "/", "/ghost/api/content/pages/" + private.ID + "/"} {
		w := request(s, "GET", path+"?key="+fixtureContentKey, "", "")
		if w.Code != 404 {
			t.Fatal("wrong resource/private page exposed", w.Code)
		}
	}
	w := request(s, "GET", "/ghost/api/admin/pages/"+private.ID+"/", "", auth)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = request(s, "POST", "/ghost/api/admin/pages/?source=html", `{"pages":[]}`, auth)
	if w.Code != 405 {
		t.Fatal("unimplemented page create accepted")
	}
	r := httptest.NewRequest("GET", "/ghost/api/content/pages/slug/page/?key="+fixtureContentKey, nil)
	r.Header.Set("Accept-Version", "v5.0")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "https://pages.example.invalid/page/") {
		t.Fatal(w.Body.String())
	}
}
