package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestSiteDiscoveryIsPublicAndScoped(t *testing.T) {
	s, m, _ := setup(t)
	m.fail = true // Discovery must not depend on database access or expose errors.
	if err := WithSiteMetadata(SiteMetadata{Title: "Publication <one>", Description: "Plain description", Logo: "/content/images/logo.png"})(s); err != nil {
		t.Fatal(err)
	}
	for _, auth := range []string{"", "Ghost invalid", "Bearer invalid"} {
		r := httptest.NewRequest("GET", "/ghost/api/admin/site/", nil)
		r.Header.Set("Authorization", auth)
		r.Header.Set("Host", "attacker.invalid")
		r.Header.Set("X-Forwarded-Host", "attacker.invalid")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		var body map[string]any
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		want := map[string]any{"site": map[string]any{"title": "Publication <one>", "description": "Plain description", "logo": "/content/images/logo.png", "url": "https://example.invalid/blog/", "version": "6.0"}}
		if !reflect.DeepEqual(body, want) {
			t.Fatal(body)
		}
		if w.Header().Get("Content-Version") != "v6.0" || w.Header().Get("X-Content-Compatibility") != "posts-html-v1" || w.Header().Get("X-Ghost-Version") != "" {
			t.Fatal(w.Header())
		}
		if strings.Contains(w.Body.String(), "password") || strings.Contains(w.Body.String(), "publication-a") || len(m.posts) != 0 {
			t.Fatal("private state exposed or modified")
		}
	}
	other, err := New(m, "publication-b", "https://other.invalid/", s.admin, fixtureContentKey)
	if err != nil {
		t.Fatal(err)
	}
	w := request(other, "GET", "/ghost/api/admin/site/", "", "")
	if !strings.Contains(w.Body.String(), `"logo":null`) || !strings.Contains(w.Body.String(), `"url":"https://other.invalid/"`) || strings.Contains(w.Body.String(), "Publication") {
		t.Fatal(w.Body.String())
	}
}

func TestSiteDiscoveryRejectsUnsupportedRequests(t *testing.T) {
	s, _, _ := setup(t)
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"POST", "/ghost/api/admin/site/", 405}, {"PUT", "/ghost/api/admin/site/", 405},
		{"GET", "/ghost/api/admin/site/?fields=admin_key", 400}, {"GET", "/ghost/api/admin/site/?bad=%zz", 400},
		{"GET", "/ghost/api/admin/site/extra/", 404},
		{"GET", "/ghost/api/content/site/", 404},
	} {
		w := request(s, tc.method, tc.path, "", "")
		if w.Code != tc.status {
			t.Fatal(tc, w.Code, w.Body.String())
		}
		if tc.status == 405 && w.Header().Get("Allow") != "GET" {
			t.Fatal("missing Allow")
		}
	}
	for _, version := range []string{"", "v6.0", "v5.0", "v4.0", "v99.0", "invalid"} {
		r := httptest.NewRequest("GET", "/ghost/api/admin/site/", nil)
		r.Header.Set("Accept-Version", version)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		want := 400
		if version == "" || version == "v6.0" || version == "v5.0" || version == "v4.0" {
			want = 200
		}
		if w.Code != want {
			t.Fatal(version, w.Code)
		}
	}
}

func TestSiteMetadataValidation(t *testing.T) {
	s, _, _ := setup(t)
	for _, meta := range []SiteMetadata{
		{Title: " "}, {Title: strings.Repeat("x", 256)}, {Description: strings.Repeat("x", 2001)}, {Title: "bad\x00"},
		{Logo: "javascript:alert(1)"}, {Logo: "//other.invalid/logo.png"}, {Logo: "https://user:password@example.invalid/logo"},
		{Logo: "https://example.invalid/logo?secret=value"}, {Logo: "file:///tmp/logo"}, {Logo: "http://example.invalid/logo"},
	} {
		if _, err := New(s.store, "p", "https://example.invalid", s.admin, fixtureContentKey, WithSiteMetadata(meta)); err == nil {
			t.Fatal("accepted invalid metadata", meta)
		}
	}
	if _, err := New(s.store, "p", "https://example.invalid", s.admin, fixtureContentKey, WithSiteMetadata(SiteMetadata{Logo: "https://assets.invalid/logo.png"})); err != nil {
		t.Fatal(err)
	}
}
