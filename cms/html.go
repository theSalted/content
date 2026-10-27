package cms

import (
	"bytes"
	"cmp"
	"golang.org/x/net/html"
	"io"
	"slices"
	"strings"
)

// inSafeProfile reports whether the sanitizer would keep every element,
// attribute and character of source. Byte-level differences are tolerated only
// where they cannot change the parsed document: character escaping (for
// example an apostrophe the sanitizer writes as &#39;), attribute order and
// void-element syntax. Source bytes are never rewritten; the caller stores the
// original input.
func inSafeProfile(source string) bool {
	if !validEmbeds(source) {
		return false
	}
	sanitized := policy.Sanitize(source)
	if sanitized == normalizeVoidClose(source) {
		return true
	}
	a, okA := contentTokens(source)
	b, okB := contentTokens(sanitized)
	if !okA || !okB || len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sameToken(a[i], b[i]) {
			return false
		}
	}
	return true
}

// contentTokens returns decoded tokens with adjacent text merged. Non-void
// self-closing tags are refused because their syntax is easily misread.
func contentTokens(source string) ([]html.Token, bool) {
	z := html.NewTokenizer(strings.NewReader(source))
	var out []html.Token
	for {
		if z.Next() == html.ErrorToken {
			return out, z.Err() == io.EOF
		}
		t := z.Token()
		// Ghost card boundary comments are inert provenance, retained in source.
		if t.Type == html.CommentToken && (t.Data == "kg-card-begin: html" || t.Data == "kg-card-end: html") {
			continue
		}
		if t.Type == html.SelfClosingTagToken {
			if !voidElement[t.Data] {
				return nil, false
			}
			t.Type = html.StartTagToken
		}
		if t.Type == html.TextToken && len(out) > 0 && out[len(out)-1].Type == html.TextToken {
			out[len(out)-1].Data += t.Data
			continue
		}
		out = append(out, t)
	}
}

func sameToken(a, b html.Token) bool {
	if a.Type != b.Type || a.Data != b.Data || len(a.Attr) != len(b.Attr) {
		return false
	}
	x, y := slices.Clone(a.Attr), slices.Clone(b.Attr)
	order := func(p, q html.Attribute) int {
		return cmp.Or(cmp.Compare(p.Namespace, q.Namespace), cmp.Compare(p.Key, q.Key), cmp.Compare(p.Val, q.Val))
	}
	slices.SortFunc(x, order)
	slices.SortFunc(y, order)
	for i := range x {
		if x[i] == y[i] {
			continue
		}
		// URL serialization drops an empty fragment. Preserve the original safe
		// HTTPS href instead of rejecting an otherwise identical document.
		if x[i].Key == "href" && y[i].Key == "href" && x[i].Namespace == y[i].Namespace && strings.HasPrefix(y[i].Val, "https://") && x[i].Val == y[i].Val+"#" {
			continue
		}
		return false
	}
	return true
}

var voidElement = map[string]bool{"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true, "img": true, "input": true, "link": true, "meta": true, "param": true, "source": true, "track": true, "wbr": true}

// normalizeVoidClose tolerates only insignificant whitespace before '/>' on
// HTML void elements. iA emits '<img ... />'; the sanitizer emits '<img .../>'.
// Source/rendered HTML remain unchanged. Every other sanitizer change still
// rejects the write, including removed attributes, elements and unsafe URLs.
func normalizeVoidClose(source string) string {
	z := html.NewTokenizer(strings.NewReader(source))
	var out strings.Builder
	for {
		kind := z.Next()
		raw := z.Raw()
		if kind == html.ErrorToken {
			out.Write(raw)
			if z.Err() != io.EOF {
				return source
			}
			return out.String()
		}
		if kind == html.SelfClosingTagToken && bytes.HasSuffix(raw, []byte("/>")) {
			saved := bytes.Clone(raw)
			name, _ := z.TagName()
			switch string(name) {
			case "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr":
				out.Write(bytes.TrimRight(saved[:len(saved)-2], " \t\r\n\f"))
				out.WriteString("/>")
				continue
			}
			raw = saved
		}
		out.Write(raw)
	}
}
