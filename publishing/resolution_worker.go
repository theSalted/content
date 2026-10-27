package publishing

import (
	"context"
	"errors"
	"time"
)

type ResolutionQueue interface {
	PrepareResolution(context.Context, string, string) (bool, error)
	ClaimResolution(context.Context, string, string, time.Duration) (*Job, error)
	FinishResolution(context.Context, Job, ResolutionOutcome) error
}

// ResolutionWorker never calls the publishing sender. A query outage retries
// only the exact read request after delivery has already been durably accepted.
type ResolutionWorker struct {
	Enabled                  bool
	Publication, Destination string
	Queue                    ResolutionQueue
	Resolver                 Resolver
	Timeout, Lease, Idle     time.Duration
	MaxAttempts              int
	Progress                 func()
	Observe                  func(Job, ResolutionOutcome)
}

func (w ResolutionWorker) validate() error {
	if !w.Enabled {
		return nil
	}
	if w.Queue == nil || w.Resolver == nil || w.Publication == "" || w.Destination == "" || w.Timeout < time.Millisecond || w.Timeout > time.Minute || w.Lease < 2*w.Timeout+5*time.Second || w.Lease > 5*time.Minute || w.Idle < time.Millisecond || w.MaxAttempts < 1 || w.MaxAttempts > 100 {
		return errors.New("invalid resolution worker configuration")
	}
	return nil
}
func (w ResolutionWorker) Step(ctx context.Context) (bool, error) {
	if !w.Enabled {
		return false, nil
	}
	if err := w.validate(); err != nil {
		return false, err
	}
	qctx, done := context.WithTimeout(ctx, w.Timeout)
	prepared, err := w.Queue.PrepareResolution(qctx, w.Publication, w.Destination)
	if err != nil {
		done()
		return false, err
	}
	job, err := w.Queue.ClaimResolution(qctx, w.Publication, w.Destination, w.Lease)
	done()
	if err != nil || job == nil {
		return prepared, err
	}
	out := ResolutionOutcome{State: "quarantined", Code: "attempt_limit"}
	if job.Attempts <= w.MaxAttempts {
		read, cancel := context.WithTimeout(ctx, w.Timeout)
		out = w.Resolver.Resolve(read, job.Payload)
		cancel()
		if out.State == "retry" {
			if job.Attempts >= w.MaxAttempts {
				out = ResolutionOutcome{State: "quarantined", Code: "attempt_limit"}
			} else {
				out.Delay = Backoff(job.Attempts)
			}
		}
	}
	finish, cancel := context.WithTimeout(ctx, w.Timeout)
	defer cancel()
	if err = w.Queue.FinishResolution(finish, *job, out); err != nil {
		return true, err
	}
	if w.Observe != nil {
		w.Observe(*job, out)
	}
	return true, nil
}
func (w ResolutionWorker) Run(ctx context.Context) error {
	if !w.Enabled {
		return nil
	}
	if err := w.validate(); err != nil {
		return err
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		worked, err := w.Step(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return errors.New("receipt resolution queue failed (details redacted)")
		}
		if w.Progress != nil {
			w.Progress()
		}
		if !worked {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(w.Idle):
			}
		}
	}
}
