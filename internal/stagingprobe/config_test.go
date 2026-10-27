package stagingprobe

import (
	"testing"
)

func fixtureEnv() map[string]string {
	return map[string]string{
		"CONTENT_PUBLISHING_ENROLL_NEW": "true", "CONTENT_PUBLISHING_DELIVERY_ENABLED": "true", "CONTENT_DATABASE_URL": "postgres://cms_api:synthetic@db.example.invalid:5432/postgres?sslmode=verify-full", "CONTENT_DELIVERY_DATABASE_URL": "postgres://cms_delivery:synthetic@db.example.invalid:5432/postgres?sslmode=verify-full", "CONTENT_SCHEMA": "content_cms_staging", "CONTENT_INSTALLATION_ID": "content-staging", "CONTENT_PUBLICATION_ID": "content-staging", "CONTENT_PUBLISHING_SOURCE": "content", "CONTENT_PUBLISHING_DESTINATION": "activitypub", "CONTENT_PUBLISHING_ENDPOINT": "http://127.0.0.1:3001/internal/v2/events", "CONTENT_PUBLISHING_TOKEN": "synthetic-publisher-token-only-not-live", "CONTENT_SITE_URL": "https://example.invalid", "CONTENT_STAGING_PEER_STATUS_URL": "http://127.0.0.1:3001/internal/staging/peer/status", "CONTENT_STAGING_RUN_ID": "11111111-1111-4111-8111-111111111111"}
}
func TestLoad(t *testing.T) {
	env := fixtureEnv()
	c, clean, e := Load(func(k string) string { return env[k] })
	defer clean()
	if e != nil || !c.RequirePeer {
		t.Fatal("valid fixture rejected", e)
	}
	for _, tc := range []struct{ key, value string }{{"CONTENT_PUBLISHING_ENDPOINT", "https://example.invalid/internal/v2/events"}, {"CONTENT_PUBLISHING_ENDPOINT", "http://localhost:3001/internal/v2/events"}, {"CONTENT_STAGING_PEER_STATUS_URL", "http://127.0.0.1:3002/internal/staging/peer/status"}, {"CONTENT_SCHEMA", "content"}, {"CONTENT_INSTALLATION_ID", "production"}, {"CONTENT_PUBLICATION_ID", "production"}, {"CONTENT_PUBLISHING_ENROLL_NEW", "false"}, {"CONTENT_DATABASE_URL", "postgres://cms_api:SECRET@db.example.invalid/postgres?sslmode=require"}, {"CONTENT_DELIVERY_DATABASE_URL", env["CONTENT_DATABASE_URL"]}, {"CONTENT_STAGING_DATABASE_SSL_CA", "SECRET-not-pem"}, {"CONTENT_STAGING_RUN_ID", "bad"}, {"CONTENT_SITE_URL", "http://example.invalid"}} {
		t.Run(tc.key, func(t *testing.T) {
			v := fixtureEnv()
			v[tc.key] = tc.value
			_, cleanup, err := Load(func(k string) string { return v[k] })
			cleanup()
			if err == nil || err.Error() != "invalid_staging_configuration" {
				t.Fatal("invalid config did not fail with redacted code")
			}
		})
	}
}
