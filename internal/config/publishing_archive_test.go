package config

import "testing"

func TestArchiveLaneNeedsExplicitGateAndEndpoint(t *testing.T) {
	base := map[string]string{"CONTENT_PUBLISHING_DELIVERY_ENABLED": "true", "CONTENT_DELIVERY_DATABASE_URL": "postgres://synthetic", "CONTENT_SCHEMA": "cms", "CONTENT_INSTALLATION_ID": "test", "CONTENT_PUBLICATION_ID": "one", "CONTENT_PUBLISHING_DESTINATION": "ap", "CONTENT_PUBLISHING_ENDPOINT": "http://127.0.0.1:3001/internal/v2/events", "CONTENT_PUBLISHING_TOKEN": "synthetic-token-only"}
	get := func(k string) string { return base[k] }
	c, e := LoadDelivery(get)
	if e != nil || c.ArchiveEnabled {
		t.Fatal(c, e)
	}
	base["CONTENT_PUBLISHING_ARCHIVE_DELIVERY_ENABLED"] = "true"
	if _, e = LoadDelivery(get); e == nil {
		t.Fatal("missing archive endpoint accepted")
	}
	base["CONTENT_PUBLISHING_ARCHIVE_ENDPOINT"] = "http://127.0.0.1:3001/internal/v2/archive"
	c, e = LoadDelivery(get)
	if e != nil || !c.ArchiveEnabled {
		t.Fatal(c, e)
	}
	base["CONTENT_PUBLISHING_ARCHIVE_DELIVERY_ENABLED"] = "yes"
	if _, e = LoadDelivery(get); e == nil {
		t.Fatal("invalid flag accepted")
	}
}
