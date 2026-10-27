package httpapi

import (
	"content/cms"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebsiteV5FieldsAndMetadata(t *testing.T) {
	s, m, auth := setup(t)
	w := request(s, "POST", "/ghost/api/admin/posts/?source=html", `{"posts":[{"title":"Historical","slug":"historical","html":"<p>Body</p>","status":"published"}]}`, auth)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	m.posts[0].Presentation = cms.Presentation{"excerpt": json.RawMessage(`"Preserved excerpt"`), "tags": json.RawMessage(`[{"id":"tag","name":"Public","slug":"public"}]`), "authors": json.RawMessage(`[{"id":"author","name":"Public author","email":"private@example.invalid"}]`), "lexical": json.RawMessage(`"private"`)}
	cases := []struct {
		query  string
		want   []string
		absent []string
	}{
		{"fields=title", []string{"title"}, []string{"id", "html", "authors", "tags", "excerpt"}},
		{"fields=id,title,excerpt,source_html,lexical,bogus&include=authors,tags", []string{"id", "title", "excerpt", "authors", "tags"}, []string{"source_html", "lexical", "bogus", "primary_author"}},
		{"formats=plaintext", []string{"plaintext", "excerpt", "url"}, []string{"html", "authors", "tags"}},
	}
	for _, tc := range cases {
		r := httptest.NewRequest("GET", "/ghost/api/content/posts/?key="+fixtureContentKey+"&"+tc.query, nil)
		r.Header.Set("Accept-Version", "v5.0")
		w = httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 200 || strings.Contains(w.Body.String(), "private@example.invalid") {
			t.Fatal(w.Code, w.Body.String())
		}
		var body struct{ Posts []map[string]any }
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		p := body.Posts[0]
		for _, key := range tc.want {
			if _, ok := p[key]; !ok {
				t.Fatal("missing", key, w.Body.String())
			}
		}
		for _, key := range tc.absent {
			if _, ok := p[key]; ok {
				t.Fatal("unexpected", key)
			}
		}
		if val, ok := p["excerpt"]; ok && val != "Preserved excerpt" {
			t.Fatal("excerpt lost")
		}
		if val, ok := p["url"]; ok && val != "https://example.invalid/blog/historical/" {
			t.Fatal("URL route lost")
		}
	}
}
