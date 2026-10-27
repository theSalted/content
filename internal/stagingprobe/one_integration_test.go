package stagingprobe

import (
	"bytes"
	"content/internal/testoracle"
	"content/storage/postgres"
	"context"
	"crypto/rand"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRealPostgresOnePost(t *testing.T) {
	if os.Getenv("CONTENT_TEST_ACTIVITYPUB") != "1" || os.Getenv("CONTENT_TEST_ALLOW_ROLE_CREATE") != "1" || os.Getenv("CONTENT_TEST_DATABASE_URL") == "" {
		t.Skip("requires disposable PostgreSQL and synthetic Node oracle opt-in")
	}
	cfg, e := pgx.ParseConfig(os.Getenv("CONTENT_TEST_DATABASE_URL"))
	if e != nil || (cfg.Host != "127.0.0.1" && cfg.Host != "localhost") || !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatal("only loopback disposable _test database allowed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	control, e := pgx.ConnectConfig(ctx, cfg)
	if e != nil {
		t.Fatal("control connection")
	}
	defer control.Close(context.Background())
	suffix := strings.ToLower(rand.Text()[:10])
	db := "staging_probe_" + suffix + "_test"
	owner := "probe_owner_" + suffix
	apiRole := "probe_api_" + suffix
	deliveryRole := "probe_delivery_" + suffix
	roles := []string{}
	created := false
	quote := func(s string) string { return pgx.Identifier{s}.Sanitize() }
	exec := func(conn *pgx.Conn, s string) {
		t.Helper()
		if _, e := conn.Exec(ctx, s); e != nil {
			t.Fatal("fixture SQL failed", e)
		}
	}
	defer func() {
		if created {
			if _, e := control.Exec(context.Background(), "DROP DATABASE "+quote(db)); e != nil {
				t.Error("disposable database cleanup", e)
			}
		}
		for _, r := range roles {
			if _, e := control.Exec(context.Background(), "DROP ROLE "+quote(r)); e != nil {
				t.Error("disposable role cleanup", e)
			}
		}
	}()
	for _, r := range []string{owner, apiRole, deliveryRole} {
		exec(control, "CREATE ROLE "+quote(r)+" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS")
		roles = append(roles, r)
	}
	exec(control, "CREATE DATABASE "+quote(db))
	created = true
	exec(control, "REVOKE ALL ON DATABASE "+quote(db)+" FROM PUBLIC")
	exec(control, "GRANT CONNECT ON DATABASE "+quote(db)+" TO "+quote(owner)+","+quote(apiRole)+","+quote(deliveryRole))
	cc := cfg.Copy()
	cc.Database = db
	admin, e := pgx.ConnectConfig(ctx, cc)
	if e != nil {
		t.Fatal("test database connection")
	}
	defer admin.Close(context.Background())
	exec(admin, "REVOKE ALL ON SCHEMA public FROM PUBLIC")
	exec(admin, "CREATE SCHEMA cms_staging AUTHORIZATION "+quote(owner))
	dsn := func(role string) string {
		u := url.URL{Scheme: "postgres", Host: net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), User: url.User(role), Path: "/" + db, RawQuery: "sslmode=disable"}
		return u.String()
	}
	if e = postgres.Migrate(ctx, dsn(owner), "cms_staging", "content-staging"); e != nil {
		t.Fatal("migration", e)
	}
	for _, s := range []string{"GRANT USAGE ON SCHEMA cms_staging TO " + quote(apiRole) + "," + quote(deliveryRole), "GRANT SELECT ON cms_staging.installation,cms_staging.posts,cms_staging.post_revisions TO " + quote(apiRole), "GRANT INSERT ON cms_staging.posts,cms_staging.post_revisions,cms_staging.outbox_events,cms_staging.publishing_streams TO " + quote(apiRole), "GRANT UPDATE(title,slug,status,visibility,revision,updated_at,published_at,trashed_at) ON cms_staging.posts TO " + quote(apiRole), "GRANT SELECT ON cms_staging.installation,cms_staging.outbox_events,cms_staging.publishing_streams,cms_staging.publishing_deliveries TO " + quote(deliveryRole), "GRANT INSERT ON cms_staging.publishing_deliveries TO " + quote(deliveryRole), "GRANT UPDATE(last_revision,object_instance,lifecycle,blocked_reason) ON cms_staging.publishing_streams TO " + quote(deliveryRole), "GRANT UPDATE(state,attempts,next_attempt_at,lease_token,lease_until,last_error_code,accepted_result) ON cms_staging.publishing_deliveries TO " + quote(deliveryRole)} {
		exec(admin, s)
	}

	// The current CMS projection and writes use the scheduling/slug tables even
	// when these optional features are disabled. Keep test-only API grants at
	// the production least-privilege contract; never grant table ownership.
	for _, statement := range []string{
		"GRANT SELECT,INSERT,DELETE ON cms_staging.publication_schedules TO " + quote(apiRole),
		"GRANT UPDATE(schedule_id,due_at,expected_revision,attempts,next_attempt_at,last_error_code) ON cms_staging.publication_schedules TO " + quote(apiRole),
		"GRANT SELECT,INSERT ON cms_staging.post_slug_claims TO " + quote(apiRole),
		"GRANT UPDATE(ever_public) ON cms_staging.post_slug_claims TO " + quote(apiRole),
	} {
		exec(admin, statement)
	}
	oracle := testoracle.Start(t, "content-production-test")
	var mu sync.Mutex
	calls := 0
	beforeNetwork := true
	client := httpClient()
	defer client.CloseIdleConnections()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, e := io.ReadAll(io.LimitReader(r.Body, 2<<20))
		if e != nil {
			http.Error(w, "read", 500)
			return
		}
		mu.Lock()
		calls++
		var count int
		var state string
		var rev int64
		e = admin.QueryRow(ctx, "SELECT status,revision FROM cms_staging.posts WHERE publication_id='content-production-test'").Scan(&state, &rev)
		e2 := admin.QueryRow(ctx, "SELECT count(*) FROM cms_staging.outbox_events WHERE publication_id='content-production-test'").Scan(&count)
		beforeNetwork = beforeNetwork && e == nil && e2 == nil && state == "draft" && rev == 3 && count == 2
		mu.Unlock()
		req, _ := http.NewRequestWithContext(r.Context(), "POST", oracle.URL+"/internal/v2/events", bytes.NewReader(b))
		req.Header.Set("Authorization", r.Header.Get("Authorization"))
		req.Header.Set("Content-Type", "application/json")
		res, e := client.Do(req)
		if e != nil {
			http.Error(w, "upstream", 500)
			return
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(res.StatusCode)
		w.Write(data)
	}))
	defer proxy.Close()
	c := Config{APIURL: dsn(apiRole), DeliveryURL: dsn(deliveryRole), Schema: "cms_staging", Installation: "content-staging", Publication: "content-production-test", Source: "content", Destination: "activitypub", Endpoint: proxy.URL + "/internal/v2/events", Token: testoracle.Token, SiteURL: "https://example.invalid", RunID: "11111111-1111-4111-8111-111111111111"}
	gate := OneGate{SpendUSD: 0, MeasuredAt: time.Now()}
	report, e := RunOne(ctx, c, gate)
	if e != nil || report.Status != "local-only-passed" || !report.WithdrawalCommitted || !report.WithdrawalAccepted || report.WithdrawalObserved {
		t.Fatal("one post", report, e)
	}
	mu.Lock()
	if calls != 2 || !beforeNetwork {
		t.Error("withdrawal not durable before delivery", calls, beforeNetwork)
	}
	mu.Unlock()
	res, e := client.Get(oracle.URL + "/_test/state")
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	var state struct {
		Activities []struct{ Kind string }
		Receipts   []json.RawMessage
	}
	if json.NewDecoder(res.Body).Decode(&state) != nil || len(state.Activities) != 2 || len(state.Receipts) != 2 || state.Activities[0].Kind != "Create" || state.Activities[1].Kind != "Delete" {
		t.Fatal("unexpected Node lifecycle")
	}
	if _, e = RunOne(ctx, c, gate); e == nil || e.Error() != "one_publication_not_empty" {
		t.Fatal("used publication replayed", e)
	}
	failedReceiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "synthetic unavailable", 503) }))
	defer failedReceiver.Close()
	c.Publication = "content-one-failure"
	c.Endpoint = failedReceiver.URL + "/internal/v2/events"
	short, stop := context.WithTimeout(ctx, 2*time.Second)
	defer stop()
	failed, e := RunOne(short, c, OneGate{MeasuredAt: time.Now()})
	if e == nil || failed.Status != "failed" || !failed.WithdrawalCommitted || failed.WithdrawalAccepted || failed.WithdrawalObserved {
		t.Fatal("failure obligation missing", failed, e)
	}
	var obligations int
	var final string
	if admin.QueryRow(ctx, "SELECT count(*) FROM cms_staging.outbox_events WHERE publication_id=$1", c.Publication).Scan(&obligations) != nil || obligations != 2 {
		t.Fatal("outbox lost after failure")
	}
	if admin.QueryRow(ctx, "SELECT status FROM cms_staging.posts WHERE publication_id=$1", c.Publication).Scan(&final) != nil || final != "draft" {
		t.Fatal("failed run left public CMS post")
	}
	t.Log("one post, two events, withdrawal committed before network; exactly Create/Delete accepted; used-scope guard; outage retains withdrawal obligation")
}
