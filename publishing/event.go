// Package publishing defines the internal v2 boundary and durable delivery loop.
// It does not implement ActivityPub or read another module's database tables.
package publishing

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

const MaxPayload = 2 << 20

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-8][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)
var sourcePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
var languagePattern = regexp.MustCompile(`^[a-z]{2,3}(?:-[A-Za-z0-9]{2,8})*$`)

type Source struct {
	ID    string `json:"id"`
	Epoch string `json:"epoch"`
}
type Document struct {
	Visibility  string     `json:"visibility"`
	Title       string     `json:"title"`
	HTML        string     `json:"html"`
	URL         string     `json:"url"`
	PublishedAt string     `json:"published_at"`
	Language    string     `json:"language,omitempty"`
	Tags        PublicTags `json:"tags,omitempty"`
}
type Event struct {
	SchemaVersion  int       `json:"schema_version"`
	Publication    string    `json:"publication_id"`
	EventID        string    `json:"event_id"`
	ContentID      string    `json:"content_id"`
	ObjectInstance string    `json:"object_instance"`
	DeliveryPolicy string    `json:"delivery_policy"`
	Source         Source    `json:"source"`
	Revision       string    `json:"revision"`
	OccurredAt     string    `json:"occurred_at"`
	Type           string    `json:"type"`
	Document       *Document `json:"document,omitempty"`
}

func ValidCounter(s string) bool {
	n, err := strconv.ParseInt(s, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == s
}
func ValidUUID(s string) bool {
	return uuidPattern.MatchString(s) || s == "00000000-0000-0000-0000-000000000000" || s == "ffffffff-ffff-ffff-ffff-ffffffffffff"
}
func PublicURL(s string) bool {
	u, e := url.Parse(s)
	return e == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}
func (e Event) Validate() error {
	bad := errors.New("invalid internal v2 event")
	if e.SchemaVersion != 2 || e.Publication == "" || len(utf16.Encode([]rune(e.Publication))) > 128 || !ValidUUID(e.EventID) || !ValidUUID(e.ContentID) || !ValidUUID(e.ObjectInstance) || e.DeliveryPolicy != "live" || !sourcePattern.MatchString(e.Source.ID) || !ValidCounter(e.Source.Epoch) || !ValidCounter(e.Revision) {
		return bad
	}
	if _, err := time.Parse(time.RFC3339Nano, e.OccurredAt); err != nil {
		return bad
	}
	switch e.Type {
	case "content.published", "content.updated":
		d := e.Document
		if d != nil {
			if err := d.Tags.Validate(); err != nil {
				return bad
			}
		}
		if d == nil || d.Visibility != "public" || !utf8.ValidString(d.Title) || strings.TrimSpace(d.Title) == "" || strings.ContainsRune(d.Title, 0) || utf8.RuneCountInString(d.Title) > 255 || !utf8.ValidString(d.HTML) || len(d.HTML) > 1<<20 || !PublicURL(d.URL) || (d.Language != "" && !languagePattern.MatchString(d.Language)) {
			return bad
		}
		if _, err := time.Parse(time.RFC3339Nano, d.PublishedAt); err != nil {
			return bad
		}
	case "content.withdrawn", "content.deleted":
		if e.Document != nil {
			return bad
		}
	default:
		return bad
	}
	return nil
}
func Encode(e Event) ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(e); err != nil {
		return nil, err
	}
	data := bytes.TrimSuffix(b.Bytes(), []byte("\n"))
	if len(data) > MaxPayload {
		return nil, errors.New("v2 payload exceeds 2 MiB")
	}
	return data, nil
}
func Decode(data []byte) (Event, error) {
	var e Event
	if len(data) > MaxPayload {
		return e, errors.New("v2 payload exceeds 2 MiB")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&e); err != nil {
		return e, errors.New("invalid v2 JSON")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return e, errors.New("invalid v2 JSON")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return e, errors.New("invalid v2 JSON")
	}
	if raw, ok := fields["document"]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return e, errors.New("null document is unsupported")
	}
	return e, e.Validate()
}
func UUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// Enrollment is frozen per new post. It never enrolls an existing post/history.
type Enrollment struct{ Destination, SourceID, SiteURL string }

func (e Enrollment) Validate() error {
	if !sourcePattern.MatchString(e.Destination) || !sourcePattern.MatchString(e.SourceID) || !PublicURL(e.SiteURL) {
		return errors.New("invalid publishing enrollment configuration")
	}
	return nil
}

type Snapshot struct {
	ID          string     `json:"id"`
	UUID        string     `json:"uuid"`
	Title       string     `json:"title"`
	Slug        string     `json:"slug"`
	Status      string     `json:"status"`
	Visibility  string     `json:"visibility"`
	PublishedAt *time.Time `json:"published_at"`
	HTML        string     `json:"html"`
	Tags        PublicTags `json:"tags,omitempty"`
}
type Intent struct {
	Publication, EventID, Kind string
	Revision                   int64
	OccurredAt                 time.Time
	Snapshot                   Snapshot
}

// Map consumes an immutable CMS intent. The caller transaction persists the
// returned instance and exact bytes before any network operation.
func Map(i Intent, cfg Enrollment, state, instance string) (Event, string, error) {
	if err := cfg.Validate(); err != nil {
		return Event{}, state, err
	}
	e := Event{SchemaVersion: 2, Publication: i.Publication, EventID: i.EventID, ContentID: i.Snapshot.UUID, ObjectInstance: instance, DeliveryPolicy: "live", Source: Source{cfg.SourceID, "1"}, Revision: strconv.FormatInt(i.Revision, 10), OccurredAt: i.OccurredAt.UTC().Format(time.RFC3339Nano)}
	next := state
	switch i.Kind {
	case "post.published":
		if state == "public" {
			return e, state, errors.New("unexpected repeated publication")
		}
		if state == "unpublished" {
			e.ObjectInstance = e.ContentID
		} else if state == "withdrawn" {
			var err error
			e.ObjectInstance, err = UUID()
			if err != nil {
				return e, state, err
			}
		} else {
			return e, state, errors.New("unknown lifecycle state")
		}
		e.Type = "content.published"
		next = "public"
	case "post.updated":
		if state != "public" {
			return e, state, errors.New("update requires published instance")
		}
		e.Type = "content.updated"
	case "post.unpublished":
		if state != "public" {
			return e, state, errors.New("withdrawal requires published instance")
		}
		e.Type = "content.withdrawn"
		next = "withdrawn"
	default:
		return e, state, errors.New("unsupported CMS intent")
	}
	if next == "public" {
		s := i.Snapshot
		if s.Status != "published" || s.Visibility != "public" || s.PublishedAt == nil {
			return e, state, errors.New("not a public snapshot")
		}
		u, err := url.JoinPath(cfg.SiteURL, s.Slug)
		if err != nil {
			return e, state, err
		}
		e.Document = &Document{Visibility: "public", Title: s.Title, HTML: s.HTML, URL: strings.TrimRight(u, "/") + "/", PublishedAt: s.PublishedAt.UTC().Format(time.RFC3339Nano), Tags: append(PublicTags(nil), s.Tags...)}
	}
	return e, next, e.Validate()
}
