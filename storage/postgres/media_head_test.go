package postgres

import (
	"content/cms"
	"content/internal/httpapi"
	"content/media"
	"content/storage/objects"
	"context"
	"crypto/rand"
	"io"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type headCountObjects struct {
	media.Objects
	gets atomic.Int32
}

func (o *headCountObjects) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	o.gets.Add(1)
	return o.Objects.Get(ctx, key)
}

// Schema-only opt-in: uses an existing loopback test login; creates no roles or
// credentials and never connects to a hosted database.
func TestMediaVisibilityHeadPostgres(t *testing.T) {
	dsn := os.Getenv("CONTENT_TEST_DATABASE_URL")
	if dsn == "" || os.Getenv("CONTENT_TEST_ALLOW_SCHEMA_CREATE") != "1" {
		t.Skip("requires explicit loopback disposable test database and schema opt-in")
	}
	cfg, e := pgx.ParseConfig(dsn)
	if e != nil || cfg.Host != "127.0.0.1" || !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatal("loopback disposable test database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, e := pgx.ConnectConfig(ctx, cfg)
	if e != nil {
		t.Fatal("test database connection failed")
	}
	defer db.Close(context.Background())
	schema := "content_media_head_" + strings.ToLower(rand.Text()[:10])
	q, _ := Schema(schema)
	if _, e = db.Exec(ctx, "CREATE SCHEMA "+q); e != nil {
		t.Fatal(e)
	}
	defer func() {
		if _, e := db.Exec(context.Background(), "DROP SCHEMA "+q+" CASCADE"); e != nil {
			t.Error("test cleanup failed", e)
		}
	}()
	if e = Migrate(ctx, dsn, schema, "head-test"); e != nil {
		t.Fatal(e)
	}
	origin := "https://images.example.invalid"
	store, e := Open(ctx, dsn, schema, "head-test", WithMediaOrigin(origin))
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	fs, e := objects.NewFilesystem(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer fs.Close()
	counter := &headCountObjects{Objects: fs}
	service, e := media.New(store, counter, origin, "head-test")
	if e != nil {
		t.Fatal(e)
	}
	image, e := service.Upload(ctx, "a", mediaPNG(t, 27), "image/png", "")
	if e != nil {
		t.Fatal(e)
	}
	key, _ := httpapi.ParseAdminKey("0123456789abcdef01234567:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	api, e := httpapi.New(store, "a", origin, key, "synthetic-content-key-for-tests", httpapi.WithMedia(service))
	if e != nil {
		t.Fatal(e)
	}
	path := strings.TrimPrefix(service.URL(image), origin)
	check := func(path string, want int) {
		t.Helper()
		before := counter.gets.Load()
		w := httptest.NewRecorder()
		api.ServeHTTP(w, httptest.NewRequest("HEAD", path, nil))
		if w.Code != want || w.Header().Get("Cache-Control") != "no-store" || counter.gets.Load() != before {
			t.Fatalf("HEAD status=%d want=%d object reads=%d", w.Code, want, counter.gets.Load()-before)
		}
		if want == 200 && (w.Body.Len() != 0 || w.Header().Get("ETag") != `"sha256-`+image.SHA256+`"` || w.Header().Get("Content-Length") != strconv.FormatInt(image.Size, 10)) {
			t.Fatal("HEAD metadata mismatch")
		}
	}
	check(path, 404)
	html := `<p><img src="` + service.URL(image) + `"></p>`
	create := func(status string) cms.Post {
		t.Helper()
		p, e := cms.NewPost("a", cms.CreatePost{Title: "Synthetic HEAD privacy", HTML: html, Status: status})
		if e != nil {
			t.Fatal(e)
		}
		p, e = store.Create(ctx, p)
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	update := func(p cms.Post, u cms.UpdatePost) cms.Post {
		t.Helper()
		u.ExpectedUpdatedAt = p.UpdatedAt
		p, e := store.Update(ctx, "a", p.ID, u)
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	first := create("draft")
	check(path, 404)
	published, draft := "published", "draft"
	first = update(first, cms.UpdatePost{Status: &published})
	check(path, 200)
	second := create("published")
	check(path, 200)
	first = update(first, cms.UpdatePost{Status: &draft})
	check(path, 200) // shared reference survives
	trash, restore := true, false
	second = update(second, cms.UpdatePost{Trash: &trash})
	check(path, 404)
	second = update(second, cms.UpdatePost{Trash: &restore})
	check(path, 404) // restore stays private
	second = update(second, cms.UpdatePost{Status: &published})
	check(path, 200)
	empty := "<p>No image</p>"
	second = update(second, cms.UpdatePost{HTML: &empty})
	check(path, 404)
	first = update(first, cms.UpdatePost{Status: &published})
	check(path, 200)
	check(strings.TrimSuffix(path, "png")+"jpg", 404)
	other, e := service.Upload(ctx, "b", mediaPNG(t, 28), "image/png", "")
	if e != nil {
		t.Fatal(e)
	}
	check(strings.TrimPrefix(service.URL(other), origin), 404)
	// GET retains byte verification and the same content identity.
	w := httptest.NewRecorder()
	api.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	if w.Code != 200 || counter.gets.Load() != 1 || w.Header().Get("ETag") != `"sha256-`+image.SHA256+`"` {
		t.Fatal("GET regression")
	}
	// A real database outage must not answer from cached visibility.
	store.Close()
	check(path, 503)
}
