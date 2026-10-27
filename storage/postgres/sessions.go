package postgres

import (
	"content/cms"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"github.com/jackc/pgx/v5"
	"regexp"
	"time"
)

var sessionDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (s *Store) CheckBrowserSessions(ctx context.Context) error {
	var allowed bool
	err := s.pool.QueryRow(ctx, "SELECT has_table_privilege(current_user,$1,'SELECT') AND has_table_privilege(current_user,$1,'INSERT') AND has_table_privilege(current_user,$1,'DELETE')", s.schema+".browser_sessions").Scan(&allowed)
	if err != nil || !allowed {
		return errors.New("browser session table or runtime privileges unavailable")
	}
	return nil
}
func (s *Store) LoadBrowserSession(ctx context.Context, scope, hash string, now time.Time) (cms.BrowserSession, error) {
	var result cms.BrowserSession
	if !sessionDigest.MatchString(scope) || !sessionDigest.MatchString(hash) {
		return result, cms.ErrSessionNotFound
	}
	err := s.pool.QueryRow(ctx, "SELECT authenticated,expires_at FROM "+s.schema+".browser_sessions WHERE scope=$1 AND token_hash=$2 AND expires_at>$3", scope, hash, now).Scan(&result.Authenticated, &result.Expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, cms.ErrSessionNotFound
	}
	return result, err
}
func (s *Store) SaveBrowserSession(ctx context.Context, scope, hash, old string, v cms.BrowserSession, now time.Time) error {
	if !sessionDigest.MatchString(scope) || !sessionDigest.MatchString(hash) || !v.Expires.After(now) || v.Expires.After(now.Add(8*time.Hour)) {
		return errors.New("invalid browser session")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	sum := sha256.Sum256([]byte("content\x00cms\x00sessions\x00" + s.schema + "\x00" + scope))
	lock := int64(binary.BigEndian.Uint64(sum[:8]))
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", lock); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "DELETE FROM "+s.schema+".browser_sessions WHERE expires_at<=$1 OR (scope=$2 AND token_hash=$3)", now, scope, old); err != nil {
		return err
	}
	var count int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM "+s.schema+".browser_sessions WHERE scope=$1", scope).Scan(&count); err != nil {
		return err
	}
	if count >= 1024 {
		return errors.New("browser session capacity reached")
	}
	if _, err = tx.Exec(ctx, "INSERT INTO "+s.schema+".browser_sessions(scope,token_hash,authenticated,expires_at) VALUES($1,$2,$3,$4)", scope, hash, v.Authenticated, v.Expires); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) DeleteBrowserSession(ctx context.Context, scope, hash string) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM "+s.schema+".browser_sessions WHERE scope=$1 AND token_hash=$2", scope, hash)
	return err
}
