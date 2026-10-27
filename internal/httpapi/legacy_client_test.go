package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestIAWriterObservedV2RouteAndEmptyTags(t *testing.T) {
	s, m, _ := setup(t)
	for _, path := range []string{"/ghost/api/v2/admin/site", "/ghost/api/v2/admin/site/"} {
		w := request(s, "GET", path, "", "")
		if w.Code != 200 || w.Header().Get("Deprecation") != `version="v2"` {
			t.Fatal(w.Code, w.Header())
		}
	}
	claims := currentClaims()
	claims.Audience = jwt.ClaimStrings{"/v2/admin/"}
	auth := signed(t, s.admin, jwt.SigningMethodHS256, claims, s.admin.id)
	for _, raw := range []string{`null`, `["unsupported-tag"]`, `{}`, `"tag"`} {
		w := request(s, "POST", "/ghost/api/v2/admin/posts/?source=html", `{"posts":[{"title":"Synthetic","html":"<p>Text</p>","status":"draft","tags":`+raw+`}]}`, auth)
		if w.Code != 422 || len(m.posts) != 0 {
			t.Fatal("unsupported tags persisted", w.Code, w.Body.String())
		}
	}
	w := request(s, "POST", "/ghost/api/v2/admin/posts/?source=html", `{"posts":[{"title":"Synthetic","html":"<p>Text</p>","status":"draft","tags":[]}]}`, auth)
	if w.Code != 201 || len(m.posts) != 1 || m.posts[0].Status != "draft" {
		t.Fatal(w.Code, w.Body.String())
	}
	var envelope struct{ Posts []map[string]any }
	if json.Unmarshal(w.Body.Bytes(), &envelope) != nil || len(envelope.Posts) != 1 {
		t.Fatal(w.Body.String())
	}
	if len(envelope.Posts[0]["tags"].([]any)) != 0 {
		t.Fatal("invented tags")
	}
	for _, v := range []string{"", "v2.0", "v6.0"} {
		r := httptest.NewRequest("GET", "/ghost/api/v2/admin/posts/", nil)
		r.Header.Set("Authorization", auth)
		r.Header.Set("Accept-Version", v)
		w = httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(v, w.Code)
		}
	}
}

func TestLegacyAudienceDoesNotCrossRouteFamilies(t *testing.T) {
	s, _, modern := setup(t)
	claims := currentClaims()
	claims.Audience = jwt.ClaimStrings{"/v2/admin/"}
	legacy := signed(t, s.admin, jwt.SigningMethodHS256, claims, s.admin.id)
	for _, tc := range []struct{ path, token string }{
		{"/ghost/api/admin/posts/", legacy}, {"/ghost/api/v2/admin/posts/", modern},
	} {
		if w := request(s, "GET", tc.path, "", tc.token); w.Code != 401 {
			t.Fatal("cross-family token accepted", tc.path, w.Code)
		}
	}
	for _, path := range []string{"/ghost/api/v3/admin/site/", "/ghost/api/v2/content/posts/", "/ghost/api/v2/admin/users/me/"} {
		if w := request(s, "GET", path, "", legacy); w.Code != 404 {
			t.Fatal("unimplemented route enabled", path, w.Code)
		}
	}
	w := request(s, "GET", "/ghost/api/v2/admin/site%0Ainjected", "", legacy)
	if strings.ContainsAny(w.Header().Get("Link"), "\r\n") {
		t.Fatal("unsafe Link header")
	}
}

func TestIssuedAtRoundingAllowanceDoesNotExtendExpiry(t *testing.T) {
	s, _, _ := setup(t)
	now := time.Unix(1800000000, 100000000)
	for _, audience := range []string{"/admin/", "/v2/admin/"} {
		for _, tc := range []struct {
			name          string
			iat, exp, nbf time.Time
			valid         bool
		}{
			{"rounded_next_second", time.Unix(1800000001, 0), now.Add(120 * time.Second), time.Time{}, true},
			{"beyond_one_second", time.Unix(1800000002, 0), now.Add(120 * time.Second), time.Time{}, false},
			{"expired", now.Add(-time.Minute), now.Add(-time.Second), time.Time{}, false},
			{"expired_this_second", now.Add(-time.Minute), time.Unix(1800000000, 0), time.Time{}, false},
			{"future_not_before", now, now.Add(time.Minute), now.Add(time.Second), false},
			{"over_five_minute_lifetime", now, now.Add(301 * time.Second), time.Time{}, false},
		} {
			t.Run(audience+tc.name, func(t *testing.T) {
				c := jwt.RegisteredClaims{IssuedAt: jwt.NewNumericDate(tc.iat), ExpiresAt: jwt.NewNumericDate(tc.exp), Audience: jwt.ClaimStrings{audience}}
				if !tc.nbf.IsZero() {
					c.NotBefore = jwt.NewNumericDate(tc.nbf)
				}
				token := signed(t, s.admin, jwt.SigningMethodHS256, c, s.admin.id)
				if err := s.admin.validateAudience(token, now, audience); (err == nil) != tc.valid {
					t.Fatalf("valid=%v, err=%v", tc.valid, err)
				}
			})
		}
	}
}
