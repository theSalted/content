package stagingprobe

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"content/cms"
	"content/publishing"
	"content/storage/postgres"
	"github.com/jackc/pgx/v5"
)

const OneDeadline = "2026-10-28T03:24:10Z"
const OneTitle = "Temporary federation production test — please ignore"
const oneBody = "<p>This is a temporary federation production test. It is automatically withdrawn immediately after publication; please ignore.</p>"

type OneGate struct {
	SpendUSD   float64
	MeasuredAt time.Time
}

func (g OneGate) Validate(now time.Time) error {
	deadline, _ := time.Parse(time.RFC3339, OneDeadline)
	if now.Add(45*time.Minute).After(deadline) || g.MeasuredAt.After(now.Add(time.Minute)) || now.Sub(g.MeasuredAt) > 5*time.Minute || math.IsNaN(g.SpendUSD) || math.IsInf(g.SpendUSD, 0) || g.SpendUSD < 0 || g.SpendUSD >= 4.50 {
		return errors.New("production_test_start_gate")
	}
	return nil
}

// LoadOne preserves the old staging loader/replay guard; only this separate,
// explicitly approved one-post command accepts a rollout-pinned publication.
func LoadOne(get func(string) string) (Config, OneGate, func(), error) {
	return loadOneAt(get, time.Now())
}
func loadOneAt(get func(string) string, now time.Time) (Config, OneGate, func(), error) {
	fail := func() (Config, OneGate, func(), error) {
		return Config{}, OneGate{}, func() {}, errors.New("production_test_configuration")
	}
	publication := get("CONTENT_ONE_POST_PUBLICATION")
	origin := get("CONTENT_ONE_POST_ORIGIN")
	if get("CONTENT_ONE_POST_APPROVED") != "true" || publication == "" || publication == "content-staging" || len(publication) > 128 || !publishing.PublicURL(origin) || get("CONTENT_PUBLICATION_ID") != publication || get("CONTENT_SITE_URL") != origin || get("CONTENT_PUBLISHING_ENDPOINT") != "http://127.0.0.1:3001/internal/v2/events" {
		return fail()
	}
	c, cleanup, e := Load(func(k string) string {
		if k == "CONTENT_PUBLICATION_ID" {
			return "content-staging"
		}
		return get(k)
	})
	if e != nil {
		return fail()
	}
	actor, e := url.Parse(get("CONTENT_ONE_POST_ACTOR"))
	if e != nil || !publishing.PublicURL(get("CONTENT_ONE_POST_ACTOR")) || actor.Scheme+"://"+actor.Host != origin || actor.Path == "" || actor.Path == "/" {
		cleanup()
		return fail()
	}
	c.ActorURL = get("CONTENT_ONE_POST_ACTOR")
	c.Publication = publication
	spend, e1 := strconv.ParseFloat(get("CONTENT_TEST_SPEND_USD"), 64)
	measured, e2 := time.Parse(time.RFC3339, get("CONTENT_TEST_SPEND_MEASURED_AT"))
	g := OneGate{spend, measured}
	if e1 != nil || e2 != nil || g.Validate(now) != nil {
		cleanup()
		return fail()
	}
	return c, g, cleanup, nil
}

type OneReport struct {
	Report
	RunID                 string `json:"run_id"`
	Deadline              string `json:"deadline"`
	Failure               string `json:"failure,omitempty"`
	WithdrawalCommitState string `json:"withdrawal_commit_state"`
	WithdrawalCommitted   bool   `json:"withdrawal_committed"`
	WithdrawalAccepted    bool   `json:"withdrawal_accepted"`
	WithdrawalObserved    bool   `json:"withdrawal_observed"`
}

// RunOne commits Create AND its withdrawal obligation before the first network
// delivery. Existing publication rows are never reset, edited or replayed.
func RunOne(ctx context.Context, c Config, g OneGate) (report OneReport, err error) {
	report = OneReport{Report: Report{Status: "failed", Checks: []string{}}, RunID: c.RunID, Deadline: OneDeadline, WithdrawalCommitState: "not_attempted"}
	defer func() {
		if err != nil {
			report.Failure = err.Error()
			report.Status = "failed"
		}
	}()
	fail := func(code string) (OneReport, error) { return report, errors.New(code) }
	if err = g.Validate(time.Now()); err != nil {
		return report, err
	}
	q, e := postgres.Schema(c.Schema)
	if e != nil {
		return fail("one_schema")
	}
	lock, e := pgx.Connect(ctx, c.APIURL)
	if e != nil {
		return fail("one_lock_connection")
	}
	defer lock.Close(context.Background())
	sum := sha256.Sum256([]byte("content/test-one/" + c.Installation + "/" + c.Schema + "/" + c.Publication))
	key := int64(binary.BigEndian.Uint64(sum[:8]))
	var locked bool
	if lock.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&locked) != nil || !locked {
		return fail("one_publication_busy")
	}
	writer, e := postgres.Open(ctx, c.APIURL, c.Schema, c.Installation, postgres.WithNewPostPublishing(publishing.Enrollment{Destination: c.Destination, SourceID: c.Source, SiteURL: c.SiteURL}))
	if e != nil {
		return fail("one_cms_open")
	}
	defer writer.Close()
	queue, e := postgres.Open(ctx, c.DeliveryURL, c.Schema, c.Installation)
	if e != nil {
		return fail("one_delivery_open")
	}
	defer queue.Close()
	conn, e := pgx.Connect(ctx, c.DeliveryURL)
	if e != nil {
		return fail("one_audit_open")
	}
	defer conn.Close(context.Background())
	a := audit{conn, q, c.Publication, c.Destination}
	_, total, e := writer.Browse(ctx, cms.Query{Publication: c.Publication, Page: 1, Limit: 1})
	if e != nil || total != 0 {
		return fail("one_publication_not_empty")
	}
	for _, table := range []string{"outbox_events", "publishing_streams", "publishing_deliveries"} {
		n, e := a.count(ctx, table, "")
		if e != nil || n != 0 {
			return fail("one_queue_not_empty")
		}
	}
	client, e := newAPI(writer, c)
	if e != nil {
		return report, e
	}
	p, e := client.write(ctx, nil, OneTitle, oneBody, "draft", "production-test-"+c.RunID)
	if e != nil {
		return report, e
	}
	report.ContentID = p.UUID
	p, e = client.write(ctx, &p, OneTitle, oneBody, "published", "")
	if e != nil {
		return report, e
	}
	report.WithdrawalCommitState = "attempted_unconfirmed"
	p, e = client.write(ctx, &p, OneTitle, oneBody, "draft", "")
	if e != nil {
		return report, e
	}
	report.WithdrawalCommitted = true
	report.WithdrawalCommitState = "committed"
	if e = client.public(ctx, p, 404); e != nil {
		return report, e
	}
	final, e := writer.Get(ctx, cms.Query{Publication: c.Publication, ID: p.ID})
	if e != nil || final.Revision != 3 || final.Status != "draft" || final.UUID != p.UUID {
		return fail("one_final_cms")
	}
	snapshots, e := a.snapshots(ctx, p.ID)
	if e != nil || len(snapshots) != 2 {
		return fail("one_withdrawal_obligation")
	}
	report.Checks = append(report.Checks, "One CMS identity: draft, publish and withdrawal committed before network delivery; final Content API 404")
	sender, e := publishing.NewHTTPClient(c.Endpoint, c.Token)
	if e != nil {
		return fail("one_sender")
	}
	defer sender.Close()
	worker := publishing.Worker{Enabled: true, Publication: c.Publication, Destination: c.Destination, Queue: queue, Sender: sender, Timeout: 5 * time.Second, Lease: 15 * time.Second, Idle: 100 * time.Millisecond, MaxAttempts: 5}
	deadline, _ := time.Parse(time.RFC3339, OneDeadline)
	for {
		if !time.Now().Before(deadline) {
			return fail("one_hard_deadline")
		}
		rows, e := a.deliveries(ctx, p.ID)
		if e != nil {
			return report, e
		}
		accepted := 0
		for _, d := range rows {
			if d.state == "accepted" {
				accepted++
				if d.revision == 3 {
					report.WithdrawalAccepted = true
				}
			} else if d.state == "quarantined" {
				return fail("one_delivery_quarantined")
			}
		}
		if accepted == 2 {
			break
		}
		if len(rows) > 2 {
			return fail("one_extra_delivery")
		}
		if _, e = worker.Step(ctx); e != nil {
			return fail("one_delivery_step")
		}
		if e = pause(ctx); e != nil {
			return report, e
		}
	}
	rows, e := a.deliveries(ctx, p.ID)
	if e != nil || len(rows) != 2 {
		return fail("one_final_delivery_count")
	}
	for i, d := range rows {
		ev, e := publishing.Decode(d.payload)
		kind := "content.published"
		if i == 1 {
			kind = "content.withdrawn"
		}
		if e != nil || d.state != "accepted" || d.result != "applied" || d.revision != int64(i+2) || ev.Revision != strconv.Itoa(i+2) || ev.EventID != d.eventID || ev.Type != kind || ev.ContentID != p.UUID || ev.ObjectInstance != p.UUID || ev.Publication != c.Publication || ev.Source.ID != c.Source || ev.Source.Epoch != "1" {
			return fail("one_event_mismatch")
		}
		if i == 0 && (ev.Document == nil || ev.Document.HTML != oneBody || ev.Document.Title != OneTitle) {
			return fail("one_snapshot_mismatch")
		}
		if i == 1 && ev.Document != nil {
			return fail("one_withdrawal_document")
		}
	}
	after, e := a.snapshots(ctx, p.ID)
	if e != nil || len(after) != 2 {
		return fail("one_outbox_count")
	}
	for r, h := range snapshots {
		if after[r] != h {
			return fail("one_outbox_mutated")
		}
	}
	report.Checks = append(report.Checks, "Exactly two immutable accepted durable events: content.published then content.withdrawn; no history, media, edits or republish")
	report.Status = "local-only-passed"
	if c.RequirePeer {
		http := httpClient()
		defer http.CloseIdleConnections()
		var peer PeerStatus
		for {
			peer, e = readPeer(ctx, c, http)
			if e != nil {
				return report, e
			}
			activities, e := peerActivities(peer)
			if e != nil {
				return report, e
			}
			if len(activities) > 2 {
				return fail("one_extra_peer_activity")
			}
			if len(activities) == 2 {
				break
			}
			if e = pause(ctx); e != nil {
				return report, e
			}
		}
		if e = verifyOnePeer(ctx, c, http, peer, p.UUID, rows); e != nil {
			return report, e
		}
		report.WithdrawalObserved = true
		report.Status = "passed"
		report.Checks = append(report.Checks, "Controlled public peer independently verified exactly one Create and Delete from the pinned real actor; current object returned verified HTTPS 410 Tombstone")
	}
	return report, nil
}

func verifyOnePeer(ctx context.Context, c Config, http *http.Client, peer PeerStatus, id string, rows []delivery) error {
	fail := func(s string) error { return errors.New(s) }
	acts, e := peerActivities(peer)
	if e != nil || len(acts) != 2 || acts[0].Type != "Create" || acts[1].Type != "Delete" {
		return fail("one_peer_lifecycle")
	}
	if c.ActorURL == "" || acts[0].Actor != c.ActorURL || acts[1].Actor != c.ActorURL {
		return fail("one_peer_actor")
	}
	var article Article
	var deleted string
	objectURL := c.SiteURL + "/ap/objects/" + c.Publication + "/" + id
	if json.Unmarshal(acts[0].Object, &article) != nil || json.Unmarshal(acts[1].Object, &deleted) != nil || article.ID != objectURL || article.AttributedTo != c.ActorURL || deleted != objectURL || article.Name != OneTitle || article.Content != oneBody || article.URL != c.SiteURL+"/production-test-"+c.RunID+"/" {
		return fail("one_peer_object")
	}
	for _, act := range acts {
		hashes := map[string]bool{}
		count := 0
		for _, attempt := range peer.Attempts {
			if attempt.ID == act.ID {
				if attempt.Type != act.Type || (attempt.Format != "legacy" && attempt.Format != "rfc9421") || len(attempt.BodySHA256) != 64 {
					return fail("one_peer_signature")
				}
				count++
				hashes[attempt.BodySHA256] = true
			}
		}
		if count < 1 || len(hashes) != 1 {
			return fail("one_peer_receipt")
		}
	}
	for _, d := range rows {
		count := 0
		for _, attempt := range peer.CMSAttempts {
			if attempt.EventID == d.eventID {
				count++
				if attempt.BodySHA256 != digest(d.payload) || attempt.OriginalStatus != 202 || attempt.DeliveredStatus != 202 {
					return fail("one_cms_receipt")
				}
			}
		}
		if count != d.attempts {
			return fail("one_cms_attempt_count")
		}
	}
	req, e := httpNewGet(ctx, objectURL)
	if e != nil {
		return e
	}
	res, e := http.Do(req)
	if e != nil {
		return fail("one_tombstone_transport")
	}
	defer res.Body.Close()
	b, e := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if e != nil || res.StatusCode != 410 {
		return fail("one_tombstone_status")
	}
	var value struct{ Type string }
	if json.Unmarshal(bytes.TrimSpace(b), &value) != nil || value.Type != "Tombstone" {
		return fail("one_tombstone_body")
	}
	return nil
}
func httpNewGet(ctx context.Context, url string) (*http.Request, error) {
	r, e := http.NewRequestWithContext(ctx, "GET", url, nil)
	if e != nil {
		return nil, errors.New("one_tombstone_request")
	}
	r.Header.Set("Accept", "application/activity+json")
	return r, nil
}
