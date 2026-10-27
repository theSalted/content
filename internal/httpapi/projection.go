package httpapi

import (
	"content/cms"
	"content/publishing"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

func validReadProjection(w http.ResponseWriter, q url.Values, public bool) bool {
	for _, name := range strings.Split(q.Get("include"), ",") {
		if name != "" && name != "tags" && name != "authors" {
			failure(w, 400, "BadRequestError", "Only include=tags,authors is implemented")
			return false
		}
	}
	for _, format := range strings.Split(q.Get("formats"), ",") {
		if format != "" && format != "html" && !(public && format == "plaintext") {
			failure(w, 400, "BadRequestError", "Only html and Content API plaintext are implemented")
			return false
		}
	}
	return true
}

func (s *Server) projectRead(p cms.Post, public bool, q url.Values) map[string]any {
	dto := s.project(p, public)
	// Defense in depth: a malformed revision cannot turn the source archive or
	// staff fields into an API response, even when fields requests those names.
	metadata, err := cms.PublicPresentation(p.Presentation)
	if err == nil {
		for key, value := range metadata {
			dto[key] = json.RawMessage(value)
		}
	}
	// Only the public post projection may expose a confirmed object URL.
	if public && cms.ContentKind(p.Kind) == "post" && p.Status == "published" && p.Visibility == "public" {
		dto["activitypub"] = nil
		if a := p.ActivityPub; a != nil && a.Acknowledged && a.State == "public" && publishing.DiscoveryURL(a.ObjectID) {
			dto["activitypub"] = *a
		}
	}
	dto["excerpt"] = cms.Excerpt(p)
	if _, ok := dto["custom_excerpt"]; !ok {
		dto["custom_excerpt"] = nil
	}
	if _, ok := dto["feature_image"]; !ok {
		dto["feature_image"] = nil
	}
	if _, ok := dto["featured"]; !ok {
		dto["featured"] = false
	}
	includes := map[string]bool{}
	for _, name := range strings.Split(q.Get("include"), ",") {
		includes[name] = true
	}
	for _, relation := range []string{"tags", "authors"} {
		primary := "primary_tag"
		if relation == "authors" {
			primary = "primary_author"
		}
		if public && !includes[relation] {
			delete(dto, relation)
			delete(dto, primary)
			continue
		}
		if _, ok := dto[relation]; !ok {
			dto[relation] = []any{}
		}
		if raw, ok := metadata[relation]; ok {
			var rows []json.RawMessage
			if json.Unmarshal(raw, &rows) == nil {
				dto[primary] = nil
				if len(rows) > 0 {
					dto[primary] = rows[0]
				}
			}
		}
		if _, ok := dto[primary]; !ok {
			dto[primary] = nil
		}
	}
	if formats := q.Get("formats"); formats != "" {
		html, plain := false, false
		for _, format := range strings.Split(formats, ",") {
			html = html || format == "html"
			plain = plain || format == "plaintext"
		}
		if !html {
			delete(dto, "html")
		}
		if plain {
			dto["plaintext"] = cms.Plaintext(p.HTML)
		}
	}
	if fields, ok := q["fields"]; ok && fields[0] != "" {
		filtered := map[string]any{}
		for _, field := range strings.Split(fields[0], ",") {
			if value, ok := dto[field]; ok {
				filtered[field] = value
			}
		}
		// Measured Ghost 6.39/v5 behavior: included arrays survive fields, primary
		// relation fields do not unless explicitly requested. Unknown fields ignored.
		for _, relation := range []string{"tags", "authors"} {
			if includes[relation] {
				filtered[relation] = dto[relation]
			}
		}
		return filtered
	}
	return dto
}
