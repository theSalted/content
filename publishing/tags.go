package publishing

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// PublicTag preserves authored spelling and a real canonical consumer URL.
// It never derives a hashtag or URL from prose.
type PublicTag struct {
	Name string `json:"name"`
	Href string `json:"href"`
}
type PublicTags []PublicTag

func (tags PublicTags) Validate() error {
	if len(tags) > 32 {
		return errors.New("at most32public tags")
	}
	seen := map[string]bool{}
	for _, t := range tags {
		if !utf8.ValidString(t.Name) || utf8.RuneCountInString(t.Name) < 1 || utf8.RuneCountInString(t.Name) > 100 || strings.TrimSpace(t.Name) == "" || strings.ContainsRune(t.Name, 0) || strings.HasPrefix(strings.TrimLeftFunc(t.Name, unicode.IsSpace), "#") || !utf8.ValidString(t.Href) || !PublicURL(t.Href) || len(utf16.Encode([]rune(t.Href))) > 2048 || seen[t.Href] {
			return errors.New("invalid or duplicate public tag")
		}
		seen[t.Href] = true
	}
	return nil
}
func (t *PublicTag) UnmarshalJSON(raw []byte) error {
	var v struct {
		Name json.RawMessage `json:"name"`
		Href json.RawMessage `json:"href"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&v) != nil || d.Decode(new(any)) != io.EOF || !validJSONString(v.Name) || !validJSONString(v.Href) {
		return errors.New("invalid public tag JSON")
	}
	if json.Unmarshal(v.Name, &t.Name) != nil || json.Unmarshal(v.Href, &t.Href) != nil {
		return errors.New("invalid public tag fields")
	}
	return PublicTags{*t}.Validate()
}
func (t *PublicTags) UnmarshalJSON(raw []byte) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("null tags unsupported")
	}
	var v []PublicTag
	if json.Unmarshal(raw, &v) != nil {
		return errors.New("invalid public tags")
	}
	*t = v
	return t.Validate()
}

// encoding/json replaces lone surrogate escapes; reject them before decoding
// while preserving a deliberately authored U+FFFD and escaped backslashes.
func validJSONString(raw []byte) bool {
	if len(raw) < 2 || raw[0] != '"' || !utf8.Valid(raw) || !json.Valid(raw) {
		return false
	}
	for i := 1; i < len(raw)-1; i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		n, e := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if e != nil {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, e := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if e != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
func ValidateTagOrigins(origins []string) error {
	if len(origins) == 0 || len(origins) > 8 {
		return errors.New("public tags require1..8explicit origins")
	}
	for _, origin := range origins {
		u, e := url.Parse(origin)
		if e != nil || !PublicURL(origin) || u.Path != "" || u.RawPath != "" {
			return errors.New("public tag origin must be HTTPS root origin")
		}
	}
	return nil
}

// ProjectPublicTags accepts only explicitly public metadata and exact canonical
// URLs on configured consumer origins. Missing URLs fail rather than inventing
// routes; internal # labels never reach the immutable publishing snapshot.
func ProjectPublicTags(raw json.RawMessage, origins []string) (PublicTags, error) {
	if err := ValidateTagOrigins(origins); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var rows []map[string]json.RawMessage
	if json.Unmarshal(raw, &rows) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, errors.New("invalid authored tag metadata")
	}
	var result PublicTags
	for _, row := range rows {
		var visibility, name, href string
		if json.Unmarshal(row["visibility"], &visibility) != nil || visibility != "public" {
			continue
		}
		if !validJSONString(row["name"]) || json.Unmarshal(row["name"], &name) != nil {
			return nil, errors.New("invalid authored tag name")
		}
		if strings.HasPrefix(strings.TrimLeftFunc(name, unicode.IsSpace), "#") {
			continue
		}
		if !validJSONString(row["url"]) || json.Unmarshal(row["url"], &href) != nil {
			return nil, errors.New("public tag has no canonical consumer URL")
		}
		u, e := url.Parse(href)
		if e != nil {
			return nil, errors.New("invalid tag URL")
		}
		allowed := false
		for _, origin := range origins {
			allowed = allowed || u.Scheme+"://"+u.Host == origin
		}
		if !allowed {
			return nil, errors.New("tag URL origin is not configured")
		}
		result = append(result, PublicTag{Name: name, Href: href})
	}
	return result, result.Validate()
}
