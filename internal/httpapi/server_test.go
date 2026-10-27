package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"content/cms"
	"github.com/golang-jwt/jwt/v5"
)

// These are synthetic test values, not application credentials.
const fixtureKey = "0123456789abcdef01234567:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
const fixtureContentKey = "synthetic-content-key-for-tests"

type memoryStore struct {
	posts []cms.Post
	fail  bool
	last  cms.Query
}

func (m *memoryStore) Ping(context.Context) error {
	if m.fail {
		return errors.New("database-password=must-not-leak")
	}
	return nil
}
func (m *memoryStore) Create(_ context.Context, p cms.Post) (cms.Post, error) {
	if m.fail {
		return p, errors.New("database-password=must-not-leak")
	}
	for _, v := range m.posts {
		if v.Publication == p.Publication && v.Slug == p.Slug {
			return p, cms.ErrConflict
		}
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p.CreatedAt = now
	p.UpdatedAt = now
	if p.Status == "published" {
		p.PublishedAt = &now
	}
	m.posts = append(m.posts, p)
	return p, nil
}
func (m *memoryStore) Get(_ context.Context, q cms.Query) (cms.Post, error) {
	m.last = q
	for _, p := range m.posts {
		if cms.ContentKind(p.Kind) == cms.ContentKind(q.Kind) && p.Publication == q.Publication && (q.ID == p.ID || q.Slug == p.Slug) && (!q.PublicOnly || (p.Status == "published" && p.Visibility == "public")) {
			return p, nil
		}
	}
	return cms.Post{}, cms.ErrNotFound
}
func (m *memoryStore) Browse(_ context.Context, q cms.Query) ([]cms.Post, int64, error) {
	m.last = q
	out := []cms.Post{}
	for _, p := range m.posts {
		if cms.ContentKind(p.Kind) == cms.ContentKind(q.Kind) && p.Publication == q.Publication && (!q.PublicOnly || (p.Status == "published" && p.Visibility == "public")) {
			out = append(out, p)
		}
	}
	return out, int64(len(out)), nil
}
func signed(t *testing.T, k AdminKey, method jwt.SigningMethod, claims jwt.RegisteredClaims, headerID string) string {
	t.Helper()
	token := jwt.NewWithClaims(method, claims)
	token.Header["kid"] = headerID
	v, err := token.SignedString(k.secret)
	if err != nil {
		t.Fatal(err)
	}
	return "Ghost " + v
}
func currentClaims() jwt.RegisteredClaims {
	now := time.Now()
	return jwt.RegisteredClaims{IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)), Audience: jwt.ClaimStrings{"/admin/"}}
}
func setup(t *testing.T) (*Server, *memoryStore, string) {
	t.Helper()
	k, err := ParseAdminKey(fixtureKey)
	if err != nil {
		t.Fatal(err)
	}
	m := &memoryStore{}
	s, err := New(m, "publication-a", "https://example.invalid/blog", k, fixtureContentKey)
	if err != nil {
		t.Fatal(err)
	}
	return s, m, signed(t, k, jwt.SigningMethodHS256, currentClaims(), k.id)
}
func request(s *Server, method, path, body, auth string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", auth)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept-Version", "v6.0")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func TestCreateReadAndPublicProjection(t *testing.T) {
	s, m, auth := setup(t)
	w := request(s, "POST", "/ghost/api/admin/posts/?source=html", `{"posts":[{"title":"Draft", "slug":"draft","html":"<p>Hello</p>"}]}`, auth)
	if w.Code != 201 {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	id := m.posts[0].ID
	for _, path := range []string{"/ghost/api/admin/posts/" + id + "/", "/ghost/api/admin/posts/slug/draft/"} {
		if w = request(s, "GET", path, "", auth); w.Code != 200 {
			t.Fatalf("read %d %s", w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/ghost/api/content/posts/" + id + "/", "/ghost/api/content/posts/slug/draft/"} {
		w = request(s, "GET", path+"?key="+fixtureContentKey, "", "")
		if w.Code != 404 {
			t.Fatalf("draft leaked: %s", w.Body.String())
		}
	}
	w = request(s, "POST", "/ghost/api/admin/posts/?source=html", `{"posts":[{"title":"Public", "slug":"public","html":"<p>Visible</p>","status":"published"}]}`, auth)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	w = request(s, "GET", "/ghost/api/content/posts/?key="+fixtureContentKey+"&limit=all", "", "")
	if w.Code != 200 || m.last.Limit != 100 || !m.last.PublicOnly || m.last.Publication != "publication-a" {
		t.Fatalf("bad browse: %s %+v", w.Body.String(), m.last)
	}
	var result struct {
		Posts []map[string]any `json:"posts"`
		Meta  struct {
			Pagination struct {
				Total int `json:"total"`
			} `json:"pagination"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Posts) != 1 || result.Meta.Pagination.Total != 1 {
		t.Fatal(w.Body.String())
	}
	for _, private := range []string{"SourceHTML", "source_html", "publication_id", "revision", "status", "lexical"} {
		if _, ok := result.Posts[0][private]; ok {
			t.Fatal("public field leaked", private)
		}
	}
	if result.Posts[0]["url"] != "https://example.invalid/blog/public/" {
		t.Fatal("site base path lost")
	}
	// A different publication cannot be retrieved by reusing an ID.
	hidden, _ := cms.NewPost("publication-b", cms.CreatePost{Title: "Other", Status: "published"})
	m.Create(context.Background(), hidden)
	w = request(s, "GET", "/ghost/api/admin/posts/"+hidden.ID+"/", "", auth)
	if w.Code != 404 {
		t.Fatal("publication escaped")
	}
}
func TestRejectsUnsupportedInputWithoutWriting(t *testing.T) {
	s, m, auth := setup(t)
	cases := []struct {
		path, body string
		status     int
	}{
		{"?source=html", `{"posts":[{"title":"x","lexical":"{}"}]}`, 400},
		{"?source=html", `{"posts":[{"title":"x","html":"<script>x</script>"}]}`, 422},
		{"?source=html", `{"posts":[{"title":"x","status":"scheduled"}]}`, 422},
		{"?source=html", `{"posts":[]}`, 422},
		{"?source=html", `{"posts":[{"title":"x"}]} {}`, 400},
		{"?source=html&source=html", `{"posts":[{"title":"x"}]}`, 400},
		{"", `{"posts":[{"title":"x"}]}`, 400},
	}
	for _, c := range cases {
		w := request(s, "POST", "/ghost/api/admin/posts/"+c.path, c.body, auth)
		if w.Code != c.status {
			t.Errorf("%s: %d %s", c.path, w.Code, w.Body.String())
		}
	}
	if len(m.posts) != 0 {
		t.Fatal("rejected input wrote records")
	}
	for _, query := range []string{"filter=status:draft", "include=roles", "formats=lexical", "limit=-1", "page=0", "page=9999999999999"} {
		w := request(s, "GET", "/ghost/api/content/posts/?key="+fixtureContentKey+"&"+query, "", "")
		if w.Code != 400 {
			t.Errorf("query accepted %s: %s", query, w.Body.String())
		}
	}
	w := request(s, "POST", "/ghost/api/admin/posts/?source=html", `{"posts":[{"title":"`+strings.Repeat("a", 2<<20)+`"}]}`, auth)
	if w.Code != 413 {
		t.Fatalf("oversize: %d", w.Code)
	}
}
func TestAuthFailureMatrix(t *testing.T) {
	s, m, good := setup(t)
	k := s.admin
	now := time.Now()
	cases := []string{"", strings.Replace(good, "Ghost ", "Bearer ", 1), good + "x", signed(t, k, jwt.SigningMethodHS384, currentClaims(), k.id), signed(t, k, jwt.SigningMethodHS256, currentClaims(), "wrong")}
	for _, change := range []func(*jwt.RegisteredClaims){func(c *jwt.RegisteredClaims) { c.Audience = jwt.ClaimStrings{"/wrong/"} }, func(c *jwt.RegisteredClaims) { c.IssuedAt = nil }, func(c *jwt.RegisteredClaims) { c.ExpiresAt = jwt.NewNumericDate(now.Add(-time.Second)) }, func(c *jwt.RegisteredClaims) { c.ExpiresAt = jwt.NewNumericDate(now.Add(10 * time.Minute)) }, func(c *jwt.RegisteredClaims) { c.IssuedAt = jwt.NewNumericDate(now.Add(time.Minute)) }} {
		c := currentClaims()
		change(&c)
		cases = append(cases, signed(t, k, jwt.SigningMethodHS256, c, k.id))
	}
	for i, auth := range cases {
		w := request(s, "POST", "/ghost/api/admin/posts/?source=html", `{"posts":[{"title":"x"}]}`, auth)
		if w.Code != 401 {
			t.Fatalf("auth case %d: %d", i, w.Code)
		}
	}
	if len(m.posts) != 0 {
		t.Fatal("unauthorized write")
	}
	if w := request(s, "GET", "/ghost/api/content/posts/?key=wrong", "", ""); w.Code != 401 {
		t.Fatal("public auth missing")
	}
}
func TestProtocolAndRedaction(t *testing.T) {
	s, m, auth := setup(t)
	for _, c := range []struct {
		method, path string
		code         int
	}{{"PATCH", "/ghost/api/admin/posts/id/", 405}, {"GET", "/ghost/api/admin/users/", 404}, {"GET", "/ghost/api/content/posts", 404}} {
		w := request(s, c.method, c.path, "", auth)
		if w.Code != c.code {
			t.Fatal(c, w.Code)
		}
	}
	r := httptest.NewRequest("GET", "/ghost/api/admin/posts/", nil)
	r.Header.Set("Accept-Version", "v99.0")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("unsupported version accepted")
	}
	m.fail = true
	w = request(s, "POST", "/ghost/api/admin/posts/?source=html", `{"posts":[{"title":"x"}]}`, auth)
	if w.Code != 500 || strings.Contains(w.Body.String(), "password") {
		t.Fatal("database error leaked", w.Body.String())
	}
	if w = request(s, "GET", "/readyz", "", ""); w.Code != 503 {
		t.Fatal("readiness failed")
	}
	if w = request(s, "GET", "/healthz", "", ""); w.Code != 200 {
		t.Fatal("health failed")
	}
}
func Example_profile() {
	fmt.Println("posts-html-v1: synthetic local tests, no measured Ghost/client parity") // Output: posts-html-v1: synthetic local tests, no measured Ghost/client parity
}

func (m *memoryStore) Update(_ context.Context, publication, id string, in cms.UpdatePost) (cms.Post, error) {
	for i, p := range m.posts {
		if p.Publication != publication || p.ID != id {
			continue
		}
		next, err := cms.ApplyUpdate(p, in)
		if err != nil {
			return cms.Post{}, err
		}
		next.Revision++
		next.UpdatedAt = p.UpdatedAt.Add(time.Millisecond)
		if next.Status == "published" && next.PublishedAt == nil {
			v := next.UpdatedAt
			next.PublishedAt = &v
		}
		m.posts[i] = next
		return next, nil
	}
	return cms.Post{}, cms.ErrNotFound
}
