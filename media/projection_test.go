package media

import (
	"strings"
	"testing"
)

func TestOwnedImageResponseProjection(t *testing.T) {
	primary, old := "https://cms.example.invalid", "https://retired.example.invalid"
	path := "/content/images/0123456789abcdef01234567/image.png"
	s := &Service{Origin: primary, OriginAliases: []string{old, "https://cdn.example.invalid"}}
	for _, tc := range []struct{ name, source, want string }{
		{"quoted", `<p><img src="` + old + path + `" alt="A &amp; B"></p>`, `<p><img src="` + primary + path + `" alt="A &amp; B"></p>`},
		{"formatting", "<IMG\tSRC = '" + old + path + "' width=3 />", "<IMG\tSRC = '" + primary + path + "' width=3 />"},
		{"unquoted", `<img src=` + old + path + `>`, `<img src=` + primary + path + `>`},
		{"boolean_attribute", `<img loading src="` + old + path + `">`, `<img loading src="` + primary + path + `">`},
		{"srcset_unknown_candidate", `<img srcset=" ` + old + path + ` 1x,\t` + primary + path + ` 2x">`, `<img srcset=" ` + primary + path + ` 1x,\t` + primary + path + ` 2x">`},
		{"srcset_spacing", "<img srcset=' " + old + path + " 1x,\t" + old + path + " 2x '>", "<img srcset=' " + primary + path + " 1x,\t" + primary + path + " 2x '>"},
		{"width_candidates", `<img srcset="` + old + path + ` 320w, https://foreign.invalid/x.png 640w">`, `<img srcset="` + primary + path + ` 320w, https://foreign.invalid/x.png 640w">`},
		{"decoded_entity", `<img src="https&#58;//retired.example.invalid` + path + `">`, `<img src="` + primary + path + `">`},
		{"other_alias", `<img src="https://cdn.example.invalid` + path + `">`, `<img src="` + primary + path + `">`},
		{"same_origin", `<img src="` + primary + path + `">`, `<img src="` + primary + path + `">`},
		{"link_text_alt", `<a href="` + old + path + `">` + old + path + `</a><img alt='src="` + old + path + `"' src="` + old + path + `">`, `<a href="` + old + path + `">` + old + path + `</a><img alt='src="` + old + path + `"' src="` + primary + path + `">`},
		{"comment_and_raw_text", `<!-- <img src="` + old + path + `"> --><script>"<img src='` + old + path + `'>"</script><textarea><img src="` + old + path + `"></textarea>`, `<!-- <img src="` + old + path + `"> --><script>"<img src='` + old + path + `'>"</script><textarea><img src="` + old + path + `"></textarea>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := s.ProjectHTML(tc.source)
			if got != tc.want {
				t.Fatalf("projection differs\ngot %s\nwant %s", got, tc.want)
			}
			if s.ProjectHTML(got) != got {
				t.Fatal("projection is not idempotent")
			}
		})
	}
}

func TestOwnedProjectionRefusesUnknownOrAmbiguousURLs(t *testing.T) {
	old := "https://retired.example.invalid"
	path := "/content/images/0123456789abcdef01234567/image.png"
	s := &Service{Origin: "https://cms.example.invalid", OriginAliases: []string{old}}
	for _, raw := range []string{
		"https://foreign.invalid" + path, "//retired.example.invalid" + path, path,
		old + path + "?", old + path + "?token=private", old + path + "#", old + path + "#fragment",
		"https://user:secret@retired.example.invalid" + path, "http://retired.example.invalid" + path,
		"https://retired.example.invalid.evil.invalid" + path, old + "/content/images/%30123456789abcdef01234567/image.png",
		old + "/content/images/invalid/image.png", old + path + "/", strings.TrimSuffix(old+path, "png") + "svg",
		"data:image/png;base64,abc", "javascript:alert(1)", old + path + "&#63;token=private",
	} {
		source := `<img src="` + raw + `">`
		if got := s.ProjectHTML(source); got != source {
			t.Fatalf("unsafe/unknown URL changed: %q", raw)
		}
	}
	for _, source := range []string{
		`<img src="` + old + path + `" src="https://foreign.invalid/other.png">`,
		`<img srcset="` + old + path + ` 1x, data:image/png;base64,abc 2x">`,
		`<img srcset="` + old + path + ` 1x,">`,
		`<img srcset="` + old + path + ` 1x 2x">`,
		`<img src="` + old + path,
		`<source srcset="` + old + path + ` 1x">`,
	} {
		if got := s.ProjectHTML(source); got != source {
			t.Fatalf("ambiguous source changed: %s", source)
		}
	}
	if got := (&Service{Origin: s.Origin}).ProjectHTML(`<img src="` + old + path + `">`); strings.Contains(got, s.Origin) {
		t.Fatal("absent aliases normalized")
	}
}

func FuzzOwnedImageProjection(f *testing.F) {
	f.Add(`<img src="https://retired.example.invalid/content/images/0123456789abcdef01234567/image.png">`)
	f.Add(`<img alt='x > src="bad"' srcset="x 1x, y 2x">`)
	f.Fuzz(func(t *testing.T, source string) {
		s := &Service{Origin: "https://cms.example.invalid", OriginAliases: []string{"https://retired.example.invalid"}}
		got := s.ProjectHTML(source)
		if s.ProjectHTML(got) != got {
			t.Fatal("non-idempotent projection")
		}
	})
}
