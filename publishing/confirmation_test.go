package publishing

import (
	"strings"
	"testing"
)

func TestDiscoveryURL(t *testing.T) {
	for _, u := range []string{"https://example.invalid/ap/objects/publication/abc", "https://example.invalid/日本語"} {
		if !DiscoveryURL(u) {
			t.Fatal("valid", u)
		}
	}
	for _, u := range []string{"", "http://example.invalid/", "https://u:p@example.invalid/x", "https://example.invalid/?", "https://example.invalid/#", "https://example.invalid/?token=secret", "https://example.invalid/%0a", "https://example.invalid/a\nb", "https://example.invalid/a b", "https://example.invalid/\\a", "https://example.invalid/" + strings.Repeat("a", 2048)} {
		if DiscoveryURL(u) {
			t.Fatal("unsafe", u)
		}
	}
}
