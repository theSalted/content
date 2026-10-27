package postgres

import (
	"content/cms"
	"content/migration/ghost"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"os"
	"strings"
	"testing"
	"time"
)

func TestGhostImportPersistence(t *testing.T) {
	dsn := os.Getenv("CONTENT_TEST_DATABASE_URL")
	if dsn == "" || os.Getenv("CONTENT_TEST_ALLOW_SCHEMA_CREATE") != "1" {
		t.Skip("requires an explicitly opted-in existing loopback disposable database")
	}
	cfg, e := pgx.ParseConfig(dsn)
	if e != nil || cfg.Host != "127.0.0.1" || !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatal("loopback test database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, e := pgx.ConnectConfig(ctx, cfg)
	if e != nil {
		t.Fatal("local test database unavailable")
	}
	defer conn.Close(context.Background())
	schema := "content_import_test_" + strings.ToLower(rand.Text()[:10])
	q, _ := Schema(schema)
	if _, e = conn.Exec(ctx, "CREATE SCHEMA "+q); e != nil {
		t.Fatal(e)
	}
	defer func() {
		if _, e := conn.Exec(context.Background(), "DROP SCHEMA "+q+" CASCADE"); e != nil {
			t.Error(e)
		}
	}()
	for range 2 {
		if e = Migrate(ctx, dsn, schema, "import-test"); e != nil {
			t.Fatal(e)
		}
	}
	store, e := Open(ctx, dsn, schema, "import-test")
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	original := `{"id":"0123456789abcdef01234567","uuid":"12345678-1234-4234-8234-123456789abc","title":"Historical","slug":"historical","html":"<p>Historical &amp; exact</p>","status":"published","visibility":"public","created_at":"2020-01-02T03:04:05.000Z","updated_at":"2021-02-03T04:05:06.123Z","published_at":"2020-01-03T03:04:05.000Z","excerpt":"Source excerpt","meta_description":"Source SEO","lexical":"retained raw source","authors":[{"id":"0123456789abcdef01234568","name":"Author","slug":"author"}]}`
	makePlan := func(post string) ghost.Plan {
		raw := []byte(`{"db":[{"data":{"posts":[` + post + `],"pages":[{"id":"page-id","title":"Unsupported page"}],"settings":[{"key":"private","value":"not-for-api"}]}}]}`)
		p, e := ghost.Build(raw, "publication-a")
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	plan := makePlan(original)
	// Forged derived fields cannot bypass the parser's validation.
	plan.Records[0].Post.HTML = "<script>not accepted</script>"
	result, e := store.ImportGhost(ctx, "publication-a", "source-a", plan)
	if e != nil || result.Imported != 1 || result.Quarantined != 1 || result.OutboxWritten != 0 {
		t.Fatal(result, e)
	}
	p, e := store.Get(ctx, cms.Query{Publication: "publication-a", ID: "0123456789abcdef01234567", PublicOnly: true})
	if e != nil {
		t.Fatal(e)
	}
	if p.HTML != "<p>Historical &amp; exact</p>" || p.SourceHTML != p.HTML || p.CreatedAt.Year() != 2020 || p.PublishedAt.Year() != 2020 || p.UpdatedAt.Nanosecond() != 123000000 || cms.Excerpt(p) != "Source excerpt" {
		t.Fatal("import fidelity failed")
	}
	for _, table := range []string{"outbox_events", "publishing_streams", "publishing_deliveries"} {
		var count int
		if e = conn.QueryRow(ctx, "SELECT count(*) FROM "+q+"."+table).Scan(&count); e != nil || count != 0 {
			t.Fatal("import emitted event", table, e, count)
		}
	}
	var raw string
	if e = conn.QueryRow(ctx, "SELECT raw_export FROM "+q+".source_import_batches").Scan(&raw); e != nil || raw != string(plan.Raw) {
		t.Fatal("archive not byte-identical", e)
	}
	again, e := store.ImportGhost(ctx, "publication-a", "source-a", plan)
	if e != nil || again.Unchanged != 2 || again.Imported != 0 {
		t.Fatal("rerun not idempotent", again, e)
	}
	if _, e = store.Get(ctx, cms.Query{Publication: "publication-b", ID: p.ID}); !errors.Is(e, cms.ErrNotFound) {
		t.Fatal("cross-publication access", e)
	}
	// A changed export must not overwrite any record or insert a new batch.
	changed := makePlan(strings.Replace(original, "Historical\"", "Changed\"", 1))
	if _, e = store.ImportGhost(ctx, "publication-a", "source-a", changed); e == nil {
		t.Fatal("changed source silently overwritten")
	}
	var count int
	_ = conn.QueryRow(ctx, "SELECT count(*) FROM "+q+".source_import_batches").Scan(&count)
	if count != 1 {
		t.Fatal("failed import left a partial batch")
	}

	// A late collision must also roll back an earlier new post and its revision.
	first := strings.ReplaceAll(original, "0123456789abcdef01234567", "0123456789abcdef01234569")
	first = strings.ReplaceAll(first, "12345678-1234-4234-8234-123456789abc", "12345678-1234-4234-8234-123456789abd")
	first = strings.ReplaceAll(first, `"slug":"historical"`, `"slug":"new-first"`)
	collision := strings.ReplaceAll(original, "0123456789abcdef01234567", "0123456789abcdef01234570")
	collision = strings.ReplaceAll(collision, `"slug":"historical"`, `"slug":"collision"`)
	broken, e := ghost.Build([]byte(`{"db":[{"data":{"posts":[`+first+`,`+collision+`]}}]}`), "publication-a")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = store.ImportGhost(ctx, "publication-a", "source-a", broken); e == nil {
		t.Fatal("identity collision accepted")
	}
	_ = conn.QueryRow(ctx, "SELECT count(*) FROM "+q+".posts").Scan(&count)
	if count != 1 {
		t.Fatal("partial post survived rollback")
	}
	if role := os.Getenv("CONTENT_TEST_EXISTING_RUNTIME_ROLE"); role != "" {
		roleQ := pgx.Identifier{role}.Sanitize()
		if _, e = conn.Exec(ctx, "GRANT USAGE ON SCHEMA "+q+" TO "+roleQ+"; GRANT SELECT ON "+q+".installation,"+q+".posts,"+q+".post_revisions TO "+roleQ); e != nil {
			t.Fatal(e)
		}
		runtimeCfg := cfg.Copy()
		runtimeCfg.User = role
		runtimeCfg.Password = ""
		runtime, e := Open(ctx, runtimeCfg.ConnString(), schema, "import-test")
		if e != nil {
			t.Fatal(e)
		}
		defer runtime.Close()
		if _, e = runtime.Get(ctx, cms.Query{Publication: "publication-a", ID: p.ID, PublicOnly: true}); e != nil {
			t.Fatal("runtime read failed", e)
		}
		for _, table := range []string{"source_import_batches", "source_import_records"} {
			var allowed bool
			if e = conn.QueryRow(ctx, "SELECT has_table_privilege($1,$2,'SELECT')", role, schema+"."+table).Scan(&allowed); e != nil || allowed {
				t.Fatal("runtime can read source archive", e)
			}
		}
		if _, e = runtime.ImportGhost(ctx, "publication-a", "forbidden", plan); e == nil {
			t.Fatal("runtime import accepted")
		}
	}
	title := "Author edit"
	edited, e := store.Update(ctx, p.Publication, p.ID, cms.UpdatePost{Title: &title, ExpectedUpdatedAt: p.UpdatedAt})
	if e != nil {
		t.Fatal(e)
	}
	if edited.CreatedAt != p.CreatedAt || !edited.PublishedAt.Equal(*p.PublishedAt) || cms.Excerpt(edited) != "Source excerpt" {
		t.Fatal("edit lost historical data")
	}
	if _, e = store.ImportGhost(ctx, "publication-a", "source-a", plan); e != nil {
		t.Fatal(e)
	}
	current, _ := store.Get(ctx, cms.Query{Publication: p.Publication, ID: p.ID})
	if current.Title != title {
		t.Fatal("rerun overwrote author edit")
	}
	if _, e = store.Update(ctx, p.Publication, p.ID, cms.UpdatePost{Title: &title, ExpectedUpdatedAt: p.UpdatedAt}); !errors.Is(e, cms.ErrStaleUpdate) {
		t.Fatal("stale update accepted")
	}
	body := "<p>Replacement</p>"
	edited, e = store.Update(ctx, p.Publication, p.ID, cms.UpdatePost{HTML: &body, ExpectedUpdatedAt: edited.UpdatedAt})
	if e != nil || cms.Excerpt(edited) != "Replacement" {
		t.Fatal("edited excerpt stale", e)
	}
	var metadata map[string]any
	bytes, _ := json.Marshal(edited.Presentation)
	json.Unmarshal(bytes, &metadata)
	if metadata["meta_description"] != "Source SEO" {
		t.Fatal("SEO lost")
	}
	// Pages preserve full data but stay outside post reads and all publishing work.
	var beforeEvents int
	_ = conn.QueryRow(ctx, "SELECT count(*) FROM "+q+".outbox_events").Scan(&beforeEvents)
	pageSource := strings.ReplaceAll(original, "0123456789abcdef01234567", "0123456789abcdef01234571")
	pageSource = strings.ReplaceAll(pageSource, "12345678-1234-4234-8234-123456789abc", "12345678-1234-4234-8234-123456789abe")
	pageSource = strings.ReplaceAll(pageSource, `"slug":"historical"`, `"slug":"page"`)
	pagePlan, e := ghost.Build([]byte(`{"db":[{"data":{"pages":[`+pageSource+`]}}]}`), "publication-a")
	if e != nil {
		t.Fatal(e)
	}
	pageResult, e := store.ImportGhost(ctx, "publication-a", "source-pages", pagePlan)
	if e != nil || pageResult.Imported != 1 {
		t.Fatal(pageResult, e)
	}
	pageID := "0123456789abcdef01234571"
	page, e := store.Get(ctx, cms.Query{Kind: "page", Publication: "publication-a", ID: pageID, PublicOnly: true})
	if e != nil || page.Kind != "page" || page.CreatedAt.Year() != 2020 || page.HTML != p.HTML {
		t.Fatal("page fidelity", e)
	}
	if _, e = store.Get(ctx, cms.Query{Publication: "publication-a", ID: pageID}); !errors.Is(e, cms.ErrNotFound) {
		t.Fatal("page leaked through posts")
	}
	_, total, e := store.Browse(ctx, cms.Query{Publication: "publication-a", PublicOnly: true, Page: 1, Limit: 100})
	if e != nil || total != 1 {
		t.Fatal("page in post feed", e, total)
	}
	_, total, e = store.Browse(ctx, cms.Query{Kind: "page", Publication: "publication-a", PublicOnly: true, Page: 1, Limit: 100})
	if e != nil || total != 1 {
		t.Fatal("page read failed", e, total)
	}
	if _, e = store.Update(ctx, "publication-a", pageID, cms.UpdatePost{Title: &title, ExpectedUpdatedAt: page.UpdatedAt}); !errors.Is(e, cms.ErrNotFound) {
		t.Fatal("post update route edited page", e)
	}
	page, e = store.Update(ctx, "publication-a", pageID, cms.UpdatePost{Kind: "page", Title: &title, ExpectedUpdatedAt: page.UpdatedAt})
	if e != nil {
		t.Fatal(e)
	}
	draft := "draft"
	page, e = store.Update(ctx, "publication-a", pageID, cms.UpdatePost{Kind: "page", Status: &draft, ExpectedUpdatedAt: page.UpdatedAt})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = store.Get(ctx, cms.Query{Kind: "page", Publication: "publication-a", ID: pageID, PublicOnly: true}); !errors.Is(e, cms.ErrNotFound) {
		t.Fatal("private page exposed")
	}
	var afterEvents int
	_ = conn.QueryRow(ctx, "SELECT count(*) FROM "+q+".outbox_events").Scan(&afterEvents)
	if beforeEvents != afterEvents {
		t.Fatal("page emitted publishing event")
	}

	// Explicit URL/metadata provenance is archived and cannot change on rerun.
	normalizedSource := strings.ReplaceAll(original, "0123456789abcdef01234567", "0123456789abcdef01234572")
	normalizedSource = strings.ReplaceAll(normalizedSource, "12345678-1234-4234-8234-123456789abc", "12345678-1234-4234-8234-123456789abf")
	normalizedSource = strings.ReplaceAll(normalizedSource, `"slug":"historical"`, `"slug":"normalized"`)
	snapshot := []byte(`{"scope":"public Content API only; not a full private/staff/database backup","origin":"https://source.example","data":{"posts":[` + normalizedSource + `]}}`)
	full := []byte(`{"db":[{"data":{"posts":[` + strings.Replace(normalizedSource, `"excerpt":"Source excerpt",`, "", 1) + `]}}]}`)
	normalizedPlan, e := ghost.BuildWithOptions(full, "publication-a", ghost.Options{SourceURL: "https://source.example", PublicSnapshot: snapshot})
	if e != nil {
		t.Fatal(e)
	}
	normalizedResult, e := store.ImportGhost(ctx, "publication-a", "source-normalized", normalizedPlan)
	if e != nil || normalizedResult.Imported != 1 {
		t.Fatal("supplement import", e)
	}
	var savedSnapshot, savedSnapshotHash string
	if e = conn.QueryRow(ctx, "SELECT raw_public_snapshot,public_snapshot_sha256 FROM "+q+".source_import_batches WHERE source_namespace='source-normalized'").Scan(&savedSnapshot, &savedSnapshotHash); e != nil || savedSnapshot != string(snapshot) || savedSnapshotHash != normalizedPlan.PublicSnapshotSHA256 {
		t.Fatal("snapshot archive changed", e)
	}
	normalizedPlan.Options.PublicSnapshot = nil
	if _, e = store.ImportGhost(ctx, "publication-a", "source-normalized", normalizedPlan); e == nil {
		t.Fatal("changed import options silently accepted")
	}

}
