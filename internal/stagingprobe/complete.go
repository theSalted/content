package stagingprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"

	"content/cms"
	"content/publishing"
	"content/storage/postgres"
	"github.com/jackc/pgx/v5"
)

// readOnlyURL forces server-enforced read-only transactions on every connection,
// including connections opened lazily by the store. It never changes role defaults.
func readOnlyURL(raw string) (string, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" {
		return "", errors.New("readonly_configuration")
	}
	q := u.Query()
	q.Set("default_transaction_read_only", "on")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

type savedIntent struct {
	eventID, kind string
	revision      int64
	snapshot      publishing.Snapshot
	raw           []byte
}

func verifySaved(c Config, p cms.Post, ds []delivery, intents []savedIntent, peer PeerStatus) (string, error) {
	fail := func(code string) (string, error) { return "", errors.New(code) }
	if len(ds) != 5 || len(intents) != 5 || len(peer.CMSAttempts) != 6 {
		return fail("recovery_event_count")
	}
	types := []string{"content.published", "content.updated", "content.withdrawn", "content.published", "content.withdrawn"}
	kinds := []string{"post.published", "post.updated", "post.unpublished", "post.published", "post.unpublished"}
	bodies := []string{"<p>Original immutable staging snapshot.</p>", "<p>Edited public staging snapshot.</p>", "", "<p>Republished staging snapshot.</p>", ""}
	instance := ""
	for i, d := range ds {
		ev, e := publishing.Decode(d.payload)
		in := intents[i]
		title := "Synthetic Go staging edited"
		if i == 0 {
			title = "Synthetic Go staging draft"
		}
		if e != nil || d.state != "accepted" || d.result != "applied" || d.revision != int64(i+2) || ev.Revision != strconv.Itoa(i+2) || ev.Type != types[i] || ev.ContentID != p.UUID || ev.EventID != d.eventID || ev.Publication != c.Publication || ev.Source.ID != c.Source || ev.Source.Epoch != "1" {
			return fail("recovery_delivery_mismatch")
		}
		if in.eventID != d.eventID || in.revision != d.revision || in.kind != kinds[i] || in.snapshot.ID != p.ID || in.snapshot.UUID != p.UUID || in.snapshot.Title != title || in.snapshot.Slug != p.Slug || in.snapshot.Visibility != "public" {
			return fail("recovery_snapshot_identity")
		}
		if i < 3 && ev.ObjectInstance != p.UUID {
			return fail("recovery_first_instance")
		}
		if i == 3 {
			instance = ev.ObjectInstance
			if instance == p.UUID {
				return fail("recovery_republish_instance")
			}
		}
		if i == 4 && ev.ObjectInstance != instance {
			return fail("recovery_withdraw_instance")
		}
		if bodies[i] == "" {
			var fields map[string]json.RawMessage
			if json.Unmarshal(in.raw, &fields) != nil || fields["html"] != nil || in.snapshot.Status != "draft" || ev.Document != nil || bytes.Contains(d.payload, []byte("PRIVATE-STAGING-CANARY")) {
				return fail("recovery_private_body")
			}
		} else if ev.Document == nil || ev.Document.HTML != bodies[i] || ev.Document.Title != title || in.snapshot.HTML != bodies[i] || in.snapshot.Status != "published" {
			return fail("recovery_snapshot_body")
		}
		attempts := 0
		for _, a := range peer.CMSAttempts {
			if a.EventID == d.eventID {
				attempts++
				if a.BodySHA256 != digest(d.payload) || a.OriginalStatus != 202 {
					return fail("recovery_receipt_bytes")
				}
			}
		}
		want := 1
		if i == 0 {
			want = 2
			if d.code != "duplicate" {
				return fail("recovery_duplicate_ack")
			}
		}
		if attempts != want || d.attempts != want {
			return fail("recovery_attempt_count")
		}
	}
	return instance, nil
}

// Complete only observes the original completed write phase. It never calls the
// CMS write API, delivery worker, mapper, migrations or any remote POST endpoint.
// Original peer receipts are recovered evidence, not a new uninterrupted run.
func Complete(ctx context.Context, c Config) (Report, error) {
	fail := func(code string) (Report, error) { return Report{}, errors.New(code) }
	if !c.RequirePeer {
		return fail("recovery_peer_required")
	}
	var e error
	c.APIURL, e = readOnlyURL(c.APIURL)
	if e != nil {
		return Report{}, e
	}
	c.DeliveryURL, e = readOnlyURL(c.DeliveryURL)
	if e != nil {
		return Report{}, e
	}
	q, e := postgres.Schema(c.Schema)
	if e != nil {
		return fail("invalid_schema")
	}
	apiConn, e := openReadOnly(ctx, c.APIURL)
	if e != nil {
		return fail("recovery_api_readonly")
	}
	defer apiConn.Close(context.Background())
	conn, e := openReadOnly(ctx, c.DeliveryURL)
	if e != nil {
		return fail("recovery_delivery_readonly")
	}
	defer conn.Close(context.Background())
	var installation, module string
	if apiConn.QueryRow(ctx, "SELECT installation_id,module_id FROM "+q+".installation WHERE singleton").Scan(&installation, &module) != nil || installation != c.Installation || module != "cms" {
		return fail("recovery_installation")
	}
	get := func(slug string) (cms.Post, error) {
		var p cms.Post
		e := apiConn.QueryRow(ctx, "SELECT p.id,p.uuid::text,p.title,p.slug,p.status,p.revision,r.html FROM "+q+".posts p JOIN "+q+".post_revisions r ON r.publication_id=p.publication_id AND r.post_id=p.id AND r.revision=p.revision WHERE p.publication_id=$1 AND p.slug=$2", c.Publication, slug).Scan(&p.ID, &p.UUID, &p.Title, &p.Slug, &p.Status, &p.Revision, &p.HTML)
		return p, e
	}
	p, e := get("staging-go-" + c.RunID)
	if e != nil || p.Revision != 6 || p.Status != "draft" || p.HTML != "<p>PRIVATE-STAGING-CANARY must never reach federation.</p>" {
		return fail("recovery_final_cms")
	}
	var total int
	if apiConn.QueryRow(ctx, "SELECT count(*) FROM "+q+".posts WHERE publication_id=$1", c.Publication).Scan(&total) != nil || total != 2 {
		return fail("recovery_publication_count")
	}
	historical, e := get("staging-history-" + c.RunID)
	if e != nil || historical.Revision != 1 || historical.Status != "published" || historical.Title != "Synthetic un-enrolled history" {
		return fail("recovery_history")
	}
	for id, want := range map[string]int{p.ID: 0, historical.ID: 1} {
		var n int
		e := apiConn.QueryRow(ctx, "SELECT count(*) FROM "+q+".posts WHERE publication_id=$1 AND id=$2 AND status='published' AND visibility='public'", c.Publication, id).Scan(&n)
		if e != nil || n != want {
			return fail("recovery_public_visibility")
		}
	}
	a := audit{conn, q, c.Publication, c.Destination}
	ds, e := a.deliveries(ctx, p.ID)
	if e != nil {
		return Report{}, e
	}
	rows, e := conn.Query(ctx, "SELECT event_id::text,revision,event_type,snapshot FROM "+q+".outbox_events WHERE publication_id=$1 AND post_id=$2 ORDER BY revision", c.Publication, p.ID)
	if e != nil {
		return fail("recovery_outbox_read")
	}
	intents := []savedIntent{}
	for rows.Next() {
		var in savedIntent
		if rows.Scan(&in.eventID, &in.revision, &in.kind, &in.raw) != nil || json.Unmarshal(in.raw, &in.snapshot) != nil {
			rows.Close()
			return fail("recovery_outbox_row")
		}
		intents = append(intents, in)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return fail("recovery_outbox_rows")
	}
	http := httpClient()
	defer http.CloseIdleConnections()
	peer, e := readPeer(ctx, c, http)
	if e != nil {
		return Report{}, e
	}
	instance, e := verifySaved(c, p, ds, intents, peer)
	if e != nil {
		return Report{}, e
	}
	var stored, state, source, destination, site, content string
	var revision, epoch int64
	var blocked *string
	e = conn.QueryRow(ctx, "SELECT object_instance::text,lifecycle,last_revision,blocked_reason,source_id,destination,site_url,content_id::text,source_epoch FROM "+q+".publishing_streams WHERE publication_id=$1 AND post_id=$2", c.Publication, p.ID).Scan(&stored, &state, &revision, &blocked, &source, &destination, &site, &content, &epoch)
	if e != nil || stored != instance || state != "withdrawn" || revision != 6 || blocked != nil || source != c.Source || destination != c.Destination || site != c.SiteURL || content != p.UUID || epoch != 1 {
		return fail("recovery_stream")
	}
	for table, want := range map[string]int{"publishing_streams": 1, "publishing_deliveries": 5, "outbox_events": 6} {
		n, e := a.count(ctx, table, "")
		if e != nil || n != want {
			return fail("recovery_queue_count")
		}
	}
	for table, want := range map[string]int{"publishing_streams": 0, "publishing_deliveries": 0, "outbox_events": 1} {
		n, e := a.count(ctx, table, " AND post_id=$2", historical.ID)
		if e != nil || n != want {
			return fail("recovery_history_queue")
		}
	}
	activities, e := peerActivities(peer)
	if e != nil || len(activities) != 5 {
		return fail("recovery_peer_activity_count")
	}
	for _, i := range []int{0, 1, 3} {
		var article Article
		ev, e := publishing.Decode(ds[i].payload)
		if e != nil || json.Unmarshal(activities[i].Object, &article) != nil || ev.Document == nil || article.ID != c.SiteURL+"/ap/objects/"+c.Publication+"/"+ev.ObjectInstance || article.Name != ev.Document.Title || article.Content != ev.Document.HTML || article.URL != ev.Document.URL {
			return fail("recovery_peer_snapshot_mismatch")
		}
	}
	if e = verifyPeer(ctx, c, http, peer, ds[0].eventID, digest(ds[0].payload), "Synthetic Go staging edited"); e != nil {
		return Report{}, e
	}
	return Report{Status: "passed", ContentID: p.UUID, Checks: []string{
		"Read-only recovery of the original run; its initial tombstone_transport failure remains recorded",
		"Explicit server-enforced read-only transactions; no CMS writes, delivery replay, Follow or new federation activities",
		"Persisted final draft revision 6 is ineligible for Content API; un-enrolled history remains published and silent",
		"Five persisted accepted payloads match original immutable outbox snapshots, event IDs and peer request hashes",
		"Original lost acknowledgment and duplicate receipt match identical persisted bytes and two delivery attempts",
		"Stable CMS identity; new republish instance; both persisted withdrawal events exclude private bodies",
		"Original controlled-peer signature receipt metadata and reconstructed Activity payloads validate the five-event lifecycle",
		"Normal verified HTTPS returned HTTP 410 Tombstone for both original and republished object URLs",
	}}, nil
}

// Poolers may drop startup parameters. An explicit transaction makes the actual
// database enforce this boundary regardless of session startup configuration.
func openReadOnly(ctx context.Context, dsn string) (*pgx.Conn, error) {
	c, e := pgx.Connect(ctx, dsn)
	if e != nil {
		return nil, e
	}
	if _, e = c.Exec(ctx, "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY"); e != nil {
		c.Close(ctx)
		return nil, e
	}
	var ro string
	if e = c.QueryRow(ctx, "SHOW transaction_read_only").Scan(&ro); e != nil || ro != "on" {
		c.Close(ctx)
		return nil, errors.New("readonly_transaction_required")
	}
	return c, nil
}
