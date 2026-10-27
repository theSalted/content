package httpapi

import (
	"content/cms"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

func optionalFeatured(w http.ResponseWriter, raw json.RawMessage) (*bool, bool) {
	if raw == nil {
		return nil, true
	}
	var value *bool
	if json.Unmarshal(raw, &value) != nil || value == nil {
		failure(w, 400, "BadRequestError", "featured must be a boolean")
		return nil, false
	}
	return value, true
}

// An explicit null means no date was supplied. Arbitrary publication dates and
// scheduling remain outside this write profile; dedicated imports preserve dates.
func emptyPublicationDate(w http.ResponseWriter, raw json.RawMessage) bool {
	if raw == nil || strings.TrimSpace(string(raw)) == "null" {
		return true
	}
	failure(w, 400, "BadRequestError", "Only published_at:null is supported; explicit dates require import")
	return false
}

func editablePublicationDate(w http.ResponseWriter, raw json.RawMessage) (*time.Time, bool) {
	return parsePublicationDate(w, raw, false)
}
func parsePublicationDate(w http.ResponseWriter, raw json.RawMessage, allowFuture bool) (*time.Time, bool) {
	if raw == nil || strings.TrimSpace(string(raw)) == "null" {
		return nil, true
	}
	var input string
	if json.Unmarshal(raw, &input) != nil {
		failure(w, 400, "BadRequestError", "published_at must be an RFC3339 timestamp or null")
		return nil, false
	}
	value, err := time.Parse(time.RFC3339Nano, input)
	if err != nil {
		failure(w, 422, "ValidationError", "published_at requires an explicit timezone")
		return nil, false
	}
	if !allowFuture {
		err = cms.ValidatePublicationDate(value, time.Now())
	} else if value.IsZero() || value.Year() < 1 || value.Year() > 9999 || value.Nanosecond()%int(time.Millisecond) != 0 {
		err = &cms.InvalidInput{Message: "publication time requires millisecond precision"}
	}
	if err != nil {
		storeError(w, err)
		return nil, false
	}
	value = value.UTC()
	return &value, true
}
