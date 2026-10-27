package cms

import "context"

type URLResolution struct {
	PostID string
	Slug   string
	Kind   string
	Alias  bool
}

type URLResolver interface {
	ResolveURL(context.Context, string, string, string) (URLResolution, error)
}

func ValidSlug(slug string) bool { return len(slug) <= 191 && slugPattern.MatchString(slug) }
