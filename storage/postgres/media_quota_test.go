package postgres

import (
	"content/media"
	"content/storage/objects"
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestMediaQuotaPostgresConcurrentReservation(t *testing.T) {
	f := newRoleFixture(t)
	grantMedia(t, f)
	var configured int64
	if e := f.admin.QueryRow(f.ctx, "SELECT limit_bytes FROM "+ident(f.schema)+".media_quota WHERE singleton").Scan(&configured); e != nil || configured != 1_000_000_000 {
		t.Fatal("default decimal GB quota", configured, e)
	}
	first, e := Open(f.ctx, f.runtimeDSN, f.schema, "installation-a")
	if e != nil {
		t.Fatal(e)
	}
	defer first.Close()
	second, e := Open(f.ctx, f.runtimeDSN, f.schema, "installation-a")
	if e != nil {
		t.Fatal(e)
	}
	defer second.Close()
	data := mediaPNG(t, 7)
	mustExec(t, f.ctx, f.admin, "UPDATE "+ident(f.schema)+".media_quota SET limit_bytes=$1", len(data))
	fs, e := objects.NewFilesystem(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer fs.Close()
	// Separate service/pool instances emulate independent replicas. Same bytes in
	// different publications must consume separate quota reservations.
	services := make([]*media.Service, 2)
	services[0], e = media.New(first, fs, "https://cms.example.invalid", "quota-test")
	if e != nil {
		t.Fatal(e)
	}
	services[1], e = media.New(second, fs, "https://cms.example.invalid", "quota-test")
	if e != nil {
		t.Fatal(e)
	}
	var assets [2]media.Asset
	var errs [2]error
	pubs := []string{"quota-a", "quota-b"}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range services {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			assets[i], errs[i] = services[i].Upload(f.ctx, pubs[i], data, "image/png", "")
		}(i)
	}
	close(start)
	wg.Wait()
	winner := -1
	failures := 0
	for i, e := range errs {
		if e == nil {
			winner = i
		} else if errors.Is(e, media.ErrQuota) {
			failures++
		} else {
			t.Fatal(e)
		}
	}
	if winner < 0 || failures != 1 {
		t.Fatal("quota oversubscribed", errs)
	}
	var used int64
	var rows int
	if e = f.admin.QueryRow(f.ctx, "SELECT COALESCE(SUM(byte_size),0),count(*) FROM "+ident(f.schema)+".media_objects").Scan(&used, &rows); e != nil || used != int64(len(data)) || rows != 1 {
		t.Fatal("reservation overflow", used, rows, e)
	}
	if a, e := services[winner].Upload(f.ctx, pubs[winner], data, "image/png", ""); e != nil || a.ID != assets[winner].ID {
		t.Fatal("full-quota retry lost idempotency", e)
	}
	// Runtime cannot enlarge/drop the quota or modify reservation sizes.
	for _, q := range []string{"UPDATE " + ident(f.schema) + ".media_quota SET limit_bytes=2000000000", "DELETE FROM " + ident(f.schema) + ".media_quota", "UPDATE " + ident(f.schema) + ".media_objects SET byte_size=1"} {
		_, e := f.runtime.Exec(f.ctx, q)
		denied(t, e)
	}
	if mediaQuotaLockID("installation-a", f.schema) == MigrationLockID("installation-a", f.schema) || mediaQuotaLockID("installation-a", f.schema) == mediaQuotaLockID("installation-a", f.otherSchema) {
		t.Fatal("quota lock domain collision")
	}
	// A pending failed storage operation consumes capacity; it is released only
	// after cleanup has removed its bytes and committed the pending-row deletion.
	mustExec(t, f.ctx, f.admin, "UPDATE "+ident(f.schema)+".media_quota SET limit_bytes=$1", 2*len(data))
	fault := &ackFailObjects{Objects: fs}
	fault.fail.Store(true)
	pending, e := media.New(first, fault, "https://cms.example.invalid", "quota-test")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pending.Upload(f.ctx, "pending", data, "image/png", ""); !errors.Is(e, media.ErrUnavailable) {
		t.Fatal("fault missing", e)
	}
	if _, e = services[0].Upload(f.ctx, "blocked", data, "image/png", ""); !errors.Is(e, media.ErrQuota) {
		t.Fatal("pending bytes did not reserve quota", e)
	}
	mustExec(t, f.ctx, f.admin, "UPDATE "+ident(f.schema)+".media_objects SET created_at=clock_timestamp()-interval '2 days' WHERE publication_id='pending'")
	// This isolated test uses its migration owner for this one maintenance action;
	// the other cleanup test verifies the restricted maintenance role separately.
	cleaner, e := Open(f.ctx, f.migrateDSN, f.schema, "installation-a")
	if e != nil {
		t.Fatal(e)
	}
	defer cleaner.Close()
	n, e := cleaner.MediaCleanup(f.ctx, "pending", time.Now().Add(-24*time.Hour), 50, func(ctx context.Context, a media.Asset) error { return fs.Delete(ctx, a.Key) })
	if e != nil || n != 1 {
		t.Fatal("cleanup", n, e)
	}
	if _, e = services[0].Upload(f.ctx, "released", data, "image/png", ""); e != nil {
		t.Fatal("cleanup did not release capacity", e)
	}
}
