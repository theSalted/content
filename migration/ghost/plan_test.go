package ghost

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const testRecord = `{"id":"0123456789abcdef01234567","uuid":"12345678-1234-4234-8234-123456789abc","title":"Historical","slug":"historical","html":"<p>Original &amp; exact</p>","status":"published","visibility":"public","created_at":"2020-01-02T03:04:05.000Z","updated_at":"2021-02-03T04:05:06.123Z","published_at":"2020-01-03T03:04:05.000Z","excerpt":"Original excerpt","meta_title":"SEO title","authors":[{"id":"0123456789abcdef01234568","name":"Author","slug":"author","email":"private@example.invalid","password":"never-public"}],"lexical":"original-source"}`

func TestHistoricalIdentityDatesAndMetadata(t *testing.T) {
	raw := []byte(`{"db":[{"data":{"posts":[` + testRecord + `]}}]}`)
	p, e := Build(raw, "test-publication")
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(p.Raw, raw) || p.PublicOnly || len(p.Records) != 1 {
		t.Fatal("archive altered")
	}
	r := p.Records[0]
	if r.Post == nil {
		t.Fatal(r.Reason)
	}
	if r.Post.ID != "0123456789abcdef01234567" || r.Post.UUID != "12345678-1234-4234-8234-123456789abc" || r.Post.CreatedAt.Year() != 2020 || r.Post.UpdatedAt.Nanosecond() != 123000000 || r.Post.PublishedAt.Year() != 2020 || r.Post.HTML != "<p>Original &amp; exact</p>" {
		t.Fatal("historical data changed")
	}
	metadata, _ := json.Marshal(r.Post.Presentation)
	if bytes.Contains(metadata, []byte("never-public")) || bytes.Contains(metadata, []byte("email")) || bytes.Contains(metadata, []byte("lexical")) {
		t.Fatal("private source projected")
	}
	if !bytes.Contains(r.Raw, []byte("original-source")) {
		t.Fatal("source lost")
	}
}

func TestQuarantineNeverSilentlyRewrites(t *testing.T) {
	for _, tc := range []struct {
		field string
		value any
	}{{"html", "<script>alert(1)</script>"}, {"status", "scheduled"}, {"visibility", "members"}, {"type", "unsupported"}, {"published_at", nil}, {"id", "invalid"}, {"updated_at", "2021-02-03T04:05:06.123456Z"}} {
		var record map[string]any
		_ = json.Unmarshal([]byte(testRecord), &record)
		record[tc.field] = tc.value
		raw, _ := json.Marshal(map[string]any{"db": []any{map[string]any{"data": map[string]any{"posts": []any{record}}}}})
		p, e := Build(raw, "test-publication")
		if e != nil {
			t.Fatal(e)
		}
		if p.Records[0].Post != nil || p.Records[0].Reason == "" {
			t.Fatal("unsupported record activated", tc.field)
		}
		if !bytes.Equal(p.Raw, raw) {
			t.Fatal("quarantine changed source")
		}
	}
}

func TestPublicExportAndDuplicateProtection(t *testing.T) {
	var record map[string]any
	_ = json.Unmarshal([]byte(testRecord), &record)
	delete(record, "status")
	envelope := map[string]any{"scope": "public Content API only; not a full private/staff/database backup", "data": map[string]any{"posts": []any{record}}}
	raw, _ := json.Marshal(envelope)
	p, e := Build(raw, "test-publication")
	if e != nil || !p.PublicOnly || p.Records[0].Post == nil {
		t.Fatal(e, p)
	}
	envelope["data"] = map[string]any{"posts": []any{record, record}}
	raw, _ = json.Marshal(envelope)
	if _, e = Build(raw, "test-publication"); e == nil {
		t.Fatal("duplicate accepted")
	}
}

func TestDatabaseExportRelationsAndTheirChanges(t *testing.T) {
	var record map[string]any
	json.Unmarshal([]byte(testRecord), &record)
	delete(record, "authors")
	data := map[string]any{"posts": []any{record}, "users": []any{map[string]any{"id": "0123456789abcdef01234568", "name": "Author", "slug": "author", "password": "private-hash"}}, "posts_authors": []any{map[string]any{"post_id": record["id"], "author_id": "0123456789abcdef01234568", "sort_order": 0}}, "tags": []any{map[string]any{"id": "tag-id", "name": "Tag", "slug": "tag", "visibility": "public"}}, "posts_tags": []any{map[string]any{"post_id": record["id"], "tag_id": "tag-id", "sort_order": 0}}}
	raw, _ := json.Marshal(map[string]any{"db": []any{map[string]any{"data": data}}})
	p, e := Build(raw, "publication")
	if e != nil || p.Records[0].Post == nil {
		t.Fatal(e, p.Records[0].Reason)
	}
	if p.Records[0].Post.Author.Name != "Author" || !bytes.Contains(p.Records[0].Post.Presentation["tags"], []byte("Tag")) || bytes.Contains(p.Records[0].Post.Presentation["authors"], []byte("private-hash")) {
		t.Fatal("relations not safely resolved")
	}
	data["tags"].([]any)[0].(map[string]any)["name"] = "Changed tag"
	raw, _ = json.Marshal(map[string]any{"db": []any{map[string]any{"data": data}}})
	changed, e := Build(raw, "publication")
	if e != nil || changed.Records[0].SHA256 == p.Records[0].SHA256 {
		t.Fatal("related source change not detected")
	}
}

func TestPageImportRemainsPage(t *testing.T) {
	raw := []byte(`{"scope":"public Content API only; not a full private/staff/database backup","data":{"pages":[` + testRecord + `]}}`)
	p, e := Build(raw, "publication")
	if e != nil || p.Records[0].Post == nil || p.Records[0].Post.Kind != "page" {
		t.Fatal(e, p)
	}
	raw = []byte(`{"db":[{"data":{"posts":[` + strings.Replace(testRecord, `"status":"published"`, `"type":"page","status":"published"`, 1) + `]}}]}`)
	p, e = Build(raw, "publication")
	if e != nil || p.Records[0].Post == nil || p.Records[0].Post.Kind != "page" {
		t.Fatal(e, p)
	}
}
