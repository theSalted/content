package review

import (
	"content/cms"
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

type creationFixture struct {
	*fixtureStore
	created int
}

func (f *creationFixture) Create(_ context.Context, p cms.Post) (cms.Post, error) {
	f.created++
	p.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	p.CreatedAt = p.UpdatedAt
	f.post = p
	return p, nil
}
func inputValue(t *testing.T, body, name string) string {
	t.Helper()
	m := regexp.MustCompile(`name="` + name + `" value="([^"]+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("missing", name)
	}
	return m[1]
}

func TestCreateDraftRequiresIntentAndRetriesWithoutDuplicate(t *testing.T) {
	s, f := setupReview(t)
	store := &creationFixture{fixtureStore: f}
	s.store = store
	c, token := login(t, s)
	page := call(s, "GET", "/ghost/posts/new/", nil, c, "")
	if page.Code != 200 || store.created != 0 {
		t.Fatal("GET created post")
	}
	values := url.Values{"csrf": {token}, "title": {"New synthetic draft"}, "creation_id": {inputValue(t, page.Body.String(), "creation_id")}, "creation_mac": {inputValue(t, page.Body.String(), "creation_mac")}}
	bad := url.Values{}
	for k, v := range values {
		bad[k] = append([]string(nil), v...)
	}
	bad.Set("creation_id", "000000000000000000000000")
	if w := call(s, "POST", "/ghost/posts/new/", bad, c, s.config.Origin); w.Code != 403 || store.created != 0 {
		t.Fatal("unsigned identity accepted")
	}
	for i := 0; i < 2; i++ {
		w := call(s, "POST", "/ghost/posts/new/", values, c, s.config.Origin)
		if w.Code != 303 || !strings.Contains(w.Header().Get("Location"), values.Get("creation_id")) {
			t.Fatal(w.Code)
		}
	}
	if store.created != 1 || f.post.Status != "draft" || f.post.PublishedAt != nil || f.post.HTML != "" {
		t.Fatal("create duplicate or published")
	}
}

func TestSlugTimeAndUnpublishRequireExplicitChanges(t *testing.T) {
	s, f := setupReview(t)
	published := time.Date(2020, 2, 3, 4, 5, 6, 123000000, time.UTC)
	f.post.Status = "published"
	f.post.PublishedAt = &published
	originalID, originalUUID := f.post.ID, f.post.UUID
	c, token := login(t, s)
	path := "/ghost/posts/" + f.post.ID + "/"
	values := editValues(f, token)
	values.Set("slug", "changed-synthetic-url")
	w := call(s, "POST", path, values, c, s.config.Origin)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "Old links will not redirect") || f.post.Revision != 1 {
		t.Fatal("unconfirmed URL change")
	}
	values.Set("confirm_slug_change", "yes")
	values.Set("published_time", "2019-01-02T03:04:05.123")
	w = call(s, "POST", path, values, c, s.config.Origin)
	if w.Code != 200 || f.post.Slug != "changed-synthetic-url" || f.post.PublishedAt.Format(time.RFC3339Nano) != "2019-01-02T03:04:05.123Z" || f.post.ID != originalID || f.post.UUID != originalUUID {
		t.Fatal("metadata save", w.Code)
	}
	date := *f.post.PublishedAt
	values = editValues(f, token)
	values.Set("published_time", "2999-01-02T03:04")
	if w = call(s, "POST", path, values, c, s.config.Origin); w.Code != 422 || !f.post.PublishedAt.Equal(date) {
		t.Fatal("future time accepted")
	}
	values = editValues(f, token)
	values.Set("action", "unpublish")
	if w = call(s, "POST", path, values, c, s.config.Origin); w.Code != 422 || f.post.Status != "published" {
		t.Fatal("unconfirmed unpublish")
	}
	values.Set("confirm_unpublish", "yes")
	if w = call(s, "POST", path, values, c, s.config.Origin); w.Code != 200 || f.post.Status != "draft" || !f.post.PublishedAt.Equal(date) {
		t.Fatal("unpublish lost history")
	}
	if w = call(s, "POST", path, values, c, s.config.Origin); w.Code != 409 {
		t.Fatal("stale metadata accepted", w.Code)
	}
}

func TestManagementFiltersAreBoundedAndRetained(t *testing.T) {
	o, err := parseList("q=Needle&status=draft&type=page&page=2")
	if err != nil || o.Search != "Needle" || o.Status != "draft" || o.Kind != "page" || o.Page != 2 {
		t.Fatal(o, err)
	}
	if nextPath(o.URL(3)) != o.URL(3) {
		t.Fatal("login lost filters")
	}
	for _, raw := range []string{"status=unknown", "q=" + strings.Repeat("x", 201), "type=other", "q=a&q=b", "q=%00"} {
		if _, err := parseList(raw); err == nil {
			t.Fatal("invalid filter accepted", raw)
		}
	}
	s, f := setupList(t)
	cookie, _ := login(t, s)
	w := call(s, "GET", "/ghost/posts/?q=Needle&status=draft&type=page", nil, cookie, "")
	if w.Code != 200 || len(f.queries) != 1 {
		t.Fatal(w.Code)
	}
	q := f.queries[0]
	if q.Search != "Needle" || q.Status != "draft" || q.Kind != "page" {
		t.Fatal("lost filter scope", q)
	}
}

type persistedFixture struct {
	values map[string]cms.BrowserSession
	fail   bool
}

func (p *persistedFixture) CheckBrowserSessions(context.Context) error {
	if p.fail {
		return errors.New("unavailable")
	}
	return nil
}
func (p *persistedFixture) LoadBrowserSession(_ context.Context, scope, hash string, now time.Time) (cms.BrowserSession, error) {
	v, ok := p.values[scope+hash]
	if !ok || !now.Before(v.Expires) {
		return v, cms.ErrSessionNotFound
	}
	return v, nil
}
func (p *persistedFixture) SaveBrowserSession(_ context.Context, scope, hash, old string, v cms.BrowserSession, _ time.Time) error {
	if p.fail {
		return errors.New("unavailable")
	}
	delete(p.values, scope+old)
	p.values[scope+hash] = v
	return nil
}
func (p *persistedFixture) DeleteBrowserSession(_ context.Context, scope, hash string) error {
	if p.fail {
		return errors.New("unavailable")
	}
	delete(p.values, scope+hash)
	return nil
}

func TestPersistentSessionsSurviveRestartAndLogoutEveryInstance(t *testing.T) {
	s, f := setupReview(t)
	backend := &persistedFixture{values: map[string]cms.BrowserSession{}}
	cfg := s.config
	cfg.Sessions = backend
	first, err := New(f, cfg)
	if err != nil {
		t.Fatal(err)
	}
	cookie, token := login(t, first)
	for key := range backend.values {
		if strings.Contains(key, cookie.Value) || strings.Contains(key, token) {
			t.Fatal("raw cookie or CSRF persisted")
		}
	}
	second, err := New(f, cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := "/ghost/posts/" + f.post.ID + "/"
	if w := call(second, "GET", path, nil, cookie, ""); w.Code != 200 || csrf(t, w) != token {
		t.Fatal("restart invalidated session")
	}
	values := editValues(f, token)
	if w := call(second, "POST", path, values, cookie, "https://foreign.invalid"); w.Code != 403 {
		t.Fatal("persistent session weakened origin")
	}
	if w := call(second, "POST", path, values, cookie, cfg.Origin); w.Code != 200 {
		t.Fatal("original form CSRF invalidated", w.Code)
	}
	if w := call(second, "POST", "/ghost/logout/", url.Values{"csrf": {token}}, cookie, cfg.Origin); w.Code != 303 {
		t.Fatal("logout failed")
	}
	if w := call(first, "GET", path, nil, cookie, ""); w.Code != 303 {
		t.Fatal("logout not shared")
	}
	cookie, _ = login(t, first)
	changed := cfg
	changed.Username = "another-owner"
	third, _ := New(f, changed)
	if w := call(third, "GET", path, nil, cookie, ""); w.Code != 303 {
		t.Fatal("session crossed owner scope")
	}
	second.now = func() time.Time { return time.Now().Add(9 * time.Hour) }
	if w := call(second, "GET", path, nil, cookie, ""); w.Code != 303 {
		t.Fatal("expired persisted session")
	}
	backend.fail = true
	if _, err := New(f, cfg); err == nil {
		t.Fatal("missing session backend silently downgraded")
	}
}

func TestTrashNeedsConfirmationAndCurrentRevisionAndRestoresOnlyDraft(t *testing.T) {
	s, f := setupReview(t)
	original := f.post
	f.post.Status = "published"
	c, token := login(t, s)
	path := "/ghost/posts/" + f.post.ID + "/"
	values := url.Values{"csrf": {token}, "updated_at": {f.post.UpdatedAt.Format(time.RFC3339Nano)}, "action": {"trash"}}
	if w := call(s, "POST", path, values, c, s.config.Origin); w.Code != 422 || f.post.TrashedAt != nil {
		t.Fatal("unconfirmed trash")
	}
	values.Set("confirm_trash", "yes")
	if w := call(s, "POST", path, values, c, "https://foreign.invalid"); w.Code != 403 || f.post.TrashedAt != nil {
		t.Fatal("cross-origin trash")
	}
	if w := call(s, "POST", path, values, c, s.config.Origin); w.Code != 303 || f.post.TrashedAt == nil || f.post.Status != "draft" {
		t.Fatal("trash failed")
	}
	values.Set("action", "restore")
	if w := call(s, "POST", path, values, c, s.config.Origin); w.Code != 409 || f.post.TrashedAt == nil {
		t.Fatal("stale restore")
	}
	values.Set("updated_at", f.post.UpdatedAt.Format(time.RFC3339Nano))
	if w := call(s, "POST", path, values, c, s.config.Origin); w.Code != 303 || f.post.TrashedAt != nil || f.post.Status != "draft" {
		t.Fatal("restore failed")
	}
	if f.post.ID != original.ID || f.post.UUID != original.UUID || f.post.SourceHTML != original.SourceHTML || f.post.HTML != original.HTML {
		t.Fatal("trash/restore changed original identity or content")
	}
}

func TestScheduleUIIsExplicitAndCancellationRetainsDraft(t *testing.T) {
	s, f := setupReview(t)
	s.config.Scheduling = true
	c, token := login(t, s)
	path := "/ghost/posts/" + f.post.ID + "/"
	values := editValues(f, token)
	values.Set("scheduled_time", "2999-01-02T03:04")
	if w := call(s, "POST", path, values, c, s.config.Origin); w.Code != 422 || f.post.Status != "draft" {
		t.Fatal("save implicitly scheduled")
	}
	values.Set("action", "schedule")
	w := call(s, "POST", path, values, c, s.config.Origin)
	if w.Code != 200 || f.post.Status != "scheduled" || f.post.PublishedAt != nil || !strings.Contains(w.Body.String(), "Edit schedule") {
		t.Fatal("schedule", w.Code)
	}
	values = editValues(f, token)
	values.Set("action", "cancel_schedule")
	if w = call(s, "POST", path, values, c, s.config.Origin); w.Code != 200 || f.post.Status != "draft" || f.post.ScheduledFor != nil {
		t.Fatal("cancel", w.Code)
	}
}

func TestPublicationSettingsAreDistinctAndPreserveHiddenPrecision(t *testing.T) {
	s, f := setupReview(t)
	s.config.Scheduling = true
	c, token := login(t, s)
	path := "/ghost/posts/" + f.post.ID + "/"
	w := call(s, "GET", path, nil, c, "")
	if strings.Contains(w.Body.String(), `id="published_time"`) || !strings.Contains(w.Body.String(), "Schedule for later") || strings.Contains(w.Body.String(), `step="0.001"`) {
		t.Fatal("draft date/publishing ambiguity")
	}
	at := time.Date(2020, 2, 3, 4, 5, 6, 123000000, time.UTC)
	f.post.Status = "published"
	f.post.PublishedAt = &at
	w = call(s, "GET", path, nil, c, "")
	if !strings.Contains(w.Body.String(), "Publish date (UTC)") || strings.Contains(w.Body.String(), "Schedule for later") {
		t.Fatal("published editing offered scheduling")
	}
	values := editValues(f, token)
	values.Set("published_time", "2020-02-03T04:05")
	w = call(s, "POST", path, values, c, s.config.Origin)
	if w.Code != 200 || !f.post.PublishedAt.Equal(at) || f.post.Status != "published" {
		t.Fatal("unchanged minute display erased exact publication date")
	}
	values = editValues(f, token)
	values.Set("action", "schedule")
	values.Set("scheduled_time", "2999-01-02T03:04")
	if w = call(s, "POST", path, values, c, s.config.Origin); w.Code != 422 || f.post.Status != "published" {
		t.Fatal("published edit became schedule")
	}
	f.post.Status = "scheduled"
	due := time.Now().UTC().Truncate(time.Millisecond).Add(-time.Minute)
	f.post.ScheduledFor = &due
	values = editValues(f, token)
	if w = call(s, "POST", path, values, c, s.config.Origin); w.Code != 200 || !f.post.ScheduledFor.Equal(due) {
		t.Fatal("scheduled content edit changed due instant", w.Code)
	}
}
