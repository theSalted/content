package review

import (
	"content/cms"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type listFixture struct {
	*fixtureStore
	posts   []cms.Post
	queries []cms.Query
	err     error
}

func (f *listFixture) Browse(_ context.Context, q cms.Query) ([]cms.Post, int64, error) {
	f.queries = append(f.queries, q)
	if f.err != nil {
		return nil, 0, f.err
	}
	var scoped []cms.Post
	for _, post := range f.posts {
		if post.Publication == q.Publication && cms.ContentKind(post.Kind) == q.Kind && (!q.PublicOnly || post.Status == "published") {
			scoped = append(scoped, post)
		}
	}
	start := (q.Page - 1) * q.Limit
	if start >= len(scoped) {
		return nil, int64(len(scoped)), nil
	}
	end := start + q.Limit
	if end > len(scoped) {
		end = len(scoped)
	}
	return scoped[start:end], int64(len(scoped)), nil
}

func setupList(t *testing.T) (*Server, *listFixture) {
	t.Helper()
	s, f := setupReview(t)
	l := &listFixture{fixtureStore: f}
	for i := 0; i < 21; i++ {
		p := f.post
		p.ID = fmt.Sprintf("%024x", i+1)
		p.Title = fmt.Sprintf("Synthetic post %02d", i+1)
		if i%2 == 1 {
			p.Status = "published"
		}
		l.posts = append(l.posts, p)
	}
	l.posts[0].Title = `Draft <script>alert("title")</script>`
	foreign := f.post
	foreign.Publication = "another"
	foreign.Title = "Foreign publication title"
	page := f.post
	page.Kind = "page"
	page.Title = "Separate page title"
	l.posts = append(l.posts, foreign, page)
	s.store = l
	return s, l
}

func TestPostsListAuthenticationAndScope(t *testing.T) {
	s, f := setupList(t)
	if !Handles("/ghost/posts/") {
		t.Fatal("list route not handled")
	}
	for _, path := range []string{"/ghost/posts/", "/ghost/posts/?page=2"} {
		w := call(s, "GET", path, nil, nil, "")
		if w.Code != 303 || !strings.HasPrefix(w.Header().Get("Location"), "/ghost/login/?next=") || strings.Contains(w.Body.String(), "Synthetic post") {
			t.Fatal("anonymous list disclosure", w.Code)
		}
	}
	if len(f.queries) != 0 {
		t.Fatal("queried private store before authentication")
	}
	cookie, _ := login(t, s)
	w := call(s, "GET", "/ghost/posts/", nil, cookie, "")
	body := w.Body.String()
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("list response", w.Code)
	}
	for _, want := range []string{"<h1>Posts</h1>", "Draft &lt;script&gt;", ">Draft<", ">Published<", ">Updated<", "01 Jan 2026", "Page 1 of 2", `href="/ghost/posts/?page=2"`, `href="/ghost/posts/000000000000000000000001/"`} {
		if !strings.Contains(body, want) {
			t.Fatal("missing list UI", want)
		}
	}
	for _, absent := range []string{"<script>alert", f.post.HTML, "Foreign publication title", "Separate page title", "Synthetic post 21", "/ghost/moderation/"} {
		if strings.Contains(body, absent) {
			t.Fatal("unexpected disclosure", absent)
		}
	}
	q := f.queries[0]
	if q.Publication != "one" || q.Kind != "post" || q.PublicOnly || q.Page != 1 || q.Limit != 20 {
		t.Fatal("incorrect list scope", q)
	}
	w = call(s, "GET", "/ghost/posts/?page=2", nil, cookie, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Synthetic post 21") || !strings.Contains(w.Body.String(), `rel="prev"`) || strings.Contains(w.Body.String(), `rel="next"`) {
		t.Fatal("second page")
	}
	w = call(s, "GET", "/ghost/posts/?page=99", nil, cookie, "")
	if w.Code != 303 || w.Header().Get("Location") != "/ghost/posts/?page=2" {
		t.Fatal("out-of-range page")
	}
	if f.updates != 0 || f.gets != 0 {
		t.Fatal("listing mutated or fetched article")
	}
}

func TestPostsListRejectsInvalidRequestsAndRetainsPrivacy(t *testing.T) {
	s, f := setupList(t)
	cookie, _ := login(t, s)
	for _, query := range []string{"page=0", "page=-1", "page=1000001", "page=abc", "page=1&page=2", "page=", "filter=all", "page=%ZZ"} {
		if w := call(s, "GET", "/ghost/posts/?"+query, nil, cookie, ""); w.Code != 400 {
			t.Fatal(query, w.Code)
		}
	}
	if len(f.queries) != 0 {
		t.Fatal("invalid request queried store")
	}
	for _, c := range []*http.Cookie{nil, cookie} {
		if w := call(s, "POST", "/ghost/posts/", url.Values{"action": {"publish"}}, c, s.config.Origin); w.Code != 405 {
			t.Fatal("list accepted mutation", w.Code)
		}
	}
	f.err = errors.New("private database details")
	w := call(s, "GET", "/ghost/posts/", nil, cookie, "")
	if w.Code != 503 || !strings.Contains(w.Body.String(), "Could not load posts") || strings.Contains(w.Body.String(), "private database details") {
		t.Fatal("list failure handling")
	}
	f.err = nil
	f.posts = nil
	w = call(s, "GET", "/ghost/posts/", nil, cookie, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "No posts found.") || strings.Contains(w.Body.String(), "Pagination") {
		t.Fatal("empty list")
	}
	s.now = func() time.Time { return time.Now().Add(9 * time.Hour) }
	count := len(f.queries)
	if w = call(s, "GET", "/ghost/posts/", nil, cookie, ""); w.Code != 303 || len(f.queries) != count {
		t.Fatal("expired session queried list")
	}
}

func TestLoginReturnsToPostsOrExactEditor(t *testing.T) {
	s, f := setupList(t)
	for _, tc := range []struct{ next, want string }{
		{"", "/ghost/posts/"},
		{"/ghost/posts/?page=2", "/ghost/posts/?page=2"},
		{"/ghost/posts/" + f.post.ID + "/", "/ghost/posts/" + f.post.ID + "/"},
		{"/ghost/pages/" + f.post.ID + "/", "/ghost/pages/" + f.post.ID + "/"},
		{"//evil.invalid/ghost/posts/", "/ghost/posts/"},
		{"/ghost/posts/?next=https://evil.invalid", "/ghost/posts/"},
		{"/ghost/moderation/", "/ghost/posts/"},
	} {
		if got := nextPath(tc.next); got != tc.want {
			t.Fatal(tc.next, got)
		}
	}
	for _, next := range []string{"", "/ghost/posts/?page=2", "/ghost/posts/" + f.post.ID + "/"} {
		path := "/ghost/login/?next=" + url.QueryEscape(next)
		pre := call(s, "GET", path, nil, nil, "")
		out := call(s, "POST", path, url.Values{"csrf": {csrf(t, pre)}, "username": {"synthetic"}, "password": {syntheticPassword}}, pre.Result().Cookies()[0], s.config.Origin)
		if out.Code != 303 || out.Header().Get("Location") != nextPath(next) {
			t.Fatal("post-login destination", out.Code, out.Header().Get("Location"))
		}
		already := call(s, "GET", path, nil, out.Result().Cookies()[0], "")
		if already.Code != 303 || already.Header().Get("Location") != nextPath(next) {
			t.Fatal("authenticated login destination")
		}
	}
	landing := call(s, "GET", "/ghost/", nil, nil, "").Body.String()
	if !strings.Contains(landing, `href="/ghost/posts/"`) || !strings.Contains(landing, "editor\\/post") || !strings.Contains(landing, " : '/ghost/posts/'") {
		t.Fatal("default landing or iA fragment handoff lost")
	}
}

func TestPublishedEditorHasListNavigationAndSavesPublishedRevision(t *testing.T) {
	s, f := setupReview(t)
	f.post.Status = "published"
	cookie, csrfToken := login(t, s)
	path := "/ghost/posts/" + f.post.ID + "/"
	w := call(s, "GET", path, nil, cookie, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `href="/ghost/posts/">Back to Content</a>`) || !strings.Contains(w.Body.String(), ">Update</button>") || strings.Contains(w.Body.String(), `value="publish"`) {
		t.Fatal("published editor controls")
	}
	values := editValues(f, csrfToken)
	w = call(s, "POST", path, values, cookie, s.config.Origin)
	if w.Code != 200 || f.post.Status != "published" || f.post.Title != "Edited title" || f.post.Revision != 2 {
		t.Fatal("published save not immediate")
	}
	w = call(s, "POST", path, values, cookie, s.config.Origin)
	if w.Code != 409 || f.post.Revision != 2 {
		t.Fatal("stale published save overwrote content")
	}
}
