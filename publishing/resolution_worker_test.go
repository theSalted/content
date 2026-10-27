package publishing

import (
	"context"
	"testing"
	"time"
)

type fakeResolutionQueue struct {
	job      Job
	out      ResolutionOutcome
	prepared bool
}

func (q *fakeResolutionQueue) PrepareResolution(context.Context, string, string) (bool, error) {
	q.prepared = true
	return false, nil
}
func (q *fakeResolutionQueue) ClaimResolution(context.Context, string, string, time.Duration) (*Job, error) {
	return &q.job, nil
}
func (q *fakeResolutionQueue) FinishResolution(_ context.Context, _ Job, out ResolutionOutcome) error {
	q.out = out
	return nil
}

type fakeResolver struct{ calls int }

func (r *fakeResolver) Resolve(context.Context, []byte) ResolutionOutcome {
	r.calls++
	return ResolutionOutcome{State: "retry", Code: "transport_error"}
}
func TestResolutionWorkerDisabledAndRetryBudget(t *testing.T) {
	q := &fakeResolutionQueue{job: Job{Attempts: 1}}
	r := &fakeResolver{}
	w := ResolutionWorker{Queue: q, Resolver: r}
	if yes, err := w.Step(context.Background()); err != nil || yes || q.prepared || r.calls != 0 {
		t.Fatal("disabled resolution performed IO")
	}
	w.Enabled = true
	w.Publication = "example"
	w.Destination = "ap"
	w.Timeout = time.Second
	w.Lease = 10 * time.Second
	w.Idle = time.Millisecond
	w.MaxAttempts = 2
	if yes, err := w.Step(context.Background()); err != nil || !yes || q.out.State != "retry" || q.out.Delay < time.Second {
		t.Fatal("query retry", q.out, err)
	}
	q.job.Attempts = 2
	if _, err := w.Step(context.Background()); err != nil || q.out.State != "quarantined" {
		t.Fatal("query budget", q.out, err)
	}
	calls := r.calls
	q.job.Attempts = 3
	if _, err := w.Step(context.Background()); err != nil || r.calls != calls {
		t.Fatal("query exceeded budget")
	}
}
