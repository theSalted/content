package httpapi

import (
	"errors"
	"net/url"
	"strings"
)

// Pages may have a different frontend base path than chronological posts.
// No personal routing rules are built into the package.
func WithPageURL(base string) Option {
	return func(s *Server) error {
		u, e := url.Parse(base)
		if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))) {
			return errors.New("page URL must be HTTPS or loopback HTTP without credentials/query/fragment")
		}
		s.pageURL = strings.TrimRight(base, "/")
		return nil
	}
}
