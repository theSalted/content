package publishing

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

// Archive is an explicit initial public snapshot, never a live broadcast event.
// Persist the encoded bytes and EventID before attempting delivery.
type Archive struct {
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
	SourceState    string    `json:"source_state"`
	Document       *Document `json:"document"`
}

func (a Archive) Validate() error {
	return a.validate(true)
}

// CMS enrollment requires its first instance to match the stable content ID.
// The receiver wire contract also permits explicit distinct instance IDs; a
// read-only receipt decoder must accept those without relaxing CMS enrollment.
func (a Archive) validate(initialCMSInstance bool) error {
	if a.SchemaVersion != 1 || a.Type != "content.archived" || a.DeliveryPolicy != "archive" || a.SourceState != "published" || (initialCMSInstance && a.ContentID != a.ObjectInstance) || strings.ContainsRune(a.Publication, 0) || len(a.Publication) > 128 || !utf8.ValidString(a.Publication) || a.Document == nil || strings.ContainsRune(a.Document.HTML, 0) {
		return errors.New("invalid archive snapshot")
	}
	return (Event{SchemaVersion: 2, Publication: a.Publication, EventID: a.EventID, ContentID: a.ContentID, ObjectInstance: a.ObjectInstance, DeliveryPolicy: "live", Source: a.Source, Revision: a.Revision, OccurredAt: a.OccurredAt, Type: "content.published", Document: a.Document}).Validate()
}
func EncodeArchive(a Archive) ([]byte, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(a); err != nil {
		return nil, err
	}
	raw := bytes.TrimSuffix(b.Bytes(), []byte("\n"))
	if len(raw) > MaxPayload {
		return nil, errors.New("archive payload exceeds 2 MiB")
	}
	return raw, nil
}
func DecodeArchive(raw []byte) (Archive, error) {
	return decodeArchive(raw, true)
}
func decodeArchive(raw []byte, initialCMSInstance bool) (Archive, error) {
	var a Archive
	if len(raw) > MaxPayload {
		return a, errors.New("archive payload exceeds 2 MiB")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&a) != nil || d.Decode(new(any)) != io.EOF {
		return a, errors.New("invalid archive JSON")
	}
	return a, a.validate(initialCMSInstance)
}

// MapArchive shares the existing live permalink/snapshot mapping. Caller must
// separately check source kind=post and lock the exact published CMS revision.
func MapArchive(i Intent, cfg Enrollment) (Archive, error) {
	if i.Kind != "post.published" {
		return Archive{}, errors.New("archive requires explicit public snapshot")
	}
	e, _, err := Map(i, cfg, "unpublished", "")
	if err != nil {
		return Archive{}, err
	}
	a := Archive{1, e.Publication, e.EventID, e.ContentID, e.ObjectInstance, "archive", e.Source, e.Revision, e.OccurredAt, "content.archived", "published", e.Document}
	return a, a.Validate()
}

// Fixed-path constructor. It cannot redirect live events to an arbitrary URL.
func NewArchiveHTTPClient(endpoint, token string) (*HTTPClient, error) {
	return newHTTPClient(endpoint, token, "/internal/v2/archive")
}
