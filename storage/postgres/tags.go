package postgres

import (
	"content/cms"
	"content/publishing"
	"encoding/json"
)

// Opt-in after the receiver advertises r3 and consumer tag URLs exist. This only
// affects new snapshots; persisted outbox/delivery/archive bytes are untouched.
func WithPublicTagOrigins(origins []string) Option {
	return func(s *Store) error {
		if e := publishing.ValidateTagOrigins(origins); e != nil {
			return e
		}
		s.tagOrigins = append([]string(nil), origins...)
		return nil
	}
}
func (s *Store) publicTags(p cms.Post) (publishing.PublicTags, error) {
	if len(s.tagOrigins) == 0 {
		return nil, nil
	}
	return publishing.ProjectPublicTags(p.Presentation["tags"], s.tagOrigins)
}
func (s *Store) publishingSnapshot(p cms.Post, includeBody bool) ([]byte, error) {
	raw, e := snapshotJSON(p, includeBody)
	if e != nil || !includeBody || len(s.tagOrigins) == 0 {
		return raw, e
	}
	tags, e := s.publicTags(p)
	if e != nil {
		return nil, e
	}
	if len(tags) == 0 {
		return raw, nil
	}
	var fields map[string]json.RawMessage
	if e = json.Unmarshal(raw, &fields); e != nil {
		return nil, e
	}
	fields["tags"], e = json.Marshal(tags)
	if e != nil {
		return nil, e
	}
	return json.Marshal(fields)
}
