package config

import "testing"

func TestMigrationNeverFallsBackToRuntimeCredentials(t *testing.T) {
	env := map[string]string{"CONTENT_SCHEMA": "cms_example", "CONTENT_INSTALLATION_ID": "example", "CONTENT_DATABASE_URL": "runtime-only"}
	if _, err := Load("migrate", func(k string) string { return env[k] }); err == nil {
		t.Fatal("runtime credentials used to migrate")
	}
	env["CONTENT_MIGRATION_DATABASE_URL"] = "migration-only"
	c, err := Load("migrate", func(k string) string { return env[k] })
	if err != nil || c.DatabaseURL != "migration-only" {
		t.Fatal(c, err)
	}
}

func TestSiteMetadataConfigurationIsServeOnly(t *testing.T) {
	env := map[string]string{"CONTENT_SCHEMA": "cms_example", "CONTENT_INSTALLATION_ID": "example", "CONTENT_DATABASE_URL": "synthetic-runtime", "CONTENT_PUBLICATION_ID": "publication", "CONTENT_SITE_URL": "https://example.invalid", "CONTENT_ADMIN_KEY": "synthetic-key", "CONTENT_CONTENT_KEY": "synthetic-content", "CONTENT_SITE_TITLE": "Example", "CONTENT_SITE_DESCRIPTION": "Public description", "CONTENT_SITE_LOGO": "/logo.png"}
	c, err := Load("serve", func(k string) string { return env[k] })
	if err != nil || c.SiteTitle != "Example" || c.SiteDescription != "Public description" || c.SiteLogo != "/logo.png" {
		t.Fatal(c, err)
	}
	env["CONTENT_MIGRATION_DATABASE_URL"] = "synthetic-migration"
	_, err = Load("migrate", func(k string) string {
		if k == "CONTENT_SITE_TITLE" || k == "CONTENT_SITE_DESCRIPTION" || k == "CONTENT_SITE_LOGO" {
			t.Fatal("migration read website metadata")
		}
		return env[k]
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestReviewAndAuthorAreExplicitOptIns(t *testing.T) {
	env := map[string]string{"CONTENT_SCHEMA": "cms_example", "CONTENT_INSTALLATION_ID": "example", "CONTENT_DATABASE_URL": "synthetic-runtime", "CONTENT_PUBLICATION_ID": "publication", "CONTENT_SITE_URL": "https://example.invalid", "CONTENT_ADMIN_KEY": "synthetic-key", "CONTENT_CONTENT_KEY": "synthetic-content"}
	get := func(k string) string { return env[k] }
	cfg, err := Load("serve", get)
	if err != nil || cfg.Review != nil || cfg.DefaultAuthor != nil {
		t.Fatal("not disabled by default")
	}
	env["CONTENT_AUTHOR_ID"] = "0123456789abcdef01234568"
	if _, err = Load("serve", get); err == nil {
		t.Fatal("partial author accepted")
	}
	env["CONTENT_AUTHOR_NAME"] = "Synthetic author"
	env["CONTENT_AUTHOR_SLUG"] = "synthetic-author"
	cfg, err = Load("serve", get)
	if err != nil || cfg.DefaultAuthor == nil {
		t.Fatal(err)
	}
	env["CONTENT_REVIEW_USERNAME"] = "synthetic"
	if _, err = Load("serve", get); err == nil {
		t.Fatal("partial browser auth accepted")
	}
	env["CONTENT_REVIEW_ORIGIN"] = "https://admin.example.invalid"
	env["CONTENT_REVIEW_PASSWORD_HASH"] = "validated-by-review-server"
	cfg, err = Load("serve", get)
	if err != nil || cfg.Review == nil || cfg.Review.Publication != "publication" {
		t.Fatal(err)
	}
}

func TestPublishingFlagsAreStrictAndDisabledByDefault(t *testing.T) {
	env := map[string]string{"CONTENT_SCHEMA": "cms_example", "CONTENT_INSTALLATION_ID": "example", "CONTENT_DATABASE_URL": "synthetic-runtime", "CONTENT_PUBLICATION_ID": "publication", "CONTENT_SITE_URL": "https://example.invalid", "CONTENT_ADMIN_KEY": "synthetic-key", "CONTENT_CONTENT_KEY": "synthetic-content"}
	get := func(k string) string { return env[k] }
	cfg, e := Load("serve", get)
	if e != nil || cfg.Scheduler {
		t.Fatal("implicit schedule worker", e)
	}
	env["CONTENT_SCHEDULER_ENABLED"] = "true"
	cfg, e = Load("serve", get)
	if e != nil || !cfg.Scheduler {
		t.Fatal(e)
	}
	for _, key := range []string{"CONTENT_SCHEDULER_ENABLED", "CONTENT_SLUG_REDIRECTS_ENABLED"} {
		env[key] = "TRUE"
		if _, e = Load("serve", get); e == nil {
			t.Fatal("ambiguous flag", key)
		}
		delete(env, key)
	}
}
