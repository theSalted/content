package httpapi

import (
	"bytes"
	"content/media"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

type imageMetadataRepo struct {
	media.Repository
	asset      media.Asset
	public     bool
	failure    bool
	calls      int
	publicOnly bool
}

func (m *imageMetadataRepo) MediaGet(_ context.Context, publication, id string, publicOnly bool) (media.Asset, error) {
	m.calls++
	m.publicOnly = publicOnly
	if m.failure {
		return media.Asset{}, errors.New("private-database-detail")
	}
	if id != m.asset.ID || publication != m.asset.Publication || publicOnly && !m.public {
		return media.Asset{}, media.ErrNotFound
	}
	return m.asset, nil
}

type imageCountObjects struct {
	media.Objects
	data []byte
	gets int
	fail bool
}

func (o *imageCountObjects) Get(context.Context, string) (io.ReadCloser, error) {
	o.gets++
	if o.fail {
		return nil, errors.New("private-object-store-detail")
	}
	return io.NopCloser(bytes.NewReader(o.data)), nil
}
func imageHeadFixture(t *testing.T) (*Server, *imageMetadataRepo, *imageCountObjects, string, string) {
	t.Helper()
	s, _, auth := setup(t)
	data := []byte("synthetic immutable stored image bytes")
	sum := sha256.Sum256(data)
	repo := &imageMetadataRepo{public: true, asset: media.Asset{ID: "0123456789abcdef01234567", Publication: s.publication, State: "ready", MIME: "image/png", Extension: "png", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data))}}
	objects := &imageCountObjects{data: data}
	service, e := media.New(repo, objects, "https://images.example.invalid", "synthetic-installation")
	if e != nil {
		t.Fatal(e)
	}
	s.media = service
	return s, repo, objects, "/content/images/" + repo.asset.ID + "/image.png", auth
}
func TestImageVisibilityHeadDoesNotReadObjects(t *testing.T) {
	s, repo, objects, path, _ := imageHeadFixture(t)
	w := request(s, "HEAD", path, "", "")
	if w.Code != 200 || w.Body.Len() != 0 || objects.gets != 0 || repo.calls != 1 || !repo.publicOnly {
		t.Fatal("HEAD must perform one public metadata check and zero object reads")
	}
	for k, v := range map[string]string{"Content-Type": "image/png", "Content-Length": strconv.FormatInt(repo.asset.Size, 10), "ETag": `"sha256-` + repo.asset.SHA256 + `"`, "Cache-Control": "no-store", "X-Content-Media-Profile": "visibility-head-v1", "X-Content-Type-Options": "nosniff"} {
		if w.Header().Get(k) != v {
			t.Fatalf("%s mismatch", k)
		}
	}
	if w.Header().Get("Set-Cookie") != "" {
		t.Fatal("visibility check set a cookie")
	}
	get := request(s, "GET", path, "", "")
	if get.Code != 200 || objects.gets != 1 || !bytes.Equal(get.Body.Bytes(), objects.data) || get.Header().Get("ETag") != w.Header().Get("ETag") {
		t.Fatal("GET content identity differs")
	}
	// HEAD certifies current eligibility, not object-store liveness. Actual GET
	// still fails closed if fetching or verifying the stored bytes fails.
	objects.fail = true
	if request(s, "HEAD", path, "", "").Code != 200 || objects.gets != 1 {
		t.Fatal("HEAD contacted object store")
	}
	if request(s, "GET", path, "", "").Code != 503 {
		t.Fatal("GET hid object-store failure")
	}
	objects.fail = false
	objects.data = []byte("corrupted")
	if request(s, "GET", path, "", "").Code != 503 {
		t.Fatal("GET skipped byte integrity")
	}
}
func TestImageHeadPrivacyAndInput(t *testing.T) {
	for _, tc := range []struct {
		name, variant, auth string
		public, dbFailure   bool
		want                int
		queries             int
	}{
		{"public", "", "", true, false, 200, 1},
		{"private", "", "", false, false, 404, 1},
		{"private authenticated", "", "valid", false, false, 200, 1},
		{"invalid auth", "", "invalid", true, false, 401, 0},
		{"database outage", "", "", true, true, 503, 1},
		{"wrong extension", "jpg", "", true, false, 404, 1},
		{"missing", "missing", "", true, false, 404, 1},
		{"foreign publication", "foreign", "valid", true, false, 404, 1},
		{"query", "?width=10", "", true, false, 404, 0},
		{"empty query", "?", "", true, false, 404, 0},
		{"encoded path", "encoded", "", true, false, 404, 0},
		{"malformed path", "malformed", "", true, false, 404, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, repo, objects, path, auth := imageHeadFixture(t)
			repo.public = tc.public
			repo.failure = tc.dbFailure
			switch tc.variant {
			case "jpg":
				path = strings.TrimSuffix(path, "png") + "jpg"
			case "missing":
				path = strings.Replace(path, repo.asset.ID, strings.Repeat("f", 24), 1)
			case "foreign":
				repo.asset.Publication = "other-publication"
			case "encoded":
				path = strings.Replace(path, "/012", "/%3012", 1)
			case "malformed":
				path = "/content/images/bad/image.png"
			default:
				path += tc.variant
			}
			if tc.auth == "valid" {
				tc.auth = auth
			}
			w := request(s, "HEAD", path, "", tc.auth)
			if w.Code != tc.want || repo.calls != tc.queries || objects.gets != 0 {
				t.Fatalf("status=%d queries=%d object reads=%d", w.Code, repo.calls, objects.gets)
			}
			if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Set-Cookie") != "" || strings.Contains(w.Body.String(), "private-") {
				t.Fatal("unsafe response")
			}
			if tc.want != 200 && w.Header().Get("ETag") != "" {
				t.Fatal("error disclosed image identity")
			}
		})
	}
}
func TestImageHeadNeverUsesStaleConditionalEligibility(t *testing.T) {
	s, repo, objects, path, _ := imageHeadFixture(t)
	first := request(s, "HEAD", path, "", "")
	repo.public = false
	r := httptest.NewRequest(http.MethodHead, path, nil)
	r.Header.Set("If-None-Match", first.Header().Get("ETag"))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 404 || repo.calls != 2 || objects.gets != 0 || w.Header().Get("ETag") != "" {
		t.Fatal("conditional request bypassed fresh eligibility")
	}
}
func TestImageHeadInvalidStoredMetadataFailsClosed(t *testing.T) {
	for _, mutate := range []func(*media.Asset){func(a *media.Asset) { a.SHA256 = "bad" }, func(a *media.Asset) { a.Size = 0 }, func(a *media.Asset) { a.MIME = "image/svg+xml" }, func(a *media.Asset) { a.State = "pending" }} {
		s, repo, objects, path, _ := imageHeadFixture(t)
		mutate(&repo.asset)
		w := request(s, "HEAD", path, "", "")
		if w.Code != 503 || objects.gets != 0 || w.Header().Get("ETag") != "" {
			t.Fatal("invalid metadata accepted")
		}
	}
}
