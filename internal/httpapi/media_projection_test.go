package httpapi

import (
	"content/cms"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func TestOwnedMediaProjectionPreservesSourceAndPrivacy(t *testing.T) {
	s, repo, objects, imagePath, auth := imageHeadFixture(t)
	s.media.Origin = "https://cms.example.invalid"
	s.media.OriginAliases = []string{"https://retired.example.invalid"}
	repo.public = false
	m := s.store.(*memoryStore)
	source := `<p><img src="https://retired.example.invalid` + imagePath + `" alt="Synthetic"></p>`
	w := request(s, "POST", "/ghost/api/admin/posts/?source=html", fmt.Sprintf(`{"posts":[{"title":"Synthetic","html":%q,"status":"draft"}]}`, source), auth)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	p := m.posts[0]
	before, _ := json.Marshal(m.posts)
	canonical := strings.Replace(source, "https://retired.example.invalid", s.media.Origin, 1)
	for _, version := range []string{"", "v2", "v4"} {
		base := "/ghost/api/"
		token := auth
		if version != "" {
			base += version + "/"
			claims := currentClaims()
			claims.Audience = jwt.ClaimStrings{"/" + version + "/admin/"}
			token = signed(t, s.admin, jwt.SigningMethodHS256, claims, s.admin.id)
		}
		for _, suffix := range []string{"posts/", "posts/" + p.ID + "/"} {
			response := request(s, "GET", base+"admin/"+suffix, "", token)
			var body struct{ Posts []map[string]any }
			_ = json.Unmarshal(response.Body.Bytes(), &body)
			if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" || len(body.Posts) != 1 || body.Posts[0]["html"] != canonical || body.Posts[0]["updated_at"] != timestamp(p.UpdatedAt) {
				t.Fatal("admin projection/CAS", version, response.Code)
			}
		}
	}
	if repo.calls != 0 || objects.gets != 0 {
		t.Fatal("projection performed media I/O")
	}
	if response := request(s, "GET", "/ghost/api/content/posts/"+p.ID+"/?key="+fixtureContentKey, "", ""); response.Code != 404 {
		t.Fatal("private post disclosed")
	}
	if response := request(s, "GET", imagePath, "", ""); response.Code != 404 {
		t.Fatal("private bytes disclosed")
	}
	if response := request(s, "GET", imagePath, "", auth); response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("authenticated private image unavailable")
	}
	after, _ := json.Marshal(m.posts)
	if string(before) != string(after) || m.posts[0].SourceHTML != source || m.posts[0].HTML != source {
		t.Fatal("response read mutated source or metadata")
	}
	path := "/ghost/api/admin/posts/" + p.ID + "/"
	w = request(s, "PUT", path, fmt.Sprintf(`{"posts":[{"updated_at":%q,"status":"published"}]}`, timestamp(p.UpdatedAt)), auth)
	if w.Code != 200 {
		t.Fatal("publish", w.Code, w.Body.String())
	}
	repo.public = true
	response := request(s, "GET", "/ghost/api/content/posts/"+p.ID+"/?key="+fixtureContentKey, "", "")
	var body struct{ Posts []map[string]any }
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	if response.Code != 200 || body.Posts[0]["html"] != canonical || m.posts[0].SourceHTML != source {
		t.Fatal("published view did not canonicalize without source rewrite")
	}
	if request(s, "HEAD", imagePath, "", "").Code != 200 {
		t.Fatal("published eligibility")
	}
	w = request(s, "PUT", path, fmt.Sprintf(`{"posts":[{"updated_at":%q,"status":"draft"}]}`, timestamp(m.posts[0].UpdatedAt)), auth)
	if w.Code != 200 {
		t.Fatal("withdraw", w.Code)
	}
	repo.public = false
	if request(s, "GET", imagePath, "", "").Code != 404 {
		t.Fatal("withdrawn image publicly readable")
	}
	if m.posts[0].SourceHTML != source || m.posts[0].HTML != source || m.posts[0].ID != p.ID || m.posts[0].UUID != p.UUID {
		t.Fatal("lifecycle changed source/identity")
	}
	if request(s, "PUT", path, fmt.Sprintf(`{"posts":[{"updated_at":%q,"title":"stale"}]}`, timestamp(p.UpdatedAt)), auth).Code != 409 {
		t.Fatal("CAS weakened")
	}
}

func TestMediaProjectionWithoutConfiguredServiceIsUnchanged(t *testing.T) {
	s, _, _ := setup(t)
	p := cms.Post{HTML: `<img src="https://retired.example.invalid/content/images/0123456789abcdef01234567/image.png">`}
	if s.project(p, true)["html"] != p.HTML {
		t.Fatal("unconfigured projection")
	}
}
