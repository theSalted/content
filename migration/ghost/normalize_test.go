package ghost

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestDatabaseRepresentationConversions(t *testing.T) {
	var r map[string]any
	json.Unmarshal([]byte(testRecord), &r)
	r["featured"] = 1
	r["html"] = `<p>__GHOST_URL__/text stays literal</p><img src="__GHOST_URL__/content/images/image.png" srcset="__GHOST_URL__/content/images/image.png 1x, __GHOST_URL__/content/images/image2.png 2x">`
	r["feature_image"] = "__GHOST_URL__/content/images/feature.png"
	raw, _ := json.Marshal(map[string]any{"db": []any{map[string]any{"data": map[string]any{"posts": []any{r}}}}})
	p, e := Build(raw, "publication")
	if e != nil || p.Records[0].Reason != "source_url_required" {
		t.Fatal("placeholder requires explicit origin", e)
	}
	p, e = BuildWithOptions(raw, "publication", Options{SourceURL: "https://source.example/blog/"})
	if e != nil || p.Records[0].Post == nil {
		t.Fatal(e, p.Records[0].Reason)
	}
	post := p.Records[0].Post
	if string(post.Presentation["featured"]) != "true" || !strings.Contains(post.HTML, `src="https://source.example/blog/content/images/image.png"`) || !strings.Contains(post.HTML, "<p>__GHOST_URL__/text stays literal</p>") || !bytes.Equal(p.Raw, raw) || !bytes.Contains(p.Records[0].Raw, []byte("__GHOST_URL__")) {
		t.Fatal("representation conversion lost provenance")
	}
	other, e := BuildWithOptions(raw, "publication", Options{SourceURL: "https://other.example"})
	if e != nil || p.Records[0].SHA256 == other.Records[0].SHA256 {
		t.Fatal("mapping change not detected")
	}
	for _, origin := range []string{"http://example.org", "https://user:pass@example.org", "https://example.org/?secret=1", "https://example.org/#fragment", "https://example.org/\""} {
		if _, e = BuildWithOptions(raw, "publication", Options{SourceURL: origin}); e == nil {
			t.Fatal("unsafe origin accepted")
		}
	}
	r["featured"] = 2
	raw, _ = json.Marshal(map[string]any{"db": []any{map[string]any{"data": map[string]any{"posts": []any{r}}}}})
	p, e = BuildWithOptions(raw, "publication", Options{SourceURL: "https://source.example"})
	if e != nil || p.Records[0].Post != nil || p.Records[0].Reason != "invalid_public_metadata" {
		t.Fatal("invalid boolean coerced", e)
	}
}

func TestMatchedPublicSnapshotPreservesComputedMetadata(t *testing.T) {
	full := []byte(`{"db":[{"data":{"posts":[` + strings.Replace(testRecord, `"excerpt":"Original excerpt",`, "", 1) + `]}}]}`)
	snapshot := []byte(`{"scope":"public Content API only; not a full private/staff/database backup","origin":"https://source.example","data":{"posts":[` + testRecord + `]}}`)
	options := Options{SourceURL: "https://source.example", PublicSnapshot: snapshot}
	p, e := BuildWithOptions(full, "p", options)
	if e != nil || p.Records[0].Post == nil || string(p.Records[0].Post.Presentation["excerpt"]) != `"Original excerpt"` || p.PublicSnapshotSHA256 == "" || !bytes.Equal(p.Options.PublicSnapshot, snapshot) {
		t.Fatal("snapshot metadata/provenance lost", e)
	}
	for _, bad := range [][]byte{bytes.Replace(snapshot, []byte("Historical"), []byte("Different revision"), 1), bytes.Replace(snapshot, []byte("https://source.example"), []byte("https://other.example"), 1), bytes.Replace(snapshot, []byte(`"status":"published"`), []byte(`"status":"draft"`), 1)} {
		options.PublicSnapshot = bad
		if _, e = BuildWithOptions(full, "p", options); e == nil {
			t.Fatal("mismatched/nonpublic snapshot accepted")
		}
	}
}
