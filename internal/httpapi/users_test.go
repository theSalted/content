package httpapi

import (
	"content/cms"
	"encoding/json"
	"github.com/golang-jwt/jwt/v5"
	"strings"
	"testing"
)

func TestObservedUlyssesSiteThenCurrentIdentity(t *testing.T) {
	s, m, _ := setup(t)
	author := cms.Author{ID: "1234567890abcdef12345678", Name: "Synthetic Author", Slug: "synthetic-author"}
	if err := WithPublishingIdentity(author)(s); err != nil {
		t.Fatal(err)
	}
	claims := currentClaims()
	claims.Audience = jwt.ClaimStrings{"/v4/admin/"}
	auth := signed(t, s.admin, jwt.SigningMethodHS256, claims, s.admin.id)
	for _, path := range []string{"/ghost/api/v4/admin/site/", "/ghost/api/v4/admin/users/me/"} {
		w := request(s, "GET", path, "", auth)
		if w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	w := request(s, "GET", "/ghost/api/v4/admin/users/me/", "", auth)
	var result struct {
		Users []map[string]any `json:"users"`
	}
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Users) != 1 {
		t.Fatal("identity envelope")
	}
	u := result.Users[0]
	if u["id"] != author.ID || u["name"] != author.Name || u["slug"] != author.Slug || len(u["roles"].([]any)) != 0 {
		t.Fatal("identity binding/roles")
	}
	for _, key := range []string{"email", "password", "api_key", "permissions", "last_seen", "created_at"} {
		if _, ok := u[key]; ok {
			t.Fatal("private or fabricated staff field", key)
		}
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Identity") != "publishing-profile" {
		t.Fatal("identity response policy")
	}
	if len(m.posts) != 0 {
		t.Fatal("handshake wrote content")
	}
	// A new profile query must not grant any additional mutation surface.
	for _, method := range []string{"POST", "PUT", "DELETE"} {
		if w := request(s, method, "/ghost/api/v4/admin/users/me/", `{"users":[{"roles":[{"name":"Owner"}]}]}`, auth); w.Code != 405 {
			t.Fatal("identity mutation", method, w.Code)
		}
	}
	for _, path := range []string{"/ghost/api/v4/admin/users/", "/ghost/api/v4/admin/users/other/", "/ghost/api/v4/admin/users/me/token/", "/ghost/api/v4/admin/session/", "/ghost/api/v4/admin/roles/", "/ghost/api/v4/admin/settings/"} {
		if w := request(s, "GET", path, "", auth); w.Code != 404 {
			t.Fatal("scope expanded", path, w.Code)
		}
	}
}
func TestPublishingIdentityRequiresExplicitProfileAndValidAuth(t *testing.T) {
	s, _, normal := setup(t)
	path := "/ghost/api/admin/users/me/"
	if request(s, "GET", path, "", normal).Code != 404 {
		t.Fatal("identity enabled by default")
	}
	if WithPublishingIdentity(cms.Author{})(s) == nil {
		t.Fatal("invented missing identity")
	}
	_ = WithPublishingIdentity(cms.Author{ID: "1234567890abcdef12345678", Name: "Only publication", Slug: "one"})(s)
	for _, auth := range []string{"", "Bearer x", "Ghost invalid", fixtureContentKey} {
		if request(s, "GET", path, "", auth).Code != 401 {
			t.Fatal("unauthenticated profile")
		}
	}
	if request(s, "GET", "/ghost/api/v4/admin/users/me/", "", normal).Code != 401 {
		t.Fatal("wrong route audience")
	}
	if request(s, "GET", path+"?include=roles.permissions", "", normal).Code != 400 {
		t.Fatal("unsupported permissions include")
	}
	for _, q := range []string{"include=roles&include=roles", "fields=id&fields=name", "email=another@example.invalid", "bad=%zz"} {
		if request(s, "GET", path+"?"+q, "", normal).Code != 400 {
			t.Fatal("unsafe query", q)
		}
	}
	w := request(s, "GET", path+"?fields=id,password,email&include=roles", "", normal)
	if w.Code != 200 || strings.Contains(w.Body.String(), "password") || strings.Contains(w.Body.String(), "email") || strings.Contains(w.Body.String(), "name") {
		t.Fatal("fields leak", w.Body.String())
	}
	other, _, otherAuth := setup(t)
	_ = WithPublishingIdentity(cms.Author{ID: "abcdef1234567890abcdef12", Name: "Other publication", Slug: "other"})(other)
	w = request(other, "GET", path, "", otherAuth)
	if strings.Contains(w.Body.String(), "Only publication") {
		t.Fatal("cross server profile leak")
	}
}
