package postgres

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"content/cms"
	"content/internal/httpapi"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
)

func TestSchemaAndLockNamespacing(t *testing.T) {
	for _, name := range []string{"public", "pg_catalog", "pg_other", "information_schema", "a\";DROP SCHEMA x;--", "a.b", "Mixed", strings.Repeat("a", 64), ""} {
		if _, err := Schema(name); err == nil {
			t.Fatal("unsafe schema accepted", name)
		}
	}
	if q, err := Schema("cms_example"); err != nil || q != `"cms_example"` {
		t.Fatal(q, err)
	}
	if MigrationLockID("one", "cms_a") != MigrationLockID("one", "cms_a") || MigrationLockID("one", "cms_a") == MigrationLockID("two", "cms_a") || MigrationLockID("one", "cms_a") == MigrationLockID("one", "cms_b") {
		t.Fatal("migration namespace collision")
	}
}

// This test creates/drops only its unique schema on an explicitly opted-in,
// loopback disposable test database. It never discovers or guesses credentials.
func TestPostgresPersistence(t *testing.T) {
	dsn := os.Getenv("CONTENT_TEST_DATABASE_URL")
	if dsn == "" || os.Getenv("CONTENT_TEST_ALLOW_SCHEMA_CREATE") != "1" {
		t.Skip("requires caller-supplied local disposable PostgreSQL DSN and CONTENT_TEST_ALLOW_SCHEMA_CREATE=1")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test connection configuration")
	}
	if cfg.Host != "localhost" && cfg.Host != "127.0.0.1" && cfg.Host != "::1" {
		t.Fatal("integration tests accept only loopback PostgreSQL")
	}
	if !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatal("integration test database name must end in _test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal("test database connection failed")
	}
	defer conn.Close(context.Background())
	schema := "content_test_" + strings.ToLower(rand.Text()[:12])
	quoted, err := Schema(schema)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := conn.Exec(cleanup, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error("test schema cleanup failed", err)
		}
	}()
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- Migrate(ctx, dsn, schema, "test-installation") }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = Migrate(ctx, dsn, schema, "test-installation"); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, dsn, schema, "wrong-installation"); err == nil {
		t.Fatal("wrong installation accepted")
	}
	s, err := Open(ctx, dsn, schema, "test-installation")
	if err != nil {
		t.Fatal(err)
	}
	create := func(pub, slug, status string) (cms.Post, error) {
		p, err := cms.NewPost(pub, cms.CreatePost{Title: "Test", Slug: slug, Status: status, HTML: "<p>Persistent</p>"})
		if err != nil {
			return p, err
		}
		return s.Create(ctx, p)
	}
	draft, err := create("a", "draft", "draft")
	if err != nil {
		t.Fatal(err)
	}
	published, err := create("a", "public", "published")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = create("b", "public", "published"); err != nil {
		t.Fatal("publication slug scope", err)
	}
	if _, err = create("a", "public", "published"); !errors.Is(err, cms.ErrConflict) {
		t.Fatal("duplicate slug", err)
	}
	// Force event insertion to fail, proving the post and revision roll back too.
	if _, err = conn.Exec(ctx, "ALTER TABLE "+quoted+`.outbox_events ADD CONSTRAINT reject_failure CHECK(post_id IS NULL) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if _, err = create("a", "must-rollback", "published"); err == nil {
		t.Fatal("outbox failure was swallowed")
	}
	if _, err = conn.Exec(ctx, "ALTER TABLE "+quoted+`.outbox_events DROP CONSTRAINT reject_failure`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(ctx, dsn, schema, "test-installation")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := s.Get(ctx, cms.Query{Publication: "a", ID: published.ID, PublicOnly: true})
	if err != nil || p.HTML != "<p>Persistent</p>" || p.Revision != 1 {
		t.Fatal("persistent read", p, err)
	}
	for _, q := range []cms.Query{{Publication: "a", ID: draft.ID, PublicOnly: true}, {Publication: "b", ID: published.ID}, {Publication: "a", Slug: "must-rollback"}} {
		if _, err = s.Get(ctx, q); !errors.Is(err, cms.ErrNotFound) {
			t.Fatal("scope/rollback", q, err)
		}
	}
	posts, total, err := s.Browse(ctx, cms.Query{Publication: "a", PublicOnly: true, Page: 1, Limit: 15})
	if err != nil || total != 1 || len(posts) != 1 {
		t.Fatal("public browse", total, len(posts), err)
	}
	var count int
	if err = conn.QueryRow(ctx, "SELECT count(*) FROM "+quoted+".outbox_events").Scan(&count); err != nil || count != 2 {
		t.Fatal("outbox count", count, err)
	}
	if err = conn.QueryRow(ctx, "SELECT count(*) FROM "+quoted+".post_revisions").Scan(&count); err != nil || count != 3 {
		t.Fatal("revision count", count, err)
	}
	t.Run("HTTP create and public read use PostgreSQL", func(t *testing.T) {
		keyText := "0123456789abcdef01234567:" + strings.Repeat("01", 32)
		admin, err := httpapi.ParseAdminKey(keyText)
		if err != nil {
			t.Fatal(err)
		}
		contentKey := "synthetic-content-key-for-tests"
		app, err := httpapi.New(s, "http-publication", "https://example.invalid", admin, contentKey)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{Audience: jwt.ClaimStrings{"/admin/"}, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute))})
		token.Header["kid"] = "0123456789abcdef01234567"
		signed, err := token.SignedString([]byte(strings.Repeat("\x01", 32)))
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest("POST", "/ghost/api/admin/posts/?source=html", strings.NewReader(`{"posts":[{"title":"Through HTTP","html":"<p>Stored in PostgreSQL</p>","status":"published","slug":"http-post"}]}`))
		request.Header.Set("Authorization", "Ghost "+signed)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		if response.Code != 201 {
			t.Fatalf("create: %d %s", response.Code, response.Body.String())
		}
		var created struct {
			Posts []struct {
				ID string `json:"id"`
			} `json:"posts"`
		}
		if err = json.Unmarshal(response.Body.Bytes(), &created); err != nil || len(created.Posts) != 1 {
			t.Fatal("create response", err)
		}
		response = httptest.NewRecorder()
		app.ServeHTTP(response, httptest.NewRequest("GET", "/ghost/api/content/posts/"+created.Posts[0].ID+"/?key="+contentKey, nil))
		if response.Code != 200 || !strings.Contains(response.Body.String(), "Stored in PostgreSQL") {
			t.Fatalf("public read: %d %s", response.Code, response.Body.String())
		}
	})

}
