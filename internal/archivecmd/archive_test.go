package archivecmd

import (
	"content/cms"
	"content/storage/postgres"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type fake struct {
	posts    map[string]cms.Post
	bindings map[string]postgres.ArchiveBinding
	writes   int
}

func (f *fake) Get(_ context.Context, q cms.Query) (cms.Post, error) {
	p, ok := f.posts[q.ID]
	if !ok || p.Publication != q.Publication {
		return p, cms.ErrNotFound
	}
	return p, nil
}
func (f *fake) ArchiveBinding(_ context.Context, pub, id string) (postgres.ArchiveBinding, error) {
	b, ok := f.bindings[id]
	if !ok {
		return b, cms.ErrNotFound
	}
	return b, nil
}
func (f *fake) EnqueueArchive(_ context.Context, r postgres.ArchiveRequest) (postgres.ArchiveReceipt, error) {
	f.writes++
	return postgres.ArchiveReceipt{EventID: "fixture", ContentID: f.posts[r.PostID].UUID, Revision: r.ExpectedRevision, State: "pending"}, nil
}
func fixture() (Manifest, *fake) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	id := "0123456789abcdef01234567"
	uuid := "11111111-1111-4111-8111-111111111111"
	m := Manifest{1, "one", "content", "ap", "https://example.invalid/blog", []Post{{id, uuid, "2", "first", at.Format(time.RFC3339)}}}
	f := &fake{posts: map[string]cms.Post{id: {ID: id, UUID: uuid, Publication: "one", Kind: "post", Status: "published", Visibility: "public", Revision: 2, Slug: "first", PublishedAt: &at}}, bindings: map[string]postgres.ArchiveBinding{}}
	return m, f
}
func TestManifestPinAndStrictShape(t *testing.T) {
	m, _ := fixture()
	raw, _ := json.Marshal(m)
	sum := sha256.Sum256(raw)
	if _, e := Decode(raw, hex.EncodeToString(sum[:])); e != nil {
		t.Fatal(e)
	}
	if _, e := Decode(raw, "wrong"); e == nil {
		t.Fatal("bad pin accepted")
	}
	m.Posts = append(m.Posts, m.Posts[0])
	raw, _ = json.Marshal(m)
	if _, e := Decode(raw, ""); e == nil {
		t.Fatal("duplicate accepted")
	}
	if _, e := Decode([]byte(`{"schema_version":1,"extra":true}`), ""); e == nil {
		t.Fatal("unknown fields accepted")
	}
}
func TestWholePlanBeforeWrites(t *testing.T) {
	m, f := fixture()
	second := m.Posts[0]
	second.ID = "1123456789abcdef01234567"
	second.UUID = "21111111-1111-4111-8111-111111111111"
	m.Posts = append(m.Posts, second)
	if _, e := Plan(context.Background(), f, m); e == nil {
		t.Fatal("missing later item accepted")
	}
	if f.writes != 0 {
		t.Fatal("partial preflight write")
	}
}
func TestPrivateAndStaleRefused(t *testing.T) {
	for _, change := range []func(*cms.Post){func(p *cms.Post) { p.Status = "draft" }, func(p *cms.Post) { p.Visibility = "members" }, func(p *cms.Post) { p.Revision++ }, func(p *cms.Post) { p.Kind = "page" }, func(p *cms.Post) { p.Slug = "changed" }} {
		m, f := fixture()
		p := f.posts[m.Posts[0].ID]
		change(&p)
		f.posts[p.ID] = p
		if _, e := Plan(context.Background(), f, m); e == nil {
			t.Fatal("ineligible item accepted")
		}
	}
}
func TestExactReceiptResumeAfterWithdrawal(t *testing.T) {
	m, f := fixture()
	p := f.posts[m.Posts[0].ID]
	p.Status = "draft"
	p.Revision = 5
	f.posts[p.ID] = p
	f.bindings[p.ID] = postgres.ArchiveBinding{ArchiveReceipt: postgres.ArchiveReceipt{EventID: "event", ContentID: p.UUID, State: "accepted", Revision: 2}, Publication: "one", PostID: p.ID, Destination: "ap", SourceID: "content", SiteURL: m.SiteURL}
	plan, e := Plan(context.Background(), f, m)
	if e != nil || !plan[0].Existing {
		t.Fatal(e)
	}
	m.Source = "different"
	if _, e = Plan(context.Background(), f, m); e == nil {
		t.Fatal("mismatched retry accepted")
	}
}
func TestReceiptOutputFailureReportsUncertainOutcome(t *testing.T) {
	m, f := fixture()
	plan, e := Plan(context.Background(), f, m)
	if e != nil {
		t.Fatal(e)
	}
	e = Apply(context.Background(), f, plan, func(string, postgres.ArchiveReceipt) error { return errors.New("disk full") })
	if e == nil || f.writes != 1 {
		t.Fatal("receipt loss hidden")
	}
}
