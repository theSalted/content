package cms

import (
	"errors"
	"strings"
	"testing"
)

func TestNewPostPreservesHTMLAndRejectsLoss(t *testing.T) {
	p, err := NewPost("publication-a", CreatePost{Title: "你好 world", HTML: "<p>Hello <strong>world</strong></p>"})
	if err != nil {
		t.Fatal(err)
	}
	if p.SourceHTML != p.HTML || p.Revision != 1 || len(p.ID) != 24 || len(p.UUID) != 36 || p.Status != "draft" {
		t.Fatalf("unexpected post: %+v", p)
	}
	for _, in := range []CreatePost{{Title: ""}, {Title: strings.Repeat("界", 256)}, {Title: "x", HTML: "<script>alert(1)</script>"}, {Title: "x", HTML: `<img src="x" onerror="alert(1)">`}, {Title: "x", Status: "scheduled"}, {Title: "x", Visibility: "members"}, {Title: "x", Slug: "../../private"}, {Title: "x", HTML: strings.Repeat("a", (1<<20)+1)}} {
		_, err := NewPost("publication-a", in)
		var bad *InvalidInput
		if !errors.As(err, &bad) {
			t.Errorf("expected validation error for %+v", in)
		}
	}
}
