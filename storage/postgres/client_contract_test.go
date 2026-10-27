package postgres

import (
	"bytes"
	"content/internal/httpapi"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Auth is signed independently of the application's JWT helper. These fixed
// synthetic values never provision a key or configure a publishing app.
func documentedJWT(t *testing.T) string {
	return clientJWTForAudience(t, "/admin/", time.Now().Unix())
}

func clientJWTForAudience(t *testing.T, audience string, issuedAt int64) string {
	t.Helper()
	header := []byte(`{"alg":"HS256","kid":"0123456789abcdef01234567","typ":"JWT"}`)
	claims, _ := json.Marshal(map[string]any{"iat": issuedAt, "exp": issuedAt + 120, "aud": audience})
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	secret, _ := hex.DecodeString("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(unsigned))
	return "Ghost " + unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func contractField(body any, path string) (any, bool) {
	for _, part := range strings.Split(path, ".") {
		switch value := body.(type) {
		case map[string]any:
			var ok bool
			body, ok = value[part]
			if !ok {
				return nil, false
			}
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(value) {
				return nil, false
			}
			body = value[i]
		default:
			return nil, false
		}
	}
	return body, true
}

func TestDocumentedHTMLClientContractPostgres(t *testing.T) {
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
	handler, err := httpapi.New(store, "client-fixture", "https://frontend.invalid/blog/", key, "synthetic-content-key-for-tests", httpapi.WithSiteMetadata(httpapi.SiteMetadata{Title: "Synthetic publication"}))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	data, err := os.ReadFile("../../contracts/client-probe/documented-html-workflow.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Profile string
		Cases   []struct {
			Name, Method, Path string
			Status             int
			Auth               bool
			Body               json.RawMessage
			Equals             map[string]any
			Capture            map[string]string
			Absent             []string
		}
	}
	if err = json.Unmarshal(data, &corpus); err != nil || corpus.Profile != "documented-html-workflow-v1" {
		t.Fatal("invalid corpus", err)
	}
	variables := map[string]string{}
	render := func(s string) string {
		for k, v := range variables {
			s = strings.ReplaceAll(s, "${"+k+"}", v)
		}
		return s
	}
	for _, tc := range corpus.Cases {
		if !t.Run(tc.Name, func(t *testing.T) {
			path, body := render(tc.Path), render(string(tc.Body))
			if strings.Contains(path+body, "${") {
				t.Fatal("unresolved fixture variable")
			}
			req, err := http.NewRequest(tc.Method, server.URL+path, bytes.NewBufferString(body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Accept-Version", "v6.0")
			if len(tc.Body) > 0 {
				req.Header.Set("Content-Type", "application/json")
			}
			if tc.Auth {
				req.Header.Set("Authorization", documentedJWT(t))
			}
			res, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			payload, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != tc.Status {
				t.Fatalf("got %d, want %d: %s", res.StatusCode, tc.Status, payload)
			}
			var decoded any
			if err = json.Unmarshal(payload, &decoded); err != nil {
				t.Fatal(err)
			}
			for path, want := range tc.Equals {
				got, ok := contractField(decoded, path)
				if !ok || !reflect.DeepEqual(got, want) {
					t.Fatalf("%s: got %#v want %#v", path, got, want)
				}
			}
			for _, path := range tc.Absent {
				if _, ok := contractField(decoded, path); ok {
					t.Fatal("private field", path)
				}
			}
			for name, path := range tc.Capture {
				value, ok := contractField(decoded, path)
				s, isString := value.(string)
				if !ok || !isString || s == "" {
					t.Fatal("capture failed", path)
				}
				variables[name] = s
			}
		}) {
			t.FailNow()
		}
	}
	if variables["id"] == variables["second_id"] {
		t.Fatal("new draft overwrote previous share")
	}
	var posts, revisions, events, deliveries int
	for query, dest := range map[string]*int{
		"SELECT count(*) FROM " + ident(f.schema) + ".posts WHERE publication_id='client-fixture'":                 &posts,
		"SELECT count(*) FROM " + ident(f.schema) + ".post_revisions WHERE publication_id='client-fixture'":        &revisions,
		"SELECT count(*) FROM " + ident(f.schema) + ".outbox_events WHERE publication_id='client-fixture'":         &events,
		"SELECT count(*) FROM " + ident(f.schema) + ".publishing_deliveries WHERE publication_id='client-fixture'": &deliveries,
	} {
		if err = f.admin.QueryRow(f.ctx, query).Scan(dest); err != nil {
			t.Fatal(err)
		}
	}
	if posts != 2 || revisions != 4 || events != 1 || deliveries != 0 {
		t.Fatalf("unexpected persistence: posts=%d revisions=%d events=%d deliveries=%d", posts, revisions, events, deliveries)
	}
}
