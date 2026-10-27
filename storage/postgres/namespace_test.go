package postgres

import (
	"encoding/json"
	"strings"
	"testing"
)

// Snapshot includes schema-local catalog objects, table contents and sequence
// state, so a rejected migration cannot hide a created/advanced Goose ledger.
func namespaceSnapshot(t *testing.T, f *roleFixture, schema string) string {
	t.Helper()
	var objects string
	if err := f.admin.QueryRow(f.ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_array(classid::regclass::text,objid,objsubid) ORDER BY classid,objid,objsubid)::text,'[]') FROM pg_depend WHERE refclassid='pg_namespace'::regclass AND refobjid=(SELECT oid FROM pg_namespace WHERE nspname=$1)`, schema).Scan(&objects); err != nil {
		t.Fatal(err)
	}
	rows, err := f.admin.Query(f.ctx, `SELECT c.relname,c.relkind::text FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relkind IN ('r','S') ORDER BY c.relname`, schema)
	if err != nil {
		t.Fatal(err)
	}
	type relation struct{ name, kind string }
	var rels []relation
	for rows.Next() {
		var r relation
		if err = rows.Scan(&r.name, &r.kind); err != nil {
			t.Fatal(err)
		}
		rels = append(rels, r)
	}
	rows.Close()
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	data := map[string]string{"objects": objects}
	for _, r := range rels {
		var value string
		query := "SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text)::text,'[]') FROM " + ident(schema) + "." + ident(r.name) + " t"
		if r.kind == "S" {
			query = "SELECT jsonb_build_array(last_value,is_called)::text FROM " + ident(schema) + "." + ident(r.name)
		}
		if err = f.admin.QueryRow(f.ctx, query).Scan(&value); err != nil {
			t.Fatal(err)
		}
		data[r.name] = value
	}
	out, _ := json.Marshal(data)
	return string(out)
}
func rejectWithoutMutation(t *testing.T, f *roleFixture, schema, installation string) {
	t.Helper()
	before := namespaceSnapshot(t, f, schema)
	err := Migrate(f.ctx, f.migrateDSN, schema, installation)
	after := namespaceSnapshot(t, f, schema)
	if err == nil {
		t.Error("migration accepted foreign or invalid namespace")
	}
	if before != after {
		t.Error("rejected migration mutated namespace")
	}
	if err != nil {
		t.Log("rejected:", err)
	}
}
func TestMigrationRejectsForeignNamespace(t *testing.T) {
	f := newRoleFixture(t)
	for _, tc := range []struct{ name, setup string }{
		{"sentinel", "CREATE TABLE %s.sentinel(value text); INSERT INTO %s.sentinel VALUES('retain-me')"},
		{"function_only", "CREATE FUNCTION %s.sentinel() RETURNS integer LANGUAGE SQL AS 'SELECT 42'"},
		{"enum_only", "CREATE TYPE %s.sentinel AS ENUM ('retain-me')"},
		{"sequence_only", "CREATE SEQUENCE %s.sentinel"},
		{"ap_schema", "CREATE TABLE %s.installation(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),installation_id text NOT NULL); INSERT INTO %s.installation VALUES(true,'installation-a'); CREATE TABLE %s.schema_migrations(version text PRIMARY KEY,checksum text NOT NULL,applied_at timestamptz NOT NULL DEFAULT now()); INSERT INTO %s.schema_migrations(version,checksum) VALUES('001','synthetic-ap-checksum'); CREATE TABLE %s.identities(actor_id text PRIMARY KEY,private_jwk jsonb NOT NULL); INSERT INTO %s.identities VALUES('https://example.invalid/actor','{\"synthetic\":true}')"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema := "foreign_" + tc.name
			mustExec(t, f.ctx, f.admin, "CREATE SCHEMA "+ident(schema)+" AUTHORIZATION "+ident(f.owner))
			mustExec(t, f.ctx, f.admin, strings.ReplaceAll(tc.setup, "%s", ident(schema)))
			if tc.name == "ap_schema" {
				mustExec(t, f.ctx, f.admin, "ALTER TABLE "+ident(schema)+".installation OWNER TO "+ident(f.owner))
			}
			rejectWithoutMutation(t, f, schema, "installation-a")
		})
	}
}

func TestMigrationRejectsInvalidCMSBinding(t *testing.T) {
	f := newRoleFixture(t)
	t.Run("wrong installation", func(t *testing.T) { rejectWithoutMutation(t, f, f.schema, "other-installation") })
	t.Run("wrong module", func(t *testing.T) {
		mustExec(t, f.ctx, f.admin, "ALTER TABLE "+ident(f.schema)+".installation DROP CONSTRAINT installation_module_id_check")
		mustExec(t, f.ctx, f.admin, "UPDATE "+ident(f.schema)+".installation SET module_id='activitypub'")
		rejectWithoutMutation(t, f, f.schema, "installation-a")
		if store, err := Open(f.ctx, f.runtimeDSN, f.schema, "installation-a"); err == nil {
			store.Close()
			t.Fatal("runtime accepted wrong module")
		}
		mustExec(t, f.ctx, f.admin, "UPDATE "+ident(f.schema)+".installation SET module_id='cms'")
		mustExec(t, f.ctx, f.admin, "ALTER TABLE "+ident(f.schema)+".installation ADD CONSTRAINT installation_module_id_check CHECK(module_id='cms')")
	})
	t.Run("missing installation binding", func(t *testing.T) {
		mustExec(t, f.ctx, f.admin, "DELETE FROM "+ident(f.schema)+".installation")
		rejectWithoutMutation(t, f, f.schema, "installation-a")
		mustExec(t, f.ctx, f.admin, "INSERT INTO "+ident(f.schema)+".installation(singleton,installation_id) VALUES(true,'installation-a')")
	})
	t.Run("mixed module namespace", func(t *testing.T) {
		mustExec(t, f.ctx, f.admin, "CREATE TABLE "+ident(f.schema)+".schema_migrations(version text PRIMARY KEY)")
		rejectWithoutMutation(t, f, f.schema, "installation-a")
		mustExec(t, f.ctx, f.admin, "DROP TABLE "+ident(f.schema)+".schema_migrations")
	})
	t.Run("module tag without ledger version", func(t *testing.T) {
		mustExec(t, f.ctx, f.admin, "UPDATE "+ident(f.schema)+".goose_db_version SET is_applied=false WHERE version_id=4")
		rejectWithoutMutation(t, f, f.schema, "installation-a")
		mustExec(t, f.ctx, f.admin, "UPDATE "+ident(f.schema)+".goose_db_version SET is_applied=true WHERE version_id=4")
	})
}
