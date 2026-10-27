package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/binary"
	"errors"
	"regexp"
	"strings"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
	"github.com/pressly/goose/v3/lock"
)

//go:embed migrations/*.sql
var migrations embed.FS
var schemaPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

func Schema(schema string) (string, error) {
	if !schemaPattern.MatchString(schema) || schema == "public" || schema == "information_schema" || strings.HasPrefix(schema, "pg_") {
		return "", errors.New("schema must be a dedicated lowercase PostgreSQL identifier, not public or a system schema")
	}
	return pgx.Identifier{schema}.Sanitize(), nil
}

// MigrationLockID is stable across releases: installation + module + schema.
func MigrationLockID(installation, schema string) int64 {
	sum := sha256.Sum256([]byte("content\x00" + installation + "\x00cms\x00" + schema))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}

// schemaOwnershipLockID uses a separate domain from the logical installation
// lock. It is stable across releases and only serializes the same CMS schema.
func schemaOwnershipLockID(schema string) int64 {
	sum := sha256.Sum256([]byte("content\x00cms\x00schema-ownership\x00" + schema))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}

// Migrate requires an existing owned schema and a direct migration-role DSN.
// It never creates databases, roles or credentials, and is never called by serve.
func Migrate(ctx context.Context, dsn, schema, installation string) (resultErr error) {
	if dsn == "" {
		return errors.New("explicit migration DSN is required")
	}
	quoted, err := Schema(schema)
	if err != nil {
		return err
	}
	if installation == "" || len(installation) > 128 || strings.ContainsRune(installation, 0) {
		return errors.New("invalid installation ID")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return errors.New("invalid migration connection configuration")
	}
	defer db.Close()
	db.SetMaxOpenConns(2)
	// Hold a dedicated session for the whole operation. Goose may skip its own
	// lock on a no-op Up; installation checks/binding must still be serialized.
	// Lock order is always schema ownership, then installation/module/schema.
	// The first lock prevents two installation IDs racing to claim a new schema.
	lockConn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer lockConn.Close()
	for _, key := range []int64{schemaOwnershipLockID(schema), MigrationLockID(installation, schema)} {
		locker, err := lock.NewPostgresSessionLocker(lock.WithLockID(key))
		if err != nil {
			return err
		}
		if err = locker.SessionLock(ctx, lockConn); err != nil {
			return err
		}
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			resultErr = errors.Join(resultErr, locker.SessionUnlock(cleanup, lockConn))
		}()
	}
	var exists bool
	if err = db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)", schema).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return errors.New("owned schema does not exist; migration does not provision schemas")
	}
	if err = verifyCMSNamespace(ctx, db, schema, quoted, installation); err != nil {
		return err
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	files := fstest.MapFS{}
	for _, e := range entries {
		body, err := migrations.ReadFile("migrations/" + e.Name())
		if err != nil {
			return err
		}
		files[e.Name()] = &fstest.MapFile{Data: []byte(strings.ReplaceAll(string(body), "{{schema}}", quoted))}
	}
	ledger, err := database.NewStore(database.DialectPostgres, quoted+`.goose_db_version`)
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectCustom, db, files, goose.WithStore(schemaLedger{ledger}), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		return err
	}
	if _, err = provider.Up(ctx); err != nil {
		return err
	}
	// Bind the migrated namespace once. A mismatched installation cannot serve it.
	var stored, module string
	if err = db.QueryRowContext(ctx, "INSERT INTO "+quoted+`.installation(singleton,installation_id) VALUES(true,$1) ON CONFLICT(singleton) DO UPDATE SET installation_id=`+quoted+`.installation.installation_id RETURNING installation_id,module_id`, installation).Scan(&stored, &module); err != nil {
		return err
	}
	return validateCMSBinding(stored, module, installation)
}

// Goose's PostgreSQL existence query treats identifier quotes as literal schema
// characters. Keep its standard ledger SQL, but resolve quoted names through a
// parameterized regclass lookup so a second migration run is idempotent.
type schemaLedger struct{ database.Store }

func (s schemaLedger) TableExists(ctx context.Context, db database.DBTxConn) (bool, error) {
	var exists bool
	err := db.QueryRowContext(ctx, "SELECT to_regclass($1) IS NOT NULL", s.Tablename()).Scan(&exists)
	return exists, err
}
