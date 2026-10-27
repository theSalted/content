package objects

import (
	"bytes"
	"context"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestS3ConfiguredEndpointAndPrivateRoundTrip(t *testing.T) {
	var mu sync.Mutex
	objects := map[string][]byte{}
	var signed atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/storage/v1/s3/synthetic-media/v1/") {
			t.Error("endpoint path changed")
			w.WriteHeader(400)
			return
		}
		if !strings.Contains(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=SYNTHETIC/") {
			t.Error("explicit SigV4 credentials missing")
			w.WriteHeader(403)
			return
		}
		signed.Add(1)
		if r.Header.Get("X-Amz-Acl") != "" {
			t.Error("public ACL requested")
		}
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case "PUT":
			data, e := io.ReadAll(r.Body)
			if e != nil {
				t.Error(e)
			}
			objects[r.URL.Path] = data
			w.Header().Set("ETag", `"synthetic"`)
			w.WriteHeader(200)
		case "GET":
			data, ok := objects[r.URL.Path]
			if !ok {
				w.WriteHeader(404)
				return
			}
			w.Write(data)
		case "DELETE":
			delete(objects, r.URL.Path)
			w.WriteHeader(204)
		default:
			w.WriteHeader(405)
		}
	}))
	defer server.Close()
	t.Setenv("AWS_ACCESS_KEY_ID", "MUST_NOT_BE_USED")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "MUST_NOT_BE_USED")
	s, e := NewS3(S3Config{Endpoint: server.URL + "/storage/v1/s3", Region: "local", Bucket: "synthetic-media", AccessKey: "SYNTHETIC", SecretKey: "synthetic-only-secret"})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	key := "v1/" + strings.Repeat("a", 64) + "/" + strings.Repeat("b", 64) + ".png"
	data := []byte("synthetic object")
	for i := 0; i < 2; i++ {
		if e = s.Put(context.Background(), key, data, "image/png"); e != nil {
			t.Fatal(e)
		}
	}
	r, e := s.Get(context.Background(), key)
	if e != nil {
		t.Fatal(e)
	}
	got, e := io.ReadAll(r)
	r.Close()
	if e != nil || !bytes.Equal(got, data) {
		t.Fatal("round trip changed")
	}
	if e = s.Delete(context.Background(), key); e != nil {
		t.Fatal(e)
	}
	if signed.Load() != 4 {
		t.Fatal("unexpected requests")
	}
}
func TestS3RejectsRedirectsAndUnsafeConfiguration(t *testing.T) {
	var leaked atomic.Int64
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
	defer sink.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, sink.URL, 307) }))
	defer redirect.Close()
	cfg := S3Config{Endpoint: redirect.URL, Region: "local", Bucket: "synthetic-media", AccessKey: "SYNTHETIC", SecretKey: "synthetic-only-secret"}
	s, e := NewS3(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	e = s.Put(context.Background(), "v1/"+strings.Repeat("a", 64)+"/"+strings.Repeat("b", 64)+".png", []byte("test"), "image/png")
	if e == nil || leaked.Load() != 0 {
		t.Fatal("credential redirect followed")
	}
	for _, endpoint := range []string{"http://public.example.invalid", "https://user:secret@example.invalid", "https://example.invalid/?secret=x", ""} {
		cfg.Endpoint = endpoint
		if _, e = NewS3(cfg); e == nil {
			t.Fatal("unsafe endpoint accepted")
		}
	}
}

type captureS3HTTP func(*http.Request) (*http.Response, error)

func (f captureS3HTTP) Do(r *http.Request) (*http.Response, error) { return f(r) }
func TestS3AddressingStyle(t *testing.T) {
	for _, style := range []string{"path", "virtual-host"} {
		t.Run(style, func(t *testing.T) {
			s, e := NewS3(S3Config{Endpoint: "https://objects.example.invalid", Region: "auto", Bucket: "synthetic-media", AccessKey: "SYNTHETIC", SecretKey: "synthetic-secret", URLStyle: style})
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			opts := s.client.Options()
			opts.HTTPClient = captureS3HTTP(func(r *http.Request) (*http.Response, error) {
				host, path := "objects.example.invalid", "/synthetic-media/v1/"
				if style == "virtual-host" {
					host, path = "synthetic-media.objects.example.invalid", "/v1/"
				}
				if r.URL.Host != host || !strings.HasPrefix(r.URL.Path, path) {
					t.Fatal("wrong addressing", r.URL.Host, r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
			})
			s.client = s3.New(opts)
			if e = s.Put(context.Background(), "v1/"+strings.Repeat("a", 64)+"/"+strings.Repeat("b", 64)+".png", []byte("synthetic"), "image/png"); e != nil {
				t.Fatal(e)
			}
		})
	}
	for _, cfg := range []S3Config{
		{Endpoint: "https://objects.example.invalid", Bucket: "synthetic", URLStyle: "invalid"},
		{Endpoint: "https://objects.example.invalid/path", Bucket: "synthetic", URLStyle: "virtual-host"},
		{Endpoint: "https://objects.example.invalid", Bucket: "synthetic.dotted", URLStyle: "virtual-host"},
	} {
		cfg.Region = "auto"
		cfg.AccessKey = "SYNTHETIC"
		cfg.SecretKey = "synthetic-secret"
		if s, e := NewS3(cfg); e == nil {
			s.Close()
			t.Fatal("unsafe addressing config")
		}
	}
}
