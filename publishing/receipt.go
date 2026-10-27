package publishing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ReceiptResolution is current, authenticated receiver evidence bound to exact
// saved event bytes. It is not the historic 202 ACK and cannot authorize a write.
type ReceiptResolution struct {
	SchemaVersion int             `json:"schema_version"`
	EventID       string          `json:"event_id"`
	Publication   string          `json:"publication_id"`
	ContentID     string          `json:"content_id"`
	Source        Source          `json:"source"`
	Revision      string          `json:"revision"`
	EventHash     string          `json:"event_hash"`
	Result        string          `json:"result"`
	Projection    *ResolvedObject `json:"projection"`
	Reason        string          `json:"reason"`
}
type ResolvedObject struct {
	ObjectID       string `json:"object_id"`
	ObjectInstance string `json:"object_instance"`
	Revision       string `json:"revision"`
	State          string `json:"state"`
}
type ResolutionOutcome struct {
	State, Code string
	Receipt     *ReceiptResolution
	Delay       time.Duration
}
type Resolver interface {
	Resolve(context.Context, []byte) ResolutionOutcome
}
type ReceiptHTTPClient struct{ transport *HTTPClient }

func NewReceiptHTTPClient(endpoint, token string) (*ReceiptHTTPClient, error) {
	c, err := newHTTPClient(endpoint, token, "/internal/v2/receipts/resolve")
	if err != nil {
		return nil, err
	}
	return &ReceiptHTTPClient{c}, nil
}
func (c *ReceiptHTTPClient) Close() { c.transport.Close() }
func (c *ReceiptHTTPClient) Resolve(ctx context.Context, payload []byte) ResolutionOutcome {
	if _, err := ReceiptEvent(payload); err != nil {
		return ResolutionOutcome{State: "quarantined", Code: "invalid_saved_event"}
	}
	// Embed exactly the stored document. No status mutation, resend or URI inference.
	body := append([]byte(`{"schema_version":1,"event":`), payload...)
	body = append(body, '}')
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.transport.endpoint, bytes.NewReader(body))
	if err != nil {
		return ResolutionOutcome{State: "quarantined", Code: "invalid_request"}
	}
	req.Header.Set("Authorization", "Bearer "+c.transport.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	r, err := c.transport.client.Do(req)
	if err != nil {
		return ResolutionOutcome{State: "retry", Code: "transport_error"}
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		if r.StatusCode >= 500 || r.StatusCode == 408 || r.StatusCode == 429 || r.StatusCode == 404 {
			return ResolutionOutcome{State: "retry", Code: "resolution_unavailable"}
		}
		return ResolutionOutcome{State: "quarantined", Code: "resolution_rejected"}
	}
	kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || kind != "application/json" {
		return ResolutionOutcome{State: "retry", Code: "invalid_resolution"}
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, (16<<10)+1))
	if err != nil || len(raw) > 16<<10 {
		return ResolutionOutcome{State: "retry", Code: "invalid_resolution"}
	}
	receipt, err := DecodeReceipt(payload, raw)
	if err != nil {
		return ResolutionOutcome{State: "quarantined", Code: "resolution_mismatch"}
	}
	return ResolutionOutcome{State: "complete", Code: receipt.Reason, Receipt: &receipt}
}

// ReceiptEvent normalizes the public identity of a saved live or archive event.
// The hash always covers its original schema, including archive-only fields.
func ReceiptEvent(raw []byte) (Event, error) {
	var version struct {
		SchemaVersion int `json:"schema_version"`
	}
	if len(raw) > MaxPayload || json.Unmarshal(raw, &version) != nil {
		return Event{}, errors.New("invalid saved event")
	}
	if version.SchemaVersion == 1 {
		a, err := decodeArchive(raw, false)
		if err != nil {
			return Event{}, err
		}
		return Event{SchemaVersion: 2, Publication: a.Publication, EventID: a.EventID, ContentID: a.ContentID, ObjectInstance: a.ObjectInstance, Source: a.Source, Revision: a.Revision, OccurredAt: a.OccurredAt, Type: "content.published", Document: a.Document, DeliveryPolicy: "live"}, nil
	}
	return Decode(raw)
}
func DecodeReceipt(payload, raw []byte) (ReceiptResolution, error) {
	var r ReceiptResolution
	fail := errors.New("receipt does not confirm the saved event")
	e, err := ReceiptEvent(payload)
	if err != nil {
		return r, err
	}
	hash, err := CanonicalEventHash(payload)
	if err != nil {
		return r, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&r) != nil || d.Decode(new(any)) != io.EOF || r.SchemaVersion != 1 || r.EventID != e.EventID || r.Publication != e.Publication || r.ContentID != e.ContentID || r.Source != e.Source || r.Revision != e.Revision || r.EventHash != hash {
		return ReceiptResolution{}, fail
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields["projection"] == nil {
		return ReceiptResolution{}, fail
	}
	if r.Result != "applied" && r.Result != "stale" {
		return ReceiptResolution{}, fail
	}
	if p := r.Projection; p != nil {
		current, err := strconv.ParseInt(p.Revision, 10, 64)
		original, _ := strconv.ParseInt(e.Revision, 10, 64)
		if r.Result != "applied" || r.Reason != "confirmed" || e.Document == nil || p.State != "public" || p.ObjectInstance != e.ObjectInstance || !DiscoveryURL(p.ObjectID) || !ValidCounter(p.Revision) || err != nil || current < original {
			return ReceiptResolution{}, fail
		}
	} else {
		switch r.Reason {
		case "stale_receipt":
			if r.Result != "stale" {
				return ReceiptResolution{}, fail
			}
		case "superseded", "not_public":
			if r.Result != "applied" {
				return ReceiptResolution{}, fail
			}
		default:
			return ReceiptResolution{}, fail
		}
	}
	return r, nil
}

// Matches the receiver's existing sorted-key canonicalJson, not ordinary Go JSON
// (which escapes HTML and U+2028/U+2029). Protocol object keys are fixed ASCII;
// numbers are only the small integer schema versions; counters remain strings.
func CanonicalEventHash(raw []byte) (string, error) {
	if _, err := ReceiptEvent(raw); err != nil {
		return "", err
	}
	var value any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&value) != nil {
		return "", errors.New("invalid canonical event")
	}
	var out strings.Builder
	var write func(any) error
	quote := func(s string) {
		out.WriteByte('"')
		for _, r := range s {
			switch r {
			case '"', '\\':
				out.WriteByte('\\')
				out.WriteRune(r)
			case '\b':
				out.WriteString(`\b`)
			case '\f':
				out.WriteString(`\f`)
			case '\n':
				out.WriteString(`\n`)
			case '\r':
				out.WriteString(`\r`)
			case '\t':
				out.WriteString(`\t`)
			default:
				if r < 0x20 {
					out.WriteString(`\u00`)
					out.WriteByte("0123456789abcdef"[byte(r)>>4])
					out.WriteByte("0123456789abcdef"[byte(r)&15])
				} else {
					out.WriteRune(r)
				}
			}
		}
		out.WriteByte('"')
	}
	write = func(v any) error {
		switch x := v.(type) {
		case nil:
			out.WriteString("null")
		case bool:
			out.WriteString(strconv.FormatBool(x))
		case string:
			quote(x)
		case json.Number:
			n, err := x.Int64()
			if err != nil {
				return err
			}
			out.WriteString(strconv.FormatInt(n, 10))
		case []any:
			out.WriteByte('[')
			for i, v := range x {
				if i > 0 {
					out.WriteByte(',')
				}
				if err := write(v); err != nil {
					return err
				}
			}
			out.WriteByte(']')
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			out.WriteByte('{')
			for i, k := range keys {
				if i > 0 {
					out.WriteByte(',')
				}
				quote(k)
				out.WriteByte(':')
				if err := write(x[k]); err != nil {
					return err
				}
			}
			out.WriteByte('}')
		default:
			return errors.New("unsupported canonical value")
		}
		return nil
	}
	if err := write(value); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(out.String()))
	return hex.EncodeToString(sum[:]), nil
}
