package postgres

import (
	"bytes"
	"content/cms"
	"content/internal/httpapi"
	"content/media"
	"content/storage/objects"
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func mediaPNG(t *testing.T, seed uint8) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for y := 0; y < 48; y++ {
		for x := 0; x < 64; x++ {
			im.SetRGBA(x, y, color.RGBA{uint8(x) + seed, uint8(y), 150, 255})
		}
	}
	var b bytes.Buffer
	if e := png.Encode(&b, im); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func grantMedia(t *testing.T, f *roleFixture) {
	q := ident(f.schema)
	mustExec(t, f.ctx, f.admin, "GRANT SELECT ON "+q+".media_quota TO "+ident(f.runtimeRole))
	mustExec(t, f.ctx, f.admin, "GRANT SELECT,INSERT ON "+q+".media_objects TO "+ident(f.runtimeRole))
	mustExec(t, f.ctx, f.admin, "GRANT UPDATE(state) ON "+q+".media_objects TO "+ident(f.runtimeRole))
	mustExec(t, f.ctx, f.admin, "GRANT SELECT,INSERT,DELETE ON "+q+".post_media TO "+ident(f.runtimeRole))
}

type ackFailObjects struct {
	media.Objects
	fail atomic.Bool
}

func (f *ackFailObjects) Put(ctx context.Context, key string, data []byte, mime string) error {
	e := f.Objects.Put(ctx, key, data, mime)
	if e != nil {
		return e
	}
	if f.fail.Load() {
		return errors.New("synthetic lost storage acknowledgement")
	}
	return nil
}

func TestMediaHTTPPostgresLifecycle(t *testing.T) {
	f := newRoleFixture(t)
	grantMedia(t, f)
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	defer server.Close()
	store, e := Open(f.ctx, f.runtimeDSN, f.schema, "installation-a", WithMediaOrigin(origin))
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	fs, e := objects.NewFilesystem(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer fs.Close()
	service, e := media.New(store, fs, origin, "installation-a/cms_a")
	if e != nil {
		t.Fatal(e)
	}
	key, _ := httpapi.ParseAdminKey("0123456789abcdef01234567:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	api, e := httpapi.New(store, "images-a", origin, key, "synthetic-content-key-for-tests", httpapi.WithMedia(service))
	if e != nil {
		t.Fatal(e)
	}
	server.Config.Handler = api
	server.Start()
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	send := func(method, path string, body io.Reader, kind, auth string) (int, []byte) {
		t.Helper()
		r, e := http.NewRequest(method, server.URL+path, body)
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Content-Type", kind)
		r.Header.Set("Authorization", auth)
		res, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		b, e := io.ReadAll(res.Body)
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(path, "/content/images/") && res.StatusCode == 200 {
			if res.Header.Get("Content-Type") != "image/png" || res.Header.Get("Cache-Control") != "no-store" || res.Header.Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("unsafe image response")
			}
		}
		return res.StatusCode, b
	}
	auth := clientJWTForAudience(t, "/v2/admin/", time.Now().Unix())
	upload := func(data []byte, mime, purpose, token string) (int, string) {
		t.Helper()
		var b bytes.Buffer
		writer := multipart.NewWriter(&b)
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", `form-data; name="file"; filename="../../synthetic.png"`)
		h.Set("Content-Type", mime)
		part, e := writer.CreatePart(h)
		if e != nil {
			t.Fatal(e)
		}
		part.Write(data)
		writer.WriteField("ref", "synthetic/image.png")
		if purpose != "" {
			writer.WriteField("purpose", purpose)
		}
		writer.Close()
		status, raw := send("POST", "/ghost/api/v2/admin/images/upload/", &b, writer.FormDataContentType(), token)
		if status != 201 {
			return status, ""
		}
		var envelope struct{ Images []struct{ URL, Ref string } }
		if e = json.Unmarshal(raw, &envelope); e != nil {
			t.Fatal(e)
		}
		if len(envelope.Images) != 1 || envelope.Images[0].Ref != "synthetic/image.png" {
			t.Fatal("Ghost image envelope")
		}
		return status, envelope.Images[0].URL
	}
	data := mediaPNG(t, 1)
	for _, tc := range []struct {
		name                string
		data                []byte
		mime, purpose, auth string
		want                int
	}{{"unauthenticated", data, "image/png", "", "", 401}, {"MIME spoof", data, "image/jpeg", "", auth, 422}, {"SVG", []byte(`<svg onload="alert(1)"></svg>`), "image/png", "", auth, 422}, {"truncated", data[:40], "image/png", "", auth, 422}, {"square validation", data, "image/png", "profile_image", auth, 422}, {"size limit", make([]byte, media.MaxBytes+1), "image/png", "", auth, 413}} {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := upload(tc.data, tc.mime, tc.purpose, tc.auth); code != tc.want {
				t.Fatal(code)
			}
		})
	}
	status, imageURL := upload(data, "image/png", "image", auth)
	if status != 201 {
		t.Fatal(status)
	}
	status, duplicate := upload(data, "image/png", "image", auth)
	if status != 201 || duplicate != imageURL {
		t.Fatal("retry created another image")
	}
	// Simultaneous retries converge on the same durable image and URL.
	var wait sync.WaitGroup
	var repeated [2]string
	var repeatErr [2]error
	for i := range repeated {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			a, e := service.Upload(f.ctx, "images-a", data, "image/png", "")
			repeated[i], repeatErr[i] = service.URL(a), e
		}(i)
	}
	wait.Wait()
	for i := range repeated {
		if repeatErr[i] != nil || repeated[i] != imageURL {
			t.Fatal("concurrent upload retry", repeatErr[i])
		}
	}
	u, _ := url.Parse(imageURL)
	id, _, _ := media.ImagePath(u.Path)
	if code, _ := send("GET", u.Path, nil, "", ""); code != 404 {
		t.Fatal("unattached image leaked")
	}
	if code, b := send("GET", u.Path, nil, "", auth); code != 200 || !bytes.Equal(b, data) {
		t.Fatal("authenticated image read", code)
	}
	// Current-publication HTTP may never expose another publication's same bytes.
	other, e := service.Upload(f.ctx, "images-b", data, "image/png", "")
	if e != nil || other.ID == id {
		t.Fatal("publication deduplication leaked", e)
	}
	if code, _ := send("GET", "/content/images/"+other.ID+"/image.png", nil, "", auth); code != 404 {
		t.Fatal("cross-publication image read")
	}
	html := `<p><img src="` + imageURL + `" alt="Synthetic"></p>`
	post, _ := cms.NewPost("images-a", cms.CreatePost{Title: "Synthetic image draft", HTML: html})
	post, e = store.Create(f.ctx, post)
	if e != nil {
		t.Fatal(e)
	}
	if code, _ := send("GET", u.Path, nil, "", ""); code != 404 {
		t.Fatal("draft image leaked")
	}
	if preview := service.Preview(f.ctx, "images-a", html); !strings.Contains(preview, "data:image/png;base64,") {
		t.Fatal("private preview missing")
	}
	foreignHTML := `<p><img src="` + service.URL(other) + `"></p>`
	bad, _ := cms.NewPost("images-a", cms.CreatePost{Title: "Foreign image", HTML: foreignHTML})
	if _, e = store.Create(f.ctx, bad); e == nil {
		t.Fatal("foreign image attached")
	}
	// Existing outbox failure guarantee must also roll back image visibility.
	mustExec(t, f.ctx, f.admin, "REVOKE INSERT ON "+ident(f.schema)+".outbox_events FROM "+ident(f.runtimeRole))
	published := "published"
	if _, e = store.Update(f.ctx, "images-a", post.ID, cms.UpdatePost{ExpectedUpdatedAt: post.UpdatedAt, Status: &published}); e == nil {
		t.Fatal("publication without outbox accepted")
	}
	mustExec(t, f.ctx, f.admin, "GRANT INSERT ON "+ident(f.schema)+".outbox_events TO "+ident(f.runtimeRole))
	if code, _ := send("GET", u.Path, nil, "", ""); code != 404 {
		t.Fatal("rollback leaked image")
	}
	post, e = store.Update(f.ctx, "images-a", post.ID, cms.UpdatePost{ExpectedUpdatedAt: post.UpdatedAt, Status: &published})
	if e != nil {
		t.Fatal(e)
	}
	if code, b := send("GET", u.Path, nil, "", ""); code != 200 || !bytes.Equal(b, data) {
		t.Fatal("published image unavailable", code)
	}
	// A configured hostname change must retain old references and privacy.
	migrated, e := Open(f.ctx, f.runtimeDSN, f.schema, "installation-a", WithMediaOrigin("https://new.example.invalid", origin))
	if e != nil {
		t.Fatal(e)
	}
	defer migrated.Close()
	title := "Synthetic after hostname transition"
	post, e = migrated.Update(f.ctx, "images-a", post.ID, cms.UpdatePost{ExpectedUpdatedAt: post.UpdatedAt, Title: &title})
	if e != nil {
		t.Fatal(e)
	}
	if post.SourceHTML != html {
		t.Fatal("domain transition rewrote stored HTML")
	}
	if code, b := send("GET", u.Path, nil, "", ""); code != 200 || !bytes.Equal(b, data) {
		t.Fatal("alias edit detached published image", code)
	}
	service.OriginAliases = []string{origin}
	service.Origin = "https://new.example.invalid"
	if preview := service.Preview(f.ctx, "images-a", html); !strings.Contains(preview, "data:image/png;base64,") {
		t.Fatal("alias preview unavailable")
	}
	if !strings.HasPrefix(service.URL(other), "https://new.example.invalid/") {
		t.Fatal("new uploads use old origin")
	}
	store = migrated
	draft := "draft"
	post, e = store.Update(f.ctx, "images-a", post.ID, cms.UpdatePost{ExpectedUpdatedAt: post.UpdatedAt, Status: &draft})
	if e != nil {
		t.Fatal(e)
	}
	if code, _ := send("GET", u.Path, nil, "", ""); code != 404 {
		t.Fatal("withdrawn image remains public")
	}
	if _, e = f.runtime.Exec(f.ctx, "UPDATE "+ident(f.schema)+".media_objects SET object_key='tamper'"); e == nil {
		t.Fatal("runtime changed immutable key")
	}
	if _, e = f.runtime.Exec(f.ctx, "DELETE FROM "+ident(f.schema)+".media_objects"); e == nil {
		t.Fatal("runtime can delete objects")
	}
	var count int
	if e = f.admin.QueryRow(f.ctx, "SELECT count(*) FROM "+ident(f.schema)+".media_objects WHERE publication_id='images-a'").Scan(&count); e != nil || count != 1 {
		t.Fatal("invalid uploads persisted or retry duplicated", count, e)
	}
	mustExec(t, f.ctx, f.admin, "UPDATE "+ident(f.schema)+".media_quota SET limit_bytes=1")
	if code, _ := upload(mediaPNG(t, 10), "image/png", "image", auth); code != 507 {
		t.Fatal("quota HTTP response", code)
	}
	if e = Migrate(f.ctx, f.migrateDSN, f.schema, "installation-a"); e != nil {
		t.Fatal("media migration repeat failed", e)
	}
}

func TestMediaPendingRecoveryAndCleanupPostgres(t *testing.T) {
	f := newRoleFixture(t)
	grantMedia(t, f)
	store, e := Open(f.ctx, f.runtimeDSN, f.schema, "installation-a")
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	directory := t.TempDir()
	fs, e := objects.NewFilesystem(directory)
	if e != nil {
		t.Fatal(e)
	}
	defer fs.Close()
	fault := &ackFailObjects{Objects: fs}
	fault.fail.Store(true)
	service, e := media.New(store, fault, "https://images.example.invalid", "installation-a/cms_a")
	if e != nil {
		t.Fatal(e)
	}
	data := mediaPNG(t, 2)
	if _, e = service.Upload(f.ctx, "pending-a", data, "image/png", ""); e == nil {
		t.Fatal("lost acknowledgement treated as success")
	}
	var id, state, key string
	if e = f.admin.QueryRow(f.ctx, "SELECT id,state,object_key FROM "+ident(f.schema)+".media_objects WHERE publication_id='pending-a'").Scan(&id, &state, &key); e != nil || state != "pending" {
		t.Fatal("durable cleanup intent missing", e)
	}
	if _, _, e = service.Read(f.ctx, "pending-a", id, false); !errors.Is(e, media.ErrNotFound) {
		t.Fatal("unfinished object served")
	}
	fault.fail.Store(false)
	asset, e := service.Upload(f.ctx, "pending-a", data, "image/png", "")
	if e != nil || asset.ID != id || asset.State != "ready" {
		t.Fatal("idempotent recovery failed", e)
	}
	fault.fail.Store(true)
	if _, e = service.Upload(f.ctx, "pending-a", mediaPNG(t, 3), "image/png", ""); e == nil {
		t.Fatal("failure missing")
	}
	var abandoned string
	if e = f.admin.QueryRow(f.ctx, "SELECT object_key FROM "+ident(f.schema)+".media_objects WHERE publication_id='pending-a' AND state='pending'").Scan(&abandoned); e != nil {
		t.Fatal(e)
	}
	mustExec(t, f.ctx, f.admin, "UPDATE "+ident(f.schema)+".media_objects SET created_at=clock_timestamp()-interval '2 days' WHERE publication_id='pending-a'")
	// Separate transient maintenance role: no post writes, ownership or DDL.
	cleanupRole := f.runtimeRole + "_cleanup"
	createTestRole(t, f.ctx, f.admin, cleanupRole, f.rolePassword)
	t.Cleanup(func() {
		mustExec(t, context.Background(), f.admin, "DROP OWNED BY "+ident(cleanupRole))
		mustExec(t, context.Background(), f.admin, "DROP ROLE "+ident(cleanupRole))
	})
	mustExec(t, f.ctx, f.admin, "GRANT CONNECT ON DATABASE "+ident(f.database)+" TO "+ident(cleanupRole))
	mustExec(t, f.ctx, f.admin, "GRANT USAGE ON SCHEMA "+ident(f.schema)+" TO "+ident(cleanupRole))
	mustExec(t, f.ctx, f.admin, "GRANT SELECT ON "+ident(f.schema)+".installation,"+ident(f.schema)+".media_objects TO "+ident(cleanupRole))
	mustExec(t, f.ctx, f.admin, "GRANT DELETE,UPDATE(state) ON "+ident(f.schema)+".media_objects TO "+ident(cleanupRole))
	maintenanceURL, _ := url.Parse(f.runtimeDSN)
	maintenanceURL.User = url.UserPassword(cleanupRole, f.rolePassword)
	probe, e := pgx.Connect(f.ctx, maintenanceURL.String())
	if e != nil {
		t.Fatal(e)
	}
	defer probe.Close(context.Background())
	for _, q := range []string{"CREATE TABLE " + ident(f.schema) + ".forbidden_media(id int)", "SELECT * FROM " + ident(f.consumerSchema) + ".private_records", "SELECT * FROM " + ident(f.schema) + ".posts", "UPDATE " + ident(f.schema) + ".media_objects SET object_key='tamper'"} {
		_, e := probe.Exec(f.ctx, q)
		denied(t, e)
	}
	maintenance, e := Open(f.ctx, maintenanceURL.String(), f.schema, "installation-a")
	if e != nil {
		t.Fatal(e)
	}
	defer maintenance.Close()
	cleaner, e := media.New(maintenance, fs, "https://images.example.invalid", "installation-a/cms_a")
	if e != nil {
		t.Fatal(e)
	}
	locked, e := f.runtime.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = locked.Exec(f.ctx, "SELECT id FROM "+ident(f.schema)+".media_objects WHERE publication_id='pending-a' AND state='pending' FOR UPDATE"); e != nil {
		t.Fatal(e)
	}
	if n, e := cleaner.Cleanup(f.ctx, "pending-a", time.Now().Add(-24*time.Hour)); e != nil || n != 0 {
		t.Fatal("cleanup did not skip active upload", n, e)
	}
	if e = locked.Rollback(f.ctx); e != nil {
		t.Fatal(e)
	}
	n, e := cleaner.Cleanup(f.ctx, "pending-a", time.Now().Add(-24*time.Hour))
	if e != nil || n != 1 {
		t.Fatal("pending cleanup", n, e)
	}
	if _, e = os.Stat(directory + "/" + abandoned); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("abandoned bytes retained")
	}
	if _, e = os.Stat(directory + "/" + key); e != nil {
		t.Fatal("ready bytes deleted")
	}
	if _, _, e = service.Read(f.ctx, "pending-a", id, false); e != nil {
		t.Fatal("ready object lost during cleanup", e)
	}
	if n, e = cleaner.Cleanup(f.ctx, "pending-a", time.Now().Add(-24*time.Hour)); e != nil || n != 0 {
		t.Fatal("cleanup not idempotent", n, e)
	}
}
