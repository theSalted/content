package main

import (
	"content/internal/config"
	"content/publishing"
	"content/storage/postgres"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// stdout is a small versioned supervisor protocol, never a payload/secret log.
func deliver() error {
	cfg, e := config.LoadDelivery(os.Getenv)
	if e != nil {
		return e
	}
	if !cfg.Enabled {
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sender, e := publishing.NewHTTPClient(cfg.Endpoint, cfg.Token)
	if e != nil {
		return e
	}
	defer sender.Close()
	var archiveSender *publishing.HTTPClient
	if cfg.ArchiveEnabled {
		archiveSender, e = publishing.NewArchiveHTTPClient(cfg.ArchiveEndpoint, cfg.Token)
		if e != nil {
			return e
		}
		defer archiveSender.Close()
	}
	var resolver *publishing.ReceiptHTTPClient
	if cfg.FederationDestination != "" {
		resolver, e = publishing.NewReceiptHTTPClient(cfg.ReceiptEndpoint, cfg.Token)
		if e != nil {
			return e
		}
		defer resolver.Close()
	}
	connect, closeConnect := context.WithTimeout(ctx, 10*time.Second)
	options := []postgres.Option{postgres.WithConnectionLimit(2)}
	if cfg.FederationDestination != "" {
		options = append(options, postgres.WithFederationProjection(cfg.FederationDestination))
	}
	store, e := postgres.Open(connect, cfg.DatabaseURL, cfg.Schema, cfg.Installation, options...)
	closeConnect()
	if e != nil {
		return errors.New("publishing database unavailable (details redacted)")
	}
	defer store.Close()
	var mu sync.Mutex
	enc := json.NewEncoder(os.Stdout)
	emit := func(event, lane string, fields map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		if fields == nil {
			fields = map[string]any{}
		}
		fields["protocol"] = "content-delivery-supervisor-v1"
		fields["event"] = event
		fields["lane"] = lane
		fields["at"] = time.Now().UTC().Format(time.RFC3339Nano)
		_ = enc.Encode(fields)
	}
	emit("started", "all", map[string]any{"archive_enabled": cfg.ArchiveEnabled, "pool_limit": 2, "resolution_enabled": resolver != nil})
	workers := 1
	if cfg.ArchiveEnabled {
		workers++
	}
	if resolver != nil {
		workers++
	}
	results := make(chan error, workers)
	var wg sync.WaitGroup
	start := func(lane string, q publishing.Queue, sender publishing.Sender) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var last time.Time
			w := publishing.Worker{Enabled: true, Publication: cfg.Publication, Destination: cfg.Destination, Queue: q, Sender: sender, Timeout: 10 * time.Second, Lease: 30 * time.Second, Idle: 5 * time.Second, MaxAttempts: 12}
			w.Progress = func() {
				if time.Since(last) >= 5*time.Second {
					last = time.Now()
					emit("progress", lane, nil)
				}
			}
			w.Observe = func(j publishing.Job, o publishing.Outcome) {
				emit("outcome", lane, map[string]any{"event_id": j.EventID, "attempt": j.Attempts, "state": o.State, "code": o.Code, "acceptance": o.Result})
			}
			results <- w.Run(ctx)
		}()
	}
	start("live", store, sender)
	if cfg.ArchiveEnabled {
		start("archive", store.ArchiveQueue(), archiveSender)
	}
	if resolver != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var last time.Time
			w := publishing.ResolutionWorker{Enabled: true, Publication: cfg.Publication, Destination: cfg.Destination, Queue: store, Resolver: resolver, Timeout: 10 * time.Second, Lease: 30 * time.Second, Idle: 5 * time.Second, MaxAttempts: 12}
			w.Progress = func() {
				if time.Since(last) >= 5*time.Second {
					last = time.Now()
					emit("progress", "resolution", nil)
				}
			}
			w.Observe = func(j publishing.Job, o publishing.ResolutionOutcome) {
				emit("resolution_outcome", "resolution", map[string]any{"event_id": j.EventID, "attempt": j.Attempts, "state": o.State, "code": o.Code})
			}
			results <- w.Run(ctx)
		}()
	}
	statsDone := make(chan struct{})
	go func() {
		defer close(statsDone)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				check, done := context.WithTimeout(ctx, 10*time.Second)
				v, err := store.PublishingStatus(check, cfg.Publication, cfg.Destination)
				done()
				if err != nil {
					emit("status_unavailable", "all", nil)
				} else {
					emit("queue_status", "all", map[string]any{"queue": v})
					if resolver != nil {
						check, done := context.WithTimeout(ctx, 10*time.Second)
						counts, err := store.ResolutionStatus(check, cfg.Publication, cfg.Destination)
						done()
						if err != nil {
							emit("status_unavailable", "resolution", nil)
						} else {
							emit("resolution_queue_status", "resolution", map[string]any{"queue": counts})
						}
					}
				}
			}
		}
	}()
	var result error
	select {
	case <-ctx.Done():
	case result = <-results:
	}
	cancel()
	wg.Wait()
	<-statsDone
	emit("stopped", "all", map[string]any{"failed": result != nil})
	if result != nil {
		return errors.New("publishing child stopped after queue failure (details redacted)")
	}
	return nil
}
