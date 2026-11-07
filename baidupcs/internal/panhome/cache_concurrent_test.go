package panhome

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestConcurrentSignatureCacheRefresh(t *testing.T) {
	client := requester.NewHTTPClient()
	var calls int32
	var started, release chan struct{}
	client.Client.Transport = signatureTransport(func(r *http.Request) (*http.Response, error) {
		call := atomic.AddInt32(&calls, 1)
		if call == 1 || call == 2 {
			started <- struct{}{}
		}
		<-release
		page := fmt.Sprintf(`{"sign1":"fixture%d","sign3":"key%d","timestamp":%d,}`, call, call, call)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: &signatureBody{Reader: strings.NewReader(page)}, Request: r}, nil
	})
	ph := NewPanHome(client)
	var previous SignRes
	for round := 0; round < 2; round++ {
		started, release = make(chan struct{}, 32), make(chan struct{})
		var releaseOnce sync.Once
		unblock := func() { releaseOnce.Do(func() { close(release) }) }
		var wg sync.WaitGroup
		results := make(chan SignRes, 32)
		errs := make(chan error, 32)
		gate := make(chan struct{})
		for i := 0; i < 32; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-gate
				sign, err := ph.CacheSignature()
				results <- sign
				errs <- err
			}()
		}
		// Always release and join before the next round changes transport channels.
		func() {
			defer wg.Wait()
			defer unblock()
			close(gate)
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("signature request did not start")
			}
			time.Sleep(30 * time.Millisecond)
		}()
		close(results)
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		var first SignRes
		for sign := range results {
			if sign == nil || sign.Sign() == "" || sign.Timestamp() != fmt.Sprint(round+1) {
				t.Fatalf("inconsistent signature in round %d: %v", round, sign)
			}
			if first == nil {
				first = sign
			} else if first != sign {
				t.Fatal("concurrent callers did not share the cached result")
			}
		}
		if got := atomic.LoadInt32(&calls); got != int32(round+1) {
			t.Fatalf("homepage requests = %d, want %d", got, round+1)
		}
		if previous != nil && previous.Timestamp() != "1" {
			t.Fatal("refresh changed a previously returned signature")
		}
		previous = first
		ph.SetSignExpires()
	}
}

func TestInvalidSignatureFieldsAreNotCached(t *testing.T) {
	for _, page := range []string{
		`{"sign1":"","sign3":"key","timestamp":123,}`,
		`{"sign1":"fixture","sign3":"","timestamp":123,}`,
		`{"sign1":"fixture","sign3":"key","timestamp":,}`,
	} {
		t.Run(page, func(t *testing.T) {
			client := requester.NewHTTPClient()
			calls := 0
			var bodies []*signatureBody
			client.Client.Transport = signatureTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				payload := validSignaturePage
				if calls == 1 {
					payload = page
				}
				body := &signatureBody{Reader: strings.NewReader(payload)}
				bodies = append(bodies, body)
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body, Request: r}, nil
			})
			ph := NewPanHome(client)
			if sign, err := ph.CacheSignature(); sign != nil || !errors.Is(err, ErrMatchPanHome) {
				t.Fatalf("invalid signature accepted: %v, %v", sign, err)
			}
			for i := 0; i < 2; i++ {
				if sign, err := ph.CacheSignature(); err != nil || sign == nil || sign.Sign() == "" || sign.Timestamp() != "123" {
					t.Fatal("healthy retry failed", sign, err)
				}
			}
			if calls != 2 {
				t.Fatalf("requests = %d, want 2", calls)
			}
			for _, body := range bodies {
				if !body.closed {
					t.Fatal("response body not closed")
				}
			}
		})
	}
}

func TestFailedRefreshPreservesPreviousSignature(t *testing.T) {
	client := requester.NewHTTPClient()
	calls := 0
	client.Client.Transport = signatureTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		page := validSignaturePage
		if calls == 2 {
			page = `{"sign1":"fixture","sign3":"","timestamp":456,}`
		} else if calls == 3 {
			page = `{"sign1":"next","sign3":"key","timestamp":789,}`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: &signatureBody{Reader: strings.NewReader(page)}, Request: r}, nil
	})
	ph := NewPanHome(client)
	previous, err := ph.CacheSignature()
	if err != nil || previous == nil {
		t.Fatal(previous, err)
	}
	previousSign := previous.Sign()
	ph.SetSignExpires()
	if sign, err := ph.CacheSignature(); sign != nil || !errors.Is(err, ErrMatchPanHome) {
		t.Fatal("invalid refresh accepted", sign, err)
	}
	next, err := ph.CacheSignature()
	if err != nil || next == nil || next.Timestamp() != "789" || next.Sign() == "" {
		t.Fatal("retry after invalid refresh failed", next, err)
	}
	if cached, err := ph.CacheSignature(); err != nil || cached != next || calls != 3 {
		t.Fatal("successful retry not cached", cached, err, calls)
	}
	if previous.Timestamp() != "123" || previous.Sign() != previousSign {
		t.Fatal("refresh mutated previously returned signature")
	}
}

func TestDirectSignatureDoesNotReplaceCachedResult(t *testing.T) {
	client := requester.NewHTTPClient()
	calls := 0
	client.Client.Transport = signatureTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		page := fmt.Sprintf(`{"sign1":"fixture%d","sign3":"key","timestamp":%d,}`, calls, calls)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: &signatureBody{Reader: strings.NewReader(page)}, Request: r}, nil
	})
	ph := NewPanHome(client)
	cached, err := ph.CacheSignature()
	if err != nil || cached == nil {
		t.Fatal(cached, err)
	}
	direct, err := ph.Signature()
	if err != nil || direct == nil || direct.Timestamp() != "2" {
		t.Fatal(direct, err)
	}
	if got, err := ph.CacheSignature(); err != nil || got != cached || got.Timestamp() != "1" || calls != 2 {
		t.Fatal("direct fetch changed cache semantics", got, err, calls)
	}
}
