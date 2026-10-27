package httpapi

import (
	"content/cms"
	"golang.org/x/net/html"
	"log/slog"
	"net/url"
	"slices"
	"strings"
)

func logValidationReason(message string) {
	reason := map[string]string{
		"title must contain 1 to 255 characters":                                    "title",
		"html must be valid UTF-8 and at most 1 MiB":                                "html_encoding_or_size",
		"HTML is outside the safe, unchanged HTML profile; no content was saved":    "html_profile",
		"only draft and published statuses are implemented":                         "status",
		"only public visibility is implemented":                                     "visibility",
		"slug must be 1 to 191 lowercase ASCII letters/digits separated by hyphens": "slug",
		"published_at cannot erase an existing publication date":                    "publication_date_preservation",
	}[message]
	if reason != "" {
		slog.Info("api request rejected", "reason", reason)
	}
}
func logContentRejection(in cms.CreatePost) {
	codes := cms.CreateValidationCodes(in)
	attrs := []any{"reason", "content_validation", "problems", codes}
	if slices.Contains(codes, "html_profile") {
		attrs = append(attrs, "html_shape", htmlShape(in.HTML))
	}
	slog.Info("api request rejected", attrs...)
}

// Structural inventory only. Recognized names map to constants and all other
// names map to "other". Attribute values, text, hostnames/paths and custom names
// never leave this function. This is evidence, not a sanitizer allowlist.
func htmlShape(source string) map[string][]string {
	sets := map[string]map[string]bool{"elements": {}, "attributes": {}, "url_schemes": {}, "syntax": {}}
	knownTags := " a p div span strong em b i u s del ins code pre blockquote ul ol li h1 h2 h3 h4 h5 h6 br hr img figure figcaption table thead tbody tfoot tr th td caption video audio source iframe script style link meta html head body title "
	knownAttrs := " href src srcset alt title id class style target rel start colspan rowspan width height loading sizes frameborder allowfullscreen referrerpolicy allow srcdoc "
	add := func(group, value string) { sets[group][value] = true }
	z := html.NewTokenizer(strings.NewReader(source))
	for {
		kind := z.Next()
		if kind == html.ErrorToken {
			break
		}
		t := z.Token()
		if kind == html.CommentToken {
			add("syntax", "comment")
			continue
		}
		if kind == html.DoctypeToken {
			add("syntax", "doctype")
			continue
		}
		if kind != html.StartTagToken && kind != html.SelfClosingTagToken {
			continue
		}
		tag := "other"
		if strings.Contains(knownTags, " "+t.Data+" ") {
			tag = t.Data
		}
		add("elements", tag)
		if kind == html.SelfClosingTagToken {
			add("syntax", "self_closing")
		}
		for _, a := range t.Attr {
			name := "other"
			if strings.Contains(knownAttrs, " "+a.Key+" ") {
				name = a.Key
			} else if strings.HasPrefix(a.Key, "on") {
				name = "event_handler"
			}
			add("attributes", tag+"."+name)
			if a.Key != "href" && a.Key != "src" {
				continue
			}
			u, e := url.Parse(strings.TrimSpace(a.Val))
			scheme := "invalid"
			if e == nil {
				switch strings.ToLower(u.Scheme) {
				case "":
					scheme = "relative"
				case "https":
					scheme = "https"
				case "http":
					scheme = "http"
				case "mailto":
					scheme = "mailto"
				case "tel":
					scheme = "tel"
				case "file":
					scheme = "file"
				case "data":
					scheme = "data"
				case "javascript":
					scheme = "javascript"
				default:
					scheme = "other"
				}
			}
			add("url_schemes", scheme)
		}
	}
	out := map[string][]string{}
	for group, set := range sets {
		values := []string{}
		for value := range set {
			values = append(values, value)
		}
		slices.Sort(values)
		out[group] = values
	}
	return out
}
