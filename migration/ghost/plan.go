// Package ghost plans a lossless archive and a conservative, silent import.
// Unsupported records remain in the archive; they never become public posts.
package ghost

import (
	"bytes"
	"content/cms"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
)

type Record struct {
	Kind     string
	SourceID string
	SHA256   string
	Raw      json.RawMessage
	Post     *cms.Post
	Reason   string
}
type Plan struct {
	SHA256               string
	Raw                  []byte
	Records              []Record
	PublicOnly           bool
	Options              Options
	PublicSnapshotSHA256 string
}

var idPattern = regexp.MustCompile(`^[0-9a-f]{24}$`)
var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func Build(raw []byte, publication string) (Plan, error) {
	return BuildWithOptions(raw, publication, Options{})
}

func BuildWithOptions(raw []byte, publication string, options Options) (Plan, error) {
	options, err := options.validated()
	if err != nil {
		return Plan{}, err
	}
	if len(raw) == 0 || len(raw) > 64<<20 || !json.Valid(raw) {
		return Plan{}, errors.New("export must be valid JSON under 64 MiB")
	}
	var root struct {
		Scope string                     `json:"scope"`
		Data  map[string]json.RawMessage `json:"data"`
		DB    []struct {
			Data map[string]json.RawMessage `json:"data"`
		} `json:"db"`
	}
	if json.Unmarshal(raw, &root) != nil {
		return Plan{}, errors.New("invalid export envelope")
	}
	public := root.Scope == "public Content API only; not a full private/staff/database backup"
	data := root.Data
	if len(root.DB) > 0 {
		if len(root.DB) != 1 {
			return Plan{}, errors.New("exactly one Ghost database export is required")
		}
		data = root.DB[0].Data
		public = false
	}
	if data == nil || (len(root.DB) == 0 && !public) {
		return Plan{}, errors.New("unrecognized Ghost export")
	}
	supplement, supplementHash, err := publicSupplement(options, publication)
	if err != nil {
		return Plan{}, err
	}
	if public && len(supplement) > 0 {
		return Plan{}, errors.New("supplement is only valid for a database export")
	}
	usedSupplement := map[string]bool{}
	sum := sha256.Sum256(raw)
	plan := Plan{Raw: bytes.Clone(raw), SHA256: hex.EncodeToString(sum[:]), PublicOnly: public, Options: options, PublicSnapshotSHA256: supplementHash}
	seen := map[string]bool{}
	for _, kind := range []string{"posts", "pages"} {
		var rows []json.RawMessage
		if value, ok := data[kind]; ok && json.Unmarshal(value, &rows) != nil {
			return Plan{}, fmt.Errorf("invalid %s collection", kind)
		}
		for _, rawRow := range rows {
			var fields map[string]json.RawMessage
			if json.Unmarshal(rawRow, &fields) != nil {
				return Plan{}, errors.New("record must be an object")
			}
			var sourceID string
			if json.Unmarshal(fields["id"], &sourceID) != nil || sourceID == "" {
				return Plan{}, errors.New("record ID is required")
			}
			key := kind + ":" + sourceID
			if seen[key] {
				return Plan{}, errors.New("duplicate source record")
			}
			seen[key] = true
			canonical, _ := json.Marshal(fields)
			hash := sha256.Sum256(canonical)
			record := Record{Kind: kind, SourceID: sourceID, SHA256: hex.EncodeToString(hash[:]), Raw: canonical}
			candidateFields := fields
			if !public {
				var relationErr error
				candidateFields, relationErr = withRelations(fields, data, sourceID)
				if relationErr != nil {
					record.Reason = "unresolved_source_relations"
					plan.Records = append(plan.Records, record)
					continue
				}
			}
			if !public {
				var normalizeErr error
				candidateFields, normalizeErr = normalizeDatabase(candidateFields, options)
				if normalizeErr != nil {
					record.Reason = normalizeErr.Error()
					plan.Records = append(plan.Records, record)
					continue
				}
			}
			effective, _ := json.Marshal(candidateFields)
			effectiveHash := sha256.Sum256(effective)
			record.SHA256 = hex.EncodeToString(effectiveHash[:])
			post, reason := candidate(candidateFields, publication, kind, public)
			if post != nil && post.Status == "published" && post.Visibility == "public" && supplement != nil {
				other := supplement[post.ID]
				if other == nil || !samePublicRevision(post, other) {
					return Plan{}, errors.New("public snapshot does not match exported public revision")
				}
				for k, v := range other.Presentation {
					post.Presentation[k] = bytes.Clone(v)
				}
				record.SHA256 = supplementedHash(candidateFields, post.Presentation)
				usedSupplement[post.ID] = true
			}
			record.Post = post
			record.Reason = reason
			plan.Records = append(plan.Records, record)
		}
	}
	if len(usedSupplement) != len(supplement) {
		return Plan{}, errors.New("unmatched public snapshot records")
	}
	if len(plan.Records) == 0 {
		return Plan{}, errors.New("export has no posts or pages")
	}
	return plan, nil
}

func candidate(fields map[string]json.RawMessage, publication, kind string, public bool) (*cms.Post, string) {
	var in struct {
		cms.CreatePost
		ID          string       `json:"id"`
		UUID        string       `json:"uuid"`
		Type        string       `json:"type"`
		CreatedAt   string       `json:"created_at"`
		UpdatedAt   string       `json:"updated_at"`
		PublishedAt *string      `json:"published_at"`
		Authors     []cms.Author `json:"authors"`
	}
	raw, _ := json.Marshal(fields)
	if json.Unmarshal(raw, &in) != nil {
		return nil, "unsupported_field_type"
	}
	if in.Type != "" && in.Type != "post" && in.Type != "page" {
		return nil, "unsupported_record_type"
	}
	if !idPattern.MatchString(in.ID) || !uuidPattern.MatchString(in.UUID) {
		return nil, "unsupported_source_identity"
	}
	if public && in.Status == "" {
		in.Status = "published"
	}
	if in.Status == "" {
		return nil, "status_missing"
	}
	if rawHTML, ok := fields["html"]; !ok || string(rawHTML) == "null" {
		return nil, "rendered_html_missing"
	}
	p, e := cms.NewPost(publication, in.CreatePost)
	if e != nil {
		return nil, "content_profile: " + e.Error()
	}
	if kind == "pages" || in.Type == "page" {
		p.Kind = "page"
	}
	p.ID = in.ID
	p.UUID = in.UUID
	p.CreatedAt, e = time.Parse(time.RFC3339Nano, in.CreatedAt)
	if e != nil {
		return nil, "invalid_created_at"
	}
	p.UpdatedAt, e = time.Parse(time.RFC3339Nano, in.UpdatedAt)
	if e != nil {
		return nil, "invalid_updated_at"
	}
	if p.CreatedAt.Nanosecond()%int(time.Millisecond) != 0 || p.UpdatedAt.Nanosecond()%int(time.Millisecond) != 0 {
		return nil, "submillisecond_date_not_supported"
	}
	if in.PublishedAt != nil {
		date, e := time.Parse(time.RFC3339Nano, *in.PublishedAt)
		if e != nil || date.Nanosecond()%int(time.Millisecond) != 0 {
			return nil, "invalid_published_at"
		}
		p.PublishedAt = &date
	}
	if p.Status == "published" && p.PublishedAt == nil {
		return nil, "published_date_missing"
	}
	if len(in.Authors) > 1 {
		return nil, "multiple_author_editing_not_implemented"
	}
	if len(in.Authors) == 1 {
		if e := in.Authors[0].Validate(); e != nil {
			return nil, "invalid_author"
		}
		p.Author = &in.Authors[0]
	}
	p.Presentation, e = cms.PublicPresentation(fields)
	if e != nil {
		return nil, "invalid_public_metadata"
	}
	return &p, ""
}
