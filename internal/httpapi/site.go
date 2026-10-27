package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

// SiteMetadata contains explicitly public, operator-supplied metadata only.
// Credentials, staff data and internal publication/installation IDs do not belong here.
type SiteMetadata struct{ Title, Description, Logo string }

type Option func(*Server) error

func WithSiteMetadata(metadata SiteMetadata) Option {
	return func(s *Server) error {
		if metadata.Title == "" {
			metadata.Title = "Content"
		}
		if !plainMetadata(metadata.Title, 255) || strings.TrimSpace(metadata.Title) == "" || !plainMetadata(metadata.Description, 2000) {
			return errors.New("invalid public site title or description")
		}
		if metadata.Logo != "" {
			u, err := url.Parse(metadata.Logo)
			if err != nil || !plainMetadata(metadata.Logo, 2048) || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
				return errors.New("invalid public site logo URL")
			}
			relative := u.Scheme == "" && u.Host == "" && strings.HasPrefix(metadata.Logo, "/") && !strings.HasPrefix(metadata.Logo, "//")
			absolute := u.Scheme == "https" && u.Hostname() != ""
			if !relative && !absolute {
				return errors.New("site logo must be a root-relative path or HTTPS URL")
			}
		}
		s.site = metadata
		return nil
	}
}

func plainMetadata(value string, max int) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, 0) && utf8.RuneCountInString(value) <= max
}

// The observed iA v2 and Ulysses v4 aliases uses the same bounded HTML profile. Broader
// minimum-version negotiation and other historical URL families remain unsupported.
func profileHeaders(w http.ResponseWriter, r *http.Request, legacyVersion string) bool {
	w.Header().Set("Vary", "Accept-Version")
	w.Header().Set("X-Content-Compatibility", "posts-html-v1")
	if v := r.Header.Get("Accept-Version"); v != "" && v != "v6.0" && v != "v5.0" && v != "v4.0" && !(legacyVersion == "v2" && v == "v2.0") {
		message := "This limited profile accepts v4.0, v5.0, v6.0 or an omitted Accept-Version"
		if legacyVersion == "v2" {
			message = "This limited v2 alias accepts v2.0, v4.0, v5.0, v6.0 or an omitted Accept-Version"
		}
		failure(w, 400, "BadRequestError", message)
		return false
	}
	w.Header().Set("Content-Version", "v6.0")
	return true
}

func (s *Server) readSite(w http.ResponseWriter, r *http.Request, legacyVersion string) {
	if !profileHeaders(w, r, legacyVersion) {
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
	if !validQuery(w, q, map[string]bool{}) {
		return
	}
	var logo any
	if s.site.Logo != "" {
		logo = s.site.Logo
	}
	// 6.0 is the adapter's wire family, not an assertion that this is Ghost or
	// that all Ghost 6 endpoints work. The compatibility header names the subset.
	reply(w, http.StatusOK, map[string]any{"site": map[string]any{
		"title": s.site.Title, "description": s.site.Description, "logo": logo,
		"url": s.siteURL + "/", "version": "6.0",
	}})
}
