package media

import (
	stdhtml "html"
	"io"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// ProjectHTML canonicalizes exact owned image aliases for a response only.
// It does not read bytes, authorize a private image, or change stored source.
// Tags, text, links, quoting and spacing outside replaced URL tokens are retained.
func (s *Service) ProjectHTML(source string) string {
	if len(s.OriginAliases) == 0 {
		return source
	}
	z := html.NewTokenizer(strings.NewReader(source))
	var out strings.Builder
	for {
		kind := z.Next()
		raw := string(z.Raw()) // TagName may reuse the tokenizer's backing buffer.
		if kind == html.ErrorToken {
			if z.Err() != io.EOF {
				return source
			}
			out.WriteString(raw)
			return out.String()
		}
		if kind == html.StartTagToken || kind == html.SelfClosingTagToken {
			name, _ := z.TagName()
			if string(name) == "img" {
				raw = s.projectImageTag(raw)
			}
		}
		out.WriteString(raw)
	}
}

func (s *Service) projectImageURL(raw string) string {
	decoded := stdhtml.UnescapeString(raw)
	u, err := url.Parse(decoded)
	if err != nil || !u.IsAbs() || u.User != nil || u.Opaque != "" || u.RawPath != "" || strings.ContainsAny(decoded, "?#\x00\t\r\n ") {
		return raw
	}
	if _, _, valid := ImagePath(u.Path); !valid {
		return raw
	}
	origin := u.Scheme + "://" + u.Host
	if origin == s.Origin {
		return raw
	}
	for _, alias := range s.OriginAliases {
		if origin == alias {
			return stdhtml.EscapeString(s.Origin + u.Path)
		}
	}
	return raw
}

func attrSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' }

// Scan raw attributes rather than reserializing the element. The HTML tokenizer
// already identified the tag; malformed/ambiguous attributes fail unchanged.
func (s *Service) projectImageTag(raw string) string {
	i := 1
	for i < len(raw) && !attrSpace(raw[i]) && raw[i] != '/' && raw[i] != '>' {
		i++
	}
	var out strings.Builder
	last := 0
	seen := map[string]bool{}
	for i < len(raw) {
		for i < len(raw) && attrSpace(raw[i]) {
			i++
		}
		if i == len(raw) || raw[i] == '/' || raw[i] == '>' {
			break
		}
		start := i
		for i < len(raw) && !attrSpace(raw[i]) && raw[i] != '=' && raw[i] != '/' && raw[i] != '>' {
			i++
		}
		if i == start {
			return raw
		}
		name := strings.ToLower(raw[start:i])
		for i < len(raw) && attrSpace(raw[i]) {
			i++
		}
		if i == len(raw) || raw[i] != '=' {
			continue
		}
		i++
		for i < len(raw) && attrSpace(raw[i]) {
			i++
		}
		if i == len(raw) {
			return raw
		}
		quote := byte(0)
		if raw[i] == '\'' || raw[i] == '"' {
			quote = raw[i]
			i++
		}
		valueStart := i
		for i < len(raw) && ((quote != 0 && raw[i] != quote) || (quote == 0 && !attrSpace(raw[i]) && raw[i] != '>')) {
			i++
		}
		if i == len(raw) {
			return raw
		}
		valueEnd := i
		if quote != 0 {
			i++
		}
		if name != "src" && name != "srcset" {
			continue
		}
		if seen[name] {
			return raw
		}
		seen[name] = true
		value := raw[valueStart:valueEnd]
		replacement := s.projectImageURL(value)
		if name == "srcset" {
			replacement = s.projectSrcset(value)
		}
		if replacement != value {
			out.WriteString(raw[last:valueStart])
			out.WriteString(replacement)
			last = valueEnd
		}
	}
	if last == 0 {
		return raw
	}
	out.WriteString(raw[last:])
	return out.String()
}

var sourceDescriptor = regexp.MustCompile(`^(?:[1-9][0-9]*w|(?:[1-9][0-9]*(?:\.[0-9]+)?|0\.[0-9]*[1-9][0-9]*)x)$`)

func (s *Service) projectSrcset(raw string) string {
	// Data URLs contain commas, so ambiguous sets remain entirely untouched.
	if strings.Contains(strings.ToLower(raw), "data:") {
		return raw
	}
	var out strings.Builder
	start := 0
	for start < len(raw) {
		end := start + strings.IndexByte(raw[start:], ',')
		if end < start {
			end = len(raw)
		}
		candidate := raw[start:end]
		i := 0
		for i < len(candidate) && attrSpace(candidate[i]) {
			i++
		}
		urlStart := i
		for i < len(candidate) && !attrSpace(candidate[i]) {
			i++
		}
		if i == urlStart {
			return raw
		}
		tail := strings.TrimSpace(candidate[i:])
		if tail != "" && !sourceDescriptor.MatchString(tail) {
			return raw
		}
		out.WriteString(candidate[:urlStart])
		out.WriteString(s.projectImageURL(candidate[urlStart:i]))
		out.WriteString(candidate[i:])
		if end == len(raw) {
			break
		}
		out.WriteByte(',')
		start = end + 1
		if start == len(raw) {
			return raw
		}
	}
	return out.String()
}
