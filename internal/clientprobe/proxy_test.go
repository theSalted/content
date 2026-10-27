package clientprobe

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestProbeForwardsWithoutRecordingValues(t *testing.T) {
	body := `{"posts":[{"title":"private-title","html":"private-html","lexical":"private-lexical","tags":["private-tag"],"private-field":"private-value"}]}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		if string(data) != body || r.Header.Get("Authorization") != "Ghost private-token" || r.Header.Get("Cookie") != "private-cookie" || r.URL.Query().Get("key") != "private-key" {
			t.Error("request changed")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		io.WriteString(w, `{"posts":[{"title":"private-title"}]}`)
	}))
	defer upstream.Close()
	var log bytes.Buffer
	probe, err := New(upstream.URL, &log)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	r := httptest.NewRequest("POST", "/ghost/api/v3/admin/posts/private-id/?source=html&key=private-key&include=private-query", strings.NewReader(body))
	r.Header.Set("Authorization", "Ghost private-token")
	r.Header.Set("Cookie", "private-cookie")
	r.Header.Set("Accept-Version", "v3.0")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	probe.ServeHTTP(w, r)
	if w.Code != 201 || !strings.Contains(w.Body.String(), "private-title") {
		t.Fatal("response changed", w.Code, w.Body.String())
	}
	if strings.Contains(log.String(), "private-") {
		t.Fatal("private value recorded", log.String())
	}
	var event Event
	if err = json.Unmarshal(log.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event.Route != "/ghost/api/v3/admin/posts/{value}/" || event.Version != "v3.0" || !event.AuthorizationPresent || event.PostFields["lexical"] != "string" || event.Query["source"] != "html" || event.Status != 201 {
		t.Fatal(event)
	}
}

func TestProbeBoundaries(t *testing.T) {
	for _, target := range []string{"https://example.invalid", "http://localhost:8080", "http://127.0.0.1:8080/path", "http://user:secret@127.0.0.1:8080", "http://127.0.0.1:8080?key=secret", "http://0.0.0.0:8080", "http://127.0.0.1:99999"} {
		if _, err := New(target, io.Discard); err == nil {
			t.Fatal("nonlocal/ambiguous target accepted", target)
		}
	}
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Location", "https://example.invalid/private-token")
		w.WriteHeader(302)
	}))
	defer upstream.Close()
	var log bytes.Buffer
	probe, err := New(upstream.URL, &log)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	w := httptest.NewRecorder()
	probe.ServeHTTP(w, httptest.NewRequest("POST", "/ghost/api/admin/posts/", strings.NewReader(strings.Repeat("x", MaxBody+1))))
	if w.Code != 413 || calls.Load() != 0 {
		t.Fatal("oversized request forwarded")
	}
	w = httptest.NewRecorder()
	probe.ServeHTTP(w, httptest.NewRequest("GET", "/ghost/api/admin/site/", nil))
	if w.Code != 502 || w.Header().Get("Location") != "" || strings.Contains(log.String(), "private-token") {
		t.Fatal("external redirect leaked", w.Code)
	}
	if safeRoute("/ghost/api/admin/users/email/private@example.invalid/") != "/ghost/api/admin/users/email/{value}/" {
		t.Fatal("email path leaked")
	}
	if safeRoute("/private-token/") != "[unrecognized]" || safeVersion("private-token") != "[other]" {
		t.Fatal("unknown value leaked")
	}
}

func TestProbeConcurrentRecords(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer upstream.Close()
	var log bytes.Buffer
	probe, err := New(upstream.URL, &log)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			probe.ServeHTTP(w, httptest.NewRequest("GET", "/ghost/api/admin/site/", nil))
			if w.Code != 204 {
				t.Error(w.Code)
			}
		}()
	}
	wg.Wait()
	seen := map[uint64]bool{}
	d := json.NewDecoder(&log)
	for d.More() {
		var event Event
		if err := d.Decode(&event); err != nil {
			t.Fatal(err)
		}
		if seen[event.Sequence] {
			t.Fatal("duplicate sequence")
		}
		seen[event.Sequence] = true
	}
	if len(seen) != 12 {
		t.Fatal("missing records", seen)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("synthetic write failure") }
func TestProbeStopsForwardingAfterLogFailure(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(200) }))
	defer upstream.Close()
	probe, err := New(upstream.URL, failingWriter{})
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	probe.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/ghost/api/admin/site/", nil))
	w := httptest.NewRecorder()
	probe.ServeHTTP(w, httptest.NewRequest("GET", "/ghost/api/admin/site/", nil))
	if !probe.Failed() || calls.Load() != 1 || w.Code != 503 {
		t.Fatal("failed capture continued")
	}
}

func TestJWTShapeNeverRecordsTokenValues(t *testing.T) {
	encode := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	now := time.Now()
	header := encode(map[string]any{"alg": "private-alg", "typ": "private-type", "kid": "private-id", "extra": "private-header"})
	claims := encode(map[string]any{"aud": "private-audience", "sub": "private-subject", "iat": now.Unix() + 10, "exp": now.Unix() + 130})
	token := "Ghost " + header + "." + claims + ".private-signature"
	shape := jwtShape(token)
	b, _ := json.Marshal(shape)
	for _, secret := range []string{"private-", header, claims, token} {
		if strings.Contains(string(b), secret) {
			t.Fatal("token value leaked")
		}
	}
	if shape["iat_ahead_bucket"] != "over_five_seconds" || shape["lifetime_seconds"] != "120" || shape["alg"] != "other" {
		t.Fatal(shape)
	}
	for _, malformed := range []string{"", "Ghost", "Ghost not.a.token", "private-scheme opaque", "Ghost " + strings.Repeat("x", 8193)} {
		b, _ := json.Marshal(jwtShape(malformed))
		if strings.Contains(string(b), "private-") {
			t.Fatal("malformed token leaked")
		}
	}
}
