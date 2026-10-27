package cms

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Author is public attribution, not a staff login or permission grant. Each post
// revision retains its own snapshot, so configuration changes cannot reattribute history.
type Author struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

var authorID = regexp.MustCompile(`^[0-9a-f]{24}$`)

func (a Author) Validate() error {
	if !authorID.MatchString(a.ID) || !utf8.ValidString(a.Name) || strings.TrimSpace(a.Name) == "" || strings.ContainsRune(a.Name, 0) || utf8.RuneCountInString(a.Name) > 255 || len(a.Slug) > 191 || !slugPattern.MatchString(a.Slug) {
		return &InvalidInput{Message: "author requires a stable 24-hex ID, nonblank name and URL-safe slug"}
	}
	return nil
}
