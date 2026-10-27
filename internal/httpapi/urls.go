package httpapi

import (
	"content/cms"
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"
)

// Consumer extension, not a Ghost API endpoint. JSON never redirects a caller's
// API key to the website; consumers validate the returned owned URL themselves.
func (s *Server) resolveURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		method(w, "GET, HEAD")
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		failure(w, 400, "BadRequestError", "Malformed query")
		return
	}
	for k, v := range q {
		if (k != "key" && k != "type" && k != "slug") || len(v) != 1 {
			failure(w, 400, "BadRequestError", "Invalid query")
			return
		}
	}
	if !equalKey(q.Get("key"), s.contentKey) {
		failure(w, 401, "UnauthorizedError", "Invalid content key")
		return
	}
	kind, slug := q.Get("type"), q.Get("slug")
	if (kind != "post" && kind != "page") || !cms.ValidSlug(slug) {
		failure(w, 400, "BadRequestError", "Invalid type or slug")
		return
	}
	resolver, ok := s.store.(cms.URLResolver)
	if !ok {
		failure(w, 503, "ServiceUnavailableError", "URL resolution unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	found, err := resolver.ResolveURL(ctx, s.publication, kind, slug)
	if errors.Is(err, cms.ErrNotFound) {
		failure(w, 404, "NotFoundError", "Post not found")
		return
	}
	if err != nil {
		failure(w, 503, "ServiceUnavailableError", "URL resolution unavailable")
		return
	}
	base := s.siteURL
	if kind == "page" && s.pageURL != "" {
		base = s.pageURL
	}
	target, _ := url.JoinPath(base, found.Slug)
	target += "/"
	match := "canonical"
	var status any
	if found.Alias {
		match = "alias"
		status = 301
	}
	if r.Method == "HEAD" {
		w.WriteHeader(200)
		return
	}
	reply(w, 200, map[string]any{"resolution": map[string]any{"match": match, "post_id": found.PostID, "slug": found.Slug, "url": target, "redirect_status": status}})
}

func WithScheduling(enabled bool) Option {
	return func(s *Server) error { s.scheduling = enabled; return nil }
}
