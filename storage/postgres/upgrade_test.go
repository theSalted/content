package postgres

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"content/cms"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
)

// Replays the released first migration before exercising the real upgrade path.
func TestPostgresEditMigrationUpgrade(t *testing.T) {
	f := newRoleFixture(t)
	schema := "upgrade_cms"
	q := ident(schema)
	mustExec(t, f.ctx, f.admin, "CREATE SCHEMA "+q+" AUTHORIZATION "+ident(f.owner))
	db, err := sql.Open("pgx", f.migrateDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	body, err := migrations.ReadFile("migrations/00001_posts.sql")
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := database.NewStore(database.DialectPostgres, q+".goose_db_version")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectCustom, db, fstest.MapFS{"00001_posts.sql": &fstest.MapFile{Data: []byte(strings.ReplaceAll(string(body), "{{schema}}", q))}}, goose.WithStore(schemaLedger{ledger}), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(f.ctx); err != nil {
		t.Fatal(err)
	}
	mustExec(t, f.ctx, f.admin, "INSERT INTO "+q+".installation VALUES(true,'upgrade-installation')")
	id := strings.Repeat("a", 24)
	timestamp := time.Date(2026, 10, 25, 12, 0, 0, 123000000, time.UTC)
	mustExec(t, f.ctx, f.admin, "INSERT INTO "+q+".posts VALUES('upgrade-publication',$1,'00000000-0000-4000-8000-000000000001','Legacy title','legacy','published','public',2,$2,$2,$2)", id, timestamp)
	// Simulated legacy historical content has no recoverable past metadata.
	for _, revision := range []int{1, 2} {
		mustExec(t, f.ctx, f.admin, "INSERT INTO "+q+".post_revisions VALUES('upgrade-publication',$1,$2,'<p>Legacy source</p>','<p>Legacy source</p>','legacy-renderer',$3)", id, revision, timestamp)
	}
	if err = Migrate(f.ctx, f.migrateDSN, schema, "upgrade-installation"); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(f.ctx, f.migrateDSN, schema, "upgrade-installation"); err != nil {
		t.Fatal("upgrade rerun", err)
	}
	var oldUnknown bool
	var title, source string
	if err = f.admin.QueryRow(f.ctx, "SELECT metadata IS NULL FROM "+q+".post_revisions WHERE revision=1").Scan(&oldUnknown); err != nil {
		t.Fatal(err)
	}
	if err = f.admin.QueryRow(f.ctx, "SELECT metadata->>'title',source_html FROM "+q+".post_revisions WHERE revision=2").Scan(&title, &source); err != nil {
		t.Fatal(err)
	}
	if !oldUnknown || title != "Legacy title" || source != "<p>Legacy source</p>" {
		t.Fatal("upgrade invented or lost metadata", oldUnknown, title, source)
	}
	_, err = f.admin.Exec(f.ctx, "INSERT INTO "+q+".post_revisions(publication_id,post_id,revision,source_html,html,renderer_version,created_at) VALUES('upgrade-publication',$1,3,'','','test',$2)", id, timestamp)
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "23514" {
		t.Fatal("new revision without metadata accepted", err)
	}
	store, err := Open(f.ctx, f.migrateDSN, schema, "upgrade-installation")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	before, err := store.Get(f.ctx, cms.Query{Publication: "upgrade-publication", ID: id})
	if err != nil {
		t.Fatal(err)
	}
	if before.Revision != 2 || !before.UpdatedAt.Equal(timestamp) {
		t.Fatal("upgrade changed identity/token", before)
	}
	draft := "draft"
	after, err := store.Update(f.ctx, "upgrade-publication", id, cms.UpdatePost{ExpectedUpdatedAt: timestamp, Status: &draft})
	if err != nil {
		t.Fatal("unpublish after upgrade", err)
	}
	if after.UUID != before.UUID || after.Revision != 3 || after.PublishedAt == nil || !after.PublishedAt.Equal(timestamp) {
		t.Fatal("upgrade/edit lost identity/history", after)
	}
}
