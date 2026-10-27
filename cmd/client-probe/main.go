// client-probe is a manual local diagnostic tool. Building it never starts it.
package main

import (
	"content/internal/clientprobe"
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	listen := flag.String("listen", "127.0.0.1:8081", "literal loopback listener")
	target := flag.String("target", "", "isolated local content HTTP origin (required)")
	output := flag.String("log", "", "new redacted JSONL file; refuses overwrite (required)")
	flag.Parse()
	if !clientprobe.LoopbackAddress(*listen) || *target == "" || *output == "" || flag.NArg() != 0 {
		return errors.New("provide -target and -log; listener and target must use literal loopback addresses")
	}
	// Validate target before creating the log. No keys are read or generated.
	validation, err := clientprobe.New(*target, os.Stderr)
	if err != nil {
		return err
	}
	validation.Close()
	f, err := os.OpenFile(*output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("cannot create new probe log; existing files are never overwritten")
	}
	defer f.Close()
	proxy, err := clientprobe.New(*target, f)
	if err != nil {
		return err
	}
	defer proxy.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := &http.Server{Addr: *listen, Handler: proxy, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 32 << 10}
	result := make(chan error, 1)
	go func() { result <- server.ListenAndServe() }()
	fmt.Fprintln(os.Stderr, "Local probe starting. Log contains redacted structure only. Use synthetic content.")
	select {
	case err = <-result:
		if !errors.Is(err, http.ErrServerClosed) {
			return errors.New("local probe listener failed")
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err = server.Shutdown(shutdown); err != nil {
			return errors.New("probe shutdown incomplete")
		}
	}
	if proxy.Failed() {
		return errors.New("probe log write failed; capture is incomplete")
	}
	return f.Sync()
}
