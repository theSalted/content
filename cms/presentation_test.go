package cms

import (
	"encoding/json"
	"testing"
	"time"
)

func TestImportedExcerptInvalidatedOnlyByBodyEdit(t *testing.T) {
	p, e := NewPost("publication", CreatePost{Title: "Title", HTML: "<p>Before</p>"})
	if e != nil {
		t.Fatal(e)
	}
	p.UpdatedAt = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	p.Presentation = Presentation{"excerpt": json.RawMessage(`"Original excerpt"`), "meta_title": json.RawMessage(`"SEO"`)}
	title := "Changed title"
	q, e := ApplyUpdate(p, UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Title: &title})
	if e != nil || Excerpt(q) != "Original excerpt" {
		t.Fatal(e)
	}
	body := "<p>New <strong>body</strong></p><p>next line</p>"
	q, e = ApplyUpdate(p, UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, HTML: &body})
	if e != nil || Excerpt(q) != "New body next line" {
		t.Fatal(e, Excerpt(q))
	}
	if Excerpt(p) != "Original excerpt" || string(q.Presentation["meta_title"]) != `"SEO"` {
		t.Fatal("revision metadata mutated")
	}
}
