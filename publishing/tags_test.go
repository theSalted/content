package publishing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAuthoredTagSharedFixtures(t *testing.T) {
	data, e := os.ReadFile("testdata/v2-tags.json")
	if e != nil {
		t.Fatal(e)
	}
	manifestBytes, e := os.ReadFile("testdata/activitypub-v2-manifest.json")
	if e != nil {
		t.Fatal(e)
	}
	var manifest struct {
		SHA map[string]string `json:"sha256"`
	}
	if json.Unmarshal(manifestBytes, &manifest) != nil {
		t.Fatal("manifest")
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != manifest.SHA["fixtures/v2-tags.json"] {
		t.Fatal("tag corpus drift")
	}
	var cases []struct {
		Name  string
		Valid bool
		Event json.RawMessage
	}
	if json.Unmarshal(data, &cases) != nil || len(cases) != 23 {
		t.Fatal("corpus")
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			e, err := Decode(c.Event)
			if (err == nil) != c.Valid {
				t.Fatal("Go/AP validation mismatch", err)
			}
			if c.Valid {
				encoded, err := Encode(e)
				if err != nil {
					t.Fatal(err)
				}
				again, err := Decode(encoded)
				if err != nil || len(again.Document.Tags) != len(e.Document.Tags) {
					t.Fatal("lost tags", err)
				}
			}
		})
	}
}
func TestPublicTagProjectionAndUnicode(t *testing.T) {
	origins := []string{"https://example.invalid"}
	raw := json.RawMessage(`[{"name":"Private","visibility":"internal"},{"name":"No explicit visibility"},{"name":" #hidden","visibility":"public"},{"name":"  中文 ✨  ","visibility":"public","url":"https://example.invalid/topics/writing/"}]`)
	tags, e := ProjectPublicTags(raw, origins)
	if e != nil || len(tags) != 1 || tags[0].Name != "  中文 ✨  " {
		t.Fatal(tags, e)
	}
	for _, raw := range []string{`null`, `[{"name":"Public","visibility":"public"}]`, `[{"name":"Public","visibility":"public","url":"https://foreign.invalid/topics/test"}]`, `[{"name":"\ud800","visibility":"public","url":"https://example.invalid/topics/test"}]`, `[{"name":"Public","visibility":"public","url":"https://example.invalid/topics/test?q=secret"}]`} {
		if _, e = ProjectPublicTags(json.RawMessage(raw), origins); e == nil {
			t.Fatal("invalid metadata accepted", raw)
		}
	}
	for _, raw := range []string{`"\ud800"`, `"\udc00"`, `"\ud800x"`, `"\ud800\ud800"`} {
		if validJSONString([]byte(raw)) {
			t.Fatal("lone surrogate accepted")
		}
	}
	for _, raw := range []string{`"\ud83d\ude00"`, `"\uFFFD"`, `"\\ud800"`, `"ordinary"`} {
		if !validJSONString([]byte(raw)) {
			t.Fatal("valid string rejected", raw)
		}
	}
	if e = (PublicTags{{strings.Repeat("😀", 100), "https://example.invalid/topic/emoji"}}).Validate(); e != nil {
		t.Fatal(e)
	}
	if e = (PublicTags{{strings.Repeat("😀", 101), "https://example.invalid/topic/emoji"}}).Validate(); e == nil {
		t.Fatal("code point limit")
	}
}
func TestTagMappingCopiesImmutableSnapshot(t *testing.T) {
	when := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	i := Intent{Publication: "example", EventID: "f7fe3e09-acda-484b-a374-61bdba466902", Kind: "post.published", Revision: 1, OccurredAt: when, Snapshot: Snapshot{UUID: "e7721079-2da1-43a6-9676-48b5ad030b78", Title: "Example", Slug: "example", Status: "published", Visibility: "public", PublishedAt: &when, Tags: PublicTags{{"Exact tag", "https://example.invalid/topics/exact/"}}}}
	cfg := Enrollment{Destination: "ap", SourceID: "content", SiteURL: "https://example.invalid/blog"}
	event, _, e := Map(i, cfg, "unpublished", "")
	if e != nil {
		t.Fatal(e)
	}
	archive, e := MapArchive(i, cfg)
	if e != nil || len(archive.Document.Tags) != 1 {
		t.Fatal(e)
	}
	i.Snapshot.Tags[0].Name = "Changed later"
	if event.Document.Tags[0].Name != "Exact tag" || archive.Document.Tags[0].Name != "Exact tag" {
		t.Fatal("mapped tags alias mutable source")
	}
	i.Kind = "post.unpublished"
	i.Revision = 2
	withdrawn, _, e := Map(i, cfg, "public", i.Snapshot.UUID)
	if e != nil || withdrawn.Document != nil {
		t.Fatal("withdrawal leaked tags", e)
	}
}
