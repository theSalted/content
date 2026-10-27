package media

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var imagePath = regexp.MustCompile(`^/content/images/([0-9a-f]{24})/image\.(png|jpg)$`)

func ImagePath(path string) (string, string, bool) {
	m := imagePath.FindStringSubmatch(path)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}
func ReferenceID(origin, raw string, aliases ...string) (string, bool, error) {
	u, e := url.Parse(raw)
	if e != nil {
		return "", false, nil
	}
	if u.IsAbs() {
		trusted := u.Scheme+"://"+u.Host == origin
		for _, alias := range aliases {
			trusted = trusted || u.Scheme+"://"+u.Host == alias
		}
		if !trusted {
			return "", false, nil
		}
	} else if u.Host != "" {
		return "", false, nil
	}
	if !strings.HasPrefix(u.Path, "/content/images/") {
		return "", false, nil
	}
	id, _, ok := ImagePath(u.Path)
	if !ok || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" {
		return "", true, ErrInvalid
	}
	return id, true, nil
}
func fragments(source string) ([]*html.Node, error) {
	return html.ParseFragment(strings.NewReader(source), &html.Node{Type: html.ElementNode, DataAtom: atom.Div, Data: "div"})
}
func images(nodes []*html.Node, visit func(*html.Node, int) error) error {
	var walk func(*html.Node) error
	walk = func(n *html.Node) error {
		if n.Type == html.ElementNode && n.Data == "img" {
			for i, a := range n.Attr {
				if a.Key == "src" {
					if e := visit(n, i); e != nil {
						return e
					}
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if e := walk(child); e != nil {
				return e
			}
		}
		return nil
	}
	for _, n := range nodes {
		if e := walk(n); e != nil {
			return e
		}
	}
	return nil
}

type ImageReference struct{ ID, Extension string }

func References(origin, source string, aliases ...string) ([]ImageReference, error) {
	nodes, e := fragments(source)
	if e != nil {
		return nil, ErrInvalid
	}
	ids := []ImageReference{}
	seen := map[string]string{}
	record := func(raw string) error {
		id, owned, e := ReferenceID(origin, raw, aliases...)
		if e != nil {
			return e
		}
		if owned {
			u, _ := url.Parse(raw)
			_, ext, _ := ImagePath(u.Path)
			if previous, ok := seen[id]; ok {
				if previous != ext {
					return ErrInvalid
				}
			} else {
				seen[id] = ext
				ids = append(ids, ImageReference{ID: id, Extension: ext})
				if len(ids) > 32 {
					return errors.New("at most 32 owned images per post")
				}
			}
		}
		return nil
	}
	var walk func(*html.Node) error
	walk = func(n *html.Node) error {
		if n.Type == html.ElementNode && n.Data == "img" {
			for _, attr := range n.Attr {
				if attr.Key == "src" {
					if e := record(attr.Val); e != nil {
						return e
					}
				}
				if attr.Key == "srcset" {
					for _, candidate := range strings.Split(attr.Val, ",") {
						fields := strings.Fields(candidate)
						if len(fields) != 2 {
							return ErrInvalid
						}
						if e := record(fields[0]); e != nil {
							return e
						}
					}
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if e := walk(child); e != nil {
				return e
			}
		}
		return nil
	}
	for _, node := range nodes {
		if e = walk(node); e != nil {
			return nil, e
		}
	}

	return ids, e
}

// Preview embeds only known owned raster bytes after browser authentication.
// It never fetches a URL from content; remote images remain blocked by CSP.
func (s *Service) Preview(ctx context.Context, publication, source string) string {
	nodes, e := fragments(source)
	if e != nil {
		return ""
	}
	remaining := 8 << 20
	_ = images(nodes, func(n *html.Node, i int) error {
		id, owned, e := ReferenceID(s.Origin, n.Attr[i].Val, s.OriginAliases...)
		if !owned {
			return nil
		}
		if e == nil {
			a, data, err := s.Read(ctx, publication, id, false)
			u, _ := url.Parse(n.Attr[i].Val)
			_, ext, _ := ImagePath(u.Path)
			if err == nil && a.Extension == ext && len(data) <= remaining {
				remaining -= len(data)
				n.Attr[i].Val = "data:" + a.MIME + ";base64," + base64.StdEncoding.EncodeToString(data)
				return nil
			}
		}
		n.Attr[i].Val = ""
		n.Attr = append(n.Attr, html.Attribute{Key: "alt", Val: "Image unavailable in this preview"})
		return nil
	})
	var b bytes.Buffer
	for _, n := range nodes {
		if html.Render(&b, n) != nil {
			return ""
		}
	}
	return b.String()
}
