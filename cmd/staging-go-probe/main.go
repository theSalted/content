// staging-go-probe is a bounded synthetic integration executable. It is not a
// production entrypoint and does not migrate/provision resources or generate keys.
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
	ctx, cancel := context.WithTimeout(ctx, 170*time.Second)
	defer cancel()
	result, e := stagingprobe.Run(ctx, c)
	if e != nil {
		json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "failed", "failure": e.Error()})
		cleanup()
		os.Exit(1)
	}
	json.NewEncoder(os.Stdout).Encode(result)
}
