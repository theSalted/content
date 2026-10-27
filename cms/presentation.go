package cms

import (
	"bytes"
	"encoding/json"
	"errors"
	"golang.org/x/net/html"
	"io"
	"strings"
)

// Presentation is public, revisioned display metadata. It is deliberately not
// an unrestricted source-record passthrough: exports can contain staff secrets.
type Presentation map[string]json.RawMessage

var presentationStrings = strings.Fields("excerpt custom_excerpt feature_image feature_image_alt feature_image_caption canonical_url meta_title meta_description og_image og_title og_description twitter_image twitter_title twitter_description custom_template")
var authorStrings = strings.Fields("id name slug profile_image cover_image bio website location facebook twitter meta_title meta_description url")
var tagStrings = strings.Fields("id name slug description feature_image visibility meta_title meta_description og_image og_title og_description twitter_image twitter_title twitter_description canonical_url url")

func PublicPresentation(fields map[string]json.RawMessage) (Presentation, error) {
	out := Presentation{}
	copyStrings := func(src map[string]json.RawMessage, dst map[string]json.RawMessage, names []string) error {
		for _, k := range names {
			if raw, ok := src[k]; ok {
				var value *string
				if json.Unmarshal(raw, &value) != nil {
					return errors.New("invalid public metadata string")
				}
				dst[k] = bytes.Clone(raw)
			}
		}
		return nil
	}
	if err := copyStrings(fields, out, presentationStrings); err != nil {
		return nil, err
	}
	if raw, ok := fields["featured"]; ok {
		var value bool
		if string(raw) == "null" || json.Unmarshal(raw, &value) != nil {
			return nil, errors.New("invalid featured flag")
		}
		out["featured"] = bytes.Clone(raw)
	}
	for _, kind := range []string{"authors", "tags"} {
		raw, ok := fields[kind]
		if !ok {
			continue
		}
		var rows []map[string]json.RawMessage
		if json.Unmarshal(raw, &rows) != nil || string(raw) == "null" {
			return nil, errors.New("invalid public relation")
		}
		clean := []map[string]json.RawMessage{}
		names := authorStrings
		if kind == "tags" {
			names = tagStrings
		}
		for _, row := range rows {
			if kind == "tags" {
				var visibility string
				_ = json.Unmarshal(row["visibility"], &visibility)
				if visibility != "" && visibility != "public" {
					continue
				}
			}
			item := map[string]json.RawMessage{}
			if err := copyStrings(row, item, names); err != nil {
				return nil, err
			}
			clean = append(clean, item)
		}
		out[kind], _ = json.Marshal(clean)
	}
	return out, nil
}

func (p Presentation) Clone() Presentation {
	out := Presentation{}
	for k, v := range p {
		out[k] = bytes.Clone(v)
	}
	return out
}

// Plaintext is a deterministic fallback for newly edited HTML. Imported Ghost
// excerpts remain exact until a body edit; Ghost's full excerpt engine is not
// claimed. Script/style contents are never included.
func Plaintext(source string) string {
	z := html.NewTokenizer(strings.NewReader(source))
	var b strings.Builder
	blocked := 0
	for {
		kind := z.Next()
		if kind == html.ErrorToken {
			if z.Err() != io.EOF {
				return ""
			}
			break
		}
		t := z.Token()
		if kind == html.StartTagToken && (t.Data == "script" || t.Data == "style") {
			blocked++
		}
		if kind == html.EndTagToken && (t.Data == "script" || t.Data == "style") {
			if blocked > 0 {
				blocked--
			}
			continue
		}
		if blocked != 0 {
			continue
		}
		if kind == html.TextToken {
			b.WriteString(t.Data)
		} else if strings.Contains(" p div br h1 h2 h3 h4 h5 h6 li tr blockquote pre hr ", " "+t.Data+" ") {
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func Excerpt(p Post) string {
	for _, k := range []string{"custom_excerpt", "excerpt"} {
		var s *string
		if json.Unmarshal(p.Presentation[k], &s) == nil && s != nil && *s != "" {
			return *s
		}
	}
	words := strings.Fields(Plaintext(p.HTML))
	if len(words) > 50 {
		return strings.Join(words[:50], " ") + "…"
	}
	return strings.Join(words, " ")
}
