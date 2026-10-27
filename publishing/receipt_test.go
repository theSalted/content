package publishing

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func receiptFixture(t *testing.T) ([]byte, ReceiptResolution) {
	t.Helper()
	b, err := os.ReadFile("testdata/v2-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Event json.RawMessage `json:"event"`
		Valid bool            `json:"valid"`
	}
	if json.Unmarshal(b, &cases) != nil {
		t.Fatal("fixture")
	}
	for _, c := range cases {
		if !c.Valid {
			continue
		}
		e, err := Decode(c.Event)
		if err != nil || e.Document == nil {
			continue
		}
		hash, err := CanonicalEventHash(c.Event)
		if err != nil {
			t.Fatal(err)
		}
		return c.Event, ReceiptResolution{SchemaVersion: 1, EventID: e.EventID, Publication: e.Publication, ContentID: e.ContentID, Source: e.Source, Revision: e.Revision, EventHash: hash, Result: "applied", Projection: &ResolvedObject{ObjectID: "https://example.invalid/objects/confirmed", ObjectInstance: e.ObjectInstance, Revision: e.Revision, State: "public"}, Reason: "confirmed"}
	}
	t.Fatal("no public fixture")
	return nil, ReceiptResolution{}
}
func TestReceiptResolutionBinding(t *testing.T) {
	raw, r := receiptFixture(t)
	encode := func(x ReceiptResolution) []byte {
		b, err := json.Marshal(x)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if _, err := DecodeReceipt(raw, encode(r)); err != nil {
		t.Fatal(err)
	}
	cases := []func(*ReceiptResolution){func(x *ReceiptResolution) { x.EventID = "00000000-0000-4000-8000-000000000001" }, func(x *ReceiptResolution) { x.Publication = "other" }, func(x *ReceiptResolution) { x.ContentID = "00000000-0000-4000-8000-000000000001" }, func(x *ReceiptResolution) { x.Source.Epoch = "2" }, func(x *ReceiptResolution) { x.Source.ID = "other" }, func(x *ReceiptResolution) { x.Revision = "999" }, func(x *ReceiptResolution) { x.EventHash = strings.Repeat("0", 64) }, func(x *ReceiptResolution) { x.Result = "stale" }, func(x *ReceiptResolution) { x.Projection.ObjectInstance = "00000000-0000-4000-8000-000000000001" }, func(x *ReceiptResolution) { x.Projection.Revision = "0" }, func(x *ReceiptResolution) { x.Projection.ObjectID = "https://user:secret@example.invalid/x" }, func(x *ReceiptResolution) { x.Projection.State = "withdrawn" }, func(x *ReceiptResolution) { x.Reason = "superseded" }}
	for n, mutate := range cases {
		v := r
		p := *r.Projection
		v.Projection = &p
		mutate(&v)
		if _, err := DecodeReceipt(raw, encode(v)); err == nil {
			t.Fatal("binding mismatch accepted", n)
		}
	}
	for _, reason := range []string{"superseded", "not_public", "stale_receipt"} {
		v := r
		v.Projection = nil
		v.Reason = reason
		if reason == "stale_receipt" {
			v.Result = "stale"
		}
		if _, err := DecodeReceipt(raw, encode(v)); err != nil {
			t.Fatal(reason, err)
		}
	}
	var fields map[string]any
	json.Unmarshal(encode(r), &fields)
	delete(fields, "projection")
	b, _ := json.Marshal(fields)
	if _, err := DecodeReceipt(raw, b); err == nil {
		t.Fatal("missing projection accepted")
	}
	fields["projection"] = nil
	fields["reason"] = "not_public"
	fields["unexpected"] = "secret"
	b, _ = json.Marshal(fields)
	if _, err := DecodeReceipt(raw, b); err == nil {
		t.Fatal("unknown fields accepted")
	}
}
func TestReceiptHTTPIsReadOnlyBoundedAndPrivate(t *testing.T) {
	raw, receipt := receiptFixture(t)
	for _, tc := range []struct {
		status int
		want   string
	}{{200, "complete"}, {503, "retry"}, {404, "retry"}, {409, "quarantined"}, {403, "quarantined"}, {307, "quarantined"}} {
		requests := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			if r.URL.Path != "/internal/v2/receipts/resolve" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("x", 32) {
				t.Error("route or credential mismatch")
			}
			body, _ := io.ReadAll(r.Body)
			want := append([]byte(`{"schema_version":1,"event":`), raw...)
			want = append(want, '}')
			if !bytes.Equal(body, want) {
				t.Error("stored event changed")
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Location", "/internal/v2/events")
			w.WriteHeader(tc.status)
			json.NewEncoder(w).Encode(receipt)
		}))
		c, err := NewReceiptHTTPClient(server.URL+"/internal/v2/receipts/resolve", strings.Repeat("x", 32))
		if err != nil {
			t.Fatal(err)
		}
		out := c.Resolve(context.Background(), raw)
		c.Close()
		server.Close()
		if out.State != tc.want || requests != 1 {
			t.Fatal(tc, out.State, requests)
		}
	}
	for _, u := range []string{"http://example.invalid/internal/v2/receipts/resolve", "https://example.invalid/internal/v2/events", "https://example.invalid/internal/v2/receipts/resolve?token=secret"} {
		if _, err := NewReceiptHTTPClient(u, strings.Repeat("x", 32)); err == nil {
			t.Fatal("unsafe resolver")
		}
	}
}
func TestCanonicalReceiptHashMatchesActualNode(t *testing.T) {
	node, module := os.Getenv("CONTENT_TEST_NODE"), os.Getenv("CONTENT_TEST_ACTIVITYPUB_MODULE")
	if node == "" || module == "" {
		t.Skip("requires explicit Node/AP fixture paths")
	}
	raw, _ := receiptFixture(t)
	e, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	e.Document.HTML = "<p>台灣 & emoji 😀, separators \u2028\u2029, literal \\u2028 \\t</p>"
	e.Document.Title = "Résumé\t😀"
	e.Document.Tags = PublicTags{{Name: "标签 😀", Href: "https://example.invalid/tags/synthetic/"}}
	raw, err = Encode(e)
	if err != nil {
		t.Fatal(err)
	}
	archive := Archive{1, e.Publication, e.EventID, e.ContentID, e.ContentID, "archive", e.Source, e.Revision, e.OccurredAt, "content.archived", "published", e.Document}
	a, err := EncodeArchive(archive)
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range [][]byte{raw, a} {
		want, err := CanonicalEventHash(payload)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		script := `import {pathToFileURL} from 'node:url';const {eventHash}=await import(pathToFileURL(process.argv[1]).href);let input='';for await(const chunk of process.stdin)input+=chunk;process.stdout.write(eventHash(JSON.parse(input)));`
		cmd := exec.CommandContext(ctx, node, "--input-type=module", "-e", script, filepath.Join(module, "src", "contracts.ts"))
		cmd.Stdin = bytes.NewReader(payload)
		got, err := cmd.Output()
		cancel()
		if err != nil || string(got) != want {
			t.Fatal("Node/Go canonical receipt hash mismatch", err, string(got), want)
		}
	}
}
