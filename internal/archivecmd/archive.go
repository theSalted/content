// Package archivecmd implements an explicit operator manifest; it never discovers
// or enrolls history automatically and never performs network delivery.
package archivecmd

import (
	"bytes"
	"content/cms"
	"content/publishing"
	"content/storage/postgres"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"time"
)

type Post struct {
	ID          string `json:"id"`
	UUID        string `json:"uuid"`
	Revision    string `json:"revision"`
	Slug        string `json:"slug"`
	PublishedAt string `json:"published_at"`
}
type Manifest struct {
	Version     int    `json:"schema_version"`
	Publication string `json:"publication_id"`
	Source      string `json:"source_id"`
	Destination string `json:"destination_id"`
	SiteURL     string `json:"site_url"`
	Posts       []Post `json:"posts"`
}

var idPattern = regexp.MustCompile(`^[0-9a-f]{24}$`)
var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func Decode(raw []byte, expectedHash string) (Manifest, error) {
	var m Manifest
	if len(raw) > 1<<20 {
		return m, errors.New("manifest exceeds 1 MiB")
	}
	sum := sha256.Sum256(raw)
	if expectedHash != "" && hex.EncodeToString(sum[:]) != expectedHash {
		return m, errors.New("manifest checksum mismatch")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&m) != nil || d.Decode(new(any)) != io.EOF {
		return m, errors.New("invalid manifest JSON")
	}
	if m.Version != 1 || m.Publication == "" || len(m.Posts) == 0 || len(m.Posts) > 1000 {
		return m, errors.New("manifest requires version1, publication and 1–1000 explicit posts")
	}
	if e := (publishing.Enrollment{Destination: m.Destination, SourceID: m.Source, SiteURL: m.SiteURL}).Validate(); e != nil {
		return m, e
	}
	seen := map[string]bool{}
	uuids := map[string]bool{}
	for _, p := range m.Posts {
		rev, e := strconv.ParseInt(p.Revision, 10, 64)
		_, te := time.Parse(time.RFC3339Nano, p.PublishedAt)
		if !idPattern.MatchString(p.ID) || !uuidPattern.MatchString(p.UUID) || e != nil || rev < 1 || strconv.FormatInt(rev, 10) != p.Revision || te != nil || p.Slug == "" || seen[p.ID] || uuids[p.UUID] {
			return m, errors.New("invalid or duplicate manifest post")
		}
		seen[p.ID] = true
		uuids[p.UUID] = true
	}
	return m, nil
}

type Store interface {
	Get(context.Context, cms.Query) (cms.Post, error)
	ArchiveBinding(context.Context, string, string) (postgres.ArchiveBinding, error)
	EnqueueArchive(context.Context, postgres.ArchiveRequest) (postgres.ArchiveReceipt, error)
}
type Planned struct {
	Request  postgres.ArchiveRequest
	Existing bool
	EventID  string
}

// Check every item before any write. Each enqueue then rechecks under its own
// row lock; a later race may leave earlier durable receipts, safe to resume with
// the same pinned manifest. An old accepted receipt is not recreated after edit.
func Plan(ctx context.Context, store Store, m Manifest) ([]Planned, error) {
	out := make([]Planned, 0, len(m.Posts))
	enrollment := publishing.Enrollment{Destination: m.Destination, SourceID: m.Source, SiteURL: m.SiteURL}
	for _, item := range m.Posts {
		rev, _ := strconv.ParseInt(item.Revision, 10, 64)
		r := postgres.ArchiveRequest{Publication: m.Publication, PostID: item.ID, ExpectedRevision: rev, Enrollment: enrollment}
		p, e := store.Get(ctx, cms.Query{Publication: m.Publication, ID: item.ID, Kind: "post"})
		if e != nil || p.UUID != item.UUID {
			return nil, errors.New("manifest post identity unavailable or changed")
		}
		binding, e := store.ArchiveBinding(ctx, m.Publication, item.ID)
		if e == nil {
			if binding.ContentID != item.UUID || binding.Revision != rev || binding.Destination != m.Destination || binding.SourceID != m.Source || binding.SiteURL != m.SiteURL {
				return nil, errors.New("existing archive binding differs from manifest")
			}
			out = append(out, Planned{r, true, binding.EventID})
			continue
		}
		if !errors.Is(e, cms.ErrNotFound) {
			return nil, errors.New("cannot inspect archive binding")
		}
		published, _ := time.Parse(time.RFC3339Nano, item.PublishedAt)
		if p.Kind != "post" || p.Status != "published" || p.Visibility != "public" || p.Revision != rev || p.Slug != item.Slug || p.PublishedAt == nil || !p.PublishedAt.Equal(published) {
			return nil, errors.New("manifest post revision, public state, slug or date changed")
		}
		out = append(out, Planned{Request: r})
	}
	return out, nil
}
func Apply(ctx context.Context, store Store, plan []Planned, record func(postID string, receipt postgres.ArchiveReceipt) error) error {
	for _, p := range plan {
		r, e := store.EnqueueArchive(ctx, p.Request)
		if e != nil {
			return errors.New("archive enqueue stopped; preserve prior receipts and resume the same manifest after reconciliation")
		}
		if e = record(p.Request.PostID, r); e != nil {
			return errors.New("receipt output failed; database outcome may be committed; inspect before retry")
		}
	}
	return nil
}
