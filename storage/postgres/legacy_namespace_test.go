package postgres

import (
	"database/sql"
	"fmt"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
	"strings"
	"testing"
	"testing/fstest"
)

func TestMigrationAdoptsRecognizedLegacyCMS(t *testing.T) {
	f := newRoleFixture(t)
	for _, version := range []int{2, 3} {
		t.Run(fmt.Sprintf("version_%d", version), func(t *testing.T) {
			schema := fmt.Sprintf("legacy_cms_%d", version)
			q := ident(schema)
			mustExec(t, f.ctx, f.admin, "CREATE SCHEMA "+q+" AUTHORIZATION "+ident(f.owner))
			db, err := sql.Open("pgx", f.migrateDSN)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			files := fstest.MapFS{}
			entries, _ := migrations.ReadDir("migrations")
			for _, entry := range entries {
				var n int
				if _, err = fmt.Sscanf(entry.Name(), "%d_", &n); err != nil {
					t.Fatal(err)
				}
				if n <= version {
					body, err := migrations.ReadFile("migrations/" + entry.Name())
					if err != nil {
						t.Fatal(err)
					}
					files[entry.Name()] = &fstest.MapFile{Data: []byte(strings.ReplaceAll(string(body), "{{schema}}", q))}
				}
			}
			ledger, err := database.NewStore(database.DialectPostgres, q+".goose_db_version")
			if err != nil {
				t.Fatal(err)
			}
			provider, err := goose.NewProvider(goose.DialectCustom, db, files, goose.WithStore(schemaLedger{ledger}), goose.WithDisableGlobalRegistry(true))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = provider.Up(f.ctx); err != nil {
				t.Fatal(err)
			}
			mustExec(t, f.ctx, f.admin, "INSERT INTO "+q+".installation VALUES(true,'legacy')")
			mustExec(t, f.ctx, f.admin, "INSERT INTO "+q+".posts VALUES('publication','aaaaaaaaaaaaaaaaaaaaaaaa','00000000-0000-4000-8000-000000000001','Preserved','preserved','published','public',1,'2026-01-01','2026-01-01','2026-01-01')")
			mustExec(t, f.ctx, f.admin, "INSERT INTO "+q+".post_revisions VALUES('publication','aaaaaaaaaaaaaaaaaaaaaaaa',1,'<p>Original</p>','<p>Original</p>','legacy','2026-01-01','{\"title\":\"Preserved\"}')")
			mustExec(t, f.ctx, f.admin, "INSERT INTO "+q+".outbox_events VALUES('00000000-0000-4000-8000-000000000002','publication','aaaaaaaaaaaaaaaaaaaaaaaa',1,'post.published',1,'{\"retained\":true}','2026-01-01')")
			data := func() string {
				var s string
				if err = f.admin.QueryRow(f.ctx, "SELECT jsonb_build_array((SELECT jsonb_agg(to_jsonb(p)-'kind'-'archive_pending'-'public_generation'-'trashed_at') FROM "+q+".posts p),(SELECT jsonb_agg(to_jsonb(r)) FROM "+q+".post_revisions r),(SELECT jsonb_agg(to_jsonb(e)) FROM "+q+".outbox_events e))::text").Scan(&s); err != nil {
					t.Fatal(err)
				}
				return s
			}
			before := data()
			if err = Migrate(f.ctx, f.migrateDSN, schema, "legacy"); err != nil {
				t.Fatal("legitimate legacy upgrade rejected", err)
			}
			if err = Migrate(f.ctx, f.migrateDSN, schema, "legacy"); err != nil {
				t.Fatal("bound upgrade rerun", err)
			}
			if before != data() {
				t.Fatal("legacy post/revision/outbox changed")
			}
			// Additive schema defaults are expected; all pre-existing values above
			// must remain byte-equivalent, and migration must not enroll history.
			var kind string
			var pending bool
			var generation int64
			var trashed bool
			if err = f.admin.QueryRow(f.ctx, "SELECT kind,archive_pending,public_generation,trashed_at IS NOT NULL FROM "+q+".posts").Scan(&kind, &pending, &generation, &trashed); err != nil || kind != "post" || pending || generation != 1 || trashed {
				t.Fatal("unsafe new-column defaults", kind, pending, err)
			}
			var module string
			var count int
			if err = f.admin.QueryRow(f.ctx, "SELECT module_id FROM "+q+".installation").Scan(&module); err != nil || module != "cms" {
				t.Fatal("missing CMS binding", module, err)
			}
			if err = f.admin.QueryRow(f.ctx, "SELECT count(*) FROM "+q+".publishing_streams").Scan(&count); err != nil || count != 0 {
				t.Fatal("legacy content enrolled", count, err)
			}
		})
	}
}
