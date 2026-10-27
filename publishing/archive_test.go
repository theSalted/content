package publishing

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func archiveFixture(t *testing.T) Archive {
	t.Helper()
	at, _ := time.Parse(time.RFC3339, "2020-02-03T12:00:00Z")
	a, e := MapArchive(Intent{Publication: "example", EventID: "11111111-1111-4111-8111-111111111111", Kind: "post.published", Revision: 4, OccurredAt: at.Add(time.Hour), Snapshot: Snapshot{ID: "one", UUID: "22222222-2222-4222-8222-222222222222", Title: "Historical", Slug: "historical", Status: "published", Visibility: "public", PublishedAt: &at, HTML: "<p>Original</p>"}}, Enrollment{"activitypub", "content", "https://example.invalid/blog/"})
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func TestArchiveContract(t *testing.T) {
	a := archiveFixture(t)
	raw, e := EncodeArchive(a)
	if e != nil {
		t.Fatal(e)
	}
	decoded, e := DecodeArchive(raw)
	if e != nil || decoded.ContentID != a.ContentID || decoded.Document.PublishedAt != "2020-02-03T12:00:00Z" || decoded.Document.URL != "https://example.invalid/blog/historical/" {
		t.Fatal(decoded, e)
	}
	again, _ := EncodeArchive(decoded)
	if !bytes.Equal(raw, again) {
		t.Fatal("encoding unstable")
	}
	if _, e = Decode(raw); e == nil {
		t.Fatal("archive accepted as live event")
	}
	for _, mutate := range []func(*Archive){func(a *Archive) { a.SourceState = "draft" }, func(a *Archive) { a.DeliveryPolicy = "live" }, func(a *Archive) { a.Document.Visibility = "members" }, func(a *Archive) { a.ObjectInstance = a.EventID }, func(a *Archive) { a.Document.HTML = "bad\x00" }} {
		b := archiveFixture(t)
		mutate(&b)
		if _, e := EncodeArchive(b); e == nil {
			t.Fatal("unsafe archive accepted")
		}
	}
	if _, e := DecodeArchive(append(raw, []byte(` {}`)...)); e == nil {
		t.Fatal("trailing JSON accepted")
	}
}

type archiveRoundTrip func(*http.Request) (*http.Response, error)

func (f archiveRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestArchiveHTTPRouteAndRetry(t *testing.T) {
	var bodies [][]byte
	client, e := NewArchiveHTTPClient("https://example.invalid/internal/v2/archive", strings.Repeat("x", 32))
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	client.client.Transport = archiveRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/internal/v2/archive" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("x", 32) {
			t.Error("wrong target or auth")
		}
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, body)
		status := 503
		ack := ""
		if len(bodies) > 1 {
			status = 202
			ack = `{"result":"duplicate","original_result":"applied"}`
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(ack)), Header: make(http.Header)}, nil
	})
	raw, _ := EncodeArchive(archiveFixture(t))
	if out := client.Send(context.Background(), raw); out.State != "retry" {
		t.Fatal(out)
	}
	if out := client.Send(context.Background(), raw); out.State != "accepted" || out.Result != "applied" {
		t.Fatal(out)
	}
	if !bytes.Equal(bodies[0], bodies[1]) {
		t.Fatal("retry changed bytes")
	}
	for _, path := range []string{"/internal/v2/events", "/elsewhere", "/internal/v2/archive?token=x"} {
		if _, e := NewArchiveHTTPClient("https://example.invalid"+path, strings.Repeat("x", 32)); e == nil {
			t.Fatal("wrong endpoint accepted")
		}
	}
}
