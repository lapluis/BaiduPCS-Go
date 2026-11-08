package downloader

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Retry-After permits non-negative decimal seconds or an HTTP date. Saturate
// very large valid delays instead of wrapping into an immediate retry.
func retryAfterDeadline(value string, now time.Time) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	digits := true
	for _, c := range value {
		if c < '0' || c > '9' {
			digits = false
			break
		}
	}
	if digits {
		seconds, err := strconv.ParseUint(value, 10, 64)
		const maxDelay = time.Duration(1<<63 - 1)
		if err != nil || seconds > uint64(maxDelay/time.Second) {
			return now.Add(maxDelay)
		}
		if seconds == 0 {
			return time.Time{}
		}
		return now.Add(time.Duration(seconds) * time.Second)
	}
	if deadline, err := http.ParseTime(value); err == nil && deadline.After(now) {
		return deadline
	}
	return time.Time{}
}

func (w *Worker) setRetryAfter(value string) {
	deadline := retryAfterDeadline(value, time.Now())
	w.controlMu.Lock()
	w.retryAfter = deadline
	w.controlMu.Unlock()
}

func (w *Worker) retryBlocked() bool {
	w.controlMu.Lock()
	defer w.controlMu.Unlock()
	return time.Now().Before(w.retryAfter)
}

// Resume can launch an execution directly. Keep the request boundary guarded
// too, and use the execution context so pause/cancel never waits for the delay.
func (w *Worker) waitRetryAfter(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		w.controlMu.Lock()
		deadline := w.retryAfter
		w.controlMu.Unlock()
		delay := time.Until(deadline)
		if delay <= 0 {
			return nil
		}
		w.status.SetStatusCode(StatusCodeRateLimited)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
