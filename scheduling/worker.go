// Package scheduling runs durable due work within the existing CMS process.
package scheduling

import (
	"context"
	"time"
)

type Store interface {
	PublishDue(context.Context, string) (bool, error)
}

// Run bounds batches and each transaction. DB rows, not timers, retain intent.
func Run(ctx context.Context, store Store, publication string, report func(error)) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			for range 20 {
				call, cancel := context.WithTimeout(ctx, 5*time.Second)
				worked, err := store.PublishDue(call, publication)
				cancel()
				if err != nil {
					if ctx.Err() == nil && report != nil {
						report(err)
					}
					if !worked {
						break
					}
				}
				if !worked || ctx.Err() != nil {
					break
				}
			}
			timer.Reset(10 * time.Second)
		}
	}
}
