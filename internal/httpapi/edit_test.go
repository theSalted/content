package httpapi

import (
	"fmt"
	"strings"
	"testing"
)

func TestUpdateHTTPValidationAndConflictShape(t *testing.T) {
	s, m, auth := setup(t)
	w := request(s, "POST", "/ghost/api/admin/posts/?source=html", `{"posts":[{"title":"Before","html":"<p>Before</p>"}]}`, auth)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	p := m.posts[0]
	path := "/ghost/api/admin/posts/" + p.ID + "/"
	token := timestamp(p.UpdatedAt)
	for _, invalidAuth := range []string{"", "Ghost invalid", "Bearer " + strings.TrimPrefix(auth, "Ghost ")} {
		denied := request(s, "PUT", path, fmt.Sprintf(`{"posts":[{"updated_at":%q,"title":"Unauthorized"}]}`, token), invalidAuth)
		if denied.Code != 401 || m.posts[0].Title != p.Title || m.posts[0].Revision != 1 {
			t.Fatal("unauthenticated edit changed state", denied.Code)
		}
	}
	w = request(s, "PUT", path, fmt.Sprintf(`{"posts":[{"updated_at":%q,"title":"After"}]}`, token), auth)
	if w.Code != 200 || m.posts[0].Title != "After" || m.posts[0].HTML != p.HTML {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(s, "PUT", path, fmt.Sprintf(`{"posts":[{"updated_at":%q,"title":"Lost edit"}]}`, token), auth)
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"errorType":"UpdateCollisionError"`) || !strings.Contains(w.Body.String(), `"code":"UPDATE_COLLISION"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	current := timestamp(m.posts[0].UpdatedAt)
	cases := []struct {
		query, fields string
		status        int
	}{
		{"", `"title":"missing token"`, 422},
		{"", `"updated_at":"invalid"`, 422},
		{"", `"updated_at":"2026-01-01T00:00:00.000000001Z"`, 422},
		{"", fmt.Sprintf(`"updated_at":%q,"title":null`, current), 400},
		{"", fmt.Sprintf(`"updated_at":%q,"html":"<p>Changed</p>"`, current), 400},
		{"?source=html", fmt.Sprintf(`"updated_at":%q,"html":"<script>x</script>"`, current), 422},
		{"", fmt.Sprintf(`"updated_at":%q,"status":"scheduled"`, current), 422},
		{"", fmt.Sprintf(`"updated_at":%q,"publication_id":"other"`, current), 400},
		{"", fmt.Sprintf(`"updated_at":%q,"lexical":"{}"`, current), 400},
		{"?save_revision=false", fmt.Sprintf(`"updated_at":%q`, current), 400},
	}
	for _, c := range cases {
		w = request(s, "PUT", path+c.query, `{"posts":[{`+c.fields+`}]}`, auth)
		if w.Code != c.status {
			t.Errorf("%s: %d %s", c.fields, w.Code, w.Body.String())
		}
	}
	if m.posts[0].Title != "After" || timestamp(m.posts[0].UpdatedAt) != current || m.posts[0].Revision != 2 {
		t.Fatal("invalid edit changed state")
	}
	for _, route := range []string{"/ghost/api/admin/posts/", "/ghost/api/admin/posts/slug/stable/", "/ghost/api/content/posts/" + p.ID + "/?key=" + fixtureContentKey} {
		w = request(s, "PUT", route, `{}`, auth)
		if w.Code != 405 {
			t.Fatal("wrong PUT route accepted", route, w.Code)
		}
	}
}
