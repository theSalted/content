// Package cms owns content independently of HTTP adapters and hosting providers.
package cms

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrNotFound       = errors.New("post not found")
	ErrConflict       = errors.New("slug already exists in this publication")
	ErrArchivePending = errors.New("post archive delivery is pending; retry after acknowledgment")
	ErrStaleUpdate    = errors.New("post was modified since the supplied updated_at")
	slugPattern       = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	policy            = contentPolicy()
)

type InvalidInput struct{ Message string }

func (e *InvalidInput) Error() string { return e.Message }

type CreatePost struct {
	Featured   *bool  `json:"-"`
	Title      string `json:"title"`
	Slug       string `json:"slug"`
	HTML       string `json:"html"`
	Status     string `json:"status"`
	Visibility string `json:"visibility"`
}

// Post is a CMS record, never a public response DTO. Raw source and publication
// scope must not accidentally escape through the Content API.
type FederationProjection struct {
	ObjectID     string `json:"object_id"`
	State        string `json:"state"`
	Acknowledged bool   `json:"acknowledged"`
}

type Post struct {
	// ActivityPub is a read-only acknowledged projection, never source metadata.
	ActivityPub  *FederationProjection
	Kind         string
	Author       *Author
	Presentation Presentation
	ID           string
	UUID         string
	Publication  string
	Title        string
	Slug         string
	Status       string
	Visibility   string
	SourceHTML   string
	HTML         string
	Revision     int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
	PublishedAt  *time.Time
	TrashedAt    *time.Time
	ScheduledFor *time.Time
}

type Query struct {
	Kind        string
	Publication string
	PublicOnly  bool
	ID          string
	Slug        string
	Page        int
	Limit       int
	Search      string
	Status      string
	Trashed     bool
}

type Store interface {
	Create(context.Context, Post) (Post, error)
	Update(context.Context, string, string, UpdatePost) (Post, error)
	Get(context.Context, Query) (Post, error)
	Browse(context.Context, Query) ([]Post, int64, error)
	Ping(context.Context) error
}

// NewPost accepts a deliberately bounded HTML profile. If the sanitizer would
// change input, reject it before persistence rather than silently lose content.
func NewPost(publication string, in CreatePost) (Post, error) {
	bad := func(s string) (Post, error) { return Post{}, &InvalidInput{s} }
	if publication == "" || len(publication) > 128 || strings.ContainsRune(publication, 0) || !utf8.ValidString(publication) {
		return bad("publication must contain 1 to 128 bytes")
	}
	if in.Status == "" {
		in.Status = "draft"
	}
	if in.Visibility == "" {
		in.Visibility = "public"
	}
	if err := validateContent(in); err != nil {
		return Post{}, err
	}
	idBytes := make([]byte, 12)
	if _, err := rand.Read(idBytes); err != nil {
		return Post{}, err
	}
	uuidBytes := make([]byte, 16)
	if _, err := rand.Read(uuidBytes); err != nil {
		return Post{}, err
	}
	uuidBytes[6] = (uuidBytes[6] & 0x0f) | 0x40
	uuidBytes[8] = (uuidBytes[8] & 0x3f) | 0x80
	uuid := fmt.Sprintf("%x-%x-%x-%x-%x", uuidBytes[:4], uuidBytes[4:6], uuidBytes[6:8], uuidBytes[8:10], uuidBytes[10:])
	id := hex.EncodeToString(idBytes)
	if in.Slug == "" {
		in.Slug = "post-" + id
	} // Explicit deviation from Ghost's title-derived slug.
	p := Post{Kind: "post", ID: id, UUID: uuid, Publication: publication, Title: in.Title, Slug: in.Slug, Status: in.Status, Visibility: in.Visibility, SourceHTML: in.HTML, HTML: in.HTML, Revision: 1}
	if in.Featured != nil {
		raw, _ := json.Marshal(*in.Featured)
		p.Presentation = Presentation{"featured": raw}
	}
	return p, nil
}

// UpdatePost is a partial edit. Nil fields are preserved; empty HTML clears the
// body. ExpectedUpdatedAt is the last-read wire token, not a desired new date.
type UpdatePost struct {
	Featured  *bool
	ClearTags bool
	// Explicit published_at:null may confirm an unpublished date, never erase history.
	RequireEmptyPublishedAt bool
	PublishedAt             *time.Time
	Trash                   *bool
	Kind                    string
	ExpectedUpdatedAt       time.Time
	Title                   *string
	Slug                    *string
	HTML                    *string
	Status                  *string
	Visibility              *string
}

func (u UpdatePost) ValidateToken() error {
	if u.ExpectedUpdatedAt.IsZero() || u.ExpectedUpdatedAt.Nanosecond()%int(time.Millisecond) != 0 {
		return &InvalidInput{"updated_at is required and must identify an exact millisecond"}
	}
	return nil
}

// ApplyUpdate must run against a locked current row in persistent stores.
// A stale token is rejected even for a no-op edit; this is a declared stricter
// behavior than Ghost's changed-field-dependent collision handling.
func ApplyUpdate(current Post, u UpdatePost) (Post, error) {
	return ApplyUpdateAt(current, u, time.Now())
}

// Persistent callers use the database clock for publication/scheduling decisions.
func ApplyUpdateAt(current Post, u UpdatePost, now time.Time) (Post, error) {
	if !ValidKind(u.Kind) || !ValidKind(current.Kind) || ContentKind(current.Kind) != ContentKind(u.Kind) {
		return Post{}, ErrNotFound
	}
	if err := u.ValidateToken(); err != nil {
		return Post{}, err
	}
	if !current.UpdatedAt.Equal(u.ExpectedUpdatedAt) {
		return Post{}, ErrStaleUpdate
	}
	if u.RequireEmptyPublishedAt && current.PublishedAt != nil {
		return Post{}, &InvalidInput{"published_at cannot erase an existing publication date"}
	}
	if current.TrashedAt != nil && (u.Trash == nil || *u.Trash) {
		return Post{}, &InvalidInput{"restore this post before editing"}
	}
	next := current
	if u.Trash != nil {
		if *u.Trash {
			value := now.UTC().Truncate(time.Millisecond)
			next.TrashedAt = &value
			next.Status = "draft"
		} else {
			next.TrashedAt = nil
			next.Status = "draft"
		}
	}
	if u.Title != nil {
		next.Title = *u.Title
	}
	if u.Slug != nil {
		if *u.Slug == "" {
			return Post{}, &InvalidInput{"slug cannot be empty on edit"}
		}
		next.Slug = *u.Slug
	}
	if u.HTML != nil {
		next.HTML = *u.HTML
		next.SourceHTML = *u.HTML
		if next.HTML != current.HTML {
			next.Presentation = current.Presentation.Clone()
			delete(next.Presentation, "excerpt")
		}
	}
	if u.Featured != nil || u.ClearTags {
		next.Presentation = next.Presentation.Clone()
		if u.Featured != nil {
			raw, _ := json.Marshal(*u.Featured)
			next.Presentation["featured"] = raw
		}
		if u.ClearTags {
			next.Presentation["tags"] = json.RawMessage(`[]`)
		}
	}
	if u.Status != nil {
		next.Status = *u.Status
	}
	if u.Visibility != nil {
		next.Visibility = *u.Visibility
	}
	if next.Status == "scheduled" {
		if current.Status == "published" {
			return Post{}, &InvalidInput{"unpublish before scheduling"}
		}
		if u.RequireEmptyPublishedAt {
			return Post{}, &InvalidInput{"published_at cannot clear a schedule; cancel it explicitly"}
		}
		if u.PublishedAt != nil {
			value := u.PublishedAt.UTC()
			unchanged := current.Status == "scheduled" && current.ScheduledFor != nil && current.ScheduledFor.Equal(value)
			if !unchanged {
				if err := ValidateScheduleDate(value, now); err != nil {
					return Post{}, err
				}
			}
			next.ScheduledFor = &value
		}
		if next.ScheduledFor == nil {
			return Post{}, &InvalidInput{"scheduling requires a future published_at"}
		}
	} else {
		next.ScheduledFor = nil
		if u.PublishedAt != nil {
			if err := ValidatePublicationDate(*u.PublishedAt, now); err != nil {
				return Post{}, err
			}
			value := u.PublishedAt.UTC()
			next.PublishedAt = &value
		}
	}
	if next.TrashedAt != nil && next.Status != "draft" {
		return Post{}, &InvalidInput{"restore this post before publishing"}
	}
	validationStatus := next.Status
	if validationStatus == "scheduled" {
		validationStatus = "draft"
	}
	if err := validateContent(CreatePost{Title: next.Title, Slug: next.Slug, HTML: next.HTML, Status: validationStatus, Visibility: next.Visibility}); err != nil {
		return Post{}, err
	}
	return next, nil
}

type contentIssue struct{ code, message string }

func validateContent(in CreatePost) error {
	if issues := contentIssues(in); len(issues) > 0 {
		return &InvalidInput{issues[0].message}
	}
	return nil
}

// CreateValidationCodes exposes fixed diagnostic categories, never field values.
// Defaults match NewPost. It does not replace validation or alter source HTML.
func CreateValidationCodes(in CreatePost) []string {
	if in.Status == "" {
		in.Status = "draft"
	}
	if in.Visibility == "" {
		in.Visibility = "public"
	}
	var codes []string
	for _, issue := range contentIssues(in) {
		codes = append(codes, issue.code)
	}
	return codes
}
func contentIssues(in CreatePost) []contentIssue {
	var issues []contentIssue
	if !utf8.ValidString(in.Title) || strings.ContainsRune(in.Title, 0) || strings.TrimSpace(in.Title) == "" || utf8.RuneCountInString(in.Title) > 255 {
		issues = append(issues, contentIssue{"title", "title must contain 1 to 255 characters"})
	}
	if !utf8.ValidString(in.HTML) || strings.ContainsRune(in.HTML, 0) || len(in.HTML) > 1<<20 {
		issues = append(issues, contentIssue{"html_encoding_or_size", "html must be valid UTF-8 and at most 1 MiB"})
	}
	if len(in.HTML) <= 1<<20 && !inSafeProfile(in.HTML) {
		issues = append(issues, contentIssue{"html_profile", "HTML is outside the safe, unchanged HTML profile; no content was saved"})
	}
	if in.Status != "draft" && in.Status != "published" {
		issues = append(issues, contentIssue{"status", "only draft and published statuses are implemented"})
	}
	if in.Visibility != "public" {
		issues = append(issues, contentIssue{"visibility", "only public visibility is implemented"})
	}
	if in.Slug != "" && (len(in.Slug) > 191 || !slugPattern.MatchString(in.Slug)) {
		issues = append(issues, contentIssue{"slug", "slug must be 1 to 191 lowercase ASCII letters/digits separated by hyphens"})
	}
	return issues
}

func ContentKind(kind string) string {
	if kind == "" {
		return "post"
	}
	return kind
}
func ValidKind(kind string) bool { return kind == "" || kind == "post" || kind == "page" }

// Publication dates are backdating metadata, not a scheduling mechanism.
func ValidatePublicationDate(value, now time.Time) error {
	if value.IsZero() || value.Year() < 1 || value.Year() > 9999 || value.Nanosecond()%int(time.Millisecond) != 0 {
		return &InvalidInput{"publication time must be a valid date with millisecond precision"}
	}
	if value.After(now) {
		return &InvalidInput{"use scheduling for a future publication time"}
	}
	return nil
}

func ValidateScheduleDate(value, now time.Time) error {
	if value.IsZero() || value.Year() < 1 || value.Year() > 9999 || value.Nanosecond()%int(time.Millisecond) != 0 || !value.After(now) {
		return &InvalidInput{"schedule requires a future timezone-qualified time with millisecond precision"}
	}
	return nil
}
