package config

import "testing"

func TestPublishingIdentityIsExplicitPublicProfile(t *testing.T) {
	env := map[string]string{"CONTENT_SCHEMA": "cms_example", "CONTENT_INSTALLATION_ID": "example", "CONTENT_DATABASE_URL": "synthetic", "CONTENT_PUBLICATION_ID": "one", "CONTENT_SITE_URL": "https://example.invalid", "CONTENT_ADMIN_KEY": "synthetic", "CONTENT_CONTENT_KEY": "synthetic"}
	get := func(k string) string { return env[k] }
	cfg, err := Load("serve", get)
	if err != nil || cfg.PublishingIdentity {
		t.Fatal("must default off", err)
	}
	env["CONTENT_PUBLISHING_IDENTITY_ENABLED"] = "true"
	if _, err = Load("serve", get); err == nil {
		t.Fatal("missing public profile accepted")
	}
	env["CONTENT_AUTHOR_ID"] = "1234567890abcdef12345678"
	env["CONTENT_AUTHOR_NAME"] = "Synthetic Author"
	env["CONTENT_AUTHOR_SLUG"] = "synthetic"
	cfg, err = Load("serve", get)
	if err != nil || !cfg.PublishingIdentity {
		t.Fatal("explicit profile refused", err)
	}
	env["CONTENT_PUBLISHING_IDENTITY_ENABLED"] = "owner"
	if _, err = Load("serve", get); err == nil {
		t.Fatal("invalid flag")
	}
	env["CONTENT_MIGRATION_DATABASE_URL"] = "synthetic-owner"
	if _, err = Load("migrate", get); err != nil {
		t.Fatal("migration affected", err)
	}
}
