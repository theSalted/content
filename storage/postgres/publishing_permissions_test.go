package postgres

import (
	"content/cms"
	"content/internal/httpapi"
	"content/media"
	"content/storage/objects"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Uses transient, non-owner local roles from the explicit role-test harness.
// Owner/admin connections only arrange fixtures and inspect committed results.
// All CMS operations, including the trigger and due worker, use the runtime DSN.
func TestPublishingManagementLeastPrivilegePostgres(t *testing.T) {
	f := newRoleFixture(t)
	grantMedia(t, f) // Existing media rights, not part of the new grant bundle.
	q, role := ident(f.schema), ident(f.runtimeRole)
	origin := "https://images.example.invalid"
	s, err := Open(f.ctx, f.runtimeDSN, f.schema, "installation-a", WithScheduling(true), WithMediaOrigin(origin))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.CheckPublishingManagement(f.ctx); err != nil {
		t.Fatal("exact grants rejected", err)
	}

	t.Run("each new privilege is checked without granting broader access", func(t *testing.T) {
		for _, item := range []struct{ table, privilege string }{
			{"post_slug_claims", "SELECT"}, {"post_slug_claims", "INSERT"}, {"post_slug_claims", "UPDATE(ever_public)"},
			{"publication_schedules", "SELECT"}, {"publication_schedules", "INSERT"}, {"publication_schedules", "DELETE"},
			{"publication_schedules", "UPDATE(schedule_id)"}, {"publication_schedules", "UPDATE(due_at)"},
			{"publication_schedules", "UPDATE(expected_revision)"}, {"publication_schedules", "UPDATE(attempts)"},
			{"publication_schedules", "UPDATE(next_attempt_at)"}, {"publication_schedules", "UPDATE(last_error_code)"},
		} {
			mustExec(t, f.ctx, f.admin, "REVOKE "+item.privilege+" ON "+q+"."+item.table+" FROM "+role)
			if err = s.CheckPublishingManagement(f.ctx); err == nil {
				t.Fatal("missing privilege not detected", item.table, item.privilege)
			}
			mustExec(t, f.ctx, f.admin, "GRANT "+item.privilege+" ON "+q+"."+item.table+" TO "+role)
		}
		if err = s.CheckPublishingManagement(f.ctx); err != nil {
			t.Fatal(err)
		}
		for _, sql := range []string{
			"DELETE FROM " + q + ".post_slug_claims",
			"UPDATE " + q + ".post_slug_claims SET post_id='forged'",
			"UPDATE " + q + ".post_slug_claims SET slug='forged'",
			"UPDATE " + q + ".post_slug_claims SET publication_id='forged'",
			"UPDATE " + q + ".post_slug_claims SET created_at=now()",
			"UPDATE " + q + ".publication_schedules SET post_id='forged'",
			"UPDATE " + q + ".publication_schedules SET publication_id='forged'",
			"DELETE FROM " + q + ".posts",
			"ALTER TABLE " + q + ".publication_schedules ADD COLUMN forbidden text",
			"TRUNCATE " + q + ".publication_schedules",
			"SELECT * FROM " + ident(f.otherSchema) + ".publication_schedules",
			"SELECT * FROM " + ident(f.consumerSchema) + ".private_records",
			"SELECT " + q + ".maintain_slug_claim()",
		} {
			_, e := f.runtime.Exec(f.ctx, sql)
			denied(t, e)
		}
	})

	fs, err := objects.NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	counter := &headCountObjects{Objects: fs}
	assets, err := media.New(s, counter, origin, "permission-fixture")
	if err != nil {
		t.Fatal(err)
	}
	a, err := assets.Upload(f.ctx, "one", mediaPNG(t, 83), "image/png", "")
	if err != nil {
		t.Fatal(err)
	}
	p, err := cms.NewPost("one", cms.CreatePost{Title: "Restricted scheduled fixture", Slug: "before-rename", HTML: `<p>Fixture</p><img src="` + assets.URL(a) + `" alt="Synthetic">`})
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.Create(f.ctx, p)
	if err != nil {
		t.Fatal("invoker trigger under exact runtime grants", err)
	}
	key, _ := httpapi.ParseAdminKey("0123456789abcdef01234567:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	api, err := httpapi.New(s, "one", "https://site.example/blog", key, "synthetic-content-key-for-tests", httpapi.WithMedia(assets), httpapi.WithScheduling(true))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(api)
	defer server.Close()
	client := server.Client()
	client.Timeout = 3 * time.Second
	head := func(want int) {
		t.Helper()
		before := counter.gets.Load()
		response, e := client.Head(server.URL + strings.TrimPrefix(assets.URL(a), origin))
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		body, e := io.ReadAll(response.Body)
		if e != nil || response.StatusCode != want || len(body) != 0 || response.Header.Get("Cache-Control") != "no-store" || counter.gets.Load() != before {
			t.Fatal("HEAD privacy or object-read regression", response.StatusCode, want, e)
		}
	}
	update := func(u cms.UpdatePost) {
		t.Helper()
		u.ExpectedUpdatedAt = p.UpdatedAt
		p, err = s.Update(f.ctx, "one", p.ID, u)
		if err != nil {
			t.Fatal("restricted update", err)
		}
	}
	scheduled, draft := "scheduled", "draft"
	due := time.Now().UTC().Truncate(time.Millisecond).Add(time.Hour)
	update(cms.UpdatePost{Status: &scheduled, PublishedAt: &due})
	head(404)
	if _, err = s.Get(f.ctx, cms.Query{Publication: "one", ID: p.ID, PublicOnly: true}); !errors.Is(err, cms.ErrNotFound) {
		t.Fatal("scheduled post publicly visible", err)
	}
	if yes, e := s.PublishDue(f.ctx, "other-publication"); e != nil || yes {
		t.Fatal("cross-publication worker", e)
	}
	update(cms.UpdatePost{Status: &draft})
	update(cms.UpdatePost{Status: &scheduled, PublishedAt: &due})
	due = due.Add(time.Hour)
	update(cms.UpdatePost{Status: &scheduled, PublishedAt: &due})
	// Simulate restart catch-up without changing the host clock or sleeping.
	mustExec(t, f.ctx, f.admin, "UPDATE "+q+".publication_schedules SET due_at=date_trunc('milliseconds',clock_timestamp()-interval '1 minute'),next_attempt_at=clock_timestamp()-interval '1 minute' WHERE publication_id='one' AND post_id=$1", p.ID)
	if yes, e := s.PublishDue(f.ctx, "one"); e != nil || !yes {
		t.Fatal("restricted due publication", e)
	}
	if yes, e := s.PublishDue(f.ctx, "one"); e != nil || yes {
		t.Fatal("due publication repeated", e)
	}
	p, err = s.Get(f.ctx, cms.Query{Publication: "one", ID: p.ID, PublicOnly: true})
	if err != nil || p.Status != "published" {
		t.Fatal("publication not committed", err)
	}
	head(200)
	slug := "after-rename"
	update(cms.UpdatePost{Slug: &slug})
	resolved, err := s.ResolveURL(f.ctx, "one", "post", "before-rename")
	if err != nil || !resolved.Alias || resolved.PostID != p.ID || resolved.Slug != slug {
		t.Fatal("restricted alias resolution", err)
	}
	update(cms.UpdatePost{Status: &draft})
	head(404)
	if _, err = s.ResolveURL(f.ctx, "one", "post", "before-rename"); !errors.Is(err, cms.ErrNotFound) {
		t.Fatal("withdrawn alias leaked", err)
	}
	var events, jobs int
	if err = f.admin.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM "+q+".outbox_events WHERE post_id=$1),(SELECT count(*) FROM "+q+".publication_schedules WHERE post_id=$1)", p.ID).Scan(&events, &jobs); err != nil || events != 3 || jobs != 0 {
		t.Fatal("wrong atomic lifecycle count", err, events, jobs)
	}
}
