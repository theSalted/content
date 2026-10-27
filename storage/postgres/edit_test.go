package postgres

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"content/cms"
	"content/internal/httpapi"
	"github.com/golang-jwt/jwt/v5"
)

type editPostDTO struct {
	ID          string  `json:"id"`
	UUID        string  `json:"uuid"`
	Title       string  `json:"title"`
	Slug        string  `json:"slug"`
	HTML        string  `json:"html"`
	Status      string  `json:"status"`
	UpdatedAt   string  `json:"updated_at"`
	CreatedAt   string  `json:"created_at"`
	PublishedAt *string `json:"published_at"`
}
type editClient struct {
	t           *testing.T
	app         *httpapi.Server
	store       *Store
	token       string
	key         string
	publication string
}

func newEditClient(t *testing.T, f *roleFixture, pub string, secretByte byte) *editClient {
	t.Helper()
	store, err := Open(f.ctx, f.runtimeDSN, f.schema, "installation-a")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	secret := bytes.Repeat([]byte{secretByte}, 32)
	kid := hex.EncodeToString(bytes.Repeat([]byte{secretByte}, 12))
	keyText := kid + ":" + hex.EncodeToString(secret)
	key, err := httpapi.ParseAdminKey(keyText)
	if err != nil {
		t.Fatal(err)
	}
	contentKey := "synthetic-content-key-" + pub
	app, err := httpapi.New(store, pub, "https://example.invalid", key, contentKey)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{Audience: jwt.ClaimStrings{"/admin/"}, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute))})
	tok.Header["kid"] = kid
	signed, err := tok.SignedString(secret)
	if err != nil {
		t.Fatal(err)
	}
	return &editClient{t, app, store, "Ghost " + signed, contentKey, pub}
}
func (c *editClient) call(method, path string, body any) *httptest.ResponseRecorder {
	data, err := json.Marshal(body)
	if err != nil {
		c.t.Fatal(err)
	}
	r := httptest.NewRequest(method, path, strings.NewReader(string(data)))
	r.Header.Set("Authorization", c.token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c.app.ServeHTTP(w, r)
	return w
}
func (c *editClient) decode(w *httptest.ResponseRecorder, status int) editPostDTO {
	c.t.Helper()
	if w.Code != status {
		c.t.Fatalf("HTTP %d want %d: %s", w.Code, status, w.Body.String())
	}
	var result struct {
		Posts []editPostDTO `json:"posts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || len(result.Posts) != 1 {
		c.t.Fatal("post response", err, w.Body.String())
	}
	return result.Posts[0]
}
func (c *editClient) create(title, status string) editPostDTO {
	return c.decode(c.call("POST", "/ghost/api/admin/posts/?source=html", map[string]any{"posts": []any{map[string]any{"title": title, "html": "<p>Before</p>", "status": status}}}), 201)
}
func (c *editClient) edit(p editPostDTO, fields map[string]any) *httptest.ResponseRecorder {
	body := map[string]any{"updated_at": p.UpdatedAt}
	for k, v := range fields {
		body[k] = v
	}
	return c.call("PUT", "/ghost/api/admin/posts/"+p.ID+"/?source=html&save_revision=true", map[string]any{"posts": []any{body}})
}
func (c *editClient) public(id string) *httptest.ResponseRecorder {
	return c.call("GET", "/ghost/api/content/posts/"+id+"/?key="+c.key, nil)
}
func editCounts(t *testing.T, f *roleFixture, pub, id string) (int, int) {
	t.Helper()
	var revisions, events int
	if err := f.admin.QueryRow(f.ctx, "SELECT count(*) FROM "+ident(f.schema)+".post_revisions WHERE publication_id=$1 AND post_id=$2", pub, id).Scan(&revisions); err != nil {
		t.Fatal(err)
	}
	if err := f.admin.QueryRow(f.ctx, "SELECT count(*) FROM "+ident(f.schema)+".outbox_events WHERE publication_id=$1 AND post_id=$2", pub, id).Scan(&events); err != nil {
		t.Fatal(err)
	}
	return revisions, events
}

func TestPostgresEdits(t *testing.T) {
	f := newRoleFixture(t)
	a := newEditClient(t, f, "edit-a", 1)
	b := newEditClient(t, f, "edit-b", 2)
	t.Run("draft edit preserves omitted fields and persists immutable revision", func(t *testing.T) {
		p := a.create("Original title", "draft")
		next := a.decode(a.edit(p, map[string]any{"title": "Changed title"}), 200)
		if next.Title != "Changed title" || next.HTML != p.HTML || next.UUID != p.UUID || next.CreatedAt != p.CreatedAt || next.UpdatedAt == p.UpdatedAt || next.PublishedAt != nil {
			t.Fatal(next)
		}
		if w := a.public(p.ID); w.Code != 404 {
			t.Fatal("draft exposed", w.Code)
		}
		var source, html, oldTitle, newTitle string
		if err := f.admin.QueryRow(f.ctx, "SELECT source_html,html,metadata->>'title' FROM "+ident(f.schema)+".post_revisions WHERE publication_id=$1 AND post_id=$2 AND revision=1", a.publication, p.ID).Scan(&source, &html, &oldTitle); err != nil {
			t.Fatal(err)
		}
		if err := f.admin.QueryRow(f.ctx, "SELECT metadata->>'title' FROM "+ident(f.schema)+".post_revisions WHERE publication_id=$1 AND post_id=$2 AND revision=2", a.publication, p.ID).Scan(&newTitle); err != nil {
			t.Fatal(err)
		}
		if source != p.HTML || html != p.HTML || oldTitle != "Original title" || newTitle != "Changed title" {
			t.Fatal("revision history lost")
		}
		w := a.edit(p, map[string]any{"title": "stale"})
		if w.Code != 409 || !strings.Contains(w.Body.String(), "UPDATE_COLLISION") {
			t.Fatal("stale edit accepted", w.Code)
		}
		if r, e := editCounts(t, f, a.publication, p.ID); r != 2 || e != 0 {
			t.Fatal("stale edit wrote state", r, e)
		}
	})
	t.Run("publication lifecycle exposes only committed published state", func(t *testing.T) {
		p := a.create("Lifecycle", "draft")
		published := a.decode(a.edit(p, map[string]any{"status": "published"}), 200)
		if published.PublishedAt == nil {
			t.Fatal("missing first publication date")
		}
		updated := a.decode(a.edit(published, map[string]any{"html": "<p>Visible edit</p>"}), 200)
		public := a.decode(a.public(p.ID), 200)
		if public.HTML != "<p>Visible edit</p>" || *updated.PublishedAt != *published.PublishedAt {
			t.Fatal("published update projection")
		}
		hidden := a.decode(a.edit(updated, map[string]any{"status": "draft", "html": "<p>Private draft after withdrawal</p>"}), 200)
		if hidden.PublishedAt == nil || *hidden.PublishedAt != *published.PublishedAt {
			t.Fatal("first publication date lost")
		}
		if w := a.public(p.ID); w.Code != 404 {
			t.Fatal("unpublished body exposed", w.Code)
		}
		if w := a.call("GET", "/ghost/api/content/posts/?key="+a.key, nil); w.Code != 200 || strings.Contains(w.Body.String(), p.ID) {
			t.Fatal("unpublished post present in public list", w.Code, w.Body.String())
		}
		var snapshot map[string]any
		if err := f.admin.QueryRow(f.ctx, "SELECT snapshot FROM "+ident(f.schema)+".outbox_events WHERE publication_id=$1 AND post_id=$2 AND event_type='post.unpublished'", a.publication, p.ID).Scan(&snapshot); err != nil {
			t.Fatal(err)
		}
		if _, ok := snapshot["html"]; ok {
			t.Fatal("withdrawal snapshot contains private body")
		}
		republished := a.decode(a.edit(hidden, map[string]any{"status": "published"}), 200)
		if *republished.PublishedAt != *published.PublishedAt {
			t.Fatal("republish reset publication date")
		}
		if r, e := editCounts(t, f, a.publication, p.ID); r != 5 || e != 4 {
			t.Fatal("lifecycle revision/outbox counts", r, e)
		}
	})
	t.Run("competing edits produce one winner and one stale conflict", func(t *testing.T) {
		p := a.create("Concurrent", "published")
		results := make(chan *httptest.ResponseRecorder, 2)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for _, title := range []string{"writer-one", "writer-two"} {
			wg.Add(1)
			go func() { defer wg.Done(); <-start; results <- a.edit(p, map[string]any{"title": title}) }()
		}
		close(start)
		wg.Wait()
		close(results)
		success, conflict := 0, 0
		for w := range results {
			switch w.Code {
			case 200:
				success++
			case 409:
				conflict++
			default:
				t.Fatal(w.Code, w.Body.String())
			}
		}
		if success != 1 || conflict != 1 {
			t.Fatal(success, conflict)
		}
		if r, e := editCounts(t, f, a.publication, p.ID); r != 2 || e != 2 {
			t.Fatal("lost update or duplicate intent", r, e)
		}
	})
	t.Run("outbox failure rolls back post revision token and public HTML", func(t *testing.T) {
		p := a.create("Rollback", "published")
		mustExec(t, f.ctx, f.admin, "ALTER TABLE "+ident(f.schema)+".outbox_events ADD CONSTRAINT reject_edit_event CHECK(event_type<>'post.updated') NOT VALID")
		defer mustExec(t, f.ctx, f.admin, "ALTER TABLE "+ident(f.schema)+".outbox_events DROP CONSTRAINT reject_edit_event")
		w := a.edit(p, map[string]any{"title": "Must roll back", "html": "<p>Never visible</p>"})
		if w.Code != 500 {
			t.Fatal(w.Code, w.Body.String())
		}
		public := a.decode(a.public(p.ID), 200)
		if public.HTML != p.HTML || public.Title != p.Title || public.UpdatedAt != p.UpdatedAt {
			t.Fatal("partial update visible", public)
		}
		if r, e := editCounts(t, f, a.publication, p.ID); r != 1 || e != 1 {
			t.Fatal("partial revision/event persisted", r, e)
		}
	})
	t.Run("cross publication unsupported fields and slug collisions do not mutate", func(t *testing.T) {
		p := a.create("Protected", "published")
		w := b.edit(p, map[string]any{"title": "other publication"})
		if w.Code != 404 {
			t.Fatal("cross-publication update", w.Code)
		}
		for _, fields := range []map[string]any{{"publication_id": "edit-b"}, {"id": "other"}, {"published_at": time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)}, {"lexical": "{}"}, {"html": nil}, {"status": "scheduled"}, {"visibility": "members"}} {
			w = a.edit(p, fields)
			if w.Code != 400 && w.Code != 422 {
				t.Fatal("unsupported update accepted", fields, w.Code)
			}
		}
		other := a.create("Taken slug", "draft")
		w = a.edit(p, map[string]any{"slug": other.Slug})
		if w.Code != 409 {
			t.Fatal("slug collision accepted", w.Code)
		}
		got := a.decode(a.public(p.ID), 200)
		if got.Slug != p.Slug || got.UpdatedAt != p.UpdatedAt {
			t.Fatal("failed writes changed post")
		}
		if r, e := editCounts(t, f, a.publication, p.ID); r != 1 || e != 1 {
			t.Fatal("denied writes changed history", r, e)
		}
	})
	t.Run("wire timestamp advances despite clock skew and no-op edits", func(t *testing.T) {
		p := a.create("Monotonic", "draft")
		future := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Millisecond)
		mustExec(t, f.ctx, f.admin, "UPDATE "+ident(f.schema)+".posts SET updated_at=$1 WHERE publication_id=$2 AND id=$3", future, a.publication, p.ID)
		p.UpdatedAt = future.Format("2006-01-02T15:04:05.000Z")
		next := a.decode(a.edit(p, map[string]any{}), 200)
		expected := future.Add(time.Millisecond).Format("2006-01-02T15:04:05.000Z")
		if next.UpdatedAt != expected {
			t.Fatal("token did not advance one millisecond", next.UpdatedAt, expected)
		}
		next = a.decode(a.edit(next, map[string]any{"html": ""}), 200)
		if next.HTML != "" {
			t.Fatal("explicit empty HTML did not clear body")
		}
		stored, err := a.store.Get(f.ctx, cms.Query{Publication: a.publication, ID: p.ID})
		if err != nil || stored.Revision != 3 {
			t.Fatal(stored, err)
		}
	})
}
