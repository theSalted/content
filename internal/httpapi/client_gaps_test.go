package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// Keep unfinished client requirements executable and visibly separate from
// positive documented API contracts; a 404 here is a gap, not compatibility.
func TestClientCompatibilityGapsRemainExplicit(t *testing.T) {
	data, err := os.ReadFile("../../contracts/client-probe/current-gaps.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Profile string
		Cases   []struct {
			Name, Method, Path, Body string
			Status                   int
			ContentType              string `json:"content_type"`
			Version                  string `json:"accept_version"`
		}
	}
	if err = json.Unmarshal(data, &corpus); err != nil || corpus.Profile != "client-gaps-local-v1" {
		t.Fatal("invalid gap corpus", err)
	}
	s, m, auth := setup(t)
	for _, tc := range corpus.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			r := httptest.NewRequest(tc.Method, tc.Path, strings.NewReader(tc.Body))
			r.Header.Set("Authorization", auth)
			kind := tc.ContentType
			if kind == "" {
				kind = "application/json"
			}
			r.Header.Set("Content-Type", kind)
			version := tc.Version
			if version == "" {
				version = "v6.0"
			}
			r.Header.Set("Accept-Version", version)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != tc.Status {
				t.Fatal(w.Code, w.Body.String())
			}
			var result struct {
				Errors []struct {
					Type string `json:"errorType"`
				}
			}
			if json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Errors) != 1 || result.Errors[0].Type == "" {
				t.Fatal("missing explicit error", w.Body.String())
			}
		})
	}
	if len(m.posts) != 0 {
		t.Fatal("unsupported workflow wrote content")
	}
}
