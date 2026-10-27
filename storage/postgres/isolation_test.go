package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"content/cms"
	"content/internal/httpapi"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type roleFixture struct {
	ctx            context.Context
	admin          *pgx.Conn
	runtime        *pgx.Conn
	runtimeDSN     string
	migrateDSN     string
	otherDSN       string
	schema         string
	otherSchema    string
	consumerSchema string
	owner          string
	otherOwner     string
	runtimeRole    string
	database       string
	rolePassword   string
}

func ident(name string) string { return pgx.Identifier{name}.Sanitize() }

// Unlike the persistence test, this fixture needs permission to create transient
// roles and a dedicated disposable database. It changes no existing database ACL.
func newRoleFixture(t *testing.T) *roleFixture {
	t.Helper()
	dsn := os.Getenv("CONTENT_TEST_DATABASE_URL")
	if dsn == "" || os.Getenv("CONTENT_TEST_ALLOW_ROLE_CREATE") != "1" {
		t.Skip("requires an explicitly disposable local PostgreSQL admin DSN and CONTENT_TEST_ALLOW_ROLE_CREATE=1")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test connection configuration")
	}
	if (cfg.Host != "127.0.0.1" && cfg.Host != "localhost" && cfg.Host != "::1") || !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatal("role tests require a loopback database ending in _test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	control, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal("test control connection failed")
	}
	t.Cleanup(func() { _ = control.Close(context.Background()) })
	suffix := strings.ToLower(rand.Text()[:12])
	f := &roleFixture{ctx: ctx, database: "content_roles_" + suffix + "_test", schema: "cms_a", otherSchema: "cms_b", consumerSchema: "consumer_data", owner: "content_migrate_a_" + suffix, otherOwner: "content_migrate_b_" + suffix, runtimeRole: "content_runtime_" + suffix}
	f.rolePassword = rand.Text()
	roles := []string{}
	databaseCreated := false
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if databaseCreated {
			if _, err := control.Exec(cleanup, "DROP DATABASE "+ident(f.database)); err != nil {
				t.Error("disposable role-test database cleanup failed", err)
			}
		}
		for _, role := range roles {
			if _, err := control.Exec(cleanup, "DROP ROLE "+ident(role)); err != nil {
				t.Error("transient role cleanup failed", err)
			}
		}
	})
	for _, role := range []string{f.owner, f.otherOwner, f.runtimeRole} {
		createTestRole(t, ctx, control, role, f.rolePassword)
		roles = append(roles, role)
	}
	mustExec(t, ctx, control, "CREATE DATABASE "+ident(f.database))
	databaseCreated = true
	// PUBLIC TEMP would otherwise permit temporary DDL even with schema CREATE
	// revoked. This database belongs only to this test; no shared ACL is changed.
	mustExec(t, ctx, control, "REVOKE ALL ON DATABASE "+ident(f.database)+" FROM PUBLIC")
	mustExec(t, ctx, control, "GRANT CONNECT ON DATABASE "+ident(f.database)+" TO "+ident(f.owner)+","+ident(f.otherOwner)+","+ident(f.runtimeRole))
	dbConfig := cfg.Copy()
	dbConfig.Database = f.database
	f.admin, err = pgx.ConnectConfig(ctx, dbConfig)
	if err != nil {
		t.Fatal("disposable database connection failed")
	}
	t.Cleanup(func() { _ = f.admin.Close(context.Background()) })
	mustExec(t, ctx, f.admin, "REVOKE ALL ON SCHEMA public FROM PUBLIC")
	mustExec(t, ctx, f.admin, "CREATE SCHEMA "+ident(f.schema)+" AUTHORIZATION "+ident(f.owner))
	mustExec(t, ctx, f.admin, "CREATE SCHEMA "+ident(f.otherSchema)+" AUTHORIZATION "+ident(f.otherOwner))
	mustExec(t, ctx, f.admin, "CREATE SCHEMA "+ident(f.consumerSchema)+" AUTHORIZATION "+ident(f.otherOwner))
	mustExec(t, ctx, f.admin, "CREATE TABLE "+ident(f.consumerSchema)+".private_records(value text NOT NULL)")
	mustExec(t, ctx, f.admin, "ALTER TABLE "+ident(f.consumerSchema)+".private_records OWNER TO "+ident(f.otherOwner))
	mustExec(t, ctx, f.admin, "INSERT INTO "+ident(f.consumerSchema)+".private_records VALUES ('synthetic consumer-only record')")
	roleDSN := func(role string) string {
		// Transient random passwords also support existing password-authenticated
		// local test servers without weakening their authentication rules.
		u := url.URL{Scheme: "postgres", User: url.UserPassword(role, f.rolePassword), Host: net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), Path: "/" + f.database}
		u.RawQuery = "sslmode=disable&application_name=content-role-isolation-test"
		return u.String()
	}
	f.migrateDSN = roleDSN(f.owner)
	f.otherDSN = roleDSN(f.otherOwner)
	f.runtimeDSN = roleDSN(f.runtimeRole)
	if err = Migrate(ctx, f.migrateDSN, f.schema, "installation-a"); err != nil {
		t.Fatal("non-superuser migration A failed", err)
	}
	if err = Migrate(ctx, f.otherDSN, f.otherSchema, "installation-b"); err != nil {
		t.Fatal("non-superuser migration B failed", err)
	}
	q := ident(f.schema)
	mustExec(t, ctx, f.admin, "GRANT SELECT,INSERT,DELETE ON "+q+".publication_schedules TO "+ident(f.runtimeRole))
	mustExec(t, ctx, f.admin, "GRANT UPDATE(schedule_id,due_at,expected_revision,attempts,next_attempt_at,last_error_code) ON "+q+".publication_schedules TO "+ident(f.runtimeRole))
	mustExec(t, ctx, f.admin, "GRANT SELECT,INSERT ON "+q+".post_slug_claims TO "+ident(f.runtimeRole))
	mustExec(t, ctx, f.admin, "GRANT UPDATE(ever_public) ON "+q+".post_slug_claims TO "+ident(f.runtimeRole))
	mustExec(t, ctx, f.admin, "GRANT USAGE ON SCHEMA "+q+" TO "+ident(f.runtimeRole))
	mustExec(t, ctx, f.admin, "GRANT SELECT ON "+q+".installation,"+q+".posts,"+q+".post_revisions TO "+ident(f.runtimeRole))
	mustExec(t, ctx, f.admin, "GRANT INSERT ON "+q+".posts,"+q+".post_revisions,"+q+".outbox_events TO "+ident(f.runtimeRole))
	mustExec(t, ctx, f.admin, "GRANT UPDATE(title,slug,status,visibility,revision,updated_at,published_at,trashed_at) ON "+q+".posts TO "+ident(f.runtimeRole))
	// A misleading search_path must not redirect schema-qualified application SQL.
	mustExec(t, ctx, f.admin, "ALTER ROLE "+ident(f.runtimeRole)+" IN DATABASE "+ident(f.database)+" SET search_path = "+ident(f.consumerSchema)+",public")
	f.runtime, err = pgx.Connect(ctx, f.runtimeDSN)
	if err != nil {
		t.Fatal("restricted runtime connection failed")
	}
	t.Cleanup(func() { _ = f.runtime.Close(context.Background()) })
	return f
}

func mustExec(t *testing.T, ctx context.Context, conn *pgx.Conn, query string, args ...any) {
	t.Helper()
	if _, err := conn.Exec(ctx, query, args...); err != nil {
		t.Fatal(query, err)
	}
}
func denied(t *testing.T, err error) {
	t.Helper()
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "42501" {
		t.Fatalf("expected PostgreSQL insufficient_privilege, got %v", err)
	}
}

func TestPostgresRoleIsolation(t *testing.T) {
	f := newRoleFixture(t)
	t.Run("runtime has no administrative capabilities", func(t *testing.T) {
		var super, createdb, createrole, bypass, dbCreate, dbTemp, schemaCreate bool
		if err := f.runtime.QueryRow(f.ctx, `SELECT rolsuper,rolcreatedb,rolcreaterole,rolbypassrls,has_database_privilege(current_database(),'CREATE'),has_database_privilege(current_database(),'TEMP'),has_schema_privilege($1,'CREATE') FROM pg_roles WHERE rolname=current_user`, f.schema).Scan(&super, &createdb, &createrole, &bypass, &dbCreate, &dbTemp, &schemaCreate); err != nil {
			t.Fatal(err)
		}
		if super || createdb || createrole || bypass || dbCreate || dbTemp || schemaCreate {
			t.Fatal("runtime has administrative privileges")
		}
	})
	t.Run("runtime cannot perform DDL or assume migration role", func(t *testing.T) {
		q := ident(f.schema)
		for _, query := range []string{
			"CREATE TABLE " + q + ".forbidden(id integer)",
			"CREATE TABLE public.forbidden(id integer)",
			"CREATE SCHEMA forbidden",
			"CREATE TEMP TABLE forbidden_temp(id integer)",
			"ALTER TABLE " + q + ".posts ADD COLUMN forbidden text",
			"DROP TABLE " + q + ".posts",
			"TRUNCATE TABLE " + q + ".outbox_events",
			"SET ROLE " + ident(f.owner),
		} {
			_, err := f.runtime.Exec(f.ctx, query)
			denied(t, err)
		}
	})
	t.Run("runtime cannot migrate or mutate installation and ledger", func(t *testing.T) {
		denied(t, Migrate(f.ctx, f.runtimeDSN, f.schema, "installation-a"))
		for _, query := range []string{
			"SELECT * FROM " + ident(f.schema) + ".goose_db_version",
			"INSERT INTO " + ident(f.schema) + ".goose_db_version(version_id,is_applied) VALUES(999,true)",
			"UPDATE " + ident(f.schema) + ".installation SET installation_id='forged'",
			"DELETE FROM " + ident(f.schema) + ".post_revisions",
			"UPDATE " + ident(f.schema) + ".posts SET publication_id='forged'",
			"UPDATE " + ident(f.schema) + ".posts SET id='forged'",
			"UPDATE " + ident(f.schema) + ".posts SET uuid='00000000-0000-0000-0000-000000000000'",
			"UPDATE " + ident(f.schema) + ".posts SET created_at=now()",
			"UPDATE " + ident(f.schema) + ".post_revisions SET html='forged'",
			"UPDATE " + ident(f.schema) + ".outbox_events SET event_type='forged'",
		} {
			_, err := f.runtime.Exec(f.ctx, query)
			denied(t, err)
		}
		if err := Migrate(f.ctx, f.migrateDSN, f.schema, "installation-a"); err != nil {
			t.Fatal("migration owner must remain able to migrate", err)
		}
	})
	t.Run("other module and consumer schemas remain private", func(t *testing.T) {
		for _, query := range []string{
			"SELECT * FROM " + ident(f.otherSchema) + ".posts",
			"SELECT * FROM " + ident(f.consumerSchema) + ".private_records",
			"INSERT INTO " + ident(f.consumerSchema) + ".private_records VALUES('forged')",
		} {
			_, err := f.runtime.Exec(f.ctx, query)
			denied(t, err)
		}
		if s, err := Open(f.ctx, f.runtimeDSN, f.otherSchema, "installation-b"); err == nil {
			s.Close()
			t.Fatal("runtime opened another module schema")
		}
		denied(t, Migrate(f.ctx, f.migrateDSN, f.otherSchema, "installation-b"))
		var value string
		if err := f.admin.QueryRow(f.ctx, "SELECT value FROM "+ident(f.consumerSchema)+".private_records").Scan(&value); err != nil || value != "synthetic consumer-only record" {
			t.Fatal("consumer data changed", err)
		}
	})
	t.Run("restricted runtime preserves HTTP publication boundaries", func(t *testing.T) { testRestrictedPublications(t, f) })
	t.Run("migration locks serialize one namespace without blocking another", func(t *testing.T) { testMigrationLocks(t, f) })
	t.Run("first migration binds one installation under concurrent claims", func(t *testing.T) {
		const schema = "cms_claim_race"
		mustExec(t, f.ctx, f.admin, "CREATE SCHEMA "+ident(schema)+" AUTHORIZATION "+ident(f.owner))
		type result struct {
			id  string
			err error
		}
		results := make(chan result, 2)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for _, id := range []string{"claim-a", "claim-b"} {
			wg.Add(1)
			go func() { defer wg.Done(); <-start; results <- result{id, Migrate(f.ctx, f.migrateDSN, schema, id)} }()
		}
		close(start)
		wg.Wait()
		close(results)
		successes := 0
		winner := ""
		for r := range results {
			if r.err == nil {
				successes++
				winner = r.id
			} else if !strings.Contains(r.err.Error(), "belongs to a different installation") {
				t.Error("claim failed outside installation boundary", r.err)
			}
		}
		var stored string
		if err := f.admin.QueryRow(f.ctx, "SELECT installation_id FROM "+ident(schema)+".installation WHERE singleton").Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if successes != 1 || stored != winner {
			t.Fatal("schema was not bound to exactly one successful claim", successes, stored, winner)
		}
	})
}

func testRestrictedPublications(t *testing.T, f *roleFixture) {
	store, err := Open(f.ctx, f.runtimeDSN, f.schema, "installation-a")
	if err != nil {
		t.Fatal("restricted runtime should open own schema despite foreign search_path", err)
	}
	defer store.Close()
	type client struct {
		app   *httpapi.Server
		admin string
		key   string
	}
	clients := map[string]client{}
	for i, pub := range []string{"publication-a", "publication-b"} {
		id := strings.Repeat(strconv.Itoa(i+1), 24)
		secret := strings.Repeat(strconv.Itoa(i+1), 64)
		key, err := httpapi.ParseAdminKey(id + ":" + secret)
		if err != nil {
			t.Fatal(err)
		}
		contentKey := "synthetic-content-key-" + pub
		app, err := httpapi.New(store, pub, "https://example.invalid/"+pub, key, contentKey)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{Audience: jwt.ClaimStrings{"/admin/"}, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute))})
		token.Header["kid"] = id
		secretBytes, _ := hex.DecodeString(secret)
		signed, err := token.SignedString(secretBytes)
		if err != nil {
			t.Fatal(err)
		}
		clients[pub] = client{app, "Ghost " + signed, contentKey}
	}
	request := func(c client, method, path, body, auth string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", auth)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		c.app.ServeHTTP(w, r)
		return w
	}
	type postDTO struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		HTML  string `json:"html"`
	}
	decode := func(w *httptest.ResponseRecorder, code int) []postDTO {
		t.Helper()
		if w.Code != code {
			t.Fatalf("HTTP %d, want %d: %s", w.Code, code, w.Body.String())
		}
		var body struct {
			Posts []postDTO `json:"posts"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Posts
	}
	ids := map[string]string{}
	for pub, c := range clients {
		// Same slug in both publications must be allowed, with independent rows.
		body := `{"posts":[{"title":"` + pub + `","slug":"shared-slug","html":"<p>` + pub + `</p>","status":"published"}]}`
		posts := decode(request(c, "POST", "/ghost/api/admin/posts/?source=html", body, c.admin), 201)
		if len(posts) != 1 {
			t.Fatal("expected created post")
		}
		ids[pub] = posts[0].ID
	}
	a, b := clients["publication-a"], clients["publication-b"]
	for pub, c := range clients {
		posts := decode(request(c, "GET", "/ghost/api/content/posts/?key="+c.key, "", ""), 200)
		if len(posts) != 1 || posts[0].Title != pub {
			t.Fatal("publication browse leaked", posts)
		}
		posts = decode(request(c, "GET", "/ghost/api/content/posts/slug/shared-slug/?key="+c.key, "", ""), 200)
		if len(posts) != 1 || posts[0].Title != pub {
			t.Fatal("slug lookup escaped publication")
		}
	}
	for _, w := range []*httptest.ResponseRecorder{
		request(b, "GET", "/ghost/api/admin/posts/"+ids["publication-a"]+"/", "", b.admin),
		request(b, "GET", "/ghost/api/content/posts/"+ids["publication-a"]+"/?key="+b.key, "", ""),
	} {
		if w.Code != 404 {
			t.Fatal("cross-publication read accepted", w.Code)
		}
	}
	for _, w := range []*httptest.ResponseRecorder{
		request(b, "POST", "/ghost/api/admin/posts/?source=html", `{"posts":[{"title":"forged"}]}`, a.admin),
		request(b, "GET", "/ghost/api/content/posts/?key="+a.key, "", ""),
	} {
		if w.Code != 401 {
			t.Fatal("cross-publication credential accepted", w.Code)
		}
	}
	for _, field := range []string{"publication", "publication_id"} {
		w := request(a, "POST", "/ghost/api/admin/posts/?source=html", `{"posts":[{"title":"forged","`+field+`":"publication-b"}]}`, a.admin)
		if w.Code != 400 {
			t.Fatal("caller chose write publication", w.Code)
		}
	}
	var total int
	if err := f.admin.QueryRow(f.ctx, "SELECT count(*) FROM "+ident(f.schema)+".posts").Scan(&total); err != nil || total != 2 {
		t.Fatal("rejected writes changed storage", total, err)
	}
	// Imported external IDs may collide across publications. Join both ID and
	// publication so the wrong revision cannot leak through an otherwise scoped row.
	for _, pub := range []string{"publication-a", "publication-b"} {
		p, err := cms.NewPost(pub, cms.CreatePost{Title: pub + " collision", Slug: "colliding-id", HTML: "<p>" + pub + " collision</p>"})
		if err != nil {
			t.Fatal(err)
		}
		p.ID = strings.Repeat("a", 24)
		if _, err = store.Create(f.ctx, p); err != nil {
			t.Fatal("scoped external ID creation", err)
		}
	}
	for pub, c := range clients {
		posts := decode(request(c, "GET", "/ghost/api/admin/posts/"+strings.Repeat("a", 24)+"/", "", c.admin), 200)
		if len(posts) != 1 || posts[0].HTML != "<p>"+pub+" collision</p>" {
			t.Fatal("revision join escaped publication", posts)
		}
	}
}

func testMigrationLocks(t *testing.T, f *roleFixture) {
	holder, err := pgx.Connect(f.ctx, f.migrateDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close(context.Background())
	peer, err := pgx.Connect(f.ctx, f.otherDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close(context.Background())
	key := MigrationLockID("installation-a", f.schema)
	mustExec(t, f.ctx, holder, "SELECT pg_advisory_lock($1)", key)
	defer func() { _, _ = holder.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", key) }()
	var acquired bool
	if err = peer.QueryRow(f.ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&acquired); err != nil || acquired {
		t.Fatal("same migration namespace was not exclusive", acquired, err)
	}
	for _, otherKey := range []int64{MigrationLockID("installation-a", f.otherSchema), MigrationLockID("installation-b", f.schema)} {
		if err = peer.QueryRow(f.ctx, "SELECT pg_try_advisory_lock($1)", otherKey).Scan(&acquired); err != nil || !acquired {
			t.Fatal("unrelated namespace blocked", err)
		}
		mustExec(t, f.ctx, peer, "SELECT pg_advisory_unlock($1)", otherKey)
	}
	// Exercise the actual migrator while the first namespace stays locked.
	otherCtx, cancel := context.WithTimeout(f.ctx, 3*time.Second)
	err = Migrate(otherCtx, f.otherDSN, f.otherSchema, "installation-b")
	cancel()
	if err != nil {
		t.Fatal("another schema's migration blocked", err)
	}
	blockedCtx, cancel := context.WithTimeout(f.ctx, 350*time.Millisecond)
	err = Migrate(blockedCtx, f.migrateDSN, f.schema, "installation-a")
	if err == nil || !errors.Is(blockedCtx.Err(), context.DeadlineExceeded) {
		cancel()
		t.Fatal("migrator ignored its namespace lock", err)
	}
	cancel()
	if err = peer.QueryRow(f.ctx, "SELECT pg_try_advisory_lock($1)", schemaOwnershipLockID(f.schema)).Scan(&acquired); err != nil || !acquired {
		t.Fatal("timeout leaked schema ownership lock", err)
	}
	mustExec(t, f.ctx, peer, "SELECT pg_advisory_unlock($1)", schemaOwnershipLockID(f.schema))
	mustExec(t, f.ctx, holder, "SELECT pg_advisory_unlock($1)", key)
	if err = Migrate(f.ctx, f.migrateDSN, f.schema, "installation-a"); err != nil {
		t.Fatal("migration failed after lock released", err)
	}
}

func createTestRole(t *testing.T, ctx context.Context, conn *pgx.Conn, role, password string) {
	t.Helper()
	// rand.Text is base32: no quote or SQL metacharacter can enter the literal.
	for _, c := range password {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			t.Fatal("invalid synthetic role password")
		}
	}
	if _, err := conn.Exec(ctx, "CREATE ROLE "+ident(role)+" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS PASSWORD '"+password+"'"); err != nil {
		t.Fatal("could not create transient test role")
	}
}
