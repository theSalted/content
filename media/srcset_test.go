package media

import "testing"

func TestOwnedSrcsetReferencesAreAuthorizedAndDeduplicated(t *testing.T) {
	source := `<img src="/content/images/0123456789abcdef01234567/image.png" srcset="/content/images/0123456789abcdef01234567/image.png 1x, /content/images/0123456789abcdef01234568/image.jpg 2x">`
	refs, e := References("https://example.invalid", source)
	if e != nil || len(refs) != 2 || refs[1].ID != "0123456789abcdef01234568" || refs[1].Extension != "jpg" {
		t.Fatal(refs, e)
	}
	for _, invalid := range []string{`<img srcset="/content/images/missing/image.png 1x">`, `<img srcset="/content/images/0123456789abcdef01234567/image.png?token=private 1x">`, `<img srcset="/content/images/0123456789abcdef01234567/image.png 1x, /content/images/0123456789abcdef01234567/image.jpg 2x">`} {
		if _, e = References("https://example.invalid", invalid); e == nil {
			t.Fatal("bad owned srcset accepted")
		}
	}
}
