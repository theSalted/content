package stagingprobe

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"content/cms"
	"content/internal/httpapi"
	"content/publishing"
	"content/storage/postgres"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
)

type Report struct {
	Status    string   `json:"status"`
	Checks    []string `json:"checks"`
	ContentID string   `json:"content_id"`
}
type post struct {
	ID, UUID, Title, HTML, Status string
	UpdatedAt                     string `json:"updated_at"`
}
type api struct {
	handler    http.Handler
	auth       string
	contentKey string
}

// This known synthetic key is confined to an in-process HTTP handler. It is not
// installed in any service, browser, disk credential store or network listener.
func newAPI(store *postgres.Store, c Config) (api, error) {
	id, secret := strings.Repeat("1", 24), strings.Repeat("2", 64)
	key, _ := httpapi.ParseAdminKey(id + ":" + secret)
	contentKey := "synthetic-staging-probe-content-key"
	h, e := httpapi.New(store, c.Publication, c.SiteURL, key, contentKey)
	if e != nil {
		return api{}, errors.New("cms_handler_configuration")
	}
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{Audience: jwt.ClaimStrings{"/admin/"}, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(4 * time.Minute))})
	token.Header["kid"] = id
	raw, _ := hex.DecodeString(secret)
	signed, e := token.SignedString(raw)
	if e != nil {
		return api{}, errors.New("synthetic_auth")
	}
	return api{h, "Ghost " + signed, contentKey}, nil
}
func (a api) request(ctx context.Context, method, path string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", a.auth)
	w := httptest.NewRecorder()
	a.handler.ServeHTTP(w, r)
	return w
}
func (a api) write(ctx context.Context, p *post, title, html, status, slug string) (post, error) {
	fields := map[string]any{"title": title, "html": html, "status": status}
	path, method, want := "/ghost/api/admin/posts/?source=html", "POST", 201
	if p != nil {
		fields["updated_at"] = p.UpdatedAt
		path = "/ghost/api/admin/posts/" + p.ID + "/?source=html"
		method = "PUT"
		want = 200
	} else {
		fields["slug"] = slug
	}
	w := a.request(ctx, method, path, map[string]any{"posts": []any{fields}})
	var out struct{ Posts []post }
	if w.Code != want || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Posts) != 1 {
		return post{}, errors.New("cms_" + strings.ToLower(method) + "_failed")
	}
	result := out.Posts[0]
	if result.ID == "" || !publishing.ValidUUID(result.UUID) || result.Status != status || result.Title != title || result.HTML != html {
		return post{}, errors.New("cms_response_mismatch")
	}
	if p != nil && (result.ID != p.ID || result.UUID != p.UUID || result.UpdatedAt == p.UpdatedAt) {
		return post{}, errors.New("cms_identity_or_revision")
	}
	return result, nil
}
func (a api) public(ctx context.Context, p post, want int) error {
	w := a.request(ctx, "GET", "/ghost/api/content/posts/"+p.ID+"/?key="+a.contentKey, nil)
	if w.Code != want {
		return errors.New("content_visibility")
	}
	return nil
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

type audit struct {
	conn                             *pgx.Conn
	schema, publication, destination string
}

func (a audit) count(ctx context.Context, table, where string, args ...any) (int, error) {
	var n int
	e := a.conn.QueryRow(ctx, "SELECT count(*) FROM "+a.schema+"."+table+" WHERE publication_id=$1"+where, append([]any{a.publication}, args...)...).Scan(&n)
	if e != nil {
		return 0, errors.New("audit_count")
	}
	return n, nil
}
func (a audit) snapshots(ctx context.Context, id string) (map[int64]string, error) {
	rows, e := a.conn.Query(ctx, "SELECT revision,snapshot::text FROM "+a.schema+".outbox_events WHERE publication_id=$1 AND post_id=$2 ORDER BY revision", a.publication, id)
	if e != nil {
		return nil, errors.New("audit_snapshots")
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var rev int64
		var b string
		if rows.Scan(&rev, &b) != nil {
			return nil, errors.New("audit_snapshot_row")
		}
		out[rev] = digest([]byte(b))
	}
	if rows.Err() != nil {
		return nil, errors.New("audit_snapshot_rows")
	}
	return out, nil
}

type delivery struct {
	eventID, state, code, result string
	revision                     int64
	attempts                     int
	payload                      []byte
}

func (a audit) deliveries(ctx context.Context, id string) ([]delivery, error) {
	rows, e := a.conn.Query(ctx, "SELECT event_id::text,revision,state,attempts,COALESCE(last_error_code,''),COALESCE(accepted_result,''),payload FROM "+a.schema+".publishing_deliveries WHERE publication_id=$1 AND post_id=$2 ORDER BY revision", a.publication, id)
	if e != nil {
		return nil, errors.New("audit_deliveries")
	}
	defer rows.Close()
	out := []delivery{}
	for rows.Next() {
		var d delivery
		if rows.Scan(&d.eventID, &d.revision, &d.state, &d.attempts, &d.code, &d.result, &d.payload) != nil {
			return nil, errors.New("audit_delivery_row")
		}
		out = append(out, d)
	}
	if rows.Err() != nil {
		return nil, errors.New("audit_delivery_rows")
	}
	return out, nil
}
func pause(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return errors.New("probe_timeout")
	case <-time.After(100 * time.Millisecond):
		return nil
	}
}

// Run never migrates or grants rights. It requires an empty synthetic publication,
// writes through the real authenticated CMS handler, then uses real durable queue
// transactions and HTTP delivery. Failed-run evidence is deliberately retained.
func Run(ctx context.Context, c Config) (Report, error) {
	fail := func(code string) (Report, error) { return Report{}, errors.New(code) }
	q, e := postgres.Schema(c.Schema)
	if e != nil {
		return fail("invalid_schema")
	}
	original, e := postgres.Open(ctx, c.APIURL, c.Schema, c.Installation)
	if e != nil {
		return fail("cms_database_open")
	}
	defer original.Close()
	writer, e := postgres.Open(ctx, c.APIURL, c.Schema, c.Installation, postgres.WithNewPostPublishing(publishing.Enrollment{Destination: c.Destination, SourceID: c.Source, SiteURL: c.SiteURL}))
	if e != nil {
		return fail("enrolled_database_open")
	}
	defer writer.Close()
	queue, e := postgres.Open(ctx, c.DeliveryURL, c.Schema, c.Installation)
	if e != nil {
		return fail("delivery_database_open")
	}
	defer func() {
		if queue != nil {
			queue.Close()
		}
	}()
	conn, e := pgx.Connect(ctx, c.DeliveryURL)
	if e != nil {
		return fail("audit_database_open")
	}
	defer conn.Close(context.Background())
	a := audit{conn, q, c.Publication, c.Destination}
	_, total, e := original.Browse(ctx, cms.Query{Publication: c.Publication, Page: 1, Limit: 1})
	if e != nil || total != 0 {
		return fail("publication_not_empty")
	}
	for _, table := range []string{"outbox_events", "publishing_streams", "publishing_deliveries"} {
		n, e := a.count(ctx, table, "")
		if e != nil || n != 0 {
			return fail("publication_queue_not_empty")
		}
	}
	oldAPI, e := newAPI(original, c)
	if e != nil {
		return Report{}, e
	}
	client, e := newAPI(writer, c)
	if e != nil {
		return Report{}, e
	}
	historical, e := oldAPI.write(ctx, nil, "Synthetic un-enrolled history", "<p>Must remain silent.</p>", "published", "staging-history-"+c.RunID)
	if e != nil {
		return Report{}, e
	}
	if worked, e := queue.PrepareNext(ctx, c.Publication, c.Destination); e != nil || worked {
		return fail("history_auto_enrolled")
	}
	title := "Synthetic Go staging draft"
	firstHTML := "<p>Original immutable staging snapshot.</p>"
	editedTitle := "Synthetic Go staging edited"
	editedHTML := "<p>Edited public staging snapshot.</p>"
	privateHTML := "<p>PRIVATE-STAGING-CANARY must never reach federation.</p>"
	republishedHTML := "<p>Republished staging snapshot.</p>"
	p, e := client.write(ctx, nil, title, firstHTML, "draft", "staging-go-"+c.RunID)
	if e != nil {
		return Report{}, e
	}
	if e = client.public(ctx, p, 404); e != nil {
		return Report{}, e
	}
	if worked, e := queue.PrepareNext(ctx, c.Publication, c.Destination); e != nil || worked {
		return fail("draft_generated_event")
	}
	p, e = client.write(ctx, &p, title, firstHTML, "published", "")
	if e != nil {
		return Report{}, e
	}
	if e = client.public(ctx, p, 200); e != nil {
		return Report{}, e
	}
	// Edit before mapping the first publish, proving it uses immutable outbox state.
	p, e = client.write(ctx, &p, editedTitle, editedHTML, "published", "")
	if e != nil {
		return Report{}, e
	}
	snapshots, e := a.snapshots(ctx, p.ID)
	if e != nil {
		return Report{}, e
	}
	if len(snapshots) != 2 {
		return fail("initial_outbox_count")
	}
	sender, e := publishing.NewHTTPClient(c.Endpoint, c.Token)
	if e != nil {
		return fail("publishing_sender")
	}
	defer sender.Close()
	sent := map[string]string{}
	sentCount := map[string]int{}
	observationError := ""
	worker := publishing.Worker{Enabled: true, Publication: c.Publication, Destination: c.Destination, Queue: queue, Sender: sender, Timeout: 5 * time.Second, Lease: 15 * time.Second, Idle: 100 * time.Millisecond, MaxAttempts: 5, Observe: func(job publishing.Job, out publishing.Outcome) {
		h := digest(job.Payload)
		if previous, ok := sent[job.EventID]; ok && previous != h {
			observationError = "retry_payload_changed"
		}
		sent[job.EventID] = h
		sentCount[job.EventID]++
		if out.State == "quarantined" {
			observationError = "delivery_quarantined"
		}
	}}
	if worked, e := worker.Step(ctx); e != nil || !worked {
		return fail("initial_delivery_step")
	}
	firstRows, e := a.deliveries(ctx, p.ID)
	if e != nil {
		return Report{}, e
	}
	if len(firstRows) != 1 || firstRows[0].state != "pending" || firstRows[0].attempts != 1 || firstRows[0].code != "http_retryable" {
		return fail("lost_ack_not_persisted")
	}
	first := firstRows[0]
	event, e := publishing.Decode(first.payload)
	if e != nil || event.Document == nil || event.Document.HTML != firstHTML || event.Document.Title != title {
		return fail("immutable_publish_snapshot")
	}
	// Reopen the queue between lost ACK and retry; no direct retry-clock mutation.
	queue.Close()
	queue, e = postgres.Open(ctx, c.DeliveryURL, c.Schema, c.Installation)
	if e != nil {
		return fail("queue_reconnect")
	}
	worker.Queue = queue
	peerClient := httpClient()
	defer peerClient.CloseIdleConnections()
	var peer PeerStatus
	drain := func(count int) error {
		for {
			rows, e := a.deliveries(ctx, p.ID)
			if e != nil {
				return e
			}
			accepted := 0
			for _, d := range rows {
				if d.state == "accepted" {
					accepted++
				}
				if d.state == "quarantined" {
					return errors.New("delivery_quarantined")
				}
			}
			if accepted == count {
				break
			}
			if accepted > count {
				return errors.New("delivery_order")
			}
			if _, e = worker.Step(ctx); e != nil {
				return errors.New("delivery_step")
			}
			if observationError != "" {
				return errors.New(observationError)
			}
			if e = pause(ctx); e != nil {
				return e
			}
		}
		if c.RequirePeer {
			var e error
			peer, e = awaitPeer(ctx, c, peerClient, count)
			return e
		}
		return nil
	}
	if e = drain(1); e != nil {
		return Report{}, e
	}
	if e = drain(2); e != nil {
		return Report{}, e
	}
	p, e = client.write(ctx, &p, editedTitle, privateHTML, "draft", "")
	if e != nil {
		return Report{}, e
	}
	if e = client.public(ctx, p, 404); e != nil {
		return Report{}, e
	}
	if e = drain(3); e != nil {
		return Report{}, e
	}
	p, e = client.write(ctx, &p, editedTitle, republishedHTML, "published", "")
	if e != nil {
		return Report{}, e
	}
	if e = client.public(ctx, p, 200); e != nil {
		return Report{}, e
	}
	if e = drain(4); e != nil {
		return Report{}, e
	}
	p, e = client.write(ctx, &p, editedTitle, privateHTML, "draft", "")
	if e != nil {
		return Report{}, e
	}
	if e = client.public(ctx, p, 404); e != nil {
		return Report{}, e
	}
	if e = drain(5); e != nil {
		return Report{}, e
	}
	final, e := writer.Get(ctx, cms.Query{Publication: c.Publication, ID: p.ID})
	if e != nil || final.Revision != 6 || final.UUID != p.UUID || final.Status != "draft" {
		return fail("final_cms_revision")
	}
	rows, e := a.deliveries(ctx, p.ID)
	if e != nil {
		return Report{}, e
	}
	if len(rows) != 5 {
		return fail("final_delivery_count")
	}
	instance := ""
	types := []string{"content.published", "content.updated", "content.withdrawn", "content.published", "content.withdrawn"}
	bodies := []string{firstHTML, editedHTML, "", republishedHTML, ""}
	for i, d := range rows {
		ev, e := publishing.Decode(d.payload)
		if e != nil || d.state != "accepted" || d.result != "applied" || d.revision != int64(i+2) || ev.Revision != strconv.Itoa(i+2) || ev.Type != types[i] || ev.ContentID != p.UUID || ev.EventID != d.eventID || ev.Publication != c.Publication || ev.Source.ID != c.Source || ev.Source.Epoch != "1" || sent[d.eventID] != digest(d.payload) {
			return fail("durable_event_mismatch")
		}
		if i < 3 && ev.ObjectInstance != p.UUID {
			return fail("first_instance_identity")
		}
		if i == 3 {
			instance = ev.ObjectInstance
			if instance == p.UUID {
				return fail("republish_instance_reused")
			}
		}
		if i == 4 && ev.ObjectInstance != instance {
			return fail("republish_withdraw_identity")
		}
		if bodies[i] == "" {
			if ev.Document != nil || bytes.Contains(d.payload, []byte("PRIVATE-STAGING-CANARY")) {
				return fail("private_body_leak")
			}
		} else if ev.Document == nil || ev.Document.HTML != bodies[i] {
			return fail("event_snapshot_changed")
		}
		if i == 0 && (d.code != "duplicate" || d.attempts < 2 || sentCount[d.eventID] < 2 || !bytes.Equal(d.payload, first.payload)) {
			return fail("duplicate_ack_not_accepted")
		}
	}
	after, e := a.snapshots(ctx, p.ID)
	if e != nil {
		return Report{}, e
	}
	if len(after) != 5 {
		return fail("final_outbox_count")
	}
	for rev, h := range snapshots {
		if after[rev] != h {
			return fail("outbox_snapshot_mutated")
		}
	}
	var storedInstance, state string
	var lastRevision int64
	var blocked *string
	if e = conn.QueryRow(ctx, "SELECT object_instance::text,lifecycle,last_revision,blocked_reason FROM "+q+".publishing_streams WHERE publication_id=$1 AND post_id=$2", c.Publication, p.ID).Scan(&storedInstance, &state, &lastRevision, &blocked); e != nil || storedInstance != instance || state != "withdrawn" || lastRevision != 6 || blocked != nil {
		return fail("persisted_stream_mismatch")
	}
	n, e := a.count(ctx, "publishing_streams", " AND post_id=$2", historical.ID)
	if e != nil || n != 0 {
		return fail("historical_stream_created")
	}
	n, e = a.count(ctx, "outbox_events", " AND post_id=$2", historical.ID)
	if e != nil || n != 1 {
		return fail("historical_outbox_changed")
	}
	if worked, e := worker.Step(ctx); e != nil || worked {
		return fail("unexpected_remaining_work")
	}
	report := Report{Status: "local-only-passed", ContentID: p.UUID, Checks: []string{"Authenticated real CMS draft, publish, edit, withdraw, republish and final withdrawal", "Stable CMS identity with revisions 1–6 and a new persisted republish object instance", "Five immutable outbox snapshots mapped to five accepted durable v2 delivery rows", "Real HTTP 503 after durable acceptance; identical event ID and bytes retried after queue reconnect", "Duplicate acknowledgment accepted; retry used persisted backoff without operator clock mutation", "Content API exposed only published states; private draft body absent from withdrawal events", "Un-enrolled historical post remained silent; no history backfill or migration privileges"}}
	if c.RequirePeer {
		if e = verifyPeer(ctx, c, peerClient, peer, first.eventID, digest(first.payload), editedTitle); e != nil {
			return Report{}, e
		}
		report.Status = "passed"
		report.Checks = append(report.Checks, "Independent controlled peer verified Create/Update/Delete/Create/Delete signatures and public delivery", "AP durable retry retained identical first Create bytes across both signature formats", "Original and republished public object URLs both returned HTTP 410 Tombstones")
	}
	return report, nil
}
