package cms

import (
	"strings"
	"testing"
)

const syntheticEmbed = `<figure class="kg-card kg-embed-card"><iframe width="200" height="113" src="https://www.youtube.com/embed/abcdefghijk?start=623&amp;feature=oembed" frameborder="0" allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture; web-share" referrerpolicy="strict-origin-when-cross-origin" allowfullscreen title="Synthetic player"></iframe></figure>`

func TestYouTubeEmbedPreservesSource(t *testing.T) {
	for _, source := range []string{syntheticEmbed, strings.ReplaceAll(syntheticEmbed, "www.youtube.com", "www.youtube-nocookie.com")} {
		p, e := NewPost("publication", CreatePost{Title: "Embed", HTML: source})
		if e != nil || p.SourceHTML != source || p.HTML != source {
			t.Fatal(e)
		}
	}
}
func TestEmbedBoundaryRejectsActiveAndAmbiguousInputs(t *testing.T) {
	for _, src := range []string{"http://www.youtube.com/embed/abcdefghijk", "https://www.youtube.com.evil.invalid/embed/abcdefghijk", "https://www.youtube.com@evil.invalid/embed/abcdefghijk", "https://www.youtube.com:444/embed/abcdefghijk", "https://www.youtube.com/watch?v=abcdefghijk", "https://www.youtube.com/embed/%61bcdefghijk", "https://www.youtube.com/embed/abcdefghijk?origin=https://evil.invalid", "https://www.youtube.com/embed/abcdefghijk?start=1&start=2", "https://127.0.0.1/embed/abcdefghijk", "javascript:alert(1)", "data:text/html,hello"} {
		if _, e := NewPost("publication", CreatePost{Title: "Unsafe", HTML: `<iframe src="` + src + `"></iframe>`}); e == nil {
			t.Fatal("unsafe embed accepted", src)
		}
	}
	for _, attr := range []string{`srcdoc="<script>alert(1)</script>"`, `onload="alert(1)"`, `allow="camera; microphone; geolocation"`, `name="admin"`, `src="https://www.youtube.com/embed/abcdefghijk"`} {
		source := strings.Replace(syntheticEmbed, "frameborder=", attr+" frameborder=", 1)
		if _, e := NewPost("publication", CreatePost{Title: "Unsafe", HTML: source}); e == nil {
			t.Fatal("unsafe attribute accepted", attr)
		}
	}
}
