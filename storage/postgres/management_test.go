package postgres

import (
	"content/cms"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestManagementReadFilterHistoryTrashAndRestorePostgres(t *testing.T) {
	f := newRoleFixture(t)
	store, err := Open(f.ctx, f.runtimeDSN, f.schema, "installation-a")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	p, err := cms.NewPost("one", cms.CreatePost{Title: "Find me", HTML: "<p>Retain original</p>", Status: "published"})
	if err != nil {
		t.Fatal(err)
	}
	p, err = store.Create(f.ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	old := p
	date := time.Date(2020, 1, 2, 3, 4, 5, 123000000, time.UTC)
	slug := "new-canonical-slug"
	p, err = store.Update(f.ctx, "one", p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Slug: &slug, PublishedAt: &date})
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != old.ID || p.UUID != old.UUID || !p.PublishedAt.Equal(date) {
		t.Fatal("lost identity/date")
	}
	rows, total, err := store.Browse(f.ctx, cms.Query{Publication: "one", Kind: "post", Page: 1, Limit: 20, Search: "FIND", Status: "published"})
	if err != nil || total != 1 || len(rows) != 1 {
		t.Fatal("filter", err, total)
	}
	rows, total, err = store.Browse(f.ctx, cms.Query{Publication: "one", Kind: "post", Page: 1, Limit: 20, Search: "' OR 1=1 --"})
	if err != nil || total != 0 || len(rows) != 0 {
		t.Fatal("search not literal", err)
	}
	trash := true
	p, err = store.Update(f.ctx, "one", p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Trash: &trash})
	if err != nil {
		t.Fatal(err)
	}
	if p.TrashedAt == nil || p.Status != "draft" || p.HTML != old.HTML || !p.PublishedAt.Equal(date) {
		t.Fatal("trash lost data")
	}
	if _, err = store.Get(f.ctx, cms.Query{Publication: "one", ID: p.ID}); !errors.Is(err, cms.ErrNotFound) {
		t.Fatal("trash visible")
	}
	if _, err = store.Get(f.ctx, cms.Query{Publication: "one", ID: p.ID, Trashed: true, PublicOnly: true}); !errors.Is(err, cms.ErrNotFound) {
		t.Fatal("public trash exposed")
	}
	retained, err := store.Get(f.ctx, cms.Query{Publication: "one", ID: p.ID, Trashed: true})
	if err != nil || retained.HTML != old.HTML {
		t.Fatal("recovery unavailable")
	}
	trash = false
	p, err = store.Update(f.ctx, "one", p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Trash: &trash})
	if err != nil || p.TrashedAt != nil || p.Status != "draft" {
		t.Fatal("restore failed", err)
	}
	var withdrawals int
	if err = f.admin.QueryRow(f.ctx, "SELECT count(*) FROM "+ident(f.schema)+".outbox_events WHERE post_id=$1 AND event_type='post.unpublished'", p.ID).Scan(&withdrawals); err != nil || withdrawals != 1 {
		t.Fatal("wrong withdrawal count", err, withdrawals)
	}
}

func TestBrowserSessionRestrictedPrivilegesAndPersistencePostgres(t *testing.T) {
	f := newRoleFixture(t)
	s, err := Open(f.ctx, f.runtimeDSN, f.schema, "installation-a")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.CheckBrowserSessions(f.ctx); err == nil {
		t.Fatal("missing grant silently allowed")
	}
	table := ident(f.schema) + ".browser_sessions"
	mustExec(t, f.ctx, f.admin, "GRANT SELECT,INSERT,DELETE ON "+table+" TO "+ident(f.runtimeRole))
	if err = s.CheckBrowserSessions(f.ctx); err != nil {
		t.Fatal(err)
	}
	scope := strings.Repeat("a", 64)
	hash := strings.Repeat("b", 64)
	now := time.Now().UTC()
	value := cms.BrowserSession{Authenticated: true, Expires: now.Add(time.Hour)}
	if err = s.SaveBrowserSession(f.ctx, scope, hash, "", value, now); err != nil {
		t.Fatal(err)
	}
	next, err := Open(f.ctx, f.runtimeDSN, f.schema, "installation-a")
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	got, err := next.LoadBrowserSession(f.ctx, scope, hash, now)
	if err != nil || !got.Authenticated {
		t.Fatal("restart persistence", err)
	}
	if _, err = next.LoadBrowserSession(f.ctx, strings.Repeat("c", 64), hash, now); !errors.Is(err, cms.ErrSessionNotFound) {
		t.Fatal("scope isolation")
	}
	if err = next.DeleteBrowserSession(f.ctx, scope, hash); err != nil {
		t.Fatal(err)
	}
	if _, err = s.LoadBrowserSession(f.ctx, scope, hash, now); !errors.Is(err, cms.ErrSessionNotFound) {
		t.Fatal("logout persistence")
	}
	if _, err = f.runtime.Exec(f.ctx, "UPDATE "+table+" SET authenticated=true"); err == nil {
		t.Fatal("runtime can alter session rows")
	}
	if _, err = f.runtime.Exec(f.ctx, "SELECT * FROM "+ident(f.otherSchema)+".browser_sessions"); err == nil {
		t.Fatal("cross-schema sessions exposed")
	}
}
