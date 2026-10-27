package postgres

import (
	"content/cms"
	"content/internal/httpapi"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Replay the observed iA request shape and isolated required author attribution.
// The real app result is separately recorded in docs/ia-writer-local-checkpoint.md.
func TestIAObservedDraftShapePostgres(t *testing.T) {
	f := newRoleFixture(t)
	store, err := Open(f.ctx, f.runtimeDSN, f.schema, "installation-a")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	key, err := httpapi.ParseAdminKey("0123456789abcdef01234567:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := httpapi.New(store, "ia-fixture", "http://127.0.0.1:8081", key, "synthetic-content-key-for-tests", httpapi.WithDefaultAuthor(cms.Author{ID: "0123456789abcdef01234568", Name: "Synthetic author", Slug: "synthetic-author"}))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	send := func(method, path, body, audience string, iat int64) (int, map[string]any) {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Authorization", clientJWTForAudience(t, audience, iat))
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var result map[string]any
		if err = json.NewDecoder(res.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, result
	}
	if status, _ := send("GET", "/ghost/api/v2/admin/site", "", "/v2/admin/", time.Now().Unix()); status != 200 {
		t.Fatal(status)
	}
	body := `{"posts":[{"title":"Synthetic iA draft","html":"<p>Only synthetic content.</p>","status":"draft","tags":[]}]}`
	// The rounded-up iat seen in actual iA traffic must not intermittently fail.
	status, result := send("POST", "/ghost/api/v2/admin/posts/?source=html", body, "/v2/admin/", time.Now().Unix()+1)
	if status != 201 {
		t.Fatal(status, result)
	}
	posts := result["posts"].([]any)
	post := posts[0].(map[string]any)
	if post["status"] != "draft" || post["published_at"] != nil || len(post["tags"].([]any)) != 0 {
		t.Fatal("wrong draft projection", post)
	}
	author := post["primary_author"].(map[string]any)
	if author["id"] != "0123456789abcdef01234568" || len(post["authors"].([]any)) != 1 {
		t.Fatal("missing persisted author")
	}
	id := post["id"].(string)
	if status, _ = send("GET", "/ghost/api/v2/admin/posts/"+id+"/", "", "/v2/admin/", time.Now().Unix()); status != 200 {
		t.Fatal(status)
	}
	for _, tc := range []struct {
		name, body, audience string
		offset               int64
		want                 int
	}{
		{"wrong_family", body, "/admin/", 0, 401},
		{"future_token", body, "/v2/admin/", 10, 401},
		{"expired_token", body, "/v2/admin/", -180, 401},
		{"nonempty_tags", strings.Replace(body, `"tags":[]`, `"tags":["Tag"]`, 1), "/v2/admin/", 0, 422},
		{"null_tags", strings.Replace(body, `"tags":[]`, `"tags":null`, 1), "/v2/admin/", 0, 422},
		{"unsupported_field", strings.Replace(body, `"tags":[]`, `"custom_excerpt":"Not implemented"`, 1), "/v2/admin/", 0, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if status, result := send("POST", "/ghost/api/v2/admin/posts/?source=html", tc.body, tc.audience, time.Now().Unix()+tc.offset); status != tc.want {
				t.Fatal(status, result)
			}
		})
	}
	status, result = send("GET", "/ghost/api/content/posts/?key=synthetic-content-key-for-tests", "", "/v2/admin/", time.Now().Unix())
	if status != 200 || len(result["posts"].([]any)) != 0 {
		t.Fatal("draft leaked", status, result)
	}
	for table, want := range map[string]int{"posts": 1, "post_revisions": 1, "outbox_events": 0, "publishing_deliveries": 0} {
		var got int
		if err = f.admin.QueryRow(f.ctx, "SELECT count(*) FROM "+ident(f.schema)+"."+table+" WHERE publication_id='ia-fixture'").Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s=%d want %d", table, got, want)
		}
	}
}
