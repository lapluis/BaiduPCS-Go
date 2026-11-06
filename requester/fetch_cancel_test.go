package requester

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"
)

type cancelPreparationBody struct {
	lengthCalls int
	closeCalls  int
	cancel      context.CancelFunc
}

func (b *cancelPreparationBody) Read([]byte) (int, error) { return 0, io.EOF }
func (b *cancelPreparationBody) Close() error             { b.closeCalls++; return nil }
func (b *cancelPreparationBody) Len() int64 {
	b.lengthCalls++
	if b.cancel != nil {
		b.cancel()
	}
	return 7
}

func TestRequestCancellationStopsBodyPreparationAndTransport(t *testing.T) {
	for _, mode := range []string{"already-canceled", "expired", "during-length", "nil-context"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			body := &cancelPreparationBody{}
			wantLengthCalls := 0
			switch mode {
			case "already-canceled":
				cancel()
			case "expired":
				var stop context.CancelFunc
				ctx, stop = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer stop()
			case "during-length":
				body.cancel = cancel
				wantLengthCalls = 1
			case "nil-context":
				ctx = nil
			}
			transport := &clientFixtureTransport{}
			client := NewHTTPClient()
			client.Client.Transport = transport
			resp, err := client.ReqWithContext(ctx, http.MethodPost, "fixture://upload/", body, nil)
			if resp != nil {
				resp.Body.Close()
			}
			if err == nil || resp != nil || transport.calls != 0 || body.lengthCalls != wantLengthCalls {
				t.Fatalf("stopped request performed work: err=%v response=%v transportCalls=%d lengthCalls=%d", err, resp, transport.calls, body.lengthCalls)
			}
			if ctx != nil && !errors.Is(err, ctx.Err()) {
				t.Fatalf("cancellation cause lost: %v", err)
			}
			wantCloses := 1
			if ctx == nil {
				wantCloses = 0
			}
			if body.closeCalls != wantCloses {
				t.Fatalf("body close count=%d want=%d", body.closeCalls, wantCloses)
			}
		})
	}
}

type preparationStringer struct{ calls *int }

func (s preparationStringer) String() string { *s.calls++; return "fixture" }

func TestCanceledRequestDoesNotEncodeFormValues(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	encoded := 0
	transport := &clientFixtureTransport{}
	client := NewHTTPClient()
	client.Client.Transport = transport
	resp, err := client.ReqWithContext(ctx, http.MethodPost, "fixture://upload/", map[string]interface{}{"field": preparationStringer{&encoded}}, nil)
	if resp != nil {
		resp.Body.Close()
	}
	if !errors.Is(err, context.Canceled) || resp != nil || encoded != 0 || transport.calls != 0 {
		t.Fatalf("canceled form encoded or sent: err=%v response=%v encoded=%d sent=%d", err, resp, encoded, transport.calls)
	}
}
