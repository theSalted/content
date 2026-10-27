package config

import "testing"

func TestMediaConfigurationIsExplicit(t *testing.T) {
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if c, e := LoadMedia(get(map[string]string{"CONTENT_MEDIA_S3_SECRET_KEY": "unused-test-value"})); e != nil || c != nil {
		t.Fatal("ambient media config enabled storage")
	}
	for _, env := range []map[string]string{
		{"CONTENT_MEDIA_BACKEND": "unknown", "CONTENT_MEDIA_ORIGIN": "https://cms.example.invalid"},
		{"CONTENT_MEDIA_BACKEND": "filesystem"},
		{"CONTENT_MEDIA_BACKEND": "filesystem", "CONTENT_MEDIA_ORIGIN": "https://cms.example.invalid"},
	} {
		if _, e := LoadMedia(get(env)); e == nil {
			t.Fatal("partial media configuration accepted")
		}
	}
	env := map[string]string{"CONTENT_MEDIA_BACKEND": "filesystem", "CONTENT_MEDIA_ORIGIN": "https://cms.example.invalid", "CONTENT_MEDIA_DIRECTORY": "/tmp/content-test-objects"}
	if c, e := LoadMedia(get(env)); e != nil || c.Backend != "filesystem" {
		t.Fatal("filesystem config", e)
	}
	env["CONTENT_DATABASE_URL"] = "synthetic-runtime-url"
	env["CONTENT_MIGRATION_DATABASE_URL"] = "synthetic-migration-url"
	env["CONTENT_SCHEMA"] = "cms_test"
	env["CONTENT_INSTALLATION_ID"] = "installation"
	env["CONTENT_PUBLICATION_ID"] = "publication"
	if _, e := LoadMediaCleanup(get(env)); e == nil {
		t.Fatal("cleanup borrowed another database role")
	}
	env["CONTENT_MEDIA_CLEANUP_DATABASE_URL"] = "synthetic-maintenance-url"
	if c, e := LoadMediaCleanup(get(env)); e != nil || c.DatabaseURL != "synthetic-maintenance-url" || c.AdminKey != "" {
		t.Fatal("cleanup config", e)
	}
}

func TestMediaOriginAliasConfiguration(t *testing.T) {
	env := map[string]string{"CONTENT_MEDIA_BACKEND": "filesystem", "CONTENT_MEDIA_ORIGIN": "https://current.example.invalid/", "CONTENT_MEDIA_DIRECTORY": "/tmp/unused", "CONTENT_MEDIA_ORIGIN_ALIASES": "https://old.example.invalid/, https://older.example.invalid"}
	get := func(k string) string { return env[k] }
	c, e := LoadMedia(get)
	if e != nil || c.Origin != "https://current.example.invalid" || len(c.OriginAliases) != 2 || c.OriginAliases[0] != "https://old.example.invalid" {
		t.Fatal(c, e)
	}
	for _, value := range []string{"*", "https://old.example.invalid/private", "https://old.example.invalid,", "http://old.example.invalid"} {
		env["CONTENT_MEDIA_ORIGIN_ALIASES"] = value
		if _, e := LoadMedia(get); e == nil {
			t.Fatal(value)
		}
	}
}
