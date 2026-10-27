// production-one-post is an explicitly bounded synthetic test, not a server.
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
	c, g, cleanup, e := stagingprobe.LoadOne(os.Getenv)
	if e != nil {
		json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "failed", "failure": "configuration_or_start_gate"})
		os.Exit(1)
	}
	defer cleanup()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	report, e := stagingprobe.RunOne(ctx, c, g)
	json.NewEncoder(os.Stdout).Encode(report)
	if e != nil {
		cleanup()
		os.Exit(1)
	}
}
