package config

import (
	"testing"
)

func TestDisabledDeliveryReadsNoCredentials(t *testing.T) {
	c, err := LoadDelivery(func(k string) string {
		if k != "CONTENT_PUBLISHING_DELIVERY_ENABLED" {
			t.Fatal("disabled configuration read", k)
		}
		return ""
	})
	if err != nil || c.Enabled {
		t.Fatal(c, err)
	}
	_, err = LoadDelivery(func(k string) string {
		if k == "CONTENT_PUBLISHING_DELIVERY_ENABLED" {
			return "true"
		}
		if k == "CONTENT_DATABASE_URL" || k == "CONTENT_MIGRATION_DATABASE_URL" {
			t.Fatal("fallback credentials read")
		}
		return ""
	})
	if err == nil {
		t.Fatal("worker accepted missing dedicated credentials")
	}
}
