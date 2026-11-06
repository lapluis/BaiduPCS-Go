package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestConcurrentLoadBalancerProbesRetainEveryValidatedServer(t *testing.T) {
	previous := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(previous)
	const servers = 512
	for round := 0; round < 20; round++ {
		d := NewDownloader("https://primary.invalid/file", nil, NewConfig())
		d.SetFileContentLength(8)
		d.SetDURLCheckFunc(func(_ *requester.HTTPClient, url string) (int64, *http.Response, error) {
			req, err := http.NewRequest(http.MethodGet, url, nil)
			if err != nil {
				return 0, nil, err
			}
			return 8, &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
		})
		// Release validated candidates in batches to exercise concurrent result collection.
		var mu sync.Mutex
		arrived := 0
		gate := make(chan struct{})
		d.SetLoadBalancerCompareFunc(func(map[string]string, *http.Response) bool {
			mu.Lock()
			current := gate
			arrived++
			if arrived == 4 {
				arrived = 0
				gate = make(chan struct{})
				close(current)
			}
			mu.Unlock()
			<-current
			return true
		})
		for i := 0; i < servers; i++ {
			d.AddLoadBalanceServer(fmt.Sprintf("https://mirror-%d.invalid/file", i))
		}
		d.lazyInit()
		results := d.checkLoadBalancers(context.Background())
		if len(results.lbr) != servers+1 {
			t.Fatalf("round %d: only %d/%d validated servers retained", round, len(results.lbr), servers+1)
		}
		seen := map[string]bool{}
		for _, result := range results.lbr {
			if result == nil || seen[result.URL] {
				t.Fatal("concurrent collection returned a missing or duplicate server")
			}
			seen[result.URL] = true
		}
		for i := 0; i < servers; i++ {
			if !seen[fmt.Sprintf("https://mirror-%d.invalid/file", i)] {
				t.Fatal("validated server omitted", i)
			}
		}
	}
}

func TestLoadBalancerErrorBodyRemainsReadableDuringValidation(t *testing.T) {
	for _, status := range []int{403, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "denied")
			}))
			defer server.Close()
			d := NewDownloader("https://primary.invalid/file", nil, NewConfig())
			d.SetFileContentLength(8)
			d.AddLoadBalanceServer(server.URL)
			var body []byte
			var readErr error
			called := false
			d.SetStatusCodeBodyCheckFunc(func(r io.Reader) error {
				called = true
				body, readErr = io.ReadAll(r)
				return errors.New("rejected error document")
			})
			d.lazyInit()
			result := d.checkLoadBalancers(context.Background())
			if !called || readErr != nil || string(body) != "denied" {
				t.Fatalf("error decoder could not read response: called=%v body=%q err=%v", called, body, readErr)
			}
			if len(result.lbr) != 1 {
				t.Fatal("error document accepted as a download server")
			}
		})
	}
}
