package publishing

import (
	"content/cms"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestV2SharedFixtures(t *testing.T) {
	data, err := os.ReadFile("testdata/v2-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	manifestBytes, err := os.ReadFile("testdata/activitypub-v2-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Profile string            `json:"profile"`
		SHA     map[string]string `json:"sha256"`
	}
	if json.Unmarshal(manifestBytes, &manifest) != nil || manifest.Profile != "content-activitypub-v2-2026-10-26-r3-authored-tags" {
		t.Fatal("invalid fixture profile")
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != manifest.SHA["fixtures/v2-cases.json"] {
		t.Fatal("fixture copy differs from AP-owned manifest")
	}
	var cases []struct {
		Name  string          `json:"name"`
		Event json.RawMessage `json:"event"`
		Valid bool            `json:"valid"`
	}
	if json.Unmarshal(data, &cases) != nil {
		t.Fatal("invalid fixture corpus")
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			e, err := Decode(c.Event)
			if (err == nil) != c.Valid {
				t.Fatal("Go/Node validation mismatch", err)
			}
			if strings.HasPrefix(c.Name, "title-") {
				var raw Event
				if json.Unmarshal(c.Event, &raw) != nil {
					t.Fatal("invalid title fixture")
				}
				post, domainErr := cms.NewPost("fixture", cms.CreatePost{Title: raw.Document.Title})
				if (domainErr == nil) != c.Valid {
					t.Fatal("CMS/adapter title rule differs", domainErr)
				}
				if c.Valid && post.Title != raw.Document.Title {
					t.Fatal("CMS title changed")
				}
			}
			if c.Valid {
				if _, err = Encode(e); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
