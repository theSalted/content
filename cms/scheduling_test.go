package cms

import (
	"testing"
	"time"
)

func TestScheduleTransitionsPreserveHistoryAndRequireExplicitFutureTime(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p, _ := NewPost("one", CreatePost{Title: "Scheduled fixture"})
	p.UpdatedAt = now
	due := now.Add(time.Hour)
	scheduled, draft, published := "scheduled", "draft", "published"
	old := now.Add(-time.Hour)
	p.PublishedAt = &old
	next, err := ApplyUpdateAt(p, UpdatePost{ExpectedUpdatedAt: now, Status: &scheduled, PublishedAt: &due}, now)
	if err != nil || next.Status != "scheduled" || !next.PublishedAt.Equal(old) || !next.ScheduledFor.Equal(due) {
		t.Fatal("schedule", err)
	}
	next, err = ApplyUpdateAt(next, UpdatePost{ExpectedUpdatedAt: now, Status: &draft}, now)
	if err != nil || next.ScheduledFor != nil || !next.PublishedAt.Equal(old) {
		t.Fatal("cancel lost history", err)
	}
	p.Status = "published"
	if _, err = ApplyUpdateAt(p, UpdatePost{ExpectedUpdatedAt: now, Status: &scheduled, PublishedAt: &due}, now); err == nil {
		t.Fatal("published directly rescheduled")
	}
	p.Status = "draft"
	if _, err = ApplyUpdateAt(p, UpdatePost{ExpectedUpdatedAt: now, Status: &scheduled}, now); err == nil {
		t.Fatal("missing due time")
	}
	if _, err = ApplyUpdateAt(p, UpdatePost{ExpectedUpdatedAt: now, Status: &published, PublishedAt: &due}, now); err == nil {
		t.Fatal("future public post")
	}
}
