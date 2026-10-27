package media

import (
	"strings"
	"testing"
)

func TestOriginAliasesPreserveOwnedReferences(t *testing.T) {
	current, old := "https://cms.example.invalid", "https://old.example.invalid"
	path := "/content/images/0123456789abcdef01234567/image.png"
	source := `<p><img src="` + old + path + `" srcset="` + old + path + ` 1x, ` + current + path + ` 2x"></p>`
	refs, e := References(current, source, old)
	if e != nil || len(refs) != 1 {
		t.Fatal(refs, e)
	}
	for _, raw := range []string{"https://foreign.invalid" + path, "//old.example.invalid" + path} {
		if _, owned, _ := ReferenceID(current, raw, old); owned {
			t.Fatal("untrusted URL owned")
		}
	}
	for _, raw := range []string{old + path + "?key=x", old + path + "#fragment", strings.Replace(old, "https://", "https://user:password@", 1) + path, old + "/content/images/invalid/image.png"} {
		if _, owned, e := ReferenceID(current, raw, old); !owned || e == nil {
			t.Fatal("invalid owned path accepted")
		}
	}
}
func TestMediaOriginValidation(t *testing.T) {
	for _, origin := range []string{"https://cms.example.invalid/", "http://127.0.0.1:2345"} {
		if _, e := NormalizeOrigin(origin); e != nil {
			t.Fatal(e)
		}
	}
	for _, origin := range []string{"https://cms.example.invalid/path", "https://user:pass@cms.example.invalid", "https://cms.example.invalid?x=1", "http://cms.example.invalid", "//cms.example.invalid", "*", ""} {
		if _, e := NormalizeOrigin(origin); e == nil {
			t.Fatal(origin)
		}
	}
}
