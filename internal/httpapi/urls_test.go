package httpapi

import (
	"content/cms"
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

type urlFixture struct {
	cms.Store
	result      cms.URLResolution
	err         error
	publication string
}

func (f *urlFixture) ResolveURL(_ context.Context, pub, kind, slug string) (cms.URLResolution, error) {
	f.publication = pub
	return f.result, f.err
}
func TestURLResolverIsAuthenticatedPublicJSONNotRedirect(t *testing.T) {
	s, m, _ := setup(t)
	f := &urlFixture{Store: m, result: cms.URLResolution{PostID: "0123456789abcdef01234567", Slug: "new-name", Kind: "post", Alias: true}}
	s.store = f
	call := func(method, suffix string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(method, "/content/api/v1/resolve/"+suffix, nil))
		return w
	}
	if w := call("GET", "?type=post&slug=old"); w.Code != 401 {
		t.Fatal(w.Code)
	}
	suffix := "?type=post&slug=old&key=" + s.contentKey
	w := call("GET", suffix)
	if w.Code != 200 || w.Header().Get("Location") != "" || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), `"redirect_status":301`) || f.publication != s.publication {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call("HEAD", suffix); w.Code != 200 || w.Body.Len() != 0 {
		t.Fatal("HEAD")
	}
	for _, query := range []string{suffix + "&slug=other", suffix + "&unknown=x", "?type=post&slug=%2F%2Fevil&key=" + s.contentKey} {
		if w = call("GET", query); w.Code != 400 {
			t.Fatal("invalid query", w.Code)
		}
	}
	f.err = cms.ErrNotFound
	if w = call("GET", suffix); w.Code != 404 || strings.Contains(w.Body.String(), f.result.PostID) {
		t.Fatal("private result")
	}
}
func TestScheduleWireUsesFutureDateOnlyWithExplicitScheduledState(t *testing.T) {
	s, m, auth := setup(t)
	s.scheduling = true
	w := request(s, "POST", "/ghost/api/admin/posts/?source=html", `{"posts":[{"title":"Schedule fixture","html":"<p>Private</p>"}]}`, auth)
	if w.Code != 201 {
		t.Fatal(w.Code)
	}
	p := m.posts[0]
	path := "/ghost/api/admin/posts/" + p.ID + "/"
	body := `{"posts":[{"updated_at":"` + timestamp(p.UpdatedAt) + `","status":"scheduled","published_at":"2999-01-01T00:00:00Z"}]}`
	w = request(s, "PUT", path, body, auth)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"scheduled"`) || !strings.Contains(w.Body.String(), `"published_at":"2999-01-01T00:00:00.000Z"`) || m.posts[0].PublishedAt != nil {
		t.Fatal(w.Code, w.Body.String())
	}
}
