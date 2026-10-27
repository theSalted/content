package review

import (
	"content/cms"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

const syntheticPassword = "synthetic-review-password-only"

var hashOnce sync.Once
var fixtureHash string

func testHash(t *testing.T) string {
	t.Helper()
	hashOnce.Do(func() {
		var err error
		fixtureHash, err = HashPassword(syntheticPassword)
		if err != nil {
			t.Fatal(err)
		}
	})
	return fixtureHash
}

type fixtureStore struct {
	post          cms.Post
	gets, updates int
	fail          error
}

func (f *fixtureStore) Get(_ context.Context, q cms.Query) (cms.Post, error) {
	f.gets++
	if q.Publication != f.post.Publication || q.ID != f.post.ID || cms.ContentKind(q.Kind) != cms.ContentKind(f.post.Kind) {
		return cms.Post{}, cms.ErrNotFound
	}
	return f.post, nil
}
func (f *fixtureStore) Update(_ context.Context, pub, id string, u cms.UpdatePost) (cms.Post, error) {
	f.updates++
	if f.fail != nil {
		return cms.Post{}, f.fail
	}
	if pub != f.post.Publication || id != f.post.ID {
		return cms.Post{}, cms.ErrNotFound
	}
	p, e := cms.ApplyUpdate(f.post, u)
	if e != nil {
		return cms.Post{}, e
	}
	p.Revision++
	p.UpdatedAt = p.UpdatedAt.Add(time.Millisecond)
	f.post = p
	return p, nil
}
func (*fixtureStore) Create(context.Context, cms.Post) (cms.Post, error) { panic("unexpected Create") }
func (*fixtureStore) Browse(context.Context, cms.Query) ([]cms.Post, int64, error) {
	panic("unexpected Browse")
}
func (*fixtureStore) Ping(context.Context) error { return nil }
func setupReview(t *testing.T) (*Server, *fixtureStore) {
	t.Helper()
	p, e := cms.NewPost("one", cms.CreatePost{Title: "Private synthetic draft", HTML: "<p>Private body</p>"})
	if e != nil {
		t.Fatal(e)
	}
	p.UpdatedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f := &fixtureStore{post: p}
	s, e := New(f, Config{Origin: "https://review.example.invalid", Publication: "one", Username: "synthetic", PasswordHash: testHash(t)})
	if e != nil {
		t.Fatal(e)
	}
	return s, f
}
func call(s *Server, method, path string, values url.Values, cookie *http.Cookie, origin string) *httptest.ResponseRecorder {
	body := ""
	if values != nil {
		body = values.Encode()
	}
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("Origin", origin)
	if values != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

var csrfRE = regexp.MustCompile(`name="csrf" value="([0-9a-f]{64})"`)

func csrf(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	m := csrfRE.FindStringSubmatch(w.Body.String())
	if m == nil {
		t.Fatal("missing form token", w.Code)
	}
	return m[1]
}
func login(t *testing.T, s *Server) (*http.Cookie, string) {
	t.Helper()
	pre := call(s, "GET", "/ghost/login/", nil, nil, "")
	old := pre.Result().Cookies()[0]
	out := call(s, "POST", "/ghost/login/", url.Values{"csrf": {csrf(t, pre)}, "username": {"synthetic"}, "password": {syntheticPassword}}, old, s.config.Origin)
	if out.Code != 303 {
		t.Fatal(out.Code, out.Body.String())
	}
	c := out.Result().Cookies()[0]
	if c.Value == old.Value || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/ghost/" {
		t.Fatal("session did not rotate with safe cookie")
	}
	if _, _, ok := s.current(&http.Request{Header: http.Header{"Cookie": {old.String()}}}); ok {
		t.Fatal("preauth session survived")
	}
	_, session, ok := s.current(&http.Request{Header: http.Header{"Cookie": {c.String()}}})
	if !ok {
		t.Fatal("missing authenticated session")
	}
	return c, session.csrf
}
func editValues(f *fixtureStore, token string) url.Values {
	return url.Values{"csrf": {token}, "title": {"Edited title"}, "html": {"<p>Edited body</p>"}, "updated_at": {f.post.UpdatedAt.Format("2006-01-02T15:04:05.000Z")}, "action": {"save"}}
}

func TestReviewAuthenticationBoundary(t *testing.T) {
	s, f := setupReview(t)
	path := "/ghost/posts/" + f.post.ID + "/"
	for _, method := range []string{"GET", "POST"} {
		w := call(s, method, path, nil, nil, "")
		if method == "GET" && w.Code != 303 || method == "POST" && w.Code != 401 {
			t.Fatal(w.Code)
		}
		if strings.Contains(w.Body.String(), f.post.Title) || f.gets != 0 {
			t.Fatal("unauthenticated draft read")
		}
	}
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("Authorization", "Ghost integration-key-is-not-a-session")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 303 || f.gets != 0 {
		t.Fatal("integration auth granted browser access")
	}
	cookie, _ := login(t, s)
	w = call(s, "GET", path, nil, cookie, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Private synthetic draft") || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "same-origin" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") || !strings.Contains(w.Header().Get("Content-Security-Policy"), "connect-src 'self'") {
		t.Fatal("review/hardening missing")
	}
	s.config.Publication = "other"
	if w = call(s, "GET", path, nil, cookie, ""); w.Code != 404 {
		t.Fatal("cross publication leaked")
	}
	s.config.Publication = "one"
	s.now = func() time.Time { return time.Now().Add(9 * time.Hour) }
	if w = call(s, "GET", path, nil, cookie, ""); w.Code != 303 {
		t.Fatal("expired session accepted")
	}
}

func TestReviewCSRFAndFormBoundaries(t *testing.T) {
	s, f := setupReview(t)
	c, token := login(t, s)
	path := "/ghost/posts/" + f.post.ID + "/"
	for _, tc := range []struct {
		name, origin, token string
		extra               bool
		want                int
	}{{"missing origin", "", token, false, 403}, {"null origin", "null", token, false, 403}, {"foreign origin", "https://evil.invalid", token, false, 403}, {"bad csrf", s.config.Origin, "wrong", false, 403}, {"unsupported", s.config.Origin, token, true, 400}} {
		t.Run(tc.name, func(t *testing.T) {
			v := editValues(f, tc.token)
			if tc.extra {
				v.Set("publication", "other")
			}
			w := call(s, "POST", path, v, c, tc.origin)
			if w.Code != tc.want || f.updates != 0 {
				t.Fatal(w.Code, f.updates)
			}
		})
	}
	v := editValues(f, token)
	v["title"] = []string{"one", "two"}
	if w := call(s, "POST", path, v, c, s.config.Origin); w.Code != 400 || f.updates != 0 {
		t.Fatal("duplicate field accepted")
	}
}

func TestReviewSavePublishConflictAndFailedSave(t *testing.T) {
	s, f := setupReview(t)
	c, token := login(t, s)
	path := "/ghost/posts/" + f.post.ID + "/"
	v := editValues(f, token)
	w := call(s, "POST", path, v, c, s.config.Origin)
	if w.Code != 200 || f.post.Status != "draft" || f.post.Revision != 2 {
		t.Fatal("save published or failed")
	}
	stale := v.Get("updated_at")
	v.Set("title", "Unsaved competing edit")
	w = call(s, "POST", path, v, c, s.config.Origin)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "Unsaved competing edit") || !strings.Contains(w.Body.String(), `value="`+stale+`"`) || f.post.Title != "Edited title" {
		t.Fatal("stale edit overwritten or lost")
	}
	v = editValues(f, token)
	v.Set("html", `<script>alert('unsafe')</script><p>Keep my text</p>`)
	w = call(s, "POST", path, v, c, s.config.Origin)
	if w.Code != 422 || strings.Contains(w.Body.String(), `<script>alert('unsafe')`) || !strings.Contains(w.Body.String(), "&lt;script&gt;") {
		t.Fatal("unsafe HTML executed or discarded")
	}
	v = editValues(f, token)
	f.fail = errors.New("private-db-password")
	w = call(s, "POST", path, v, c, s.config.Origin)
	if w.Code != 503 || strings.Contains(w.Body.String(), "private-db-password") || !strings.Contains(w.Body.String(), "Edited body") {
		t.Fatal("failure leaked or lost draft")
	}
	f.fail = nil
	v.Set("action", "publish")
	w = call(s, "POST", path, v, c, s.config.Origin)
	if w.Code != 200 || f.post.Status != "published" || !strings.Contains(w.Body.String(), "Published.") {
		t.Fatal("explicit publish failed")
	}
	v = editValues(f, token)
	w = call(s, "POST", path, v, c, s.config.Origin)
	if w.Code != 200 || f.post.Status != "published" {
		t.Fatal("save implicitly withdrew publication")
	}
	w = call(s, "POST", "/ghost/logout/", url.Values{"csrf": {token}}, c, s.config.Origin)
	if w.Code != 303 {
		t.Fatal(w.Code)
	}
	if w = call(s, "GET", path, nil, c, ""); w.Code != 303 {
		t.Fatal("logout session survived")
	}
}

func TestReviewLoginThrottleAndRedirect(t *testing.T) {
	s, _ := setupReview(t)
	w := call(s, "GET", "/ghost/login/?next=//evil.invalid", nil, nil, "")
	c := w.Result().Cookies()[0]
	token := csrf(t, w)
	if strings.Contains(w.Body.String(), "evil.invalid") {
		t.Fatal("open redirect")
	}
	for i := 0; i < 6; i++ {
		w = call(s, "POST", "/ghost/login/", url.Values{"csrf": {token}, "username": {"not-an-account"}, "password": {"wrong"}}, c, s.config.Origin)
		want := 401
		if i == 5 {
			want = 429
		}
		if w.Code != want {
			t.Fatal(i, w.Code)
		}
	}
	w = call(s, "GET", "/ghost/", nil, nil, "")
	if !strings.Contains(w.Body.String(), "editor\\/post") || strings.Contains(w.Body.String(), "Private body") {
		t.Fatal("handoff route or confidentiality")
	}
}

func TestReviewPasswordAndConfiguration(t *testing.T) {
	hash := testHash(t)
	d, e := parseDigest(hash)
	if e != nil || !d.matches(syntheticPassword) || d.matches("wrong") {
		t.Fatal("password verification")
	}
	for _, v := range []string{"plain-password", strings.Replace(hash, "600000", "1", 1), strings.Replace(hash, "600000", "2000001", 1)} {
		if _, e := parseDigest(v); e == nil {
			t.Fatal("unsafe digest accepted")
		}
	}
	for _, p := range []string{"short", strings.Repeat("x", 1025), "synthetic\x00password"} {
		if _, e := HashPassword(p); e == nil {
			t.Fatal("invalid password accepted")
		}
	}
	s, f := setupReview(t)
	for _, origin := range []string{"http://public.example", "https://example.invalid/path", "https://user:pass@example.invalid", "https://example.invalid/?secret=x"} {
		c := s.config
		c.Origin = origin
		if _, e := New(f, c); e == nil {
			t.Fatal("unsafe origin", origin)
		}
	}
	c := s.config
	c.Origin = "http://127.0.0.1:8080"
	local, e := New(f, c)
	if e != nil || local.secure {
		t.Fatal("local testing disabled")
	}
}

func TestEditorPageAndSourceFidelity(t *testing.T) {
	s, f := setupReview(t)
	c, token := login(t, s)
	path := "/ghost/posts/" + f.post.ID + "/"
	initial := call(s, "GET", path, nil, c, "").Body.String()
	for _, removed := range []string{"Content review", "Review your post.", "<iframe", "Preview updates", "scripts and interactive", "This draft is private."} {
		if strings.Contains(initial, removed) {
			t.Fatalf("obsolete editor copy: %s", removed)
		}
	}
	for _, needed := range []string{`data-mode="rich"`, `data-mode="html"`, `data-mode="markdown"`, `id="rich-body"`, `id="markdown"`, `name="html"`, `src="` + editorPath + `" defer`} {
		if !strings.Contains(initial, needed) {
			t.Fatalf("missing editor element %s", needed)
		}
	}
	nonce := regexp.MustCompile(`script-src 'nonce-([0-9a-f]{64})'`).FindStringSubmatch(call(s, "GET", path, nil, c, "").Header().Get("Content-Security-Policy"))
	if nonce == nil || !regexp.MustCompile(`<script nonce="[0-9a-f]{64}" src="/ghost/assets/editor-[0-9a-f]{16}\.js" defer>`).MatchString(initial) {
		t.Fatal("editor script is not nonce-bound")
	}
	// Markdown and Rich text are client views; only the HTML source is posted.
	dialogs := initial[strings.Index(initial, `<dialog id="link-dialog">`) : strings.LastIndex(initial, "</dialog>")+len("</dialog>")]
	if !strings.Contains(initial, `<textarea id="markdown" aria-label="Body" hidden></textarea>`) || strings.Contains(dialogs, ` name="`) {
		t.Fatal("client-only editor fields would be submitted")
	}
	for _, removed := range []string{"Preview", "@import", "Shortcut", "Keyboard"} {
		if strings.Contains(initial, removed) {
			t.Fatalf("editor filler present: %s", removed)
		}
	}
	loginPage := call(s, "GET", "/ghost/login/", nil, nil, "").Body.String()
	if strings.Contains(loginPage, "separate operator") || strings.Contains(loginPage, "Content review") || strings.Contains(loginPage, "publishing API key") {
		t.Fatal("login caption remains")
	}
	body := `<h2>A heading</h2><p><a href="https://example.invalid/" rel="nofollow">A link</a> and <strong>bold</strong>.</p><ul><li>One</li><li>Two</li></ul><ol><li>Three</li></ol><p><img src="https://example.invalid/image.png" alt="Synthetic" /></p><table><thead><tr><th>Item</th><th>Value</th></tr></thead><tbody><tr><td>A</td><td>B</td></tr></tbody></table>`
	v := editValues(f, token)
	v.Set("html", body)
	w := call(s, "POST", path, v, c, s.config.Origin)
	if w.Code != 200 || f.post.SourceHTML != body || f.post.HTML != body {
		t.Fatalf("rich HTML source changed or rejected: %d", w.Code)
	}
	if w = call(s, "POST", path, v, c, s.config.Origin); w.Code != 409 || !strings.Contains(w.Body.String(), "&lt;table&gt;") {
		t.Fatal("table source lost on conflict")
	}
}
func TestPrivateEditorImageAuthentication(t *testing.T) {
	s, f := setupReview(t)
	path := "/ghost/images/0123456789abcdef01234567/image.png"
	if !Handles(path) {
		t.Fatal("private image route missing")
	}
	w := call(s, "GET", path, nil, nil, "")
	if w.Code != 401 || f.gets != 0 {
		t.Fatal("private image bypassed browser auth")
	}
	cookie, _ := login(t, s)
	w = call(s, "GET", path, nil, cookie, "")
	if w.Code != 404 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("unconfigured image service not closed")
	}
}

func TestEditorAssetRoute(t *testing.T) {
	s, f := setupReview(t)
	if !Handles(editorPath) || len(editorJS) < 1000 {
		t.Fatal("editor asset missing")
	}
	w := call(s, "GET", editorPath, nil, nil, "")
	if w.Code != 200 || w.Body.String() != string(editorJS) || w.Header().Get("Content-Type") != "text/javascript; charset=utf-8" || w.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") || f.gets != 0 {
		t.Fatal("editor asset not served safely", w.Code, w.Header())
	}
	if w = call(s, "HEAD", editorPath, nil, nil, ""); w.Code != 200 || w.Body.Len() != 0 {
		t.Fatal("HEAD asset", w.Code)
	}
	for _, path := range []string{"/ghost/assets/editor-0000000000000000.js", "/ghost/assets/", editorPath + "?v=1"} {
		if w = call(s, "GET", path, nil, nil, ""); w.Code != 404 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("unknown asset served", path, w.Code)
		}
	}
	if w = call(s, "POST", editorPath, url.Values{}, nil, s.config.Origin); w.Code != 405 {
		t.Fatal("asset accepted POST", w.Code)
	}
}

func TestPageReviewScopeAndSave(t *testing.T) {
	s, f := setupReview(t)
	f.post.Kind = "page"
	c, token := login(t, s)
	path := "/ghost/pages/" + f.post.ID + "/"
	if !Handles(path) {
		t.Fatal("page route not mounted by main handler")
	}
	if w := call(s, "GET", path, nil, c, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `action="`+path+`"`) {
		t.Fatal("page editor unavailable or incorrect form action", w.Code)
	}
	if w := call(s, "GET", "/ghost/posts/"+f.post.ID+"/", nil, c, ""); w.Code != 404 {
		t.Fatal("page exposed as post", w.Code)
	}
	v := editValues(f, token)
	if w := call(s, "POST", path, v, c, s.config.Origin); w.Code != 200 || f.post.Kind != "page" {
		t.Fatal("page save failed", w.Code)
	}
}

func TestExplicitReviewAliasStillRequiresCSRF(t *testing.T) {
	_, f := setupReview(t)
	config := Config{Origin: "https://new.example.invalid", OriginAliases: []string{"https://old.example.invalid/"}, Publication: "one", Username: "synthetic", PasswordHash: testHash(t)}
	s, e := New(f, config)
	if e != nil {
		t.Fatal(e)
	}
	c, token := login(t, s)
	path := "/ghost/posts/" + f.post.ID + "/"
	v := editValues(f, token)
	if w := call(s, "POST", path, v, c, "https://old.example.invalid"); w.Code != 200 {
		t.Fatal("trusted old origin", w.Code)
	}
	for _, origin := range []string{"https://old.example.invalid.evil", "https://foreign.invalid", "null", ""} {
		if w := call(s, "POST", path, v, c, origin); w.Code != 403 {
			t.Fatal("foreign origin accepted", w.Code)
		}
	}
	v.Set("csrf", "wrong")
	if w := call(s, "POST", path, v, c, "https://old.example.invalid"); w.Code != 403 {
		t.Fatal("alias bypassed CSRF")
	}
	config.OriginAliases = []string{"http://127.0.0.1:8080"}
	if _, e := New(f, config); e == nil {
		t.Fatal("HTTPS downgraded via alias")
	}
}

// Module-owned feedback APIs must not regain a CMS dashboard or login target.
func TestReviewRejectsRemovedModuleRoutes(t *testing.T) {
	s, _ := setupReview(t)
	cookie, _ := login(t, s)
	for _, path := range []string{"/ghost/moderation/", "/ghost/moderation/decision/", "/ghost/moderation/block/"} {
		if Handles(path) {
			t.Fatal("review claims module route", path)
		}
		for _, method := range []string{"GET", "POST"} {
			out := call(s, method, path, nil, cookie, s.config.Origin)
			if out.Code != 404 {
				t.Fatal("removed module route still available", method, path, out.Code)
			}
		}
		if nextPath(path) != "/ghost/posts/" {
			t.Fatal("removed login target")
		}
	}
	page := call(s, "GET", "/ghost/", nil, cookie, "")
	if page.Code != 200 || strings.Contains(page.Body.String(), "/ghost/moderation/") || strings.Contains(page.Body.String(), "Interactions") {
		t.Fatal("removed navigation remains")
	}
}
