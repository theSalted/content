package cms

import (
	"errors"
	"testing"
	"time"
)

func TestApplyUpdatePartialAndTokenValidation(t *testing.T) {
	p, err := NewPost("a", CreatePost{Title: "Before", Slug: "stable", HTML: "<p>Preserved</p>"})
	if err != nil {
		t.Fatal(err)
	}
	p.UpdatedAt = time.Date(2026, 1, 1, 0, 0, 0, 123000000, time.UTC)
	title := "After"
	next, err := ApplyUpdate(p, UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, Title: &title})
	if err != nil || next.Title != "After" || next.HTML != p.HTML || next.ID != p.ID || next.UUID != p.UUID || next.Publication != p.Publication {
		t.Fatal(next, err)
	}
	empty := ""
	next, err = ApplyUpdate(p, UpdatePost{ExpectedUpdatedAt: p.UpdatedAt, HTML: &empty})
	if err != nil || next.SourceHTML != "" || next.HTML != "" {
		t.Fatal("empty body must clear", err)
	}
	if _, err = ApplyUpdate(p, UpdatePost{ExpectedUpdatedAt: p.UpdatedAt.Add(-time.Millisecond)}); !errors.Is(err, ErrStaleUpdate) {
		t.Fatal("stale no-op accepted", err)
	}
	for _, u := range []UpdatePost{{}, {ExpectedUpdatedAt: p.UpdatedAt.Add(time.Nanosecond)}, {ExpectedUpdatedAt: p.UpdatedAt, Title: &empty}, {ExpectedUpdatedAt: p.UpdatedAt, Slug: &empty}, {ExpectedUpdatedAt: p.UpdatedAt, Status: &empty}} {
		if _, err := ApplyUpdate(p, u); err == nil {
			t.Fatal("invalid edit accepted", u)
		}
	}
}
