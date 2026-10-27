package postgres

import "errors"

// WithConnectionLimit keeps a worker inside its deployment role's connection
// budget. Default API behavior remains five connections.
func WithConnectionLimit(n int32) Option {
	return func(s *Store) error {
		if n < 1 || n > 5 {
			return errors.New("connection limit must be between one and five")
		}
		s.maxConnections = n
		return nil
	}
}
