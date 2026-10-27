package main

import (
	"bufio"
	"bytes"
	"content/cms"
	"content/publishing"
	"content/storage/postgres"
	"context"
	"crypto/rand"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestDeliveryHelperProcess(t *testing.T) {
	if os.Getenv("CONTENT_DELIVERY_HELPER") != "true" {
		return
	}
	if e := deliver(); e != nil {
		os.Exit(2)
	}
	os.Exit(0)
}
func TestDeliveryChildArchiveLiveAndShutdown(t *testing.T) {
	dsn := os.Getenv("CONTENT_TEST_DATABASE_URL")
	if dsn == "" || os.Getenv("CONTENT_TEST_ALLOW_SCHEMA_CREATE") != "1" {
		t.Skip("existing opted-in disposable database required")
	}
	cfg, e := pgx.ParseConfig(dsn)
	if e != nil || cfg.Host != "127.0.0.1" || !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatal("loopback test database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	conn, e := pgx.ConnectConfig(ctx, cfg)
	if e != nil {
		t.Fatal("test DB unavailable")
	}
	defer conn.Close(context.Background())
	schema := "child_test_" + strings.ToLower(rand.Text()[:10])
	q, _ := postgres.Schema(schema)
	if _, e = conn.Exec(ctx, "CREATE SCHEMA "+q); e != nil {
		t.Fatal(e)
	}
	defer conn.Exec(context.Background(), "DROP SCHEMA "+q+" CASCADE")
	if e = postgres.Migrate(ctx, dsn, schema, "test-child"); e != nil {
		t.Fatal(e)
	}
	store, e := postgres.Open(ctx, dsn, schema, "test-child", postgres.WithConnectionLimit(2))
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	p, e := cms.NewPost("one", cms.CreatePost{Title: "Historical synthetic", HTML: "<p>Original</p>", Status: "published"})
	if e != nil {
		t.Fatal(e)
	}
	p, e = store.Create(ctx, p)
	if e != nil {
		t.Fatal(e)
	}
	enroll := publishing.Enrollment{Destination: "ap", SourceID: "content", SiteURL: "https://example.invalid/blog"}
	if _, e = store.EnqueueArchive(ctx, postgres.ArchiveRequest{Publication: "one", PostID: p.ID, ExpectedRevision: p.Revision, Enrollment: enroll}); e != nil {
		t.Fatal(e)
	}
	var mu sync.Mutex
	var archiveBodies [][]byte
	var events []publishing.Event
	token := strings.Repeat("synthetic-only-", 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(401)
			return
		}
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 2<<20))
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/internal/v2/archive":
			if _, e := publishing.DecodeArchive(raw); e != nil {
				w.WriteHeader(422)
				return
			}
			archiveBodies = append(archiveBodies, bytes.Clone(raw))
			if len(archiveBodies) == 1 {
				w.WriteHeader(503)
				return
			}
			w.WriteHeader(202)
			io.WriteString(w, `{"result":"duplicate","original_result":"applied"}`)
		case "/internal/v2/events":
			v, e := publishing.Decode(raw)
			if e != nil {
				w.WriteHeader(422)
				return
			}
			events = append(events, v)
			w.WriteHeader(202)
			io.WriteString(w, `{"result":"applied","object_id":"https://example.invalid/ap/object"}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestDeliveryHelperProcess$")
	cmd.Env = []string{"CONTENT_DELIVERY_HELPER=true", "CONTENT_PUBLISHING_DELIVERY_ENABLED=true", "CONTENT_PUBLISHING_ARCHIVE_DELIVERY_ENABLED=true", "CONTENT_DELIVERY_DATABASE_URL=" + dsn, "CONTENT_SCHEMA=" + schema, "CONTENT_INSTALLATION_ID=test-child", "CONTENT_PUBLICATION_ID=one", "CONTENT_PUBLISHING_DESTINATION=ap", "CONTENT_PUBLISHING_ENDPOINT=" + server.URL + "/internal/v2/events", "CONTENT_PUBLISHING_ARCHIVE_ENDPOINT=" + server.URL + "/internal/v2/archive", "CONTENT_PUBLISHING_TOKEN=" + token}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	stopped := false
	defer func() {
		if !stopped {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	wait := func(check func() bool) {
		t.Helper()
		for !check() {
			select {
			case <-ctx.Done():
				t.Fatal("child integration timeout")
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	wait(func() bool { b, e := store.ArchiveBinding(ctx, "one", p.ID); return e == nil && b.State == "accepted" })
	mu.Lock()
	if len(events) != 0 || len(archiveBodies) != 2 || !bytes.Equal(archiveBodies[0], archiveBodies[1]) {
		mu.Unlock()
		t.Fatal("history broadcast or nonidentical archive retry")
	}
	mu.Unlock()
	title := "Updated after archive"
	p, e = store.Update(ctx, "one", p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Title: &title})
	if e != nil {
		t.Fatal(e)
	}
	draft := "draft"
	private := "<p>Private withdrawn body</p>"
	p, e = store.Update(ctx, "one", p.ID, cms.UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Status: &draft, HTML: &private})
	if e != nil {
		t.Fatal(e)
	}
	newStore, e := postgres.Open(ctx, dsn, schema, "test-child", postgres.WithNewPostPublishing(enroll), postgres.WithConnectionLimit(1))
	if e != nil {
		t.Fatal(e)
	}
	defer newStore.Close()
	fresh, _ := cms.NewPost("one", cms.CreatePost{Title: "New synthetic", HTML: "<p>New</p>", Status: "published"})
	fresh, e = newStore.Create(ctx, fresh)
	if e != nil {
		t.Fatal(e)
	}
	wait(func() bool {
		state, e := store.PublishingStatus(ctx, "one", "ap")
		return e == nil && state.Live.Accepted == 3 && state.Archive.Accepted == 1 && state.BlockedStreams == 0
	})
	mu.Lock()
	oldTypes := []string{}
	newCreate := false
	for _, v := range events {
		if v.ContentID == p.UUID {
			if v.ObjectInstance != p.UUID {
				t.Error("archived identity changed")
			}
			oldTypes = append(oldTypes, v.Type)
			if v.Type == "content.withdrawn" && v.Document != nil {
				t.Error("private body leaked")
			}
		} else if v.ContentID == fresh.UUID && v.Type == "content.published" {
			newCreate = true
		}
	}
	mu.Unlock()
	if strings.Join(oldTypes, ",") != "content.updated,content.withdrawn" || !newCreate {
		t.Fatal("lifecycle sequence incorrect")
	}
	if e = cmd.Process.Signal(syscall.SIGTERM); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case e = <-done:
		stopped = true
		if e != nil {
			t.Fatal("child failed graceful shutdown")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child ignored SIGTERM")
	}
	if strings.Contains(stdout.String(), token) || strings.Contains(stderr.String(), token) || strings.Contains(stdout.String(), dsn) {
		t.Fatal("sensitive child output")
	}
	seen := map[string]bool{}
	scan := bufio.NewScanner(bytes.NewReader(stdout.Bytes()))
	for scan.Scan() {
		var v map[string]any
		if json.Unmarshal(scan.Bytes(), &v) != nil || v["protocol"] != "content-delivery-supervisor-v1" {
			t.Fatal("invalid supervisor record")
		}
		if v["event"] == "progress" {
			seen[v["lane"].(string)] = true
		}
		if v["event"] == "started" && v["pool_limit"] != float64(2) {
			t.Fatal("pool budget changed")
		}
	}
	if !seen["live"] || !seen["archive"] {
		t.Fatal("missing queue progress health")
	}
}
