package httpapi

import (
	"encoding/json"
	"log/slog"
)

// Rejected request diagnostics contain only fixed reason codes and the shapes
// of recognized API fields. Never retain request bytes, values, credentials,
// arbitrary field names, URLs or headers in logs.
func logBodyRejection(raw []byte, reason string) {
	fields := map[string]string{}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) == nil {
		var posts []map[string]json.RawMessage
		if json.Unmarshal(envelope["posts"], &posts) == nil && len(posts) == 1 {
			for _, name := range []string{"title", "slug", "html", "mobiledoc", "lexical", "status", "visibility", "tags", "authors", "author", "feature_image", "featured", "custom_excerpt", "excerpt", "published_at", "created_at", "updated_at", "canonical_url", "meta_title", "meta_description", "codeinjection_head", "codeinjection_foot", "email_only", "email_subject", "newsletter", "tiers", "send_email_when_published"} {
				if value, ok := posts[0][name]; ok {
					fields[name] = jsonShape(value)
				}
			}
		}
	}
	slog.Info("api request rejected", "reason", reason, "post_field_shapes", fields)
}
func jsonShape(raw json.RawMessage) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return "invalid"
	}
	switch v := value.(type) {
	case nil:
		return "null"
	case string:
		if v == "" {
			return "empty_string"
		}
		return "string"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case []any:
		if len(v) == 0 {
			return "empty_array"
		}
		return "array"
	case map[string]any:
		if len(v) == 0 {
			return "empty_object"
		}
		return "object"
	default:
		return "other"
	}
}

// Only server-authored constants map to log codes. Callers cannot log values
// accidentally by passing an error containing a field, header or URL.
func logRequestRejection(message string) {
	reason := map[string]string{
		"Malformed query": "malformed_query",
		"Repeated query parameters are unsupported":                                         "repeated_query",
		"Unsupported query parameter":                                                       "unsupported_query",
		"Creation requires source=html; only HTML is implemented":                           "create_source_or_formats",
		"Could not read JSON":                                                               "body_read_failed",
		"Only source=html is implemented":                                                   "edit_source",
		"Only formats=html is implemented":                                                  "edit_formats",
		"Only save_revision=true is supported; internal revisions are always retained":      "edit_save_revision",
		"Editable fields must be strings; null is unsupported":                              "edit_field_type",
		"HTML edits require source=html":                                                    "edit_html_source",
		"This limited profile accepts v4.0, v5.0, v6.0 or an omitted Accept-Version":        "unsupported_version",
		"This limited v2 alias accepts v2.0, v4.0, v5.0, v6.0 or an omitted Accept-Version": "unsupported_version",
	}[message]
	if reason != "" {
		slog.Info("api request rejected", "reason", reason)
	}
}
