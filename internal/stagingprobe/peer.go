package stagingprobe

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"
)

type PeerStatus struct {
	RunID        string                                          `json:"runId"`
	DurableRetry bool                                            `json:"durableRetryObserved"`
	Activities   []json.RawMessage                               `json:"activities"`
	Attempts     []struct{ ID, Type, Format, BodySHA256 string } `json:"attempts"`
	CMSAttempts  []struct {
		EventID, BodySHA256             string
		OriginalStatus, DeliveredStatus int
		Ack                             string
	} `json:"cmsAttempts"`
	Objects []struct {
		ID     string
		Object json.RawMessage
	} `json:"objects"`
}
type Activity struct {
	ID, Type, Actor string
	Object          json.RawMessage
}
type Article struct{ ID, Name, Content, URL, Published, Updated, AttributedTo string }

func httpClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	return &http.Client{Transport: tr, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func readPeer(ctx context.Context, c Config, client *http.Client) (PeerStatus, error) {
	var out PeerStatus
	r, e := http.NewRequestWithContext(ctx, "GET", c.PeerURL, nil)
	if e != nil {
		return out, errors.New("peer_request")
	}
	r.Header.Set("Authorization", "Bearer "+c.Token)
	response, e := client.Do(r)
	if e != nil {
		return out, errors.New("peer_transport")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return out, errors.New("peer_status")
	}
	b, e := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if e != nil || len(b) > 2<<20 || json.Unmarshal(b, &out) != nil || out.RunID != c.RunID {
		return out, errors.New("peer_report")
	}
	return out, nil
}
func peerActivities(s PeerStatus) ([]Activity, error) {
	out := []Activity{}
	for _, raw := range s.Activities {
		var a Activity
		if json.Unmarshal(raw, &a) != nil {
			return nil, errors.New("peer_activity")
		}
		if a.Type != "Accept" {
			out = append(out, a)
		}
	}
	return out, nil
}
func awaitPeer(ctx context.Context, c Config, client *http.Client, count int) (PeerStatus, error) {
	for {
		s, e := readPeer(ctx, c, client)
		if e != nil {
			return s, e
		}
		a, e := peerActivities(s)
		if e != nil {
			return s, e
		}
		if len(a) > count {
			return s, errors.New("unexpected_peer_activity")
		}
		if len(a) == count {
			attempts := 0
			if count == 1 {
				for _, attempt := range s.Attempts {
					if attempt.ID == a[0].ID {
						attempts++
					}
				}
			}
			if count != 1 || (s.DurableRetry && attempts >= 3) {
				return s, nil
			}
		}
		select {
		case <-ctx.Done():
			return s, errors.New("peer_timeout")
		case <-time.After(300 * time.Millisecond):
		}
	}
}
func verifyPeer(ctx context.Context, c Config, client *http.Client, s PeerStatus, firstEvent, firstHash, editedTitle string) error {
	a, e := peerActivities(s)
	if e != nil || len(a) != 5 {
		return errors.New("peer_lifecycle_count")
	}
	for i, kind := range []string{"Create", "Update", "Delete", "Create", "Delete"} {
		if a[i].Type != kind {
			return errors.New("peer_lifecycle_order")
		}
	}
	var first, update, second Article
	var deleted, finalDeleted string
	if json.Unmarshal(a[0].Object, &first) != nil || json.Unmarshal(a[1].Object, &update) != nil || json.Unmarshal(a[3].Object, &second) != nil || json.Unmarshal(a[2].Object, &deleted) != nil || json.Unmarshal(a[4].Object, &finalDeleted) != nil {
		return errors.New("peer_objects")
	}
	if first.ID == "" || first.ID != update.ID || deleted != first.ID || second.ID == first.ID || second.ID == "" || finalDeleted != second.ID || update.Name != editedTitle {
		return errors.New("peer_object_identity")
	}
	before := first.Updated
	if before == "" {
		before = first.Published
	}
	oldTime, e1 := time.Parse(time.RFC3339Nano, before)
	newTime, e2 := time.Parse(time.RFC3339Nano, update.Updated)
	if e1 != nil || e2 != nil || !newTime.After(oldTime) {
		return errors.New("peer_updated_clock")
	}
	hashes := map[string]bool{}
	attempts := 0
	for _, x := range s.Attempts {
		if x.ID == a[0].ID {
			if x.Type != "Create" || x.Format != "legacy" && x.Format != "rfc9421" {
				return errors.New("peer_signature_record")
			}
			hashes[x.BodySHA256] = true
			attempts++
		}
	}
	if attempts < 3 || len(hashes) != 1 || !s.DurableRetry {
		return errors.New("peer_durable_retry")
	}
	lost, duplicate := false, false
	for _, x := range s.CMSAttempts {
		if x.EventID != firstEvent {
			continue
		}
		if x.BodySHA256 != firstHash {
			return errors.New("cms_retry_bytes")
		}
		if x.OriginalStatus == 202 && x.DeliveredStatus == 503 {
			lost = true
		}
		if x.DeliveredStatus == 202 && x.Ack == "duplicate" {
			duplicate = true
		}
	}
	if !lost || !duplicate {
		return errors.New("cms_lost_ack_evidence")
	}
	if len(s.Objects) != 2 {
		return errors.New("peer_object_count")
	}
	seen := map[string]bool{}
	base, _ := url.Parse(c.SiteURL)
	for _, object := range s.Objects {
		if string(object.Object) != "null" || object.ID != first.ID && object.ID != second.ID || seen[object.ID] {
			return errors.New("peer_tombstone_state")
		}
		seen[object.ID] = true
		u, e := url.Parse(object.ID)
		if e != nil || u.Scheme != "https" || u.Host != base.Host || u.User != nil {
			return errors.New("peer_object_origin")
		}
		r, e := http.NewRequestWithContext(ctx, "GET", object.ID, nil)
		if e != nil {
			return errors.New("tombstone_request")
		}
		r.Header.Set("Accept", "application/activity+json")
		response, e := client.Do(r)
		if e != nil {
			return errors.New("tombstone_transport")
		}
		b, e := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		response.Body.Close()
		var value struct{ Type string }
		if e != nil || response.StatusCode != 410 || json.Unmarshal(b, &value) != nil || value.Type != "Tombstone" {
			return errors.New("public_tombstone")
		}
	}
	return nil
}
