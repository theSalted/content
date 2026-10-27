// staging-go-complete verifies an existing synthetic staging run without writes.
package main

import (
	"content/internal/stagingprobe"
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	c, cleanup, e := stagingprobe.Load(os.Getenv)
	if e != nil {
		json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "failed", "failure": "configuration"})
		os.Exit(1)
	}
	defer cleanup()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	report, e := stagingprobe.Complete(ctx, c)
	if e != nil {
		json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "failed", "failure": e.Error()})
		cleanup()
		os.Exit(1)
	}
	json.NewEncoder(os.Stdout).Encode(report)
}
