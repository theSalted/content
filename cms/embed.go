package cms

import (
	"bytes"
	"golang.org/x/net/html"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var iframeAttribute = regexp.MustCompile(`(?s)^\s+([A-Za-z][A-Za-z0-9-]*)(?:\s*=\s*(?:"[^"]*"|'[^']*'))?`)

var youtubePath = regexp.MustCompile(`^/embed/[A-Za-z0-9_-]{11}$`)

// Only the standard HTTPS YouTube player form is accepted. This validates
// stored markup; it never fetches the URL or enables frames in the admin UI.
// Unsupported hosts/parameters remain errors, not rewritten content.
func validEmbeds(source string) bool {
	z := html.NewTokenizer(strings.NewReader(source))
	for {
		kind := z.Next()
		if kind == html.ErrorToken {
			return z.Err() == io.EOF
		}
		raw := bytes.Clone(z.Raw())
		t := z.Token()
		if t.Data != "iframe" || kind == html.EndTagToken {
			continue
		}
		if kind != html.StartTagToken {
			return false
		}

		// x/net/html follows browser semantics and discards duplicate attributes.
		// Check the original opening tag as well; accept quoted values and boolean
		// attributes, not ambiguous duplicate src/srcdoc spelling.
		rest := string(raw)[len("<iframe"):]
		rawNames := map[string]bool{}
		for strings.TrimSpace(rest) != ">" {
			match := iframeAttribute.FindStringSubmatch(rest)
			if match == nil {
				return false
			}
			name := strings.ToLower(match[1])
			if rawNames[name] {
				return false
			}
			rawNames[name] = true
			rest = rest[len(match[0]):]
		}
		found := false
		seen := map[string]bool{}
		for _, a := range t.Attr {
			if seen[a.Key] {
				return false
			}
			seen[a.Key] = true
			if a.Key != "src" {
				continue
			}
			found = true
			u, e := url.Parse(a.Val)
			if e != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.RawPath != "" || u.Fragment != "" || u.Opaque != "" || !youtubePath.MatchString(u.Path) {
				return false
			}
			if u.Host != "www.youtube.com" && u.Host != "www.youtube-nocookie.com" {
				return false
			}
			query, e := url.ParseQuery(u.RawQuery)
			if e != nil {
				return false
			}
			for name, values := range query {
				if len(values) != 1 {
					return false
				}
				switch name {
				case "feature":
					if values[0] != "oembed" {
						return false
					}
				case "start", "end":
					n, e := strconv.Atoi(values[0])
					if e != nil || n < 0 || n > 604800 {
						return false
					}
				case "autoplay", "controls", "rel", "playsinline", "fs":
					if values[0] != "0" && values[0] != "1" {
						return false
					}
				default:
					return false
				}
			}
		}
		if !found {
			return false
		}
	}
}
