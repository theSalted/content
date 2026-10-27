package postgres

import (
	"content/cms"
	"content/internal/httpapi"
	"content/internal/review"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestBrowserDraftReviewPostgres(t *testing.T) {
	f := newRoleFixture(t)
	store, err := Open(f.ctx, f.runtimeDSN, f.schema, "installation-a")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	key, err := httpapi.ParseAdminKey("0123456789abcdef01234567:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	author := cms.Author{ID: "0123456789abcdef01234568", Name: "Synthetic review author", Slug: "synthetic-author"}
	api, err := httpapi.New(store, "review-fixture", "https://public.example.invalid", key, "synthetic-content-key-for-tests", httpapi.WithDefaultAuthor(author))
	if err != nil {
		t.Fatal(err)
	}
	var ui *review.Server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if review.Handles(r.URL.Path) {
			ui.ServeHTTP(w, r)
		} else {
			api.ServeHTTP(w, r)
		}
	}))
	defer server.Close()
	hash, err := review.HashPassword("synthetic-review-password-only")
	if err != nil {
		t.Fatal(err)
	}
	ui, err = review.New(store, review.Config{Origin: server.URL, Publication: "review-fixture", Username: "synthetic", PasswordHash: hash})
	if err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Timeout: 5 * time.Second, Jar: jar, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	send := func(method, path, body, kind, auth string) (int, string, http.Header) {
		t.Helper()
		req, e := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Content-Type", kind)
		req.Header.Set("Origin", server.URL)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		res, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		data, e := io.ReadAll(res.Body)
		if e != nil {
			t.Fatal(e)
		}
		return res.StatusCode, string(data), res.Header
	}
	status, body, _ := send("POST", "/ghost/api/v2/admin/posts/?source=html", `{"posts":[{"title":"Synthetic iA draft","html":"<p>First body.</p>","status":"draft","tags":[]}]}`, "application/json", clientJWTForAudience(t, "/v2/admin/", time.Now().Unix()))
	if status != 201 {
		t.Fatal(status, body)
	}
	var result struct {
		Posts []struct {
			ID, UpdatedAt string
			Author        cms.Author `json:"primary_author"`
		}
	}
	// JSON uses snake_case for updated_at; fetch the canonical saved record for its wire token.
	if err = json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatal(err)
	}
	id := result.Posts[0].ID
	if result.Posts[0].Author != author {
		t.Fatal("iA author missing")
	}
	path := "/ghost/posts/" + id + "/"
	if code, body, _ := send("GET", path, "", "", ""); code != 303 || strings.Contains(body, "First body") {
		t.Fatal("unauthenticated draft leaked")
	}
	csrfRE := regexp.MustCompile(`name="csrf" value="([0-9a-f]{64})"`)
	getCSRF := func(body string) string {
		t.Helper()
		v := csrfRE.FindStringSubmatch(body)
		if v == nil {
			t.Fatal("missing CSRF token")
		}
		return v[1]
	}
	_, body, _ = send("GET", "/ghost/login/?next="+url.QueryEscape(path), "", "", "")
	credentials := url.Values{"csrf": {getCSRF(body)}, "username": {"synthetic"}, "password": {"synthetic-review-password-only"}}
	if code, _, headers := send("POST", "/ghost/login/?next="+url.QueryEscape(path), credentials.Encode(), "application/x-www-form-urlencoded", ""); code != 303 || headers.Get("Location") != path {
		t.Fatal("login failed", code)
	}
	status, body, _ = send("GET", path, "", "", "")
	if status != 200 || !strings.Contains(body, "First body") {
		t.Fatal("review failed", status)
	}
	token := getCSRF(body)
	p, err := store.Get(f.ctx, cms.Query{Publication: "review-fixture", ID: id})
	if err != nil {
		t.Fatal(err)
	}
	original := p.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z")
	values := url.Values{"csrf": {token}, "title": {"Saved from review"}, "html": {"<p>Edited in review.</p>"}, "updated_at": {original}, "action": {"save"}}
	if code, _, _ := send("POST", path, values.Encode(), "application/x-www-form-urlencoded", ""); code != 200 {
		t.Fatal("save failed", code)
	}
	p, err = store.Get(f.ctx, cms.Query{Publication: "review-fixture", ID: id})
	if err != nil || p.Status != "draft" || p.Revision != 2 || p.Author == nil || *p.Author != author {
		t.Fatal("save lost attribution or published", p, err)
	}
	if _, err = store.Get(f.ctx, cms.Query{Publication: "review-fixture", ID: id, PublicOnly: true}); err != cms.ErrNotFound {
		t.Fatal("draft publicly visible")
	}
	values.Set("title", "Competing unsaved title")
	if code, body, _ := send("POST", path, values.Encode(), "application/x-www-form-urlencoded", ""); code != 409 || !strings.Contains(body, "Competing unsaved title") {
		t.Fatal("stale form not preserved", code)
	}
	foreign, _ := cms.NewPost("other-publication", cms.CreatePost{Title: "Foreign private post", HTML: "<p>Foreign body</p>"})
	foreign, err = store.Create(f.ctx, foreign)
	if err != nil {
		t.Fatal(err)
	}
	if code, body, _ := send("GET", "/ghost/posts/"+foreign.ID+"/", "", "", ""); code != 404 || strings.Contains(body, "Foreign body") {
		t.Fatal("cross publication read")
	}
	if code, _, _ := send("POST", "/ghost/posts/"+foreign.ID+"/", values.Encode(), "application/x-www-form-urlencoded", ""); code != 404 {
		t.Fatal("cross publication write")
	}
	values.Set("updated_at", p.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z"))
	values.Set("title", "Published explicitly")
	values.Set("action", "publish")
	if code, body, _ := send("POST", path, values.Encode(), "application/x-www-form-urlencoded", ""); code != 200 || !strings.Contains(body, "Published.") {
		t.Fatal("publish failed", code)
	}
	status, body, _ = send("GET", "/ghost/api/content/posts/"+id+"/?key=synthetic-content-key-for-tests", "", "", "")
	if status != 200 || !strings.Contains(body, "Published explicitly") || !strings.Contains(body, "Edited in review") {
		t.Fatal("published Content API mismatch", status)
	}
	for table, want := range map[string]int{"posts": 1, "post_revisions": 3, "outbox_events": 1, "publishing_deliveries": 0} {
		var got int
		if err = f.admin.QueryRow(f.ctx, "SELECT count(*) FROM "+ident(f.schema)+"."+table+" WHERE publication_id='review-fixture'").Scan(&got); err != nil || got != want {
			t.Fatal(table, got, want, err)
		}
	}
	// Old records remain unattributed. Updating configured defaults never rewrites history.
	old, err := store.Get(f.ctx, cms.Query{Publication: "other-publication", ID: foreign.ID})
	if err != nil || old.Author != nil {
		t.Fatal("invented legacy author")
	}
	var historical cms.Author
	var raw []byte
	if err = f.admin.QueryRow(f.ctx, "SELECT metadata->'author' FROM "+ident(f.schema)+".post_revisions WHERE publication_id='review-fixture' AND post_id=$1 AND revision=1", id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &historical); err != nil || historical != author {
		t.Fatal("author history changed")
	}
}
