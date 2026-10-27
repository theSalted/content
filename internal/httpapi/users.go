package httpapi

import (
	"content/cms"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// WithPublishingIdentity exposes only an explicitly selected public profile to
// the current single-publication integration key. It does not create a Ghost
// staff account or grant browser, role, user-management or token authority.
func WithPublishingIdentity(author cms.Author) Option {
	return func(s *Server) error {
		if err := author.Validate(); err != nil {
			return err
		}
		copy := author
		s.publishingIdentity = &copy
		return nil
	}
}
func (s *Server) readCurrentPublishingIdentity(w http.ResponseWriter, r *http.Request, legacyVersion string) {
	if s.publishingIdentity == nil {
		failure(w, 404, "NotFoundError", "Publishing identity is not configured")
		return
	}
	if !profileHeaders(w, r, legacyVersion) {
		return
	}
	audience := "/admin/"
	if legacyVersion != "" {
		audience = "/" + legacyVersion + "/admin/"
	}
	if s.admin.validateAudience(r.Header.Get("Authorization"), time.Now(), audience) != nil {
		failure(w, 401, "UnauthorizedError", "Invalid admin token")
		return
	}
	if r.Method != http.MethodGet {
		method(w, http.MethodGet)
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		failure(w, 400, "BadRequestError", "Malformed query")
		return
	}
	if !validQuery(w, q, map[string]bool{"include": true, "fields": true}) {
		return
	}
	if values := q["include"]; len(values) > 0 && (len(values) != 1 || values[0] != "roles") {
		failure(w, 400, "BadRequestError", "Only roles can be included")
		return
	}
	if len(q["fields"]) > 1 {
		failure(w, 400, "BadRequestError", "Repeated fields are unsupported")
		return
	}
	a := s.publishingIdentity
	// The identity is a publishing profile, not a fabricated staff administrator.
	// Empty roles and absent private account fields are intentional limitations.
	user := map[string]any{"id": a.ID, "name": a.Name, "slug": a.Slug, "roles": []any{}, "status": "active", "visibility": "public", "profile_image": nil, "cover_image": nil, "bio": nil, "website": nil, "location": nil, "facebook": nil, "twitter": nil}
	if raw, ok := q["fields"]; ok {
		filtered := map[string]any{}
		for _, key := range strings.Split(raw[0], ",") {
			if value, ok := user[key]; ok {
				filtered[key] = value
			}
		}
		if q.Get("include") == "roles" {
			filtered["roles"] = []any{}
		}
		user = filtered
	}
	w.Header().Set("X-Content-Identity", "publishing-profile")
	reply(w, 200, map[string]any{"users": []any{user}})
}
