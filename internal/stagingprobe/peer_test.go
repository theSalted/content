package stagingprobe

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestPeerEvidence(t *testing.T) {
	raw := `{"runId":"run","durableRetryObserved":true,"activities":[{"id":"accept","type":"Accept"},{"id":"create","type":"Create","object":{"id":"https://example.invalid/objects/first","name":"Original","published":"2026-10-26T00:00:00Z"}},{"id":"update","type":"Update","object":{"id":"https://example.invalid/objects/first","name":"Edited","updated":"2026-10-26T00:00:01Z"}},{"id":"delete","type":"Delete","object":"https://example.invalid/objects/first"},{"id":"republish","type":"Create","object":{"id":"https://example.invalid/objects/second"}},{"id":"final-delete","type":"Delete","object":"https://example.invalid/objects/second"}],"attempts":[{"id":"create","type":"Create","format":"legacy","bodySha256":"body"},{"id":"create","type":"Create","format":"rfc9421","bodySha256":"body"},{"id":"create","type":"Create","format":"rfc9421","bodySha256":"body"}],"cmsAttempts":[{"eventId":"event","bodySha256":"bytes","originalStatus":202,"deliveredStatus":503,"ack":"applied"},{"eventId":"event","bodySha256":"bytes","originalStatus":202,"deliveredStatus":202,"ack":"duplicate"}],"objects":[{"id":"https://example.invalid/objects/first","object":null},{"id":"https://example.invalid/objects/second","object":null}]}`
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" || r.URL.Host != "example.invalid" {
			t.Error("public tombstone request leaked auth or escaped origin")
		}
		return &http.Response{StatusCode: 410, Body: io.NopCloser(strings.NewReader(`{"type":"Tombstone"}`))}, nil
	})}
	c := Config{SiteURL: "https://example.invalid"}
	var s PeerStatus
	if json.Unmarshal([]byte(raw), &s) != nil {
		t.Fatal("fixture")
	}
	if e := verifyPeer(context.Background(), c, client, s, "event", "bytes", "Edited"); e != nil {
		t.Fatal("valid evidence rejected", e)
	}
	for _, tc := range []struct{ name, old, new string }{{"retry body changed", `"bodySha256":"bytes","originalStatus":202,"deliveredStatus":202`, `"bodySha256":"changed","originalStatus":202,"deliveredStatus":202`}, {"durable retry missing", `"durableRetryObserved":true`, `"durableRetryObserved":false`}, {"wrong lifecycle", `"id":"update","type":"Update"`, `"id":"update","type":"Create"`}, {"public object remains", `"object":null`, `"object":{"type":"Article"}`}, {"wrong signature record", `"format":"legacy"`, `"format":"unchecked"`}, {"wrong title", `"name":"Edited"`, `"name":"Wrong"`}} {
		t.Run(tc.name, func(t *testing.T) {
			var v PeerStatus
			json.Unmarshal([]byte(strings.Replace(raw, tc.old, tc.new, 1)), &v)
			if verifyPeer(context.Background(), c, client, v, "event", "bytes", "Edited") == nil {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
}
