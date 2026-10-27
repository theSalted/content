package publishing

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPDeliveryOutcomes(t *testing.T) {
	for _, c := range []struct {
		name                string
		status              int
		body, state, result string
	}{
		{"applied", 202, `{"result":"applied","object_id":"https://example.invalid/ap/objects/test"}`, "accepted", "applied"},
		{"stale", 202, `{"result":"stale","object_id":"https://example.invalid/ap/objects/test"}`, "accepted", "stale"},
		{"duplicate", 202, `{"result":"duplicate","original_result":"applied"}`, "accepted", "applied"},
		{"duplicate-stale", 202, `{"result":"duplicate","original_result":"stale"}`, "accepted", "stale"},
		{"bad-ack", 202, `{"result":"applied"}`, "retry", ""},
		{"trailing-ack", 202, `{"result":"duplicate","original_result":"applied"} {}`, "retry", ""},
		{"unknown-ack", 202, `{"result":"duplicate","original_result":"applied","x":1}`, "retry", ""},
		{"server-error", 503, `{"secret":"never retain this"}`, "retry", ""},
		{"rate-limit", 429, ``, "retry", ""},
		{"conflict", 409, ``, "quarantined", ""},
		{"unauthorized", 401, ``, "quarantined", ""},
		{"oversized", 413, ``, "quarantined", ""},
		{"wrong-success", 200, `{}`, "quarantined", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				if string(b) != `{"synthetic":"unchanged"}` || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("x", 32) || r.Header.Get("Content-Type") != "application/json" {
					t.Error("request altered")
				}
				w.WriteHeader(c.status)
				_, _ = io.WriteString(w, c.body)
			}))
			defer server.Close()
			client, err := NewHTTPClient(server.URL+"/internal/v2/events", strings.Repeat("x", 32))
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			outcome := client.Send(context.Background(), []byte(`{"synthetic":"unchanged"}`))
			if outcome.State != c.state || outcome.Result != c.result {
				t.Fatal(outcome)
			}
		})
	}
}
func TestHTTPDeliveryDoesNotFollowRedirectAndTimesOut(t *testing.T) {
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer redirect.Close()
	c, err := NewHTTPClient(redirect.URL+"/internal/v2/events", strings.Repeat("x", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if out := c.Send(context.Background(), []byte(`{}`)); out.State != "quarantined" || calls.Load() != 0 {
		t.Fatal("redirect followed", out, calls.Load())
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(200 * time.Millisecond):
		}
	}))
	defer slow.Close()
	c2, _ := NewHTTPClient(slow.URL+"/internal/v2/events", strings.Repeat("x", 32))
	defer c2.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if out := c2.Send(ctx, []byte(`{}`)); out.State != "retry" || out.Code != "transport_error" {
		t.Fatal(out)
	}
	for _, endpoint := range []string{"http://example.invalid/internal/v2/events", "https://user:secret@example.invalid/internal/v2/events", "https://example.invalid/internal/v2/events?x=1", "https://example.invalid/other"} {
		if _, err := NewHTTPClient(endpoint, strings.Repeat("x", 32)); err == nil {
			t.Fatal("invalid endpoint accepted")
		}
	}
}

type panicQueue struct{}

func (panicQueue) PrepareNext(context.Context, string, string) (bool, error) {
	panic("disabled queue access")
}
func (panicQueue) Claim(context.Context, string, string, time.Duration) (*Job, error) {
	panic("disabled queue access")
}
func (panicQueue) Finish(context.Context, Job, Outcome) error { panic("disabled queue access") }

type fakeSender struct{ calls int }

func (s *fakeSender) Send(context.Context, []byte) Outcome {
	s.calls++
	return Outcome{State: "retry", Code: "transport_error"}
}

type fakeQueue struct {
	job Job
	out Outcome
}

func (q *fakeQueue) PrepareNext(context.Context, string, string) (bool, error) { return false, nil }
func (q *fakeQueue) Claim(context.Context, string, string, time.Duration) (*Job, error) {
	return &q.job, nil
}
func (q *fakeQueue) Finish(_ context.Context, _ Job, o Outcome) error { q.out = o; return nil }
func TestWorkerDisabledAndRetryBudget(t *testing.T) {
	sender := &fakeSender{}
	w := Worker{Queue: panicQueue{}, Sender: sender}
	if worked, err := w.Step(context.Background()); err != nil || worked {
		t.Fatal(worked, err)
	}
	if err := w.Run(context.Background()); err != nil || sender.calls != 0 {
		t.Fatal("disabled work performed")
	}
	q := &fakeQueue{job: Job{Attempts: 1}}
	w = Worker{Enabled: true, Publication: "example", Destination: "ap", Queue: q, Sender: sender, Timeout: time.Second, Lease: 10 * time.Second, Idle: time.Millisecond, MaxAttempts: 3}
	_, err := w.Step(context.Background())
	if err != nil || q.out.State != "retry" || q.out.Delay < time.Second || q.out.Delay > 2*time.Second {
		t.Fatal(q.out, err)
	}
	q.job.Attempts = 3
	_, err = w.Step(context.Background())
	if err != nil || q.out.State != "quarantined" {
		t.Fatal(q.out, err)
	}
	calls := sender.calls
	q.job.Attempts = 4
	_, err = w.Step(context.Background())
	if err != nil || sender.calls != calls || q.out.Code != "attempt_limit" {
		t.Fatal("crash exhausted job resent")
	}
	for i := 1; i < 25; i++ {
		d := Backoff(i)
		if d < time.Second || d > 5*time.Minute {
			t.Fatal("unbounded backoff", d)
		}
	}
}
func TestMapLifecycleAndLimits(t *testing.T) {
	now := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	cfg := Enrollment{"ap", "content", "https://example.invalid/blog"}
	i := Intent{Publication: "example", EventID: "f7fe3e09-acda-484b-a374-61bdba466902", Kind: "post.published", Revision: 1, OccurredAt: now, Snapshot: Snapshot{UUID: "e7721079-2da1-43a6-9676-48b5ad030b78", Title: "中文 café", HTML: "<p>中文 &amp; café</p>", Slug: "sample", Status: "published", Visibility: "public", PublishedAt: &now}}
	e, state, err := Map(i, cfg, "unpublished", "")
	if err != nil || state != "public" || e.ObjectInstance != i.Snapshot.UUID || e.Document.URL != "https://example.invalid/blog/sample/" {
		t.Fatal(e, err)
	}
	raw, err := Encode(e)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if json.Unmarshal(raw, &parsed) != nil {
		t.Fatal("invalid payload")
	}
	if parsed["revision"] != "1" || parsed["source"].(map[string]any)["epoch"] != "1" {
		t.Fatal("numeric counter")
	}
	i.Kind = "post.unpublished"
	i.Revision = 2
	i.Snapshot.HTML = "private draft"
	i.Snapshot.Status = "draft"
	withdrawn, state, err := Map(i, cfg, state, e.ObjectInstance)
	if err != nil || withdrawn.Document != nil || state != "withdrawn" {
		t.Fatal(withdrawn, err)
	}
	i.Kind = "post.published"
	i.Revision = 3
	i.Snapshot.Status = "published"
	again, state, err := Map(i, cfg, state, withdrawn.ObjectInstance)
	if err != nil || again.ContentID != e.ContentID || again.ObjectInstance == e.ObjectInstance || state != "public" {
		t.Fatal(again, err)
	}
	i.Snapshot.Title = strings.Repeat("😀", 255)
	if mapped, _, err := Map(i, cfg, "withdrawn", e.ObjectInstance); err != nil || mapped.Document.Title != i.Snapshot.Title {
		t.Fatal("valid Unicode title changed or rejected", err)
	}
	i.Snapshot.Title += "😀"
	if _, _, err := Map(i, cfg, "withdrawn", e.ObjectInstance); err == nil {
		t.Fatal("code point title overflow accepted")
	}
	i.Snapshot.Title = "Valid"
	i.Snapshot.HTML = strings.Repeat("a", 1<<20)
	if _, _, err := Map(i, cfg, "withdrawn", e.ObjectInstance); err != nil {
		t.Fatal("1 MiB HTML rejected", err)
	}
	i.Snapshot.HTML += "a"
	if _, _, err := Map(i, cfg, "withdrawn", e.ObjectInstance); err == nil {
		t.Fatal("oversized HTML accepted")
	}
}

type canceledQueue struct{ panicQueue }

func (canceledQueue) PrepareNext(ctx context.Context, _ string, _ string) (bool, error) {
	<-ctx.Done()
	return false, ctx.Err()
}
func TestWorkerRunCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	w := Worker{Enabled: true, Publication: "example", Destination: "ap", Queue: canceledQueue{}, Sender: &fakeSender{}, Timeout: time.Second, Lease: 10 * time.Second, Idle: time.Millisecond, MaxAttempts: 3}
	if err := w.Run(ctx); err != nil {
		t.Fatal("shutdown should leave recoverable work, not fail", err)
	}
}
