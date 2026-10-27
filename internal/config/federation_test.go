package config

import "testing"

func TestFederationProjectionOptIn(t *testing.T) {
	for _, tc := range []struct {
		flag, dest, want string
		bad              bool
	}{{"", "", "", false}, {"false", "invalid'", "", false}, {"true", "ap", "ap", false}, {"true", "", "", true}, {"true", "a' OR 1=1", "", true}, {"yes", "ap", "", true}} {
		get := func(k string) string {
			switch k {
			case "CONTENT_FEDERATION_PROJECTION_ENABLED":
				return tc.flag
			case "CONTENT_PUBLISHING_DESTINATION":
				return tc.dest
			}
			t.Fatal("unexpected credential read", k)
			return ""
		}
		got, err := LoadFederationProjection(get)
		if (err != nil) != tc.bad || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
}
