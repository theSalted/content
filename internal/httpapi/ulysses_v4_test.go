package httpapi

import (
	"encoding/json"
	"github.com/golang-jwt/jwt/v5"
	"net/http/httptest"
	"testing"
)

func TestUlyssesObservedV4Discovery(t *testing.T) {
	s, _, _ := setup(t)
	for _, path := range []string{"/ghost/api/v4/admin/site/", "/ghost/api/v4/admin/site", "/ghost/api/admin/site"} {
		for _, version := range []string{"", "v4.0"} {
			r := httptest.NewRequest("GET", path, nil)
			r.Header.Set("Accept-Version", version)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatal(path, version, w.Code)
			}
			var body struct{ Site map[string]any }
			if json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Site["url"] != "https://example.invalid/blog/" {
				t.Fatal("discovery shape changed")
			}
		}
	}
}
func TestV4AudienceAliasesPreserveScope(t *testing.T) {
	s, m, _ := setup(t)
	claims := currentClaims()
	claims.Audience = jwt.ClaimStrings{"/v4/admin/"}
	v4 := signed(t, s.admin, jwt.SigningMethodHS256, claims, s.admin.id)
	// Official Ghost4 binds versioned routes to /v4/admin/; retain strict expiry,
	// signature, key ID and route-family checks while using the same CMS scope.
	w := request(s, "POST", "/ghost/api/v4/admin/posts?source=html", `{"posts":[{"title":"Ulysses synthetic","html":"<p>Private</p>","status":"draft"}]}`, v4)
	if w.Code != 201 || len(m.posts) != 1 || m.posts[0].Status != "draft" {
		t.Fatal(w.Code, w.Body.String())
	}
	if request(s, "GET", "/ghost/api/v4/admin/posts/", "", v4).Code != 200 {
		t.Fatal("v4 read rejected")
	}
	for _, family := range []string{"v2/", "v4/", ""} {
		for _, aud := range []string{"/v2/admin/", "/v4/admin/", "/admin/"} {
			claims.Audience = jwt.ClaimStrings{aud}
			auth := signed(t, s.admin, jwt.SigningMethodHS256, claims, s.admin.id)
			out := request(s, "GET", "/ghost/api/"+family+"admin/posts/", "", auth)
			want := 401
			if aud == "/"+family+"admin/" {
				want = 200
			}
			if out.Code != want {
				t.Fatal(family, aud, out.Code, want)
			}
		}
	}
	for _, path := range []string{"/ghost/api/v4/admin/users/me/", "/ghost/api/v4/admin/session/", "/ghost/api/v4/content/posts/", "/ghost/api/v4/admin/site/extra/"} {
		if request(s, "GET", path, "", v4).Code != 404 {
			t.Fatal("speculative endpoint exposed", path)
		}
	}
	if request(s, "GET", "/ghost/api/v4/admin/posts/", "", "Bearer "+v4).Code != 401 {
		t.Fatal("invalid auth accepted")
	}
}
