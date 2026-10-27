package publishing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Job struct {
	EventID, LeaseToken string
	Payload             []byte
	Attempts            int
}

// Confirmation is normalized receiver evidence, not the publishing stream cursor.
// Transport may populate it only from a versioned, event-bound applied receipt.
// Legacy and stale acknowledgements must leave it nil.
type Confirmation struct{ ObjectID, State, Revision string }

type Outcome struct {
	Confirmation        *Confirmation
	State, Code, Result string
	Delay               time.Duration
}
type Queue interface {
	PrepareNext(context.Context, string, string) (bool, error)
	Claim(context.Context, string, string, time.Duration) (*Job, error)
	Finish(context.Context, Job, Outcome) error
}
type Sender interface {
	Send(context.Context, []byte) Outcome
}

// Worker has no hidden defaults which enable network or database access.
type Worker struct {
	Enabled                  bool
	Publication, Destination string
	Queue                    Queue
	Sender                   Sender
	Timeout, Lease, Idle     time.Duration
	MaxAttempts              int
	Observe                  func(Job, Outcome)
	Progress                 func()
}

func (w Worker) validate() error {
	if !w.Enabled {
		return nil
	}
	if w.Queue == nil || w.Sender == nil || w.Publication == "" || w.Destination == "" || w.Timeout < time.Millisecond || w.Timeout > time.Minute || w.Lease < 2*w.Timeout+5*time.Second || w.Lease > 5*time.Minute || w.Idle < time.Millisecond || w.MaxAttempts < 1 || w.MaxAttempts > 100 {
		return errors.New("invalid publishing worker configuration")
	}
	return nil
}
func Backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 10 {
		attempt = 10
	}
	ceiling := time.Second * time.Duration(1<<uint(attempt))
	if ceiling > 5*time.Minute {
		ceiling = 5 * time.Minute
	}
	return ceiling/2 + time.Duration(rand.Int64N(int64(ceiling/2)+1))
}

// Step returns whether work was found. Network I/O is outside all queue transactions.
func (w Worker) Step(ctx context.Context) (bool, error) {
	if !w.Enabled {
		return false, nil
	}
	if err := w.validate(); err != nil {
		return false, err
	}
	queueCtx, queueCancel := context.WithTimeout(ctx, w.Timeout)
	prepared, err := w.Queue.PrepareNext(queueCtx, w.Publication, w.Destination)
	if err != nil {
		queueCancel()
		return false, err
	}
	job, err := w.Queue.Claim(queueCtx, w.Publication, w.Destination, w.Lease)
	queueCancel()
	if err != nil || job == nil {
		return prepared, err
	}
	outcome := Outcome{State: "quarantined", Code: "attempt_limit"}
	if job.Attempts <= w.MaxAttempts {
		sendCtx, cancel := context.WithTimeout(ctx, w.Timeout)
		outcome = w.Sender.Send(sendCtx, job.Payload)
		cancel()
		if outcome.State == "retry" {
			if job.Attempts >= w.MaxAttempts {
				outcome = Outcome{State: "quarantined", Code: "attempt_limit"}
			} else {
				outcome.Delay = Backoff(job.Attempts)
			}
		}
	}
	// A canceled process may fail to record the result; lease expiry safely retries.
	finishCtx, finishCancel := context.WithTimeout(ctx, w.Timeout)
	defer finishCancel()
	if err = w.Queue.Finish(finishCtx, *job, outcome); err != nil {
		return true, err
	}
	if w.Observe != nil {
		w.Observe(*job, outcome)
	}
	return true, nil
}
func (w Worker) Run(ctx context.Context) error {
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
			return errors.New("publishing queue operation failed (details redacted)")
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

type HTTPClient struct {
	endpoint, token string
	client          *http.Client
}

func NewHTTPClient(endpoint, token string) (*HTTPClient, error) {
	return newHTTPClient(endpoint, token, "/internal/v2/events")
}
func newHTTPClient(endpoint, token, path string) (*HTTPClient, error) {
	u, err := url.Parse(endpoint)
	loopback := err == nil && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1" || u.Hostname() == "localhost")
	if err != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && loopback)) || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != path || len(token) < 24 || strings.ContainsAny(token, "\r\n\t ") {
		return nil, errors.New("explicit HTTPS internal v2 endpoint (or loopback HTTP) and scoped bearer token required")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.ResponseHeaderTimeout = 10 * time.Second
	tr.MaxResponseHeaderBytes = 32 << 10
	return &HTTPClient{endpoint, token, &http.Client{Transport: tr, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (s *HTTPClient) Close() { s.client.CloseIdleConnections() }
func (s *HTTPClient) Send(ctx context.Context, payload []byte) Outcome {
	if len(payload) > MaxPayload || !json.Valid(payload) {
		return Outcome{State: "quarantined", Code: "invalid_payload"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(payload))
	if err != nil {
		return Outcome{State: "quarantined", Code: "invalid_request"}
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(req)
	if err != nil {
		return Outcome{State: "retry", Code: "transport_error"}
	}
	defer response.Body.Close()
	if response.StatusCode == 202 {
		raw, err := io.ReadAll(io.LimitReader(response.Body, (16<<10)+1))
		if err != nil || len(raw) > 16<<10 {
			return Outcome{State: "retry", Code: "invalid_ack"}
		}
		var ack struct {
			Result   string `json:"result"`
			Original string `json:"original_result"`
			ObjectID string `json:"object_id"`
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if d.Decode(&ack) != nil || d.Decode(new(any)) != io.EOF {
			return Outcome{State: "retry", Code: "invalid_ack"}
		}
		if ack.Result == "duplicate" && (ack.Original == "applied" || ack.Original == "stale") && ack.ObjectID == "" {
			return Outcome{State: "accepted", Result: ack.Original, Code: "duplicate"}
		}
		if (ack.Result == "applied" || ack.Result == "stale") && ack.Original == "" && PublicURL(ack.ObjectID) {
			return Outcome{State: "accepted", Result: ack.Result, Code: ack.Result}
		}
		return Outcome{State: "retry", Code: "invalid_ack"}
	}
	if response.StatusCode >= 500 || response.StatusCode == 408 || response.StatusCode == 429 {
		return Outcome{State: "retry", Code: "http_retryable"}
	}
	// Never follow redirects or persist a remote response body/token/URL in diagnostics.
	return Outcome{State: "quarantined", Code: "http_rejected"}
}
