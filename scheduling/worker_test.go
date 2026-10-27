package scheduling

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type workerFixture struct {
	calls  atomic.Int32
	cancel context.CancelFunc
}

func (s *workerFixture) PublishDue(ctx context.Context, pub string) (bool, error) {
	if pub != "synthetic" {
		panic("lost publication scope")
	}
	if _, ok := ctx.Deadline(); !ok {
		panic("unbounded operation")
	}
	if s.calls.Add(1) == 1 {
		return true, errors.New("synthetic job backed off")
	}
	s.cancel()
	return true, nil
}
func TestWorkerRunsImmediatelyContinuesAfterBackedOffJobAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &workerFixture{cancel: cancel}
	var reports atomic.Int32
	done := make(chan struct{})
	go func() { Run(ctx, store, "synthetic", func(error) { reports.Add(1) }); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("startup/next-job/shutdown blocked")
	}
	if store.calls.Load() != 2 || reports.Load() != 1 {
		t.Fatal("poison job blocked other due work")
	}
}
