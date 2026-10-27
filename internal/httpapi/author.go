package httpapi

import "content/cms"

// WithDefaultAuthor attributes newly created posts to an explicitly configured
// public author. It never creates a staff identity, claims roles or alters old posts.
func WithDefaultAuthor(author cms.Author) Option {
	return func(s *Server) error {
		if err := author.Validate(); err != nil {
			return err
		}
		s.defaultAuthor = &author
		return nil
	}
}
