package postgres

import (
	"context"
	"database/sql"
	"errors"
)

var ErrForeignNamespace = errors.New("schema is occupied without recognized CMS ownership metadata")

// verifyCMSNamespace runs before constructing a Goose provider: even provider
// initialization can create the ledger. This is entirely read-only and runs
// while the existing schema/installation migration locks are held.
func verifyCMSNamespace(ctx context.Context, db *sql.DB, schema, quoted, installation string) error {
	var occupied bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_depend WHERE refclassid='pg_namespace'::regclass AND refobjid=(SELECT oid FROM pg_namespace WHERE nspname=$1))`, schema).Scan(&occupied); err != nil {
		return err
	}
	if !occupied {
		return nil
	}
	type relation struct{ name, kind, parent string }
	rows, err := db.QueryContext(ctx, `SELECT c.relname,c.relkind::text,COALESCE(p.relname,'') FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace LEFT JOIN pg_index i ON i.indexrelid=c.oid LEFT JOIN pg_class p ON p.oid=i.indrelid AND p.relnamespace=n.oid WHERE n.nspname=$1`, schema)
	if err != nil {
		return err
	}
	var relations []relation
	tables := map[string]bool{}
	for rows.Next() {
		var r relation
		if err = rows.Scan(&r.name, &r.kind, &r.parent); err != nil {
			rows.Close()
			return err
		}
		relations = append(relations, r)
		if r.kind == "r" {
			tables[r.name] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	// An installation table alone is insufficient: the AP module uses that name.
	if !tables["installation"] || !tables["goose_db_version"] {
		return ErrForeignNamespace
	}
	for _, name := range []string{"posts", "post_revisions", "outbox_events"} {
		if !tables[name] {
			return ErrForeignNamespace
		}
	}
	columns := map[string]map[string]string{}
	rows, err = db.QueryContext(ctx, `SELECT c.relname,a.attname,format_type(a.atttypid,a.atttypmod) FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND a.attnum>0 AND NOT a.attisdropped AND c.relkind='r'`, schema)
	if err != nil {
		return err
	}
	for rows.Next() {
		var table, name, kind string
		if err = rows.Scan(&table, &name, &kind); err != nil {
			rows.Close()
			return err
		}
		if columns[table] == nil {
			columns[table] = map[string]string{}
		}
		columns[table][name] = kind
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	// Check table shape before querying data; do not execute a foreign view.
	for table, want := range cmsBaseColumns {
		for column, kind := range want {
			if columns[table][column] != kind {
				return ErrForeignNamespace
			}
		}
	}
	_, tagged := columns["installation"]["module_id"]
	if tagged && columns["installation"]["module_id"] != "text" {
		return ErrForeignNamespace
	}
	var singleton bool
	var bound string
	var module *string
	count := 0
	rows, err = db.QueryContext(ctx, "SELECT singleton,installation_id,to_jsonb(i)->>'module_id' FROM "+quoted+".installation i")
	if err != nil {
		return err
	}
	for rows.Next() {
		count++
		if err = rows.Scan(&singleton, &bound, &module); err != nil {
			rows.Close()
			return err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if count != 1 || !singleton || bound == "" {
		return errors.New("CMS installation binding is missing or ambiguous; explicit repair required")
	}
	if tagged && (module == nil || *module != "cms") {
		return errors.New("schema belongs to a different module")
	}
	if bound != installation {
		return errors.New("schema belongs to a different installation")
	}
	// Legacy 00001..00003 lacked a module tag. Only a complete known CMS ledger
	// and layout may acquire the tag in forward migration 00004; never infer CMS
	// ownership merely from a matching installation name.
	rows, err = db.QueryContext(ctx, "SELECT version_id,is_applied FROM "+quoted+".goose_db_version WHERE version_id>0 ORDER BY id")
	if err != nil {
		return err
	}
	latest := int64(0)
	for rows.Next() {
		var version int64
		var applied bool
		if err = rows.Scan(&version, &applied); err != nil {
			rows.Close()
			return err
		}
		if !applied || version != latest+1 || version > 14 {
			rows.Close()
			return errors.New("unrecognized CMS migration history")
		}
		latest = version
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if latest < 1 || tagged != (latest >= 4) {
		return errors.New("CMS module identity and migration history disagree")
	}
	allowed := map[string]bool{"installation": true, "goose_db_version": true, "posts": true, "post_revisions": true, "outbox_events": true}
	if latest >= 3 {
		allowed["publishing_streams"] = true
		allowed["publishing_deliveries"] = true
	}
	if latest >= 5 {
		allowed["media_objects"] = true
		allowed["post_media"] = true
	}
	if latest >= 6 {
		allowed["media_quota"] = true
	}
	if latest >= 7 {
		allowed["source_import_batches"] = true
		allowed["source_import_records"] = true
	}
	if latest >= 9 {
		allowed["publishing_archives"] = true
	}
	if latest >= 10 {
		allowed["federation_projections"] = true
		allowed["federation_resolution_jobs"] = true
	}
	if latest >= 11 {
		allowed["browser_sessions"] = true
	}
	if latest >= 13 {
		allowed["post_slug_claims"] = true
	}
	if latest >= 14 {
		allowed["publication_schedules"] = true
	}
	for name := range allowed {
		if !tables[name] {
			return ErrForeignNamespace
		}
	}
	for _, r := range relations {
		switch r.kind {
		case "r":
			if !allowed[r.name] {
				return ErrForeignNamespace
			}
		case "i", "I":
			if !allowed[r.parent] {
				return ErrForeignNamespace
			}
		case "S":
			if r.name != "goose_db_version_id_seq" {
				return ErrForeignNamespace
			}
		default:
			return ErrForeignNamespace
		}
	}
	if latest >= 2 && columns["post_revisions"]["metadata"] != "jsonb" {
		return ErrForeignNamespace
	}
	if latest >= 8 && columns["posts"]["kind"] != "text" {
		return ErrForeignNamespace
	}
	if latest >= 9 && columns["posts"]["archive_pending"] != "boolean" {
		return ErrForeignNamespace
	}
	if latest >= 10 && columns["posts"]["public_generation"] != "bigint" {
		return ErrForeignNamespace
	}
	if latest >= 11 {
		for column, kind := range map[string]string{"scope": "text", "token_hash": "text", "authenticated": "boolean", "expires_at": "timestamp with time zone"} {
			if columns["browser_sessions"][column] != kind {
				return ErrForeignNamespace
			}
		}
	}
	if latest >= 12 && columns["posts"]["trashed_at"] != "timestamp with time zone" {
		return ErrForeignNamespace
	}
	if latest >= 13 {
		for column, kind := range map[string]string{"publication_id": "text", "slug": "text", "post_id": "text", "ever_public": "boolean", "created_at": "timestamp with time zone"} {
			if columns["post_slug_claims"][column] != kind {
				return ErrForeignNamespace
			}
		}
	}
	if latest >= 14 {
		for column, kind := range map[string]string{"publication_id": "text", "post_id": "text", "schedule_id": "uuid", "due_at": "timestamp with time zone", "expected_revision": "bigint", "attempts": "integer", "next_attempt_at": "timestamp with time zone", "last_error_code": "text"} {
			if columns["publication_schedules"][column] != kind {
				return ErrForeignNamespace
			}
		}
	}
	var foreignObjects bool
	if err = db.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM pg_depend WHERE refclassid='pg_namespace'::regclass AND refobjid=(SELECT oid FROM pg_namespace WHERE nspname=$1) AND classid NOT IN ('pg_class'::regclass,'pg_type'::regclass)
 AND NOT ($2 AND classid='pg_proc'::regclass AND objid IN (SELECT oid FROM pg_proc WHERE pronamespace=(SELECT oid FROM pg_namespace WHERE nspname=$1) AND proname='maintain_public_generation' AND pronargs=0 AND prorettype='trigger'::regtype AND NOT prosecdef AND prokind='f'))
 AND NOT ($3 AND classid='pg_proc'::regclass AND objid IN (SELECT oid FROM pg_proc WHERE pronamespace=(SELECT oid FROM pg_namespace WHERE nspname=$1) AND proname='maintain_slug_claim' AND pronargs=0 AND prorettype='trigger'::regtype AND NOT prosecdef AND prokind='f'))
 ) OR EXISTS(SELECT 1 FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname=$1 AND t.typrelid=0 AND t.typelem=0)`, schema, latest >= 10, latest >= 13).Scan(&foreignObjects); err != nil {
		return err
	}
	if foreignObjects {
		return ErrForeignNamespace
	}
	return nil
}

// Stable core shape proves this is the legacy CMS namespace; privileges and the
// operator-owned schema remain the security boundary against deliberate forgery.
var cmsBaseColumns = map[string]map[string]string{
	"installation":     {"singleton": "boolean", "installation_id": "text"},
	"goose_db_version": {"id": "integer", "version_id": "bigint", "is_applied": "boolean", "tstamp": "timestamp without time zone"},
	"posts":            {"publication_id": "text", "id": "text", "uuid": "uuid", "title": "text", "slug": "text", "status": "text", "visibility": "text", "revision": "bigint", "created_at": "timestamp with time zone", "updated_at": "timestamp with time zone", "published_at": "timestamp with time zone"},
	"post_revisions":   {"publication_id": "text", "post_id": "text", "revision": "bigint", "source_html": "text", "html": "text", "renderer_version": "text", "created_at": "timestamp with time zone"},
	"outbox_events":    {"event_id": "uuid", "publication_id": "text", "post_id": "text", "revision": "bigint", "event_type": "text", "schema_version": "integer", "snapshot": "jsonb", "created_at": "timestamp with time zone"},
}

func validateCMSBinding(stored, module, installation string) error {
	if module != "cms" {
		return errors.New("schema belongs to a different module")
	}
	if stored != installation {
		return errors.New("schema belongs to a different installation")
	}
	return nil
}
