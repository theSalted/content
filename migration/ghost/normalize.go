package ghost

import (
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// Options describes an explicit source-origin expansion, never a destination
// rewrite. Original export/editor bytes remain in the operator-only archive.
type Options struct {
	SourceURL      string
	PublicSnapshot []byte
}

func (o Options) validated() (Options, error) {
	if o.SourceURL == "" {
		return o, nil
	}
	u, e := url.Parse(o.SourceURL)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.ForceQuery || strings.ContainsAny(o.SourceURL, "\"'<>\\\r\n\t ") {
		return Options{}, errors.New("source-url must be an absolute HTTPS base URL without credentials, query or fragment")
	}
	o.SourceURL = strings.TrimRight(u.String(), "/")
	return o, nil
}

var urlAttribute = regexp.MustCompile(`(?is)\b(?:src|href|srcset|poster)\s*=\s*(?:"([^"]*)"|'([^']*)')`)

func expandHTML(source, origin string) string {
	z := html.NewTokenizer(strings.NewReader(source))
	var out strings.Builder
	for {
		kind := z.Next()
		raw := string(z.Raw())
		if kind == html.StartTagToken || kind == html.SelfClosingTagToken {
			raw = urlAttribute.ReplaceAllStringFunc(raw, func(attribute string) string {
				indexes := urlAttribute.FindStringSubmatchIndex(attribute)
				start, end := indexes[2], indexes[3]
				if start < 0 {
					start, end = indexes[4], indexes[5]
				}
				value := strings.ReplaceAll(attribute[start:end], "__GHOST_URL__/", origin+"/")
				return attribute[:start] + value + attribute[end:]
			})
		}
		out.WriteString(raw)
		if kind == html.ErrorToken {
			return out.String()
		}
	}
}

// Database rows use 0/1 booleans and source-origin tokens. These are explicit
// representation conversions; malformed flags are not coerced. No HTML text,
// original editor JSON, credentials, settings, or unknown fields are rewritten.
func normalizeDatabase(fields map[string]json.RawMessage, o Options) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	for k, v := range fields {
		out[k] = v
	}
	if raw, ok := out["featured"]; ok {
		switch string(raw) {
		case "0":
			out["featured"] = json.RawMessage("false")
		case "1":
			out["featured"] = json.RawMessage("true")
		}
	}
	for _, key := range []string{"html", "feature_image", "og_image", "twitter_image", "canonical_url"} {
		var s string
		if json.Unmarshal(out[key], &s) != nil || !strings.Contains(s, "__GHOST_URL__") {
			continue
		}
		if o.SourceURL == "" {
			return nil, errors.New("source_url_required")
		}
		if key == "html" {
			s = expandHTML(s, o.SourceURL)
		} else {
			s = strings.ReplaceAll(s, "__GHOST_URL__/", o.SourceURL+"/")
		}
		out[key], _ = json.Marshal(s)
	}
	for _, key := range []string{"authors", "tags"} {
		var rows []map[string]json.RawMessage
		if json.Unmarshal(out[key], &rows) != nil {
			continue
		}
		for _, row := range rows {
			for _, field := range []string{"profile_image", "cover_image", "feature_image", "og_image", "twitter_image", "canonical_url", "url"} {
				var s string
				if json.Unmarshal(row[field], &s) != nil || !strings.Contains(s, "__GHOST_URL__") {
					continue
				}
				if o.SourceURL == "" {
					return nil, errors.New("source_url_required")
				}
				row[field], _ = json.Marshal(strings.ReplaceAll(s, "__GHOST_URL__/", o.SourceURL+"/"))
			}
		}
		out[key], _ = json.Marshal(rows)
	}
	return out, nil
}
