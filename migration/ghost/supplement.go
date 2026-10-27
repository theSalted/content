package ghost

import (
	"content/cms"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

// A separately captured public API snapshot supplies Ghost-computed display
// metadata absent from a database export (notably excerpt). It is accepted only
// from the explicitly selected source and against the identical public revision.
func publicSupplement(o Options, publication string) (map[string]*cms.Post, string, error) {
	if len(o.PublicSnapshot) == 0 {
		return nil, "", nil
	}
	if o.SourceURL == "" {
		return nil, "", errors.New("public snapshot requires source-url")
	}
	var envelope struct {
		Origin string `json:"origin"`
	}
	if json.Unmarshal(o.PublicSnapshot, &envelope) != nil || strings.TrimRight(envelope.Origin, "/") != o.SourceURL {
		return nil, "", errors.New("public snapshot source origin mismatch")
	}
	p, e := Build(o.PublicSnapshot, publication)
	if e != nil || !p.PublicOnly {
		return nil, "", errors.New("supplement must be the labelled public API snapshot")
	}
	out := map[string]*cms.Post{}
	for _, r := range p.Records {
		if r.Post == nil || r.Post.Status != "published" || r.Post.Visibility != "public" {
			return nil, "", errors.New("supplement contains an incompatible or non-public record")
		}
		if out[r.Post.ID] != nil {
			return nil, "", errors.New("duplicate supplemental identity")
		}
		out[r.Post.ID] = r.Post
	}
	return out, p.SHA256, nil
}
func samePublicRevision(a, b *cms.Post) bool {
	return a.ID == b.ID && a.UUID == b.UUID && a.Kind == b.Kind && a.Title == b.Title && a.Slug == b.Slug && a.HTML == b.HTML && a.CreatedAt.Equal(b.CreatedAt) && a.UpdatedAt.Equal(b.UpdatedAt) && a.PublishedAt != nil && b.PublishedAt != nil && a.PublishedAt.Equal(*b.PublishedAt)
}
func supplementedHash(fields map[string]json.RawMessage, metadata cms.Presentation) string {
	encoded, _ := json.Marshal(struct {
		Fields         map[string]json.RawMessage
		PublicMetadata cms.Presentation
	}{fields, metadata})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
